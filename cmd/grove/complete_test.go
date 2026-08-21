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
	// `grove DIR` works anywhere, so paths are still on offer; branches are not.
	out := captureComplete(t, t.TempDir(), "sa")
	if out != pathSentinel {
		t.Fatalf("expected only %q outside a grove project, got %q", pathSentinel, out)
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

func TestCompleteLaunchPathSentinel(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	_, wt := setupCompleteProject(t)
	out := captureComplete(t, wt, "launch", "")
	if out != pathSentinel {
		t.Fatalf("expected %q, got %q", pathSentinel, out)
	}
}

func TestCompleteBareWordOffersPaths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	_, wt := setupCompleteProject(t)
	out := captureComplete(t, wt, "../../")
	if out != pathSentinel {
		t.Fatalf("expected %q, got %q", pathSentinel, out)
	}
}

func TestCompleteBareWordOffersPathsAlongsideBranches(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	_, wt := setupCompleteProject(t)
	lines := strings.Split(captureComplete(t, wt, "salsa"), "\n")
	if lines[0] != pathSentinel {
		t.Fatalf("expected %q first, got %q", pathSentinel, lines)
	}
	if !strings.Contains(strings.Join(lines, "\n"), "salsa-feature") {
		t.Fatalf("expected salsa-feature in %q", lines)
	}
}

func TestCompleteBareEmptyWordOmitsPaths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	_, wt := setupCompleteProject(t)
	out := captureComplete(t, wt, "")
	if strings.Contains(out, pathSentinel) {
		t.Fatalf("did not expect %q for the empty word, got %q", pathSentinel, out)
	}
	if !strings.Contains(out, "open") {
		t.Fatalf("expected subcommands in %q", out)
	}
}

func TestCompleteBranchArgOmitsPaths(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	_, wt := setupCompleteProject(t)
	out := captureComplete(t, wt, "open", "../")
	if out != "" {
		t.Fatalf("expected no candidates for a path after open, got %q", out)
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

func TestCompleteListFlags(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	_, wt := setupCompleteProject(t)
	out := captureComplete(t, wt, "list", "-")
	if !strings.Contains(out, "-t") || !strings.Contains(out, "--porcelain") {
		t.Fatalf("expected list flags, got %q", out)
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
