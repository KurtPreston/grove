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

	"grove/internal/project"
)

func gitDo(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func gitRev(t *testing.T, dir, rev string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", rev).Output()
	if err != nil {
		t.Fatalf("git rev-parse %s failed: %v", rev, err)
	}
	return strings.TrimSpace(string(out))
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
	gitDo(t, base, "--git-dir=.", "worktree", "add", filepath.Join(proj, "older"), "main")
	gitDo(t, base, "--git-dir=.", "worktree", "add", "-b", "newer", filepath.Join(proj, "newer"))
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

	// Skip the header line: its temp path can contain "older" (macOS /var/folders)
	sorted := captureList(t, filepath.Join(proj, "older"), "-t")
	if _, rows, ok := strings.Cut(sorted, "\n"); ok {
		sorted = rows
	}
	if i, j := strings.Index(sorted, "newer"), strings.Index(sorted, "older"); i < 0 || j < 0 || i > j {
		t.Fatalf("-t should list newer before older, got %q", sorted)
	}

	porcelain := captureList(t, filepath.Join(proj, "older"), "-t", "--porcelain")
	lines := strings.Split(strings.TrimSpace(porcelain), "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "newer\t") {
		t.Fatalf("-t --porcelain should start with newer, got %q", porcelain)
	}

	// Only the worktree with local changes gets the dirty marker
	if err := os.WriteFile(filepath.Join(proj, "newer", "wip.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatal(err)
	}
	marks := listDirtyMarks(t, captureList(t, filepath.Join(proj, "older")))
	if marks["newer"] != "*" || marks["older"] != " " {
		t.Fatalf("dirty marks = %q, want newer=* older=blank", marks)
	}

	// --no-status skips the check, so nothing is marked
	marks = listDirtyMarks(t, captureList(t, filepath.Join(proj, "older"), "--no-status"))
	if marks["newer"] != " " || marks["older"] != " " {
		t.Fatalf("--no-status marks = %q, want all blank", marks)
	}
}

// listDirtyMarks maps each branch row of plain `grove list` output to its
// dirty column (the character after the tmux marker).
func listDirtyMarks(t *testing.T, out string) map[string]string {
	t.Helper()
	marks := map[string]string{}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(line) < 7 || len(fields) == 0 {
			t.Fatalf("unexpected list row %q", line)
		}
		marks[fields[len(fields)-1]] = line[6:7]
	}
	return marks
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

// TestIsTerminalRejectsNonTTYs guards against treating any character device as
// a terminal: /dev/null is one, and agents often run with stdin pointed at it.
func TestIsTerminalRejectsNonTTYs(t *testing.T) {
	// Open /dev/null, a pipe, and a regular file
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	file, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	// None of them can answer a prompt
	for name, f := range map[string]*os.File{"/dev/null": devNull, "pipe": r, "file": file} {
		if isTerminal(f) {
			t.Errorf("isTerminal(%s) = true, want false", name)
		}
	}
}

// TestBaseResolverDefaultsWhenStdinIsDevNull reproduces an agent running
// 'grove path BRANCH' with stdin at /dev/null: it must base off the default
// branch instead of launching fzf or the numbered menu.
func TestBaseResolverDefaultsWhenStdinIsDevNull(t *testing.T) {
	// Point stdin at /dev/null for the duration of the test
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer devNull.Close()
	orig := os.Stdin
	os.Stdin = devNull
	defer func() { os.Stdin = orig }()

	// A nil project is safe because the non-interactive path never touches it
	got, err := baseResolver(nil, "")("main")
	if err != nil || got != "main" {
		t.Fatalf("baseResolver = %q, %v; want \"main\", nil", got, err)
	}
}

// TestWantsHelp verifies -h/--help anywhere in a subcommand's args requests
// help, so e.g. `grove path --help` never creates a "help" branch.
func TestWantsHelp(t *testing.T) {
	cases := map[string]struct {
		args []string
		want bool
	}{
		"long flag":      {[]string{"--help"}, true},
		"short flag":     {[]string{"-h"}, true},
		"after a branch": {[]string{"feature", "--help"}, true},
		"bare word help": {[]string{"help"}, false},
		"plain branch":   {[]string{"feature"}, false},
		"no args":        {nil, false},
	}
	for name, c := range cases {
		if got := wantsHelp(c.args); got != c.want {
			t.Errorf("%s: wantsHelp(%v) = %v, want %v", name, c.args, got, c.want)
		}
	}
}

func TestPopNoFetch(t *testing.T) {
	// The flag is removed wherever it appears; other args keep their order
	args, noFetch := popNoFetch([]string{"feature", "--no-fetch", "--from", "main"})
	if !noFetch || strings.Join(args, " ") != "feature --from main" {
		t.Errorf("popNoFetch = %v, %v; want [feature --from main], true", args, noFetch)
	}
	if _, noFetch := popNoFetch([]string{"feature"}); noFetch {
		t.Error("popNoFetch reported --no-fetch when absent")
	}
}
