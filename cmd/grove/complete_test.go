package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func captureComplete(t *testing.T, dir string, args ...string) string {
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
	t.Cleanup(func() { os.Stdout = oldOut })

	cmdComplete(args)

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(buf.String())
}

func setupCompleteProject(t *testing.T) (projectDir, worktreeDir string) {
	t.Helper()
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

	worktree := filepath.Join(proj, "main")
	gitDo(t, base, "worktree", "add", worktree, "main")

	gitDo(t, worktree, "checkout", "-q", "-b", "salsa-feature")
	gitCommit(t, worktree, "feat.txt", "1", "c1")
	gitDo(t, worktree, "checkout", "-q", "main")

	gitDo(t, base, "update-ref", "refs/remotes/origin/main", "main")
	gitDo(t, base, "update-ref", "refs/remotes/origin/remote-only", "main")

	return proj, worktree
}

func TestCompleteOutsideProject(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	out := captureComplete(t, t.TempDir(), "sa")
	if out != "" {
		t.Fatalf("expected no output outside grove project, got %q", out)
	}
}

func TestCompleteFirstWordBranches(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	_, wt := setupCompleteProject(t)
	out := captureComplete(t, wt, "salsa")
	if !strings.Contains(out, "salsa-feature") {
		t.Fatalf("expected salsa-feature in %q", out)
	}
	if strings.Contains(out, "remote-only") {
		t.Fatalf("did not expect remote-only to match salsa prefix, got %q", out)
	}
}

func TestCompleteFirstWordIncludesRemoteBranches(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	_, wt := setupCompleteProject(t)
	out := captureComplete(t, wt, "remote")
	if !strings.Contains(out, "remote-only") {
		t.Fatalf("expected remote-only in %q", out)
	}
}

func TestCompleteOpenBranchArg(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	_, wt := setupCompleteProject(t)
	out := captureComplete(t, wt, "open", "salsa")
	if out != "salsa-feature" {
		t.Fatalf("expected salsa-feature, got %q", out)
	}
}

func TestCompleteFromFlag(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	_, wt := setupCompleteProject(t)
	out := captureComplete(t, wt, "open", "salsa-feature", "--from", "main")
	if out != "main" {
		t.Fatalf("expected main, got %q", out)
	}
}

func TestCompleteLaunchFilesSentinel(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	_, wt := setupCompleteProject(t)
	out := captureComplete(t, wt, "launch", "")
	if out != filesSentinel {
		t.Fatalf("expected %q, got %q", filesSentinel, out)
	}
}

func TestCompleteFlags(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	_, wt := setupCompleteProject(t)
	out := captureComplete(t, wt, "open", "salsa-feature", "--")
	if !strings.Contains(out, "--from") || !strings.Contains(out, "--force") {
		t.Fatalf("expected open flags, got %q", out)
	}
}

func TestCompleteSubcommands(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	_, wt := setupCompleteProject(t)
	out := captureComplete(t, wt, "op")
	if !strings.Contains(out, "open") {
		t.Fatalf("expected open subcommand, got %q", out)
	}
}
