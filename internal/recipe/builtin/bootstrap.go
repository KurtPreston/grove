package builtin

import (
	"os"
	"os/exec"

	"grove/internal/config"
	"grove/internal/recipe"
	"grove/internal/ui"
)

func init() { recipe.Register("bootstrap", bootstrapRecipe) }

// DefaultBootstrapShell is the login shell used to run bootstrap commands when
// the recipe omits "shell".
const DefaultBootstrapShell = "bash"

// bootstrapRecipe runs one-time, per-project setup the first time a worktree is
// created — e.g. `nvm use && yarn install && yarn build`. It is intentionally
// quiet and safe to leave in the recipe list for every project:
//
//   - It is a no-op for worktrees that already existed (use `grove open --force`
//     to re-run on an existing worktree).
//   - It is a no-op when the bootstrap recipe defines no command.
//
// The command runs in the new worktree directory through a login shell so the
// user's environment (nvm, asdf, rbenv, …) is sourced; that is what lets
// shell-function tools like `nvm use` work in a non-interactive run.
func bootstrapRecipe(ctx recipe.Context, rc config.RecipeConfig) error {
	if rc.Command == "" {
		// Nothing configured for this project; stay quiet.
		return nil
	}
	if !ctx.Created && !ctx.Force {
		ui.Info("bootstrap: worktree already existed; skipping (use 'grove open --force' to run anyway).")
		return nil
	}

	shell := rc.Shell
	if shell == "" {
		shell = DefaultBootstrapShell
	}

	ui.Info("bootstrap: running configured command")
	// -l = login shell, so profile/rc files (and thus nvm etc.) are sourced.
	cmd := exec.Command(shell, "-l", "-c", rc.Command)
	cmd.Dir = ctx.Dir
	cmd.Env = ctx.Env()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
