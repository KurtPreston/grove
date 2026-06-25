package builtin

import (
	"os"
	"os/exec"
	"path/filepath"

	"grove/internal/recipe"
	"grove/internal/ui"
)

func init() { recipe.Register("bootstrap", bootstrapRecipe) }

// bootstrapRecipe runs one-time, per-project setup the first time a worktree is
// created — e.g. `nvm use && yarn install && yarn build`. It is intentionally
// quiet and safe to leave in GROVE_RECIPES for every project:
//
//   - It is a no-op for worktrees that already existed (set GROVE_BOOTSTRAP_FORCE
//     to re-run on an existing worktree).
//   - It is a no-op for projects that define no bootstrap commands.
//
// Bootstrap commands come from the first source found, in priority order:
//
//  1. GROVE_BOOTSTRAP            inline commands (handy via direnv per project)
//  2. <worktree>/.grove/bootstrap   committed: travels with the repo
//  3. <project>/.grove/bootstrap    machine-local: applies to every worktree
//
// The script/commands run in the new worktree directory through a login shell so
// the user's environment (nvm, asdf, rbenv, …) is sourced; that is what lets
// shell-function tools like `nvm use` work in a non-interactive run.
func bootstrapRecipe(ctx recipe.Context) error {
	if !ctx.Created && os.Getenv("GROVE_BOOTSTRAP_FORCE") == "" {
		ui.Info("bootstrap: worktree already existed; skipping (set GROVE_BOOTSTRAP_FORCE=1 to run anyway).")
		return nil
	}

	shell := os.Getenv("GROVE_BOOTSTRAP_SHELL")
	if shell == "" {
		shell = "bash"
	}

	// -l = login shell, so profile/rc files (and thus nvm etc.) are sourced.
	args := []string{"-l"}
	if inline := os.Getenv("GROVE_BOOTSTRAP"); inline != "" {
		ui.Info("bootstrap: running GROVE_BOOTSTRAP commands")
		args = append(args, "-c", inline)
	} else if script := findBootstrapScript(ctx); script != "" {
		ui.Info("bootstrap: running " + script)
		args = append(args, script)
	} else {
		// Nothing configured for this project; stay quiet.
		return nil
	}

	cmd := exec.Command(shell, args...)
	cmd.Dir = ctx.Dir
	cmd.Env = ctx.Env()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// findBootstrapScript returns the first existing bootstrap script: the
// repo-committed one in the worktree, else the machine-local one in the project
// directory. Returns "" when neither exists.
func findBootstrapScript(ctx recipe.Context) string {
	for _, p := range []string{
		filepath.Join(ctx.Dir, ".grove", "bootstrap"),
		filepath.Join(ctx.ProjectDir, ".grove", "bootstrap"),
	} {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}
