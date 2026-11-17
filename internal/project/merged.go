package project

import (
	"regexp"
	"strings"

	"grove/internal/ui"
)

// Prune fetches (with --prune) so merge detection sees the latest
// origin/<default> and remote-tracking refs before classifying worktrees.
func (p *Project) Prune() {
	if !GitQuiet(p.Base, "--git-dir=.", "fetch", "--prune", "origin") {
		ui.Warn("fetch failed; using local state.")
	}
}

// BranchSquashMerged reports whether branch's net changes are already present in
// into (e.g. "origin/main") even when the branch was squash- or rebase-merged.
// Those merges rewrite history, so the branch tip is not an ancestor of into and
// `git branch --merged` never lists it.
//
// It synthesizes a commit holding branch's tree parented on the merge-base of
// branch and into, then asks `git cherry` whether that combined diff is already
// upstream: a leading "-" means an equivalent patch exists in into. The
// synthesized commit is dangling (no ref points at it) and is reclaimed by gc.
// When the patches differ only in context, landedIn finds the landing commit.
func (p *Project) BranchSquashMerged(branch, into string) bool {
	base, err := GitOut(p.Base, "--git-dir=.", "merge-base", into, branch)
	if err != nil {
		return false
	}
	base = strings.TrimSpace(base)
	if base == "" {
		return false
	}
	tree, err := GitOut(p.Base, "--git-dir=.", "rev-parse", branch+"^{tree}")
	if err != nil {
		return false
	}
	tree = strings.TrimSpace(tree)
	if tree == "" {
		return false
	}
	virt, err := GitOut(p.Base, "--git-dir=.", "commit-tree", tree, "-p", base, "-m", "_")
	if err != nil {
		return false
	}
	virt = strings.TrimSpace(virt)
	if virt == "" {
		return false
	}
	out, err := GitOut(p.Base, "--git-dir=.", "cherry", into, virt)
	if err != nil {
		return false
	}
	if strings.HasPrefix(strings.TrimSpace(out), "-") {
		return true
	}
	return p.landedIn(branch, into, base)
}

// Bounds on the landing-commit search, so a branch whose files main touches
// constantly can't make prune walk all of history
const (
	maxLandingFiles      = 500
	maxLandingCandidates = 50
)

// landedIn reports whether a commit on into since base already holds all of
// branch's changes: merging branch into it (git merge-tree) yields that
// commit's own tree. It catches a squash whose diff carries different context
// than the branch's, because into changed nearby lines before the merge, where
// the patch IDs `git cherry` compares never match. Merging into the landing
// commit rather than into's tip keeps later edits to the same lines from
// conflicting.
//
// Only commits touching every file the branch changed can hold all of it, so
// only those are tried, oldest first, up to maxLandingCandidates. It needs git
// 2.38 or later for merge-tree --write-tree, and reports false on older ones.
func (p *Project) landedIn(branch, into, base string) bool {
	// Files the branch changed since it forked
	out, err := GitOut(p.Base, "--git-dir=.", "-c", "core.quotePath=false", "diff", "--name-only", "--no-renames", base, branch, "--")
	files := nonEmptyLines(out)
	if err != nil || len(files) == 0 || len(files) > maxLandingFiles {
		return false
	}

	// Commits since the fork touching every one of those files
	for _, c := range p.landingCandidates(base, into, files) {
		merged, err := GitOut(p.Base, "--git-dir=.", "merge-tree", "--write-tree", "--no-messages", c.commit, branch)
		if err == nil && strings.TrimSpace(merged) == c.tree {
			return true
		}
	}
	return false
}

// landingCommit is a commit that could have landed a branch, with its tree.
type landingCommit struct{ commit, tree string }

// landingCandidates lists the non-merge commits in base..into that touch every
// one of files, oldest first and at most maxLandingCandidates of them.
func (p *Project) landingCandidates(base, into string, files []string) []landingCommit {
	args := []string{"--git-dir=.", "-c", "core.quotePath=false", "log", "--no-merges", "--no-renames",
		"--name-only", "--format=%x01%H %T", base + ".." + into, "--"}
	out, err := GitOut(p.Base, append(args, files...)...)
	if err != nil {
		return nil
	}

	// Each record is "<commit> <tree>" followed by the files it touched
	var found []landingCommit
	for _, rec := range strings.Split(out, "\x01") {
		lines := nonEmptyLines(rec)
		if len(lines) == 0 {
			continue
		}
		head := strings.Fields(lines[0])
		if len(head) != 2 {
			continue
		}
		touched := map[string]bool{}
		for _, f := range lines[1:] {
			touched[f] = true
		}
		all := true
		for _, f := range files {
			all = all && touched[f]
		}
		if all {
			found = append(found, landingCommit{commit: head[0], tree: head[1]})
		}
	}

	// git log lists newest first; the landing commit is usually among the oldest
	for i, j := 0, len(found)-1; i < j; i, j = i+1, j-1 {
		found[i], found[j] = found[j], found[i]
	}
	if len(found) > maxLandingCandidates {
		found = found[:maxLandingCandidates]
	}
	return found
}

// CommitMerged reports whether rev is an ancestor of into (e.g. "origin/main").
// It is the detached-HEAD counterpart of `git branch --merged`, which lists only
// branches.
func (p *Project) CommitMerged(rev, into string) bool {
	return GitQuiet(p.Base, "--git-dir=.", "merge-base", "--is-ancestor", rev, into)
}

// HasCommit reports whether the commit named by oid is in the repository.
func (p *Project) HasCommit(oid string) bool {
	return GitQuiet(p.Base, "--git-dir=.", "cat-file", "-e", oid+"^{commit}")
}

// prSuffix matches the " (#123)" that forges append to a squash commit's subject.
var prSuffix = regexp.MustCompile(`\s+\(#\d+\)$`)

// SubjectsUpstream reports whether every non-merge commit that only head holds
// (reachable from no branch, remote-tracking ref, or tag) has a commit with the
// same subject in into but not in head, ignoring a trailing " (#N)".
//
// It catches a detached worktree left at a commit that was later squash-merged
// against a newer default branch: the squash's diff then differs from the
// commit's (conflict resolutions, a "Merge main" folded in), so the
// patch-equivalence check misses it, and no ref still holds the original commit.
// It is a heuristic, so it reports false when head holds no commits of its own:
// a HEAD that some ref still holds is not evidence of anything upstream.
func (p *Project) SubjectsUpstream(head, into string) bool {
	// Subjects of the commits only head holds
	own, err := GitOut(p.Base, "--git-dir=.", "log", "--no-merges", "--format=%s", head, "--not", "--branches", "--remotes", "--tags")
	if err != nil || strings.TrimSpace(own) == "" {
		return false
	}

	// Subjects that landed upstream since head forked, without their PR numbers
	theirs, err := GitOut(p.Base, "--git-dir=.", "log", "--no-merges", "--format=%s", into, "--not", head)
	if err != nil {
		return false
	}
	landed := make(map[string]bool)
	for _, s := range strings.Split(theirs, "\n") {
		landed[prSuffix.ReplaceAllString(strings.TrimSpace(s), "")] = true
	}

	// Every commit of head's own must have landed
	for _, s := range strings.Split(strings.TrimSpace(own), "\n") {
		s = prSuffix.ReplaceAllString(strings.TrimSpace(s), "")
		if s == "" || !landed[s] {
			return false
		}
	}
	return true
}

// nonEmptyLines splits git output into its non-blank lines.
func nonEmptyLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l != "" {
			lines = append(lines, l)
		}
	}
	return lines
}
