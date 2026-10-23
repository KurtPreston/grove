package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"grove/internal/config"
	"grove/internal/project"
)

func squashOff() config.Config {
	off := false
	return config.Config{Prune: &config.PruneConfig{DetectSquash: &off}}
}

// TestPruneReason exercises the full prune decision matrix on a real repo. It
// stands up a branch for each outcome, freezes an origin/main tracking ref to
// represent the upstream default, and asserts pruneInputs.reason classifies each one.
//
// A plain repo's .git directory doubles as p.Base: pruneInputs.reason only shells
// git against it, and its worktree makes the merges/tracking config easy to script. The
// origin/main ref is synthesized with update-ref instead of a second remote so
// the "merged", "squashed", "never pushed", and "upstream present" states are
// exact.
func TestPruneReason(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	gitDo(t, base, "init", "-q", "-b", "main")
	gitDo(t, base, "config", "user.email", "grove@test")
	gitDo(t, base, "config", "user.name", "grove")
	gitDo(t, base, "config", "commit.gpgsign", "false")
	// An origin remote (never fetched) installs the standard fetch refspec so
	// branch@{upstream} maps refs/heads/* to refs/remotes/origin/*, matching a
	// real grove clone. Without it, @{upstream} can't resolve a tracking ref.
	gitDo(t, base, "remote", "add", "origin", base)
	p := &project.Project{Base: filepath.Join(base, ".git"), Dir: base}

	gitCommit(t, base, "base.txt", "0", "c0")

	// Regular merge: --no-ff makes the branch tip an ancestor of main.
	gitDo(t, base, "checkout", "-q", "-b", "feat-merged")
	gitCommit(t, base, "merged.txt", "m", "m1")
	gitDo(t, base, "checkout", "-q", "main")
	gitDo(t, base, "merge", "--no-ff", "-m", "merge feat-merged", "feat-merged")

	// Squash merge: change lands as one new commit; branch tip not an ancestor.
	gitDo(t, base, "checkout", "-q", "-b", "feat-squash", "main")
	gitCommit(t, base, "squash.txt", "s", "s1")
	gitDo(t, base, "checkout", "-q", "main")
	gitDo(t, base, "merge", "--squash", "feat-squash")
	gitDo(t, base, "commit", "-q", "-m", "squash feat-squash")

	// Squash under a PR-numbered subject, against a newer main: the diff differs,
	// so only the subject ties the landed commit to the detached one. The branch is
	// deleted, as forges do on merge, leaving the commit held by no ref.
	gitDo(t, base, "checkout", "-q", "-b", "feat-pr", "main")
	gitCommit(t, base, "pr.txt", "p", "p1")
	prHead := gitRev(t, base, "feat-pr")
	gitDo(t, base, "checkout", "-q", "main")
	gitCommit(t, base, "pr.txt", "p, resolved", "p1 (#7)")
	gitDo(t, base, "branch", "-q", "-D", "feat-pr")

	// Orphaned and unmerged: a commit no ref holds whose subject never landed.
	gitDo(t, base, "checkout", "-q", "-b", "feat-lost", "main")
	gitCommit(t, base, "lost.txt", "l", "l1")
	lostHead := gitRev(t, base, "feat-lost")
	gitDo(t, base, "checkout", "-q", "main")
	gitDo(t, base, "branch", "-q", "-D", "feat-lost")

	// Freeze the upstream default so it contains the merged + squashed work.
	gitDo(t, base, "update-ref", "refs/remotes/origin/main", "main")

	// never pushed (a.k.a. "gone" upstream): grove points every branch at
	// origin/<branch> for pushability, so a branch that was never pushed has
	// branch.<name>.remote set with no origin/<name> ref — the exact same state as
	// one whose upstream was pushed and later deleted. Neither is a prune
	// candidate, so unpushed work is never discarded.
	gitDo(t, base, "checkout", "-q", "-b", "feat-unpushed", "main")
	gitCommit(t, base, "unpushed.txt", "g", "g1")
	gitDo(t, base, "config", "branch.feat-unpushed.remote", "origin")
	gitDo(t, base, "config", "branch.feat-unpushed.merge", "refs/heads/feat-unpushed")

	// upstream present: tracks an existing origin ref; unmerged -> kept.
	gitDo(t, base, "checkout", "-q", "-b", "feat-upstream", "main")
	gitCommit(t, base, "upstream.txt", "u", "u1")
	gitDo(t, base, "update-ref", "refs/remotes/origin/feat-upstream", "feat-upstream")
	gitDo(t, base, "config", "branch.feat-upstream.remote", "origin")
	gitDo(t, base, "config", "branch.feat-upstream.merge", "refs/heads/feat-upstream")

	// open: unmerged, untracked -> kept, unless the forge says it merged.
	gitDo(t, base, "checkout", "-q", "-b", "feat-open", "main")
	gitCommit(t, base, "open.txt", "o", "o1")

	gitDo(t, base, "checkout", "-q", "main")
	openHead := gitRev(t, base, "feat-open")
	c0 := gitRev(t, base, "main~3")

	// protected: cut from main with no new commits, so it is an ancestor of main
	gitDo(t, base, "branch", "release-1", "main~1")

	const def = "main"
	const otherCwd = "/some/other/cwd"
	std := config.Config{} // squash detection on, forge off
	wt := func(b string) project.Worktree { return project.Worktree{Path: "/wt/" + b, Branch: b} }
	detached := func(head string) project.Worktree {
		return project.Worktree{Path: "/wt/detached", Head: head, Detached: true}
	}

	tests := []struct {
		name        string
		w           project.Worktree
		cwd         string
		cfg         config.Config
		forgeMerged map[string]string
		partial     bool
		want        string
	}{
		{"regular merge", wt("feat-merged"), otherCwd, std, nil, false, "merged"},
		{"squash merge", wt("feat-squash"), otherCwd, std, nil, false, "squashed"},
		{"never-pushed upstream kept", wt("feat-unpushed"), otherCwd, std, nil, false, ""},
		{"upstream present kept", wt("feat-upstream"), otherCwd, std, nil, false, ""},
		{"open unmerged kept", wt("feat-open"), otherCwd, std, nil, false, ""},
		{"forge merged", wt("feat-open"), otherCwd, std, map[string]string{"feat-open": openHead}, false, "forge"},
		{"forge merged before newer commits kept", wt("feat-open"), otherCwd, std, map[string]string{"feat-open": c0}, false, ""},
		{"protected branch kept", wt("release-1"), otherCwd, std, nil, false, ""},
		{"protect overridable", wt("release-1"), otherCwd, config.Config{Prune: &config.PruneConfig{Protect: []string{}}}, nil, false, "merged"},
		{"squash detection disabled", wt("feat-squash"), otherCwd, squashOff(), nil, false, ""},
		{"partial clone skips squash detection", wt("feat-squash"), otherCwd, std, nil, true, ""},
		{"default branch kept", wt("main"), otherCwd, std, nil, false, ""},
		{"current dir kept", project.Worktree{Path: otherCwd, Branch: "feat-merged"}, otherCwd, std, nil, false, ""},
		{"bare kept", project.Worktree{Path: "/wt/bare", Bare: true}, otherCwd, std, nil, false, ""},
		{"no branch or head kept", project.Worktree{Path: "/wt/detached"}, otherCwd, std, nil, false, ""},
		{"detached merged", detached(gitRev(t, base, "feat-merged")), otherCwd, std, nil, false, "merged"},
		{"detached squashed", detached(gitRev(t, base, "feat-squash")), otherCwd, std, nil, false, "squashed"},
		{"detached subject landed", detached(prHead), otherCwd, std, nil, false, "subject"},
		{"detached subject needs squash detection", detached(prHead), otherCwd, squashOff(), nil, false, ""},
		{"detached subject skipped on partial clone", detached(prHead), otherCwd, std, nil, true, ""},
		{"detached unmerged kept", detached(lostHead), otherCwd, std, nil, false, ""},
		{"detached on open branch kept", detached(gitRev(t, base, "feat-open")), otherCwd, std, nil, false, ""},
		{"detached current dir kept", project.Worktree{Path: otherCwd, Head: prHead, Detached: true}, otherCwd, std, nil, false, ""},
	}
	merged := mergedBranches(p, "origin/"+def)
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := pruneInputs{
				p: p, def: def, cwd: tc.cwd, cfg: tc.cfg, partial: tc.partial,
				merged: merged, forgeMerged: tc.forgeMerged,
			}
			got := in.reason(tc.w)
			if got != tc.want {
				t.Errorf("reason(%q) = %q, want %q", tc.w.Branch, got, tc.want)
			}
		})
	}
}

// TestSplitPruneCandidates pins the safety rule that a merged-but-dirty worktree
// is kept: prune preserves the branch ref, but uncommitted and untracked work
// would be gone for good.
func TestSplitPruneCandidates(t *testing.T) {
	cands := []pruneCandidate{
		{branch: "clean-a", reason: "merged"},
		{branch: "dirty", reason: "merged", dirty: true},
		{branch: "clean-b", reason: "squashed"},
	}

	remove, kept := splitPruneCandidates(cands, false)
	if got := branchList(remove); got != "clean-a,clean-b" {
		t.Errorf("remove = %q, want clean-a,clean-b", got)
	}
	if got := branchList(kept); got != "dirty" {
		t.Errorf("kept = %q, want dirty", got)
	}

	remove, kept = splitPruneCandidates(cands, true)
	if got := branchList(remove); got != "clean-a,dirty,clean-b" {
		t.Errorf("forced remove = %q, want clean-a,dirty,clean-b", got)
	}
	if len(kept) != 0 {
		t.Errorf("--force should keep nothing, got %q", branchList(kept))
	}
}

// TestPruneBranchCandidates checks which local and origin branches prune deletes:
// merged ones, minus the default branch, protected names, and branches a
// remaining worktree still has checked out.
func TestPruneBranchCandidates(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	gitDo(t, base, "init", "-q", "-b", "main")
	gitDo(t, base, "remote", "add", "origin", base)
	p := &project.Project{Base: filepath.Join(base, ".git"), Dir: base}
	gitCommit(t, base, "base.txt", "0", "c0")

	// Merged local branches, one per checkout situation, plus a protected one
	for _, b := range []string{"feat-merged", "feat-checked", "feat-removed", "release-1"} {
		gitDo(t, base, "branch", b)
	}

	// An unmerged local branch, kept
	gitDo(t, base, "checkout", "-q", "-b", "feat-open")
	gitCommit(t, base, "open.txt", "o", "o1")
	gitDo(t, base, "checkout", "-q", "main")
	gitCommit(t, base, "next.txt", "n", "c1")

	// origin: the default, a merged and an open branch, HEAD, and a merged
	// branch on another remote that must not be mistaken for origin's
	gitDo(t, base, "update-ref", "refs/remotes/origin/main", "main")
	gitDo(t, base, "update-ref", "refs/remotes/origin/old", "main~1")
	gitDo(t, base, "update-ref", "refs/remotes/origin/open", "feat-open")
	gitDo(t, base, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	gitDo(t, base, "update-ref", "refs/remotes/upstream/open", "main~1")

	in := pruneInputs{
		p: p, def: "main", cwd: "/elsewhere", cfg: config.Config{},
		merged: mergedBranches(p, "origin/main"),
	}
	wts := []project.Worktree{
		{Path: base, Branch: "main"},
		{Path: "/wt/feat-checked", Branch: "feat-checked"},
		{Path: "/wt/feat-removed", Branch: "feat-removed"},
	}
	remove := []pruneCandidate{{branch: "feat-removed", path: "/wt/feat-removed", reason: "merged"}}

	if got := branchList(in.localBranchCandidates(wts, remove)); got != "feat-merged,feat-removed" {
		t.Errorf("local candidates = %q, want feat-merged,feat-removed", got)
	}
	if got := branchList(in.originBranchCandidates()); got != "origin/old" {
		t.Errorf("origin candidates = %q, want origin/old", got)
	}
}

// TestLocalPrompt pins the confirmation wording for each mix of work.
func TestLocalPrompt(t *testing.T) {
	tests := []struct {
		worktrees, branches, dirty int
		want                       string
	}{
		{2, 0, 0, "Remove 2 worktrees? [y/N] "},
		{0, 1, 0, "Delete 1 local branch? [y/N] "},
		{1, 3, 1, "Remove 1 worktree and delete 3 local branches? 1 has local changes that will be discarded. [y/N] "},
	}
	for _, tc := range tests {
		if got := localPrompt(tc.worktrees, tc.branches, tc.dirty); got != tc.want {
			t.Errorf("localPrompt(%d, %d, %d) = %q, want %q", tc.worktrees, tc.branches, tc.dirty, got, tc.want)
		}
	}
}

func branchList(cands []pruneCandidate) string {
	names := make([]string, len(cands))
	for i, c := range cands {
		names[i] = c.branch
	}
	return strings.Join(names, ",")
}
