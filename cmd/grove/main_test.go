package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"grove/internal/config"
	"grove/internal/project"
)

func gitDo(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func gitCommit(t *testing.T, repo, name, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDo(t, repo, "add", name)
	gitDo(t, repo, "commit", "-q", "-m", msg)
}

func squashOff() config.Config {
	off := false
	return config.Config{Prune: &config.PruneConfig{DetectSquash: &off}}
}

// TestPruneReason exercises the full prune decision matrix on a real repo. It
// stands up a branch for each outcome, freezes an origin/main tracking ref to
// represent the upstream default, and asserts pruneReason classifies each one.
//
// A plain (non-bare) repo doubles as p.Base: pruneReason only shells git against
// it, and a working repo makes the merges/tracking config easy to script. The
// origin/main ref is synthesized with update-ref instead of a second remote so
// the "merged", "squashed", "gone", and "upstream present" states are exact.
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
	p := &project.Project{Base: base, Dir: base}

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

	// Freeze the upstream default so it contains the merged + squashed work.
	gitDo(t, base, "update-ref", "refs/remotes/origin/main", "main")

	// gone: was tracking a remote branch that no longer exists; unmerged.
	gitDo(t, base, "checkout", "-q", "-b", "feat-gone", "main")
	gitCommit(t, base, "gone.txt", "g", "g1")
	gitDo(t, base, "config", "branch.feat-gone.remote", "origin")
	gitDo(t, base, "config", "branch.feat-gone.merge", "refs/heads/feat-gone")

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

	const def = "main"
	const otherCwd = "/some/other/cwd"
	std := config.Config{} // squash detection on, forge off
	wt := func(b string) project.Worktree { return project.Worktree{Path: "/wt/" + b, Branch: b} }

	tests := []struct {
		name        string
		w           project.Worktree
		cwd         string
		cfg         config.Config
		forgeMerged map[string]bool
		want        string
	}{
		{"regular merge", wt("feat-merged"), otherCwd, std, nil, "merged"},
		{"squash merge", wt("feat-squash"), otherCwd, std, nil, "squashed"},
		{"gone upstream", wt("feat-gone"), otherCwd, std, nil, "gone"},
		{"upstream present kept", wt("feat-upstream"), otherCwd, std, nil, ""},
		{"open unmerged kept", wt("feat-open"), otherCwd, std, nil, ""},
		{"forge merged", wt("feat-open"), otherCwd, std, map[string]bool{"feat-open": true}, "forge"},
		{"squash detection disabled", wt("feat-squash"), otherCwd, squashOff(), nil, ""},
		{"default branch kept", wt("main"), otherCwd, std, nil, ""},
		{"current dir kept", project.Worktree{Path: otherCwd, Branch: "feat-merged"}, otherCwd, std, nil, ""},
		{"bare kept", project.Worktree{Path: "/wt/bare", Bare: true}, otherCwd, std, nil, ""},
		{"no branch kept", project.Worktree{Path: "/wt/detached"}, otherCwd, std, nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pruneReason(p, tc.w, def, tc.cwd, tc.cfg, tc.forgeMerged)
			if got != tc.want {
				t.Errorf("pruneReason(%q) = %q, want %q", tc.w.Branch, got, tc.want)
			}
		})
	}
}
