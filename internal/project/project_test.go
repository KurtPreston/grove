package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// runGit runs git in dir, failing the test on error. A fixed identity is set so
// the initial commit works regardless of the host's global git config.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir,
		"-c", "user.email=grove@test",
		"-c", "user.name=grove",
		"-c", "init.defaultBranch=main",
	}, args...)
	cmd := exec.Command("git", full...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

// newTestProject builds a grove-shaped project (bare .base + worktree folders)
// under a temp dir and returns it. It seeds one commit so worktrees can be added.
func newTestProject(t *testing.T, branches ...string) *Project {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, src, "init", "-q")
	runGit(t, src, "commit", "-q", "--allow-empty", "-m", "init")

	proj := filepath.Join(root, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(proj, ".base")
	runGit(t, proj, "clone", "-q", "--bare", src, base)

	p := &Project{Base: base, Dir: proj}
	for _, b := range branches {
		runGit(t, base, "worktree", "add", "-q", "-b", b, filepath.Join(proj, b))
	}
	return p
}

func branchNames(wts []Worktree) map[string]Worktree {
	m := make(map[string]Worktree, len(wts))
	for _, w := range wts {
		if w.Branch != "" {
			m[w.Branch] = w
		}
	}
	return m
}

func TestWorktreesMarksDeletedDirPrunable(t *testing.T) {
	p := newTestProject(t, "feature-a", "feature-b")

	wts, err := p.Worktrees()
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	if w := branchNames(wts)["feature-a"]; w.Prunable {
		t.Fatalf("feature-a should not be prunable while its folder exists")
	}

	if err := os.RemoveAll(filepath.Join(p.Dir, "feature-a")); err != nil {
		t.Fatal(err)
	}

	wts, err = p.Worktrees()
	if err != nil {
		t.Fatalf("Worktrees after rm: %v", err)
	}
	by := branchNames(wts)
	if !by["feature-a"].Prunable {
		t.Errorf("feature-a should be prunable after its folder is deleted")
	}
	if by["feature-b"].Prunable {
		t.Errorf("feature-b should not be prunable; its folder still exists")
	}
}

func TestReconcileStalePrunesDeletedDirAndKeepsBranch(t *testing.T) {
	p := newTestProject(t, "feature-a", "feature-b")

	if got := p.ReconcileStale(); got != nil {
		t.Fatalf("nothing deleted yet, want no reconcile, got %v", got)
	}

	if err := os.RemoveAll(filepath.Join(p.Dir, "feature-a")); err != nil {
		t.Fatal(err)
	}

	got := p.ReconcileStale()
	if len(got) != 1 || got[0] != "feature-a" {
		t.Fatalf("ReconcileStale = %v, want [feature-a]", got)
	}

	// The stale entry is gone from git's view...
	if _, ok := branchNames(mustWorktrees(t, p))["feature-a"]; ok {
		t.Errorf("feature-a worktree should no longer be listed after reconcile")
	}
	// ...but the live worktree remains...
	if _, ok := branchNames(mustWorktrees(t, p))["feature-b"]; !ok {
		t.Errorf("feature-b worktree should still be listed")
	}
	// ...and the branch ref is kept (grove never deletes branches).
	if !GitQuiet(p.Base, "show-ref", "--verify", "--quiet", "refs/heads/feature-a") {
		t.Errorf("branch ref feature-a should be kept after reconcile")
	}

	// Idempotent: a second call finds nothing to do.
	if got := p.ReconcileStale(); got != nil {
		t.Errorf("second ReconcileStale should be a no-op, got %v", got)
	}
}

func mustWorktrees(t *testing.T, p *Project) []Worktree {
	t.Helper()
	wts, err := p.Worktrees()
	if err != nil {
		t.Fatalf("Worktrees: %v", err)
	}
	return wts
}
