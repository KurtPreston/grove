package builtin

import (
	"encoding/json"
	"os"
	"path/filepath"

	"grove/internal/config"
	"grove/internal/project"
	"grove/internal/recipe"
	"grove/internal/ui"
)

func init() { recipe.Register("vscode-color-config", vscodeRecipe) }

// vscodeRecipe writes the branch color into the worktree's .vscode/settings.json
// (shared by VSCode and Cursor for workspace-level colorCustomizations) and keeps
// the generated file out of `git status`.
func vscodeRecipe(ctx recipe.Context, _ config.RecipeConfig) error {
	return writeVscodeSettings(ctx.Dir, ctx.Color, ctx.Fg)
}

func writeVscodeSettings(dir, hex, fg string) error {
	cc := map[string]interface{}{
		"titleBar.activeBackground":   hex,
		"titleBar.activeForeground":   fg,
		"titleBar.inactiveBackground": hex,
		"titleBar.inactiveForeground": fg,
		"activityBar.background":      hex,
		"activityBar.foreground":      fg,
	}

	vsdir := filepath.Join(dir, ".vscode")
	if err := os.MkdirAll(vsdir, 0o755); err != nil {
		return err
	}
	f := filepath.Join(vsdir, "settings.json")

	settings := map[string]interface{}{}
	if b, err := os.ReadFile(f); err == nil && len(b) > 0 {
		if err := json.Unmarshal(b, &settings); err != nil {
			ui.Warn("could not parse " + f + " (invalid JSON?); leaving it untouched.")
			return nil
		}
	}

	existing, _ := settings["workbench.colorCustomizations"].(map[string]interface{})
	if existing == nil {
		existing = map[string]interface{}{}
	}
	for k, v := range cc {
		existing[k] = v
	}
	settings["workbench.colorCustomizations"] = existing

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	if err := os.WriteFile(f, out, 0o644); err != nil {
		return err
	}
	project.AddLocalExclude(dir, "/.vscode/settings.json")
	return nil
}
