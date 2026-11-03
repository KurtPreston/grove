package main

import (
	"path/filepath"

	"grove/internal/color"
	"grove/internal/project"
	"grove/internal/recipe"
)

// buildContext builds the recipe.Context for a worktree being opened.
func buildContext(p *project.Project, branch, dir string, created bool) recipe.Context {
	hex := color.ForBranch(branch)
	return recipe.Context{
		Branch:        branch,
		Dir:           dir,
		Color:         hex,
		Fg:            color.FgForHex(hex),
		Project:       p.Name(),
		ProjectDir:    p.Dir,
		Base:          p.Base,
		DefaultBranch: p.DefaultBranch(),
		InSSH:         inSSH,
		Created:       created,
	}
}

// buildBeforeContext builds the recipe.Context passed to the
// beforeCreateBranch bucket. It runs before anything is created, so Dir is the
// worktree's planned (not yet real) path — useful as GROVE_DIR for a hook that
// wants to know where the worktree will land — and Created is always false.
func buildBeforeContext(p *project.Project, branch string) recipe.Context {
	hex := color.ForBranch(branch)
	return recipe.Context{
		Branch:        branch,
		Dir:           filepath.Join(p.Dir, project.Sanitize(branch)),
		Color:         hex,
		Fg:            color.FgForHex(hex),
		Project:       p.Name(),
		ProjectDir:    p.Dir,
		Base:          p.Base,
		DefaultBranch: p.DefaultBranch(),
		InSSH:         inSSH,
	}
}

// buildLaunchContext builds a recipe Context for a plain directory (no
// worktree/project). The folder basename doubles as the branch/project name, so
// the webhook recipe opens a view named after the folder and the color is
// derived from it.
func buildLaunchContext(name, dir string) recipe.Context {
	hex := color.ForBranch(name)
	return recipe.Context{
		Branch:     name,
		Dir:        dir,
		Color:      hex,
		Fg:         color.FgForHex(hex),
		Project:    name,
		ProjectDir: dir,
		InSSH:      inSSH,
	}
}
