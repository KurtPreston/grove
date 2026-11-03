package project

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// newSparseProject builds a test project whose "seed" branch holds files in
// several folders, so tests can tell which ones a worktree checked out.
func newSparseProject(t *testing.T) *Project {
	t.Helper()
	p := newTestProject(t, "seed")
	seed := filepath.Join(p.Dir, "seed")

	// Commit one file at the root and one in each of three folders
	for _, f := range []string{"root.txt", "apps/web/a.txt", "apps/api/b.txt", "packages/ui/c.txt"} {
		path := filepath.Join(seed, f)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(f+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, seed, "add", ".")
	runGit(t, seed, "commit", "-q", "-m", "files")
	return p
}

// fromSeed bases new branches off the seeded branch.
func fromSeed(string) (string, error) { return "seed", nil }

// assertFiles checks which of the seeded files exist in dir.
func assertFiles(t *testing.T, dir string, want map[string]bool) {
	t.Helper()
	for f, present := range want {
		_, err := os.Stat(filepath.Join(dir, f))
		if got := err == nil; got != present {
			t.Errorf("%s present = %v, want %v", f, got, present)
		}
	}
}

// createWorktree creates branch off seed with the given sparse folders.
func createWorktree(t *testing.T, p *Project, branch string, sparse []string) string {
	t.Helper()
	dir, created, err := p.EnsureWorktree(EnsureWorktreeOptions{Branch: branch, PickBase: fromSeed, NoFetch: true, Sparse: sparse})
	if err != nil || !created {
		t.Fatalf("EnsureWorktree(%s) = %v, created %v", branch, err, created)
	}
	return dir
}

func TestEnsureWorktreeSparse(t *testing.T) {
	p := newSparseProject(t)

	// A brand-new branch checks out only the chosen folder plus root files
	dir := createWorktree(t, p, "feat", []string{"apps/web"})
	assertFiles(t, dir, map[string]bool{"root.txt": true, "apps/web/a.txt": true, "apps/api/b.txt": false, "packages/ui/c.txt": false})
	if !p.WorktreeClean(dir) {
		t.Error("sparse worktree has local changes right after creation")
	}
	if got := SparsePaths(dir); !reflect.DeepEqual(got, []string{"apps/web"}) {
		t.Errorf("SparsePaths = %v, want [apps/web]", got)
	}
	assertTracksOrigin(t, p.Base, "feat")

	// An existing local branch takes the same sparse path
	runBaseGit(t, p.Base, "branch", "local", "seed")
	local := createWorktree(t, p, "local", []string{"packages/ui"})
	assertFiles(t, local, map[string]bool{"packages/ui/c.txt": true, "apps/web/a.txt": false})

	// Other worktrees stay full
	assertFiles(t, filepath.Join(p.Dir, "seed"), map[string]bool{"apps/api/b.txt": true})
}

func TestEnsureWorktreeFullByDefault(t *testing.T) {
	p := newSparseProject(t)
	dir := createWorktree(t, p, "feat", nil)
	assertFiles(t, dir, map[string]bool{"apps/web/a.txt": true, "apps/api/b.txt": true})
	if got := SparsePaths(dir); got != nil {
		t.Errorf("SparsePaths = %v, want nil for a full checkout", got)
	}
}

func TestSetSparseWidensToFull(t *testing.T) {
	p := newSparseProject(t)
	dir := createWorktree(t, p, "feat", []string{"apps/web"})

	// Widening is allowed even with local changes
	if err := os.WriteFile(filepath.Join(dir, "apps/web/a.txt"), []byte("edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.SetSparse(dir, nil); err != nil {
		t.Fatalf("SetSparse(full): %v", err)
	}
	assertFiles(t, dir, map[string]bool{"apps/api/b.txt": true, "packages/ui/c.txt": true})
	if got := SparsePaths(dir); got != nil {
		t.Errorf("SparsePaths = %v after widening, want nil", got)
	}
}

func TestSetSparseNarrowingNeedsCleanWorktree(t *testing.T) {
	p := newSparseProject(t)
	dir := createWorktree(t, p, "feat", nil)

	// A dirty full worktree refuses to narrow and keeps its files
	edited := filepath.Join(dir, "apps/api/b.txt")
	if err := os.WriteFile(edited, []byte("edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.SetSparse(dir, []string{"apps/web"}); err == nil {
		t.Fatal("SetSparse narrowed a worktree with local changes")
	}
	assertFiles(t, dir, map[string]bool{"apps/api/b.txt": true})

	// Once clean, it narrows
	runGit(t, dir, "checkout", "--", ".")
	if err := p.SetSparse(dir, []string{"apps/web"}); err != nil {
		t.Fatalf("SetSparse on a clean worktree: %v", err)
	}
	assertFiles(t, dir, map[string]bool{"apps/web/a.txt": true, "apps/api/b.txt": false})
}

func TestSetSparseBetweenSparseSets(t *testing.T) {
	p := newSparseProject(t)
	dir := createWorktree(t, p, "feat", []string{"apps/web"})
	if err := os.WriteFile(filepath.Join(dir, "apps/web/a.txt"), []byte("edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Adding folders is allowed with local changes, including a parent folder
	if err := p.SetSparse(dir, []string{"apps/web", "packages/ui"}); err != nil {
		t.Fatalf("SetSparse(add folder): %v", err)
	}
	if err := p.SetSparse(dir, []string{"apps", "packages/ui"}); err != nil {
		t.Fatalf("SetSparse(parent folder): %v", err)
	}
	assertFiles(t, dir, map[string]bool{"apps/api/b.txt": true, "packages/ui/c.txt": true})

	// Dropping a folder while dirty is refused
	if err := p.SetSparse(dir, []string{"apps"}); err == nil {
		t.Fatal("SetSparse dropped a folder from a worktree with local changes")
	}
	assertFiles(t, dir, map[string]bool{"packages/ui/c.txt": true})
}

func TestNormalizeSparse(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{nil, nil},
		{[]string{"", " "}, nil},
		{[]string{"/apps/web/", "apps/web"}, []string{"apps/web"}},
		{[]string{"apps/web, ci", "bazel"}, []string{"apps/web", "ci", "bazel"}},
	}
	for _, c := range cases {
		if got := NormalizeSparse(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("NormalizeSparse(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
