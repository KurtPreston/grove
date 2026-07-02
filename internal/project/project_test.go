package project

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// newMergeRepo builds a plain (non-bare) repo with one commit on main and a
// persisted git identity. BranchSquashMerged only shells out against the repo,
// so a working repo — where commits and merges are easy to script — stands in
// for the bare .base here. The local identity/gpgsign config matters because
// BranchSquashMerged's own `commit-tree` call runs without grove's test flags.
func newMergeRepo(t *testing.T) *Project {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "grove@test")
	runGit(t, dir, "config", "user.name", "grove")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	return &Project{Base: dir, Dir: dir}
}

// commitFile writes name in repo and commits it. Each branch below touches a
// distinct file so merges and cherry-picks never conflict.
func commitFile(t *testing.T, repo, name, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", name)
	runGit(t, repo, "commit", "-q", "-m", msg)
}

// TestBranchSquashMerged covers the patch-equivalence detection that catches
// history-rewriting merges `git branch --merged` misses. The interesting cases
// are branches whose tip is NOT an ancestor of the target yet whose net diff is
// already present there (squash and rebase merges).
func TestBranchSquashMerged(t *testing.T) {
	p := newMergeRepo(t)
	base := p.Base
	commitFile(t, base, "base.txt", "0", "c0")

	// Squash-merge: the branch's change lands on main as one new commit; the
	// branch tip is not an ancestor of main.
	runGit(t, base, "checkout", "-q", "-b", "feat-squash")
	commitFile(t, base, "squash.txt", "s", "s1")
	runGit(t, base, "checkout", "-q", "main")
	runGit(t, base, "merge", "--squash", "feat-squash")
	runGit(t, base, "commit", "-q", "-m", "squash feat-squash")

	// Rebase-merge: cherry-pick reproduces the same patch under a new hash. An
	// unrelated commit on main first gives the replayed commit a new parent, so
	// the branch tip is genuinely not an ancestor of main (otherwise the replay
	// would collapse to the identical commit and look like a plain merge).
	runGit(t, base, "checkout", "-q", "-b", "feat-rebase", "main")
	commitFile(t, base, "rebase.txt", "r", "r1")
	runGit(t, base, "checkout", "-q", "main")
	commitFile(t, base, "mainx.txt", "x", "unrelated main commit")
	runGit(t, base, "cherry-pick", "feat-rebase")

	// Never merged: the branch's change is absent from main.
	runGit(t, base, "checkout", "-q", "-b", "feat-open", "main")
	commitFile(t, base, "open.txt", "o", "o1")

	// Partially merged: two commits on the branch, only the first landed on
	// main, so the branch's combined diff is not present upstream.
	runGit(t, base, "checkout", "-q", "-b", "feat-partial", "main")
	commitFile(t, base, "p1.txt", "1", "p1")
	commitFile(t, base, "p2.txt", "2", "p2")
	runGit(t, base, "checkout", "-q", "main")
	runGit(t, base, "cherry-pick", "feat-partial~1")

	runGit(t, base, "checkout", "-q", "main")

	cases := []struct {
		branch string
		want   bool
	}{
		{"feat-squash", true},
		{"feat-rebase", true},
		{"feat-open", false},
		{"feat-partial", false},
	}
	for _, c := range cases {
		if got := p.BranchSquashMerged(c.branch, "main"); got != c.want {
			t.Errorf("BranchSquashMerged(%q, main) = %v, want %v", c.branch, got, c.want)
		}
	}
}

// gitConfig reads a single config value from base, returning "" when unset. Used
// to assert the branch.<name>.remote/merge that make a branch pushable.
func gitConfig(t *testing.T, base, key string) string {
	t.Helper()
	out, err := GitOut(base, "config", "--get", key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// assertTracksOrigin asserts branch is configured to push/pull against
// origin/<branch> — the tracking grove sets so a plain `git push` needs no
// --set-upstream.
func assertTracksOrigin(t *testing.T, base, branch string) {
	t.Helper()
	if got := gitConfig(t, base, "branch."+branch+".remote"); got != "origin" {
		t.Errorf("branch.%s.remote = %q, want origin", branch, got)
	}
	if got := gitConfig(t, base, "branch."+branch+".merge"); got != "refs/heads/"+branch {
		t.Errorf("branch.%s.merge = %q, want refs/heads/%s", branch, got, branch)
	}
}

// TestEnsureWorktreeSetsUpstream covers the two creation paths that historically
// left a branch with no upstream (so `git push` failed): a brand-new branch and a
// pre-existing local branch. Both should end up tracking origin/<branch>.
func TestEnsureWorktreeSetsUpstream(t *testing.T) {
	p := newTestProject(t)

	// Path: brand-new branch off the default.
	if _, _, err := p.EnsureWorktree("feature/new", nil); err != nil {
		t.Fatalf("EnsureWorktree(feature/new): %v", err)
	}
	assertTracksOrigin(t, p.Base, "feature/new")

	// Path: a local branch that already exists in the bare repo with no tracking
	// config (e.g. carried over from a full clone).
	runGit(t, p.Base, "branch", "local/only", "main")
	if _, _, err := p.EnsureWorktree("local/only", nil); err != nil {
		t.Fatalf("EnsureWorktree(local/only): %v", err)
	}
	assertTracksOrigin(t, p.Base, "local/only")
}

// TestEnsureWorktreeKeepsExistingUpstream verifies grove never clobbers an
// upstream a branch was already deliberately configured to track.
func TestEnsureWorktreeKeepsExistingUpstream(t *testing.T) {
	p := newTestProject(t)
	runGit(t, p.Base, "branch", "tracked", "main")
	runGit(t, p.Base, "config", "branch.tracked.remote", "upstream")
	runGit(t, p.Base, "config", "branch.tracked.merge", "refs/heads/somewhere")

	if _, _, err := p.EnsureWorktree("tracked", nil); err != nil {
		t.Fatalf("EnsureWorktree(tracked): %v", err)
	}
	if got := gitConfig(t, p.Base, "branch.tracked.remote"); got != "upstream" {
		t.Errorf("branch.tracked.remote = %q, want upstream (pre-existing, not clobbered)", got)
	}
	if got := gitConfig(t, p.Base, "branch.tracked.merge"); got != "refs/heads/somewhere" {
		t.Errorf("branch.tracked.merge = %q, want refs/heads/somewhere (pre-existing, not clobbered)", got)
	}
}

// TestCloneSetsUpstreamOnDefaultBranch verifies the default-branch worktree
// created by clone is pushable: a bare clone leaves no branch tracking config, so
// grove wires it up itself.
func TestCloneSetsUpstreamOnDefaultBranch(t *testing.T) {
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

	p, _, branch, err := Clone(src, filepath.Join(root, "proj"), nil)
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	assertTracksOrigin(t, p.Base, branch)
}
