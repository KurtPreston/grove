package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"grove/internal/config"
	"grove/internal/project"
)

func TestResolvePruneScope(t *testing.T) {
	local, invalid, empty := "local", "typo", ""
	tests := []struct {
		name  string
		cfg   *string
		args  []string
		want  string
		rest  string
		error bool
	}{
		{name: "default", want: "all"},
		{name: "config", cfg: &local, want: "local"},
		{name: "override", cfg: &local, args: []string{"--dry-run", "--scope", "all"}, want: "all", rest: "--dry-run"},
		{name: "equals", args: []string{"--scope=local", "--no-remote"}, want: "local", rest: "--no-remote"},
		{name: "last wins", args: []string{"--scope", "local", "--scope=all"}, want: "all"},
		{name: "invalid config", cfg: &invalid, error: true},
		{name: "empty config", cfg: &empty, error: true},
		{name: "override invalid config", cfg: &invalid, args: []string{"--scope=local"}, want: "local"},
		{name: "invalid flag", args: []string{"--scope=typo"}, error: true},
		{name: "missing value", args: []string{"--scope"}, error: true},
		{name: "flag as value", args: []string{"--scope", "--remote"}, error: true},
		{name: "empty flag", args: []string{"--scope="}, error: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{Prune: &config.PruneConfig{Scope: tc.cfg}}
			rest, scope, err := resolvePruneScope(tc.args, cfg)
			if (err != nil) != tc.error {
				t.Fatalf("error = %v, want error %v", err, tc.error)
			}
			if !tc.error && (scope != tc.want || strings.Join(rest, ",") != tc.rest) {
				t.Errorf("scope/rest = %q/%v, want %q/%q", scope, rest, tc.want, tc.rest)
			}
		})
	}
}

func TestScopedOriginBranchCandidates(t *testing.T) {
	base := t.TempDir()
	gitDo(t, base, "init", "-q", "-b", "main")
	gitCommit(t, base, "base.txt", "0", "base")
	for _, name := range []string{"main", "matching", "renamed", "unrelated", "release-1"} {
		gitDo(t, base, "update-ref", "refs/remotes/origin/"+name, "main")
	}
	gitDo(t, base, "checkout", "-q", "-b", "open")
	gitCommit(t, base, "open.txt", "1", "open")
	gitDo(t, base, "update-ref", "refs/remotes/origin/open", "open")
	gitDo(t, base, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")
	p := &project.Project{Base: filepath.Join(base, ".git"), Dir: base}
	in := pruneInputs{
		p: p, def: "main", cfg: squashOff(),
		originNames: []string{"main", "matching", "renamed", "missing", "open", "release-1", "HEAD"},
	}
	if got := branchList(in.originBranchCandidates()); got != "origin/matching,origin/renamed" {
		t.Errorf("local scope candidates = %q", got)
	}
	in.originNames = []string{}
	if got := in.originBranchCandidates(); len(got) != 0 {
		t.Errorf("empty local scope = %v", got)
	}
	in.originNames = nil
	if got := branchList(in.originBranchCandidates()); got != "origin/matching,origin/renamed,origin/unrelated" {
		t.Errorf("all scope candidates = %q", got)
	}
}

// Exercise the real command so the scope gates both fetching and classification,
// including PR mirroring, config defaults, CLI overrides, and --no-remote.
func TestCmdPruneScope(t *testing.T) {
	proj, wt := setupCompleteProject(t)
	base := filepath.Join(proj, ".base")
	src := gitOutput(t, base, "--git-dir=.", "remote", "get-url", "origin")
	gitDo(t, src, "branch", "remote-only")
	gitDo(t, src, "update-ref", "refs/pull/42/head", "main")
	if err := os.WriteFile(filepath.Join(proj, "grove.json"), []byte(`{"prune":{"scope":"local"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	local := capturePrune(t, wt, []string{"--dry-run"})
	if strings.Contains(local, "origin/remote-only") || !strings.Contains(local, "skipping the global pull request mirror") {
		t.Errorf("unexpected local output: %s", local)
	}
	if project.GitQuiet(base, "--git-dir=.", "show-ref", "--verify", "refs/grove/pull/42") {
		t.Fatal("local scope mirrored pull request refs")
	}
	all := capturePrune(t, wt, []string{"--dry-run", "--scope", "all"})
	if !strings.Contains(all, "origin/remote-only") {
		t.Errorf("all scope omitted unrelated merged branch: %s", all)
	}
	if !project.GitQuiet(base, "--git-dir=.", "show-ref", "--verify", "refs/grove/pull/42") {
		t.Fatal("all scope did not mirror pull request refs")
	}
	noRemote := capturePrune(t, wt, []string{"--dry-run", "--scope=all", "--no-remote"})
	if strings.Contains(noRemote, "origin/remote-only") {
		t.Errorf("--no-remote listed remote candidate: %s", noRemote)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := project.GitOut(dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

func capturePrune(t *testing.T, dir string, args []string) string {
	t.Helper()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldWd) }()
	log, err := os.CreateTemp(t.TempDir(), "prune-output")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	oldStderr := os.Stderr
	os.Stderr = log
	defer func() { os.Stderr = oldStderr }()
	cmdPrune(args)
	out, err := os.ReadFile(log.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
