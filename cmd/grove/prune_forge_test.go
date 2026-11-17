package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"grove/internal/config"
	"grove/internal/project"
)

// TestForgeMergedBranchesByName checks that the forge check finds a branch by
// name even when the repository-wide list misses it, fetches the merged
// request's head the clone never saw, and counts the branch as landed because
// its tip sits inside that request.
func TestForgeMergedBranchesByName(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	// A forge whose request #7 grew past the clone's copy of its branch, then
	// merged and had its branch deleted, leaving only refs/pull/7/head
	forge := t.TempDir()
	gitDo(t, forge, "init", "-q", "-b", "main")
	gitCommit(t, forge, "base.txt", "0", "c0")
	gitDo(t, forge, "checkout", "-q", "-b", "feat")
	gitCommit(t, forge, "feat.txt", "1", "f1")
	gitDo(t, forge, "checkout", "-q", "main")
	clone := filepath.Join(t.TempDir(), "clone")
	gitDo(t, filepath.Dir(clone), "clone", "-q", forge, clone)
	gitDo(t, clone, "branch", "-q", "feat", "origin/feat")
	gitDo(t, forge, "checkout", "-q", "feat")
	gitCommit(t, forge, "feat.txt", "2", "review fix")
	head := gitRev(t, forge, "feat")
	gitDo(t, forge, "update-ref", "refs/pull/7/head", "feat")
	gitDo(t, forge, "checkout", "-q", "main")
	gitDo(t, forge, "branch", "-q", "-D", "feat")

	// A gh stand-in that reports #7 only when asked about feat by name, merged
	// into main
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$*\" in\n" +
		"*'--base main'*'--head feat'*) echo '[{\"number\":7,\"headRefName\":\"feat\",\"headRefOid\":\"" + head + "\"}]' ;;\n" +
		"*) echo '[]' ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// Look feat up, fetch its request's head, and judge the branch by it
	p := &project.Project{Base: filepath.Join(clone, ".git"), Dir: clone}
	cfg := config.Config{Prune: &config.PruneConfig{Forge: &config.ForgeConfig{Enabled: true, Repo: "owner/repo"}}}
	in := pruneInputs{p: p, def: "main", cfg: cfg}
	in.forgeMerged = in.forgeMergedBranches()
	if !project.GitQuiet(p.Base, "--git-dir=.", "show-ref", "--verify", "refs/grove/pull/7") {
		t.Fatal("merged request head not fetched")
	}
	if got := in.refReason("feat", "feat", false); got != "forge" {
		t.Errorf("refReason(feat) = %q, want forge", got)
	}
}
