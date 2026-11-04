package project

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalOriginBranches(t *testing.T) {
	p := newTestProject(t)
	runBaseGit(t, p.Base, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*")
	for _, name := range []string{"feature", "renamed", "fork", "gone"} {
		runBaseGit(t, p.Base, "branch", name, "main")
	}
	runBaseGit(t, p.Base, "config", "branch.renamed.remote", "origin")
	runBaseGit(t, p.Base, "config", "branch.renamed.merge", "refs/heads/remote-name")
	runBaseGit(t, p.Base, "config", "branch.gone.remote", "origin")
	runBaseGit(t, p.Base, "config", "branch.gone.merge", "refs/heads/deleted")
	runBaseGit(t, p.Base, "remote", "add", "upstream", p.Dir)
	runBaseGit(t, p.Base, "config", "branch.fork.remote", "upstream")
	runBaseGit(t, p.Base, "config", "branch.fork.merge", "refs/heads/not-origin")

	names, err := p.LocalOriginBranches()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(names, ","), "deleted,feature,fork,gone,main,remote-name,renamed"; got != want {
		t.Errorf("LocalOriginBranches = %q, want %q", got, want)
	}
}

func TestPruneLocal(t *testing.T) {
	forge := newMergeRepo(t)
	commitFile(t, forge.Dir, "base.txt", "0", "base")
	clone := filepath.Join(t.TempDir(), "clone")
	runGit(t, filepath.Dir(clone), "clone", "-q", forge.Dir, clone)
	p := &Project{Base: filepath.Join(clone, ".git"), Dir: clone}

	// Advance selected branches, including a differently named upstream.
	commitFile(t, forge.Dir, "main.txt", "1", "advance main")
	for _, name := range []string{"feature", "remote-name", "unrelated"} {
		runGit(t, forge.Dir, "checkout", "-q", "-b", name, "main")
		commitFile(t, forge.Dir, name+".txt", "1", name)
	}
	unrelated, err := GitOut(forge.Dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	runGit(t, forge.Dir, "tag", "unrelated-tag")
	runGit(t, forge.Dir, "update-ref", "refs/pull/42/head", "HEAD")
	runGit(t, forge.Dir, "tag", "selected-tag", "feature")
	runGit(t, clone, "branch", "feature")
	runGit(t, clone, "branch", "renamed")
	runGit(t, clone, "config", "branch.renamed.remote", "origin")
	runGit(t, clone, "config", "branch.renamed.merge", "refs/heads/remote-name")

	// A stale selected ref is pruned, but an unrelated stale ref is untouched.
	runGit(t, clone, "branch", "deleted")
	runGit(t, clone, "update-ref", "refs/remotes/origin/deleted", "HEAD")
	runGit(t, clone, "update-ref", "refs/remotes/origin/other-deleted", "HEAD")
	names, err := p.LocalOriginBranches()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.PruneLocal(names, "main"); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"main", "feature", "remote-name"} {
		got, err := GitOut(p.Base, "--git-dir=.", "rev-parse", "refs/remotes/origin/"+name)
		want, wantErr := GitOut(forge.Dir, "rev-parse", "refs/heads/"+name)
		if err != nil || wantErr != nil || got != want {
			t.Errorf("origin/%s = %q (%v), want %q (%v)", name, got, err, want, wantErr)
		}
	}
	for _, ref := range []string{
		"refs/remotes/origin/deleted", "refs/remotes/origin/unrelated",
		"refs/tags/unrelated-tag", "refs/tags/selected-tag", "refs/grove/pull/42",
	} {
		if GitQuiet(p.Base, "--git-dir=.", "show-ref", "--verify", ref) {
			t.Errorf("unexpected ref %s", ref)
		}
	}
	if GitQuiet(p.Base, "--git-dir=.", "cat-file", "-e", strings.TrimSpace(unrelated)) {
		t.Error("downloaded unrelated branch objects")
	}
	if !GitQuiet(p.Base, "--git-dir=.", "show-ref", "--verify", "refs/remotes/origin/other-deleted") {
		t.Error("pruned unrelated tracking ref")
	}
}

func TestPruneLocalFetchFailure(t *testing.T) {
	p := newTestProject(t)
	runBaseGit(t, p.Base, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing"))
	runBaseGit(t, p.Base, "update-ref", "refs/remotes/origin/main", "main")
	if err := p.PruneLocal([]string{"main"}, "main"); err == nil {
		t.Fatal("expected fetch failure")
	}
	if !GitQuiet(p.Base, "--git-dir=.", "show-ref", "--verify", "refs/remotes/origin/main") {
		t.Error("fetch failure deleted tracking ref")
	}
}
