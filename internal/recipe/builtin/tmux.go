package builtin

import (
	"grove/internal/project"
	"grove/internal/recipe"
	"grove/internal/tmux"
	"grove/internal/ui"
)

func init() { recipe.Register("tmux", tmuxRecipe) }

// tmuxRecipe ensures the project session and this worktree's window (colored),
// then attaches/switches to it.
func tmuxRecipe(ctx recipe.Context) error {
	if !tmux.Has() {
		ui.Warn("tmux not installed; skipping tmux recipe.")
		return nil
	}
	session := project.Sanitize(ctx.Project)
	win := project.Sanitize(ctx.Branch)
	tmux.EnsureSession(session, ctx.ProjectDir)
	tmux.EnsureWorktreeWindow(session, win, ctx.Dir, ctx.Color, ctx.Fg, ctx.TmuxLayout)
	tmux.KillPlaceholder(session)
	tmux.AttachOrSwitch(session, win)
	return nil
}
