package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gitConfig reads a single config value from base, returning "" when unset. Used
// to assert the branch.<name>.remote/merge that make a branch pushable.
func gitConfig(t *testing.T, base, key string) string {
	t.Helper()
	out, err := GitOut(base, "--git-dir=.", "config", "--get", key)
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
	if _, _, err := p.EnsureWorktree(EnsureWorktreeOptions{Branch: "feature/new", PickBase: baseDefault}); err != nil {
		t.Fatalf("EnsureWorktree(feature/new): %v", err)
	}
	assertTracksOrigin(t, p.Base, "feature/new")

	// Path: a local branch that already exists in the bare repo with no tracking
	// config (e.g. carried over from a full clone).
	runBaseGit(t, p.Base, "branch", "local/only", "main")
	if _, _, err := p.EnsureWorktree(EnsureWorktreeOptions{Branch: "local/only", PickBase: baseDefault}); err != nil {
		t.Fatalf("EnsureWorktree(local/only): %v", err)
	}
	assertTracksOrigin(t, p.Base, "local/only")
}

// baseDefault is a pickBase resolver that always keeps grove's historical
// behavior of basing a new branch off the project default branch.
func baseDefault(def string) (string, error) { return def, nil }

// TestEnsureWorktreeKeepsExistingUpstream verifies grove never clobbers an
// upstream a branch was already deliberately configured to track.
func TestEnsureWorktreeKeepsExistingUpstream(t *testing.T) {
	p := newTestProject(t)
	runBaseGit(t, p.Base, "branch", "tracked", "main")
	runBaseGit(t, p.Base, "config", "branch.tracked.remote", "upstream")
	runBaseGit(t, p.Base, "config", "branch.tracked.merge", "refs/heads/somewhere")

	if _, _, err := p.EnsureWorktree(EnsureWorktreeOptions{Branch: "tracked", PickBase: baseDefault}); err != nil {
		t.Fatalf("EnsureWorktree(tracked): %v", err)
	}
	if got := gitConfig(t, p.Base, "branch.tracked.remote"); got != "upstream" {
		t.Errorf("branch.tracked.remote = %q, want upstream (pre-existing, not clobbered)", got)
	}
	if got := gitConfig(t, p.Base, "branch.tracked.merge"); got != "refs/heads/somewhere" {
		t.Errorf("branch.tracked.merge = %q, want refs/heads/somewhere (pre-existing, not clobbered)", got)
	}
}

// TestEnsureWorktreeFixesBaseBranchUpstream verifies grove repoints a stale
// upstream left pointing at a *different* branch on origin (the ref the branch was
// forked from) back to the branch's own same-named origin branch. With
// push.default=upstream/tracking such an upstream silently redirects a bare
// `git push` to that base branch (often a protected one), so grove corrects it —
// unlike the deliberate different-remote case above, which it preserves.
func TestEnsureWorktreeFixesBaseBranchUpstream(t *testing.T) {
	p := newTestProject(t)
	runBaseGit(t, p.Base, "branch", "feature/forked", "main")
	runBaseGit(t, p.Base, "config", "branch.feature/forked.remote", "origin")
	runBaseGit(t, p.Base, "config", "branch.feature/forked.merge", "refs/heads/main")

	if _, _, err := p.EnsureWorktree(EnsureWorktreeOptions{Branch: "feature/forked", PickBase: baseDefault}); err != nil {
		t.Fatalf("EnsureWorktree(feature/forked): %v", err)
	}
	assertTracksOrigin(t, p.Base, "feature/forked")
}

// revParse resolves a rev in dir to its full object id, failing the test on error.
func revParse(t *testing.T, dir, rev string) string {
	t.Helper()
	out, err := GitOut(dir, "rev-parse", rev)
	if err != nil {
		t.Fatalf("rev-parse %s: %v", rev, err)
	}
	return strings.TrimSpace(out)
}

// baseRevParse resolves rev in the bare repository at base, addressing it
// explicitly so it works under safe.bareRepository=explicit.
func baseRevParse(t *testing.T, base, rev string) string {
	t.Helper()
	out, err := GitOut(base, "--git-dir=.", "rev-parse", rev)
	if err != nil {
		t.Fatalf("rev-parse %s: %v", rev, err)
	}
	return strings.TrimSpace(out)
}

// TestEnsureWorktreeBasesOffChosenBranch verifies a brand-new branch is created
// off the branch the pickBase resolver returns, not the project default. branch1
// is advanced past main first so "based off branch1" is observable, and the new
// branch must still track origin/<branch> so it stays pushable.
func TestEnsureWorktreeBasesOffChosenBranch(t *testing.T) {
	p := newTestProject(t, "branch1")

	// Advance branch1 beyond main via its worktree so the two tips differ.
	commitFile(t, filepath.Join(p.Dir, "branch1"), "b1.txt", "b1", "branch1 commit")

	base := func(def string) (string, error) { return "branch1", nil }
	if _, _, err := p.EnsureWorktree(EnsureWorktreeOptions{Branch: "feature/off-branch1", PickBase: base}); err != nil {
		t.Fatalf("EnsureWorktree(feature/off-branch1): %v", err)
	}

	newTip := baseRevParse(t, p.Base, "feature/off-branch1")
	if want := baseRevParse(t, p.Base, "branch1"); newTip != want {
		t.Errorf("new branch tip = %s, want branch1 tip %s", newTip, want)
	}
	if mainTip := baseRevParse(t, p.Base, "main"); newTip == mainTip {
		t.Errorf("new branch tip should differ from main (%s); it looks based off main, not branch1", mainTip)
	}
	assertTracksOrigin(t, p.Base, "feature/off-branch1")
}

// TestEnsureWorktreeBeforeCreateAbortsOnError verifies a beforeCreate hook
// that returns an error aborts creation entirely — the branch and worktree are
// never created — and that its error propagates to the caller. This is the
// grove.json beforeCreateBranch bucket's abort contract.
func TestEnsureWorktreeBeforeCreateAbortsOnError(t *testing.T) {
	p := newTestProject(t)
	wantErr := errors.New("ticket check failed")

	_, _, err := p.EnsureWorktree(EnsureWorktreeOptions{Branch: "feature/new", PickBase: baseDefault, BeforeCreate: func() error { return wantErr }})
	if !errors.Is(err, wantErr) {
		t.Fatalf("EnsureWorktree error = %v, want %v", err, wantErr)
	}
	if GitQuiet(p.Base, "--git-dir=.", "show-ref", "--verify", "--quiet", "refs/heads/feature/new") {
		t.Error("branch feature/new was created despite beforeCreate returning an error")
	}
	if pathExists(filepath.Join(p.Dir, "feature", "new")) {
		t.Error("worktree dir was created despite beforeCreate returning an error")
	}
}

// TestEnsureWorktreeRejectsFlagLikeBranch verifies an unrecognized flag such as
// --verbose is refused instead of silently becoming a new branch and worktree.
func TestEnsureWorktreeRejectsFlagLikeBranch(t *testing.T) {
	p := newTestProject(t)

	// Every dash-prefixed name errors before anything is created
	for _, name := range []string{"--verbose", "-x"} {
		if _, _, err := p.EnsureWorktree(EnsureWorktreeOptions{Branch: name, PickBase: baseDefault}); err == nil {
			t.Errorf("EnsureWorktree(%q) succeeded, want error", name)
		}
		if pathExists(filepath.Join(p.Dir, Sanitize(name))) {
			t.Errorf("worktree dir for %q was created", name)
		}
	}
}

// TestEnsureWorktreeBeforeCreateSkippedForExistingBranch verifies beforeCreate
// only gates brand-new branches: reusing a branch that already exists locally
// must not consult it, since there is nothing left to abort.
func TestEnsureWorktreeBeforeCreateSkippedForExistingBranch(t *testing.T) {
	p := newTestProject(t)
	runBaseGit(t, p.Base, "branch", "local/only", "main")

	called := false
	if _, _, err := p.EnsureWorktree(EnsureWorktreeOptions{
		Branch:   "local/only",
		PickBase: baseDefault,
		BeforeCreate: func() error {
			called = true
			return nil
		},
	}); err != nil {
		t.Fatalf("EnsureWorktree(local/only): %v", err)
	}
	if called {
		t.Error("beforeCreate was called for a pre-existing local branch")
	}
}

// TestResolveBaseRefUnknownBranchErrors ensures a bad base branch (e.g. a typo
// passed via --from) fails loudly instead of silently basing off HEAD.
func TestResolveBaseRefUnknownBranchErrors(t *testing.T) {
	p := newTestProject(t)
	if _, err := p.resolveBaseRef("does-not-exist"); err == nil {
		t.Errorf("resolveBaseRef(does-not-exist) = nil error, want an error")
	}
}

// TestEnsureWorktreeWithExplicitBareRepository verifies grove still works when
// git refuses implicit bare-repo discovery (safe.bareRepository=explicit), as in
// sandboxed agent shells; previously every `git -C .base` call failed there and
// a new branch reported its base branch as missing.
func TestEnsureWorktreeWithExplicitBareRepository(t *testing.T) {
	p := newTestProject(t)

	// Apply the restriction the same way such shells do: via env-supplied config
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "safe.bareRepository")
	t.Setenv("GIT_CONFIG_VALUE_0", "explicit")

	// A new branch must resolve main and land on main's tip
	dir, _, err := p.EnsureWorktree(EnsureWorktreeOptions{Branch: "feature"})
	if err != nil {
		t.Fatalf("EnsureWorktree(feature): %v", err)
	}
	if got, want := revParse(t, dir, "feature"), revParse(t, dir, "main"); got != want {
		t.Errorf("feature tip = %s, want main tip %s", got, want)
	}
}

// newClonedProject clones a fresh origin through Clone, so origin/* refs are
// laid out as in a real grove project, and returns the project and the origin
// repository's path.
func newClonedProject(t *testing.T) (*Project, string) {
	t.Helper()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, src, "init", "-q")
	runGit(t, src, "commit", "-q", "--allow-empty", "-m", "init")

	p, _, _, err := Clone(src, filepath.Join(root, "proj"), nil)
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	return p, src
}

// hasOriginRef reports whether the project has fetched origin/<branch>.
func hasOriginRef(p *Project, branch string) bool {
	return GitQuiet(p.Base, "--git-dir=.", "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+branch)
}

// TestEnsureWorktreeNoFetchUsesLocalRefs verifies NoFetch never contacts
// origin: a new branch starts from the stale local origin/main, and branches
// pushed to origin since the clone stay unfetched.
func TestEnsureWorktreeNoFetchUsesLocalRefs(t *testing.T) {
	p, src := newClonedProject(t)
	staleMain := baseRevParse(t, p.Base, "origin/main")

	// Origin moves on after the clone
	runGit(t, src, "commit", "-q", "--allow-empty", "-m", "upstream work")
	runGit(t, src, "branch", "teammate")

	opts := EnsureWorktreeOptions{Branch: "feature", PickBase: baseDefault, NoFetch: true}
	if _, _, err := p.EnsureWorktree(opts); err != nil {
		t.Fatalf("EnsureWorktree(feature): %v", err)
	}

	// The branch is based on what was already local, and nothing was fetched
	if got := baseRevParse(t, p.Base, "feature"); got != staleMain {
		t.Errorf("feature tip = %s, want stale origin/main %s", got, staleMain)
	}
	if got := baseRevParse(t, p.Base, "origin/main"); got != staleMain {
		t.Errorf("origin/main moved to %s; NoFetch should not fetch", got)
	}
	if hasOriginRef(p, "teammate") {
		t.Error("origin/teammate was fetched despite NoFetch")
	}
}

// TestEnsureWorktreeFetchesOnlyBaseBranch verifies a brand-new branch starts
// from origin's latest base branch, while origin's other branches are left
// unfetched.
func TestEnsureWorktreeFetchesOnlyBaseBranch(t *testing.T) {
	p, src := newClonedProject(t)

	// Origin gains a commit on main plus an unrelated branch
	runGit(t, src, "commit", "-q", "--allow-empty", "-m", "upstream work")
	runGit(t, src, "branch", "unrelated")

	if _, _, err := p.EnsureWorktree(EnsureWorktreeOptions{Branch: "feature", PickBase: baseDefault}); err != nil {
		t.Fatalf("EnsureWorktree(feature): %v", err)
	}

	// The new branch starts from origin's new main tip
	if got, want := baseRevParse(t, p.Base, "feature"), revParse(t, src, "main"); got != want {
		t.Errorf("feature tip = %s, want origin main tip %s", got, want)
	}
	assertTracksOrigin(t, p.Base, "feature")
	if hasOriginRef(p, "unrelated") {
		t.Error("origin/unrelated was fetched; only the base branch should be")
	}
}

// TestEnsureWorktreeTracksUnfetchedRemoteBranch verifies a teammate's branch
// pushed after the clone, so never fetched, is found on origin and tracked
// rather than recreated as a brand-new branch.
func TestEnsureWorktreeTracksUnfetchedRemoteBranch(t *testing.T) {
	p, src := newClonedProject(t)

	// A teammate pushes a branch with its own commit
	runGit(t, src, "checkout", "-q", "-b", "teammate")
	runGit(t, src, "commit", "-q", "--allow-empty", "-m", "teammate work")
	runGit(t, src, "checkout", "-q", "main")

	// Reusing a branch must not consult the new-branch callbacks
	opts := EnsureWorktreeOptions{
		Branch:       "teammate",
		PickBase:     func(string) (string, error) { return "", errors.New("PickBase called") },
		BeforeCreate: func() error { return errors.New("BeforeCreate called") },
	}
	if _, _, err := p.EnsureWorktree(opts); err != nil {
		t.Fatalf("EnsureWorktree(teammate): %v", err)
	}
	if got, want := baseRevParse(t, p.Base, "teammate"), revParse(t, src, "teammate"); got != want {
		t.Errorf("teammate tip = %s, want origin teammate tip %s", got, want)
	}
	assertTracksOrigin(t, p.Base, "teammate")
}

// TestEnsureWorktreeFallsBackWhenOriginUnreachable verifies a failed fetch
// only warns: the branch is still created from the local origin/main.
func TestEnsureWorktreeFallsBackWhenOriginUnreachable(t *testing.T) {
	p, _ := newClonedProject(t)
	runBaseGit(t, p.Base, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing"))

	if _, _, err := p.EnsureWorktree(EnsureWorktreeOptions{Branch: "feature", PickBase: baseDefault}); err != nil {
		t.Fatalf("EnsureWorktree(feature): %v", err)
	}
	if got, want := baseRevParse(t, p.Base, "feature"), baseRevParse(t, p.Base, "origin/main"); got != want {
		t.Errorf("feature tip = %s, want local origin/main %s", got, want)
	}
}

// TestLsRemoteHasMatchesExactRef verifies a ref whose name only ends in the
// wanted ref doesn't count as a match.
func TestLsRemoteHasMatchesExactRef(t *testing.T) {
	out := "abc123\trefs/heads/team/feature\n"
	if lsRemoteHas(out, "refs/heads/feature") {
		t.Error("refs/heads/team/feature matched refs/heads/feature")
	}
	if !lsRemoteHas(out+"def456\trefs/heads/feature\n", "refs/heads/feature") {
		t.Error("refs/heads/feature not matched")
	}
}
