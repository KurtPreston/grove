package builtin

import (
	"os"

	"grove/internal/config"
	"grove/internal/recipe"
	"grove/internal/ui"
)

func init() { recipe.Register("cd", cdRecipe) }

// cdRecipe asks the calling shell to change into the worktree directory. The
// grove binary cannot change its parent shell's working directory, so it writes
// the target path to the file named by $GROVE_CD_FILE; grove's shell integration
// (shell/grove.bash or grove.fish, sourced from your rc file) reads that file
// after grove exits and performs the cd.
//
// Auto-cd is therefore opt-in: add a { "type": "cd" } recipe to enable it. When
// the shell integration is not sourced, $GROVE_CD_FILE is unset and this recipe
// warns once and stays a no-op rather than silently doing nothing.
func cdRecipe(ctx recipe.Context, _ config.RecipeConfig) error {
	f := os.Getenv("GROVE_CD_FILE")
	if f == "" {
		ui.Warn("cd recipe: shell integration not active ($GROVE_CD_FILE unset); " +
			"source shell/grove.bash or grove.fish to let grove move your shell.")
		return nil
	}
	return os.WriteFile(f, []byte(ctx.Dir), 0o644)
}
