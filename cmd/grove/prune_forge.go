package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"

	"grove/internal/project"
	"grove/internal/ui"
)

// forgePR is a merged pull request as the forge reports it.
type forgePR struct {
	Number      int    `json:"number"`
	HeadRefName string `json:"headRefName"`
	HeadRefOid  string `json:"headRefOid"`
}

// forgeRecentLimit caps the repository-wide merged list, which covers origin's
// branches; local branches are looked up by name, so their age doesn't matter.
const forgeRecentLimit = "200"

// forgeBranchLimit caps the merged pull requests looked up per branch name,
// which more than one request can reuse.
const forgeBranchLimit = "20"

// forgeMergedBranches maps each head-branch name to its merged pull requests,
// according to the forge (via gh), counting only requests merged into the
// default branch. Besides the most recent merges across the
// repository, it looks up every name in names, so a branch merged long ago on
// a busy repository is still found. It then fetches the head of each request
// found by name that isn't local yet, since a branch deleted on merge leaves no
// other ref to it. It returns nil (warning once) when the repo can't be
// resolved, gh is unavailable, or the query fails, so prune falls back to
// git-only detection.
func (in pruneInputs) forgeMergedBranches() map[string][]forgePR {
	p, names := in.p, in.forgeLookupNames()
	repo := in.cfg.ForgeRepo()
	if repo == "" {
		repo = p.OriginRepoSlug()
	}
	if repo == "" {
		ui.Warn("prune.forge: could not determine origin repo; skipping forge check.")
		return nil
	}
	if _, err := exec.LookPath("gh"); err != nil {
		ui.Warn("prune.forge: gh not found on PATH; skipping forge check.")
		return nil
	}

	// The most recent merges across the repository
	recent, err := ghMergedPRs(repo, in.def, "--limit", forgeRecentLimit)
	if err != nil {
		ui.Warn(fmt.Sprintf("prune.forge: %v; skipping forge check.", err))
		return nil
	}
	merged := map[string][]forgePR{}
	addForgePRs(merged, recent)

	// Each named branch's own merges, looked up in parallel
	byName := make([][]forgePR, len(names))
	failed := make([]bool, len(names))
	done := runParallel(len(names), statusWorkers(), func(i int) {
		prs, lookupErr := ghMergedPRs(repo, in.def, "--head", names[i], "--limit", forgeBranchLimit)
		byName[i], failed[i] = prs, lookupErr != nil
	})
	var named []forgePR
	failures := 0
	for i := range names {
		<-done[i]
		if failed[i] {
			failures++
		}
		named = append(named, byName[i]...)
	}
	if failures > 0 {
		ui.Warn(fmt.Sprintf("prune.forge: looking up %d %s failed; judging them without the forge.", failures, plural(failures, "branch")))
	}
	addForgePRs(merged, named)
	fetchForgeHeads(p, named)
	return merged
}

// ghMergedPRs lists pull requests merged into base on repo, narrowed by extra
// gh flags. Requests merged into any other branch, such as the lower layer of a
// stack, haven't landed upstream.
func ghMergedPRs(repo, base string, extra ...string) ([]forgePR, error) {
	args := append([]string{"pr", "list", "--repo", repo, "--state", "merged", "--base", base,
		"--json", "number,headRefName,headRefOid"}, extra...)
	out, err := exec.Command("gh", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("'gh pr list' failed")
	}
	var prs []forgePR
	if err := json.Unmarshal(out, &prs); err != nil {
		return nil, fmt.Errorf("could not parse gh output")
	}
	return prs, nil
}

// addForgePRs files each pull request under its head branch, once per number.
func addForgePRs(merged map[string][]forgePR, prs []forgePR) {
	for _, pr := range prs {
		if pr.HeadRefName == "" {
			continue
		}
		seen := false
		for _, have := range merged[pr.HeadRefName] {
			seen = seen || have.Number == pr.Number
		}
		if !seen {
			merged[pr.HeadRefName] = append(merged[pr.HeadRefName], pr)
		}
	}
}

// fetchForgeHeads fetches, in one call, the head of every pull request whose
// head commit isn't local, into refs/grove/pull/N where the pull request check
// also keeps them. A failed fetch only warns: those branches are then judged
// as if the forge hadn't matched them.
func fetchForgeHeads(p *project.Project, prs []forgePR) {
	args := []string{"--git-dir=.", "fetch", "--quiet", "--no-tags", "origin"}
	want := map[int]bool{}
	for _, pr := range prs {
		if want[pr.Number] || pr.HeadRefOid == "" || p.HasCommit(pr.HeadRefOid) {
			continue
		}
		want[pr.Number] = true
		n := strconv.Itoa(pr.Number)
		args = append(args, "+refs/pull/"+n+"/head:refs/grove/pull/"+n)
	}
	if len(want) == 0 {
		return
	}
	ui.Info(fmt.Sprintf("Fetching %d merged pull request %s...", len(want), plural(len(want), "head")))
	if !project.GitQuiet(p.Base, args...) {
		ui.Warn("prune.forge: fetching pull request heads failed; judging those branches without them.")
	}
}

// forgeLookupNames returns the branch names to ask the forge about: every
// local branch, plus the selected origin names in local scope, minus the
// default branch, protected names, and branches already merged by ancestry.
func (in pruneInputs) forgeLookupNames() []string {
	seen := map[string]bool{}
	var names []string
	for _, name := range append(in.p.LocalBranches(), in.originNames...) {
		if seen[name] || name == "HEAD" || name == in.def || in.cfg.PruneProtected(name) || in.merged[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	return names
}

// forgeReason reports whether ref, the tip of branch name, sits inside a pull
// request the forge reports merged. Its tip must be an ancestor of (or equal
// to) that request's head, so commits added after the merge, or a reused
// branch name, never count as landed.
func (in pruneInputs) forgeReason(name, ref string) string {
	for _, pr := range in.forgeMerged[name] {
		if pr.HeadRefOid != "" && in.p.CommitMerged(ref, pr.HeadRefOid) {
			return "forge"
		}
	}
	return ""
}
