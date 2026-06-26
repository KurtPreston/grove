package builtin

import (
	"grove/internal/config"
	"grove/internal/project"
	"grove/internal/recipe"
	"grove/internal/tmux"
	"grove/internal/ui"
)

func init() { recipe.Register("tmux", tmuxRecipe) }

// DefaultLayout is the tmux pane layout used when a tmux recipe omits "layout":
// a plain shell plus a "claude" pane.
const DefaultLayout = "shell=,claude=claude"

// tmuxRecipe ensures the project session and this worktree's window (colored),
// then attaches/switches to it.
func tmuxRecipe(ctx recipe.Context, rc config.RecipeConfig) error {
	if !tmux.Has() {
		ui.Warn("tmux not installed; skipping tmux recipe.")
		return nil
	}
	session := project.Sanitize(ctx.Project)
	win := project.Sanitize(ctx.Branch)
	tmux.EnsureSession(session, ctx.ProjectDir)
	tmux.EnsureWorktreeWindow(session, win, ctx.Dir, ctx.Color, ctx.Fg, LayoutOr(rc.Layout))
	tmux.KillPlaceholder(session)
	tmux.AttachOrSwitch(session, win)
	return nil
}

// LayoutOr returns the given layout, or DefaultLayout when it is empty.
func LayoutOr(layout string) string {
	if layout == "" {
		return DefaultLayout
	}
	return layout
}
