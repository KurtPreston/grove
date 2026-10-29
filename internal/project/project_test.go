package project

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// runBaseGit runs git against the bare repository at base. It passes --git-dir
// explicitly, as grove does, so tests also pass under safe.bareRepository=explicit.
func runBaseGit(t *testing.T, base string, args ...string) {
	t.Helper()
	runGit(t, base, append([]string{"--git-dir=."}, args...)...)
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
		runBaseGit(t, base, "worktree", "add", "-q", "-b", b, filepath.Join(proj, b))
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

func TestCommitTimes(t *testing.T) {
	p := newTestProject(t, "older", "newer")
	commitDated(t, filepath.Join(p.Dir, "older"), "2020-01-01 00:00:00 +0000", "old.txt", "old")
	commitDated(t, filepath.Join(p.Dir, "newer"), "2021-06-15 12:00:00 +0000", "new.txt", "new")

	wts := mustWorktrees(t, p)
	by := branchNames(wts)
	times := p.CommitTimes([]string{by["older"].Head, by["newer"].Head, by["older"].Head, ""})

	gotOlder := times[by["older"].Head].UTC()
	gotNewer := times[by["newer"].Head].UTC()
	if !gotOlder.Equal(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("older committer time = %v, want 2020-01-01 00:00:00 UTC", gotOlder)
	}
	if !gotNewer.Equal(time.Date(2021, 6, 15, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("newer committer time = %v, want 2021-06-15 12:00:00 UTC", gotNewer)
	}
	if len(times) != 2 {
		t.Errorf("CommitTimes returned %d entries, want 2 (duplicates and empty SHAs skipped)", len(times))
	}
}

func commitDated(t *testing.T, dir, when, name, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(msg), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", name)
	cmd := exec.Command("git", "-C", dir,
		"-c", "user.email=grove@test",
		"-c", "user.name=grove",
		"-c", "commit.gpgsign=false",
		"commit", "-q", "-m", msg)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE="+when,
		"GIT_COMMITTER_DATE="+when,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("dated commit in %s failed: %v\n%s", dir, err, out)
	}
}

func TestReconcileStalePrunesDeletedDirAndKeepsBranch(t *testing.T) {
	p := newTestProject(t, "feature-a", "feature-b")

	if got, _ := p.ReconcileStale(); got != nil {
		t.Fatalf("nothing deleted yet, want no reconcile, got %v", got)
	}

	if err := os.RemoveAll(filepath.Join(p.Dir, "feature-a")); err != nil {
		t.Fatal(err)
	}

	got, live := p.ReconcileStale()
	if len(got) != 1 || got[0] != "feature-a" {
		t.Fatalf("ReconcileStale = %v, want [feature-a]", got)
	}

	// The returned list already reflects the reconcile, matching git's view
	if len(live) != len(mustWorktrees(t, p)) {
		t.Errorf("returned worktrees %v should match git's post-reconcile list", live)
	}
	if _, ok := branchNames(live)["feature-a"]; ok {
		t.Errorf("returned worktrees should not include the reconciled feature-a")
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
	if !GitQuiet(p.Base, "--git-dir=.", "show-ref", "--verify", "--quiet", "refs/heads/feature-a") {
		t.Errorf("branch ref feature-a should be kept after reconcile")
	}

	// Idempotent: a second call finds nothing to do.
	if got, _ := p.ReconcileStale(); got != nil {
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
// so a working repo's .git directory stands in for the bare .base here while
// commits and merges use its worktree. The local identity/gpgsign config matters because
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
	return &Project{Base: filepath.Join(dir, ".git"), Dir: dir}
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
	base := p.Dir
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

// TestWorktreeWithGitLikeFiles verifies that project files named HEAD and
// objects/ in a worktree don't make grove treat it as a bare repository. Git
// commands there must still use the worktree's own .git file.
func TestWorktreeWithGitLikeFiles(t *testing.T) {
	p := newTestProject(t)
	dir, _, err := p.EnsureWorktree(EnsureWorktreeOptions{Branch: "feature"})
	if err != nil {
		t.Fatal(err)
	}

	// Project files must not redirect Git away from the worktree's .git file.
	if err := os.WriteFile(filepath.Join(dir, "HEAD"), []byte("project data\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := WorktreeDirtiness(context.Background(), dir); got != Dirty {
		t.Errorf("worktree with project files = %v, want Dirty", got)
	}
	if got := strings.TrimSpace(mustGitOut(t, dir, "status", "--porcelain")); got != "?? HEAD" {
		t.Errorf("status = %q, want untracked HEAD", got)
	}
	if err := Git(dir, "add", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if !GitQuiet(dir, "ls-files", "--error-unmatch", "HEAD") {
		t.Error("HEAD was not staged in the worktree")
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

// TestCloneKeepsOnlyDefaultBranchLocal verifies clone doesn't copy every origin
// branch into refs/heads, as a plain bare clone does; the others exist only as
// origin/* refs.
func TestCloneKeepsOnlyDefaultBranchLocal(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	// Origin has a default branch plus two feature branches
	root := t.TempDir()
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, src, "init", "-q", "-b", "trunk")
	runGit(t, src, "commit", "-q", "--allow-empty", "-m", "init")
	runGit(t, src, "branch", "feat1")
	runGit(t, src, "branch", "feat2")

	p, _, branch, err := Clone(src, filepath.Join(root, "proj"), nil)
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if branch != "trunk" {
		t.Fatalf("default branch = %q, want trunk", branch)
	}

	// Only the default branch is local; the features are remote-tracking
	if got := p.LocalBranches(); len(got) != 1 || got[0] != "trunk" {
		t.Errorf("local branches = %v, want [trunk]", got)
	}
	origin := map[string]bool{}
	for _, b := range p.OriginBranches() {
		origin[b] = true
	}
	for _, b := range []string{"trunk", "feat1", "feat2"} {
		if !origin[b] {
			t.Errorf("origin/%s missing; origin branches = %v", b, p.OriginBranches())
		}
	}
}

// TestBranchListSkipsOriginHEAD checks that the origin/HEAD alias doesn't
// surface as a branch named "origin", and that slashed remote branches keep
// their full name.
func TestBranchListSkipsOriginHEAD(t *testing.T) {
	p := newTestProject(t)

	// Fake a fetched origin with a symbolic HEAD and a slashed branch
	runBaseGit(t, p.Base, "update-ref", "refs/remotes/origin/remote-only", "HEAD")
	runBaseGit(t, p.Base, "update-ref", "refs/remotes/origin/feature/x", "HEAD")
	runBaseGit(t, p.Base, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/remote-only")

	// Collect the list and check for the alias and the real branches
	got := map[string]bool{}
	for _, b := range p.BranchList() {
		got[b] = true
	}
	for _, bad := range []string{"origin", "HEAD", "origin/HEAD"} {
		if got[bad] {
			t.Errorf("BranchList() includes %q: %v", bad, p.BranchList())
		}
	}
	for _, want := range []string{"remote-only", "feature/x"} {
		if !got[want] {
			t.Errorf("BranchList() missing %q: %v", want, p.BranchList())
		}
	}
}

func TestWorktreeDirtiness(t *testing.T) {
	p := newTestProject(t, "feature")
	dir := filepath.Join(p.Dir, "feature")
	ctx := context.Background()

	// A fresh worktree is clean; an untracked file makes it dirty
	if got := WorktreeDirtiness(ctx, dir); got != Clean {
		t.Fatalf("fresh worktree = %v, want Clean", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := WorktreeDirtiness(ctx, dir); got != Dirty {
		t.Fatalf("worktree with untracked file = %v, want Dirty", got)
	}

	// A missing directory can't be checked
	if got := WorktreeDirtiness(ctx, filepath.Join(p.Dir, "nope")); got != DirtyUnknown {
		t.Fatalf("missing dir = %v, want DirtyUnknown", got)
	}

	// While another git holds the index lock, skip the check rather than pile on
	gitDir := strings.TrimSpace(mustGitOut(t, dir, "rev-parse", "--absolute-git-dir"))
	lock := filepath.Join(gitDir, "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := WorktreeDirtiness(ctx, dir); got != DirtyUnknown {
		t.Fatalf("locked index = %v, want DirtyUnknown", got)
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
}

// TestWorktreeDirtinessTimeoutLetsGitFinish checks that a status whose deadline
// passes reports DirtyUnknown without killing git: the abandoned status runs to
// completion (so its index refresh warms the next listing) and leaves no
// index.lock behind.
func TestWorktreeDirtinessTimeoutLetsGitFinish(t *testing.T) {
	p := newTestProject(t, "feature")
	dir := filepath.Join(p.Dir, "feature")
	gitDir := strings.TrimSpace(mustGitOut(t, dir, "rev-parse", "--absolute-git-dir"))

	// An already-expired deadline gives up immediately
	ctx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	if got := WorktreeDirtiness(ctx, dir); got != DirtyUnknown {
		t.Fatalf("expired deadline = %v, want DirtyUnknown", got)
	}

	// The abandoned status still finishes and releases its lock
	lock := filepath.Join(gitDir, "index.lock")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(lock); errors.Is(err, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("index.lock still present after abandoned status")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := WorktreeDirtiness(context.Background(), dir); got != Clean {
		t.Fatalf("status after abandoned run = %v, want Clean", got)
	}
}

func mustGitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := GitOut(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

// TestPullLatestFetchesOnlyUpstream verifies PullLatest fast-forwards a
// worktree to its upstream without fetching the remote's other branches.
func TestPullLatestFetchesOnlyUpstream(t *testing.T) {
	p := newTestProject(t)
	src := filepath.Join(filepath.Dir(p.Dir), "src")

	// Track origin like a grove clone, with a main worktree and a second branch.
	runGit(t, src, "branch", "other")
	runBaseGit(t, p.Base, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	runBaseGit(t, p.Base, "fetch", "-q", "origin")
	dir := filepath.Join(p.Dir, "main")
	runBaseGit(t, p.Base, "worktree", "add", "-q", dir, "main")
	runBaseGit(t, p.Base, "config", "branch.main.remote", "origin")
	runBaseGit(t, p.Base, "config", "branch.main.merge", "refs/heads/main")
	staleOther := baseRevParse(t, p.Base, "refs/remotes/origin/other")

	// Advance both branches on origin.
	runGit(t, src, "commit", "-q", "--allow-empty", "-m", "main moves")
	runGit(t, src, "checkout", "-q", "other")
	runGit(t, src, "commit", "-q", "--allow-empty", "-m", "other moves")
	runGit(t, src, "checkout", "-q", "main")

	// main fast-forwards; origin/other is left untouched.
	if !PullLatest(dir, "main") {
		t.Fatal("PullLatest(main) = false, want true")
	}
	if got, want := revParse(t, dir, "HEAD"), revParse(t, src, "main"); got != want {
		t.Errorf("worktree HEAD = %s, want origin main %s", got, want)
	}
	if got := baseRevParse(t, p.Base, "refs/remotes/origin/other"); got != staleOther {
		t.Errorf("origin/other = %s, want unfetched %s", got, staleOther)
	}

	// A diverged worktree is left alone rather than merged.
	runGit(t, dir, "commit", "-q", "--allow-empty", "-m", "local work")
	runGit(t, src, "commit", "-q", "--allow-empty", "-m", "main moves again")
	if PullLatest(dir, "main") {
		t.Error("PullLatest on a diverged branch = true, want false")
	}

	// A branch with no upstream reports false without touching anything.
	runBaseGit(t, p.Base, "config", "--unset", "branch.main.remote")
	if PullLatest(dir, "main") {
		t.Error("PullLatest with no upstream = true, want false")
	}
}
