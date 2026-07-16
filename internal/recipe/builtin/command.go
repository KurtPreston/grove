package builtin

import (
	"os"
	"os/exec"

	"grove/internal/config"
	"grove/internal/recipe"
	"grove/internal/ui"
)

func init() { recipe.Register("command", commandRecipe) }

// DefaultCommandShell is the login shell used to run a command recipe when the
// recipe omits "shell".
const DefaultCommandShell = "bash"

// commandRecipe runs a configured shell command. Which hooks bucket a
// "command" entry lives in decides when it runs (see config.Hooks); this
// handler is unconditional beyond the no-op when no command is configured. A
// common use is one-time, per-project setup — e.g. `nvm use && yarn install &&
// yarn build` in the afterFirstOpen bucket, so it only runs once.
//
// The command runs through a login shell so the user's environment (nvm, asdf,
// rbenv, …) is sourced; that is what lets shell-function tools like `nvm use`
// work in a non-interactive run.
func commandRecipe(ctx recipe.Context, rc config.RecipeConfig) error {
	if rc.Command == "" {
		// Nothing configured for this project; stay quiet.
		return nil
	}

	shell := rc.Shell
	if shell == "" {
		shell = DefaultCommandShell
	}

	ui.Info("command: running configured command")
	// -l = login shell, so profile/rc files (and thus nvm etc.) are sourced.
	cmd := exec.Command(shell, "-l", "-c", rc.Command)
	// A beforeCreateBranch command runs before the worktree directory exists:
	// ctx.Dir is the planned (not-yet-real) path, so it isn't a valid cwd yet.
	// Fall back to the project root in that case.
	cmd.Dir = ctx.Dir
	if fi, err := os.Stat(cmd.Dir); err != nil || !fi.IsDir() {
		cmd.Dir = ctx.ProjectDir
	}
	cmd.Env = ctx.Env()
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
