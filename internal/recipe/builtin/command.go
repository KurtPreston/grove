package builtin

import (
	"os"
	"os/exec"
	"strings"

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
	cmd.Env = sanitizeLaunchEnv(ctx.Env())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// sanitizeLaunchEnv drops the VS Code / Electron launcher variables an editor
// process leaks into its child environments. The common case is opening a
// worktree in Cursor/VS Code via `command` (e.g. "cursor $GROVE_DIR"): when
// grove itself was spawned from inside the editor (an integrated terminal, a
// task, or an agent/extension-host process), grove inherits that editor's
// private launch state — VSCODE_PID, VSCODE_IPC_HOOK, VSCODE_NLS_CONFIG,
// VSCODE_CODE_CACHE_PATH, VSCODE_ESM_ENTRYPOINT, ELECTRON_RUN_AS_NODE, etc. The
// `cursor`/`code` CLI forwards most of these to the fresh window it spawns, and
// the new process reads them at startup and mis-initializes as if it were a
// nested/duplicate instance, so the window opens for an instant and then quits
// (the dock icon flashes and vanishes). VS Code's own integrated terminal
// scrubs these for exactly this reason, which is why a hand-typed `cursor .`
// works while the same command run through grove does not.
//
// Stripping them is safe for non-editor commands too (npm, git, …): these
// variables only carry meaning to an Electron/VS Code process.
func sanitizeLaunchEnv(env []string) []string {
	out := env[:0:0]
	for _, kv := range env {
		key := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			key = kv[:i]
		}
		if strings.HasPrefix(key, "VSCODE_") ||
			key == "ELECTRON_RUN_AS_NODE" ||
			key == "ELECTRON_NO_ATTACH_CONSOLE" {
			continue
		}
		out = append(out, kv)
	}
	return out
}
