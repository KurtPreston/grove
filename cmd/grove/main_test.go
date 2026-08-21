package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	// Pass identity (and disable signing) per-invocation so commits succeed even
	// when the repo has no local config and the environment has no global git
	// identity — as on CI runners.
	gitDo(t, repo,
		"-c", "user.email=grove@test",
		"-c", "user.name=grove",
		"-c", "commit.gpgsign=false",
		"commit", "-q", "-m", msg)
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
		partial     bool
		want        string
	}{
		{"regular merge", wt("feat-merged"), otherCwd, std, nil, false, "merged"},
		{"squash merge", wt("feat-squash"), otherCwd, std, nil, false, "squashed"},
		{"never-pushed upstream kept", wt("feat-unpushed"), otherCwd, std, nil, false, ""},
		{"upstream present kept", wt("feat-upstream"), otherCwd, std, nil, false, ""},
		{"open unmerged kept", wt("feat-open"), otherCwd, std, nil, false, ""},
		{"forge merged", wt("feat-open"), otherCwd, std, map[string]bool{"feat-open": true}, false, "forge"},
		{"squash detection disabled", wt("feat-squash"), otherCwd, squashOff(), nil, false, ""},
		{"partial clone skips squash detection", wt("feat-squash"), otherCwd, std, nil, true, ""},
		{"default branch kept", wt("main"), otherCwd, std, nil, false, ""},
		{"current dir kept", project.Worktree{Path: otherCwd, Branch: "feat-merged"}, otherCwd, std, nil, false, ""},
		{"bare kept", project.Worktree{Path: "/wt/bare", Bare: true}, otherCwd, std, nil, false, ""},
		{"no branch kept", project.Worktree{Path: "/wt/detached"}, otherCwd, std, nil, false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := pruneReason(p, tc.w, def, tc.cwd, tc.cfg, tc.forgeMerged, tc.partial)
			if got != tc.want {
				t.Errorf("pruneReason(%q) = %q, want %q", tc.w.Branch, got, tc.want)
			}
		})
	}
}

func TestFormatCommitAge(t *testing.T) {
	now := time.Date(2026, 8, 18, 15, 0, 0, 0, time.UTC)
	tests := []struct {
		ago  time.Duration
		want string
	}{
		{0, "just now"},
		{30 * time.Second, "just now"},
		{time.Minute, "1 minute ago"},
		{2 * time.Minute, "2 minutes ago"},
		{time.Hour, "1 hour ago"},
		{5 * time.Hour, "5 hours ago"},
		{24 * time.Hour, "1 day ago"},
		{10 * 24 * time.Hour, "10 days ago"},
		{30 * 24 * time.Hour, "1 month ago"},
		{90 * 24 * time.Hour, "3 months ago"},
		{365 * 24 * time.Hour, "1 year ago"},
		{800 * 24 * time.Hour, "2 years ago"},
	}
	for _, tc := range tests {
		got := formatCommitAge(now.Add(-tc.ago), now)
		if got != tc.want {
			t.Errorf("formatCommitAge(%v ago) = %q, want %q", tc.ago, got, tc.want)
		}
	}
	if got := formatCommitAge(time.Time{}, now); got != "--" {
		t.Errorf("zero time = %q, want --", got)
	}
	if got := formatCommitAge(now.Add(time.Hour), now); got != "just now" {
		t.Errorf("future time = %q, want just now", got)
	}
}

func TestSortWorktreesByCommitTime(t *testing.T) {
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	mid := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	new := time.Date(2022, 1, 1, 0, 0, 0, 0, time.UTC)
	wts := []project.Worktree{
		{Branch: "older", Head: "aaa"},
		{Branch: "tied-b", Head: "ccc"},
		{Branch: "tied-a", Head: "ccc"},
		{Branch: "newer", Head: "bbb"},
		{Branch: "unknown", Head: "ddd"},
	}
	times := map[string]time.Time{
		"aaa": old,
		"bbb": new,
		"ccc": mid,
	}
	sortWorktreesByCommitTime(wts, times)
	got := make([]string, len(wts))
	for i, w := range wts {
		got[i] = w.Branch
	}
	want := []string{"newer", "tied-a", "tied-b", "older", "unknown"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestListShowsCommitAgeAndSortsWithT(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	gitDo(t, src, "init", "-q", "-b", "main")
	gitCommit(t, src, "base.txt", "0", "c0")

	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(proj, ".base")
	gitDo(t, proj, "clone", "-q", "--bare", src, base)
	gitDo(t, base, "worktree", "add", filepath.Join(proj, "older"), "main")
	gitDo(t, base, "worktree", "add", "-b", "newer", filepath.Join(proj, "newer"))
	gitDo(t, filepath.Join(proj, "older"), "checkout", "-q", "-b", "older")
	gitCommitDated(t, filepath.Join(proj, "older"), "2020-01-01 00:00:00 +0000", "old.txt", "old", "old")
	gitCommitDated(t, filepath.Join(proj, "newer"), "2021-06-15 12:00:00 +0000", "new.txt", "new", "new")

	out := captureList(t, filepath.Join(proj, "older"))
	if !strings.Contains(out, "older") || !strings.Contains(out, "newer") {
		t.Fatalf("list missing branches: %q", out)
	}
	if !strings.Contains(out, "years ago") && !strings.Contains(out, "year ago") {
		t.Fatalf("list missing commit age: %q", out)
	}
	if strings.Contains(out, "\x1b[") {
		t.Fatalf("list should drop color when stdout is not a terminal: %q", out)
	}

	sorted := captureList(t, filepath.Join(proj, "older"), "-t")
	if i, j := strings.Index(sorted, "newer"), strings.Index(sorted, "older"); i < 0 || j < 0 || i > j {
		t.Fatalf("-t should list newer before older, got %q", sorted)
	}

	porcelain := captureList(t, filepath.Join(proj, "older"), "-t", "--porcelain")
	lines := strings.Split(strings.TrimSpace(porcelain), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "newer\t") {
		t.Fatalf("-t --porcelain should start with newer, got %q", porcelain)
	}
}

func gitCommitDated(t *testing.T, repo, when, name, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitDo(t, repo, "add", name)
	cmd := exec.Command("git", "-C", repo,
		"-c", "user.email=grove@test",
		"-c", "user.name=grove",
		"-c", "commit.gpgsign=false",
		"commit", "-q", "-m", msg)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE="+when,
		"GIT_COMMITTER_DATE="+when,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("dated commit failed: %v\n%s", err, out)
	}
}

func captureList(t *testing.T, dir string, args ...string) string {
	t.Helper()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	oldOut := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	cmdList(args)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = oldOut
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}
