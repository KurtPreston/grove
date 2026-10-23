package project

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestFetchPullHeads checks that pull request heads published under GitHub/Gitea
// and GitLab ref names are mirrored into refs/grove/, pruned once the forge
// drops them, and found by the commits they contain.
func TestFetchPullHeads(t *testing.T) {
	// A forge repo publishing three requests: two over feat, one elsewhere
	forge := newMergeRepo(t)
	commitFile(t, forge.Dir, "base.txt", "0", "c0")
	runGit(t, forge.Dir, "checkout", "-q", "-b", "feat")
	commitFile(t, forge.Dir, "f1.txt", "1", "f1")
	commitFile(t, forge.Dir, "f2.txt", "2", "f2")
	runGit(t, forge.Dir, "checkout", "-q", "-b", "other", "main")
	commitFile(t, forge.Dir, "o.txt", "o", "o1")
	runGit(t, forge.Dir, "update-ref", "refs/pull/3/head", "feat")
	runGit(t, forge.Dir, "update-ref", "refs/merge-requests/4/head", "feat~1")
	runGit(t, forge.Dir, "update-ref", "refs/pull/5/head", "other")

	// A clone of it standing in for the project's repository
	clone := filepath.Join(t.TempDir(), "clone")
	runGit(t, filepath.Dir(clone), "clone", "-q", forge.Dir, clone)
	p := &Project{Base: filepath.Join(clone, ".git"), Dir: clone}
	if !p.FetchPullHeads() {
		t.Fatal("FetchPullHeads failed")
	}

	// Each request is found by the commits it carries
	f1, _ := GitOut(p.Base, "--git-dir=.", "rev-parse", "origin/feat~1")
	if got := strings.Join(p.PullHeadsContaining(strings.TrimSpace(f1)), ","); got != "refs/grove/merge-requests/4,refs/grove/pull/3" {
		t.Errorf("PullHeadsContaining(f1) = %q", got)
	}
	if got := strings.Join(p.PullHeadsContaining("origin/other"), ","); got != "refs/grove/pull/5" {
		t.Errorf("PullHeadsContaining(other) = %q", got)
	}

	// A request the forge stops publishing is pruned on the next fetch
	runGit(t, forge.Dir, "update-ref", "-d", "refs/pull/5/head")
	if !p.FetchPullHeads() {
		t.Fatal("second FetchPullHeads failed")
	}
	if got := p.PullHeadsContaining("origin/other"); len(got) != 0 {
		t.Errorf("pruned request still listed: %q", got)
	}
}

// TestFetchPullHeadsWithoutPullRefs checks that a remote publishing no pull
// request refs, like a plain git server, fetches cleanly with nothing to find.
func TestFetchPullHeadsWithoutPullRefs(t *testing.T) {
	forge := newMergeRepo(t)
	commitFile(t, forge.Dir, "base.txt", "0", "c0")
	clone := filepath.Join(t.TempDir(), "clone")
	runGit(t, filepath.Dir(clone), "clone", "-q", forge.Dir, clone)
	p := &Project{Base: filepath.Join(clone, ".git"), Dir: clone}

	if !p.FetchPullHeads() {
		t.Fatal("FetchPullHeads failed")
	}
	if got := p.PullHeadsContaining("HEAD"); len(got) != 0 {
		t.Errorf("PullHeadsContaining(HEAD) = %q, want none", got)
	}
}

// TestPullLabel pins each forge's notation for its requests.
func TestPullLabel(t *testing.T) {
	for ref, want := range map[string]string{
		"refs/grove/pull/50":          "#50",
		"refs/grove/merge-requests/7": "!7",
		"refs/heads/other":            "refs/heads/other",
	} {
		if got := PullLabel(ref); got != want {
			t.Errorf("PullLabel(%q) = %q, want %q", ref, got, want)
		}
	}
}
