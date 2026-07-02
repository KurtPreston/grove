// Package recipe defines grove's recipe contract: a unit of "trigger a dev
// environment / side effect for a branch". Built-in recipes register themselves
// here; anything not built in is looked up as an external `grove-recipe-<name>`
// executable on PATH, so users can add their own without touching grove.
//
// A recipe's configuration travels with it: each entry in grove.json's recipes
// array carries a type plus the settings that type needs. Built-in recipes
// receive that entry directly; external recipes receive it as GROVE_RECIPE_*
// environment variables alongside the shared Context env.
package recipe

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"grove/internal/config"
	"grove/internal/project"
	"grove/internal/ui"
)

// Context is the branch-level information every recipe shares, independent of
// any single recipe's configuration. It is passed to built-in recipes directly
// and exported to external recipes as environment variables.
type Context struct {
	Branch        string
	Dir           string
	Color         string
	Fg            string
	Project       string
	ProjectDir    string
	Base          string
	DefaultBranch string
	InSSH         bool

	// Created reports whether the worktree was created on this invocation (vs.
	// an existing one being reopened). The lifecycle gate (see shouldRun) uses
	// it so onCreate-only recipes run only on fresh worktrees.
	Created bool

	// Force requests that create-only recipes run even when the worktree
	// already existed. Set by `grove open --force`.
	Force bool
}

// Env renders the context as environment variables for external recipes,
// inheriting the current environment.
func (c Context) Env() []string {
	e := os.Environ()
	inSSH := ""
	if c.InSSH {
		inSSH = "1"
	}
	created := ""
	if c.Created {
		created = "1"
	}
	for _, kv := range [][2]string{
		{"GROVE_BRANCH", c.Branch},
		{"GROVE_NAME", project.Sanitize(c.Branch)},
		{"GROVE_DIR", c.Dir},
		{"GROVE_COLOR", c.Color},
		{"GROVE_FG", c.Fg},
		{"GROVE_PROJECT", c.Project},
		{"GROVE_PROJECT_DIR", c.ProjectDir},
		{"GROVE_BASE", c.Base},
		{"GROVE_DEFAULT_BRANCH", c.DefaultBranch},
		{"GROVE_IN_SSH", inSSH},
		{"GROVE_CREATED", created},
	} {
		e = append(e, kv[0]+"="+kv[1])
	}
	return e
}

// Recipe is a built-in recipe handler. It receives the shared Context plus its
// own configuration entry from grove.json.
type Recipe func(Context, config.RecipeConfig) error

var registry = map[string]Recipe{}

// Register adds a built-in recipe. Called from builtin package init().
func Register(name string, r Recipe) { registry[name] = r }

// Run executes each configured recipe in order: a built-in if registered, else
// an external `grove-recipe-<type>` on PATH. Unknown recipes warn but don't
// abort. Entries without a type are skipped (config.Load already warned).
func Run(recipes []config.RecipeConfig, ctx Context) {
	for _, rc := range recipes {
		name := rc.Type
		if name == "" {
			continue
		}
		if !shouldRun(rc, ctx) {
			ui.Info("recipe: " + name + " (skipped: does not run on this open; use --force or set onOpen)")
			continue
		}
		if r, ok := registry[name]; ok {
			ui.Info("recipe: " + name)
			if err := r(ctx, rc); err != nil {
				ui.Warn(fmt.Sprintf("recipe %s failed: %v", name, err))
			}
			continue
		}
		bin := "grove-recipe-" + name
		if path, err := exec.LookPath(bin); err == nil {
			ui.Info("recipe: " + name + " (external)")
			cmd := exec.Command(path)
			cmd.Env = append(ctx.Env(), recipeEnv(ctx, rc)...)
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stderr
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				ui.Warn(fmt.Sprintf("recipe %s failed: %v", name, err))
			}
			continue
		}
		ui.Warn("unknown recipe: " + name + " (no built-in and no grove-recipe-" + name + " on PATH)")
	}
}

// shouldRun applies the shared lifecycle gate to a recipe entry. Both flags
// default to true (nil). A fresh create (or --force) is also the first open, so
// on a create event a recipe runs if either onCreate or onOpen is set; on a
// reopen (or plain-folder launch) it runs only if onOpen is set.
func shouldRun(rc config.RecipeConfig, ctx Context) bool {
	if ctx.Created || ctx.Force {
		return runsOnCreate(rc) || runsOnOpen(rc)
	}
	return runsOnOpen(rc)
}

func runsOnCreate(rc config.RecipeConfig) bool { return rc.OnCreate == nil || *rc.OnCreate }
func runsOnOpen(rc config.RecipeConfig) bool   { return rc.OnOpen == nil || *rc.OnOpen }

// recipeEnv exports a recipe's configuration entry as GROVE_RECIPE_* variables
// so external recipes can read the same settings the built-ins receive. String
// values are env-substituted from ctx before export.
func recipeEnv(ctx Context, rc config.RecipeConfig) []string {
	env := ctx.Env()
	var out []string
	add := func(k, v string) {
		v = ExpandString(v, env)
		if v != "" {
			out = append(out, "GROVE_RECIPE_"+k+"="+v)
		}
	}
	add("TYPE", rc.Type)
	add("URL", rc.URL)
	add("TOKEN", rc.Token)
	add("LAYOUT", rc.Layout)
	add("COMMAND", rc.Command)
	add("SHELL", rc.Shell)
	for k, v := range rc.Params {
		if s, ok := v.(string); ok {
			add(strings.ToUpper(k), s)
		}
	}
	return out
}
