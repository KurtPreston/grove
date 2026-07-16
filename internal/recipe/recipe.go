// Package recipe defines grove's recipe contract: a unit of "trigger a dev
// environment / side effect for a branch". Built-in recipes register themselves
// here; anything not built in is looked up as an external `grove-recipe-<name>`
// executable on PATH, so users can add their own without touching grove.
//
// A recipe's configuration travels with it: each entry in one of grove.json's
// "hooks" buckets (config.Hooks) carries a type plus the settings that type
// needs. Built-in recipes receive that entry directly; external recipes
// receive it as GROVE_RECIPE_* environment variables alongside the shared
// Context env. Which bucket an entry lives in decides when it runs and whether
// a failure aborts (Run) or is fatal (RunBefore) - see config.Hooks.
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
	// an existing one being reopened). Exported as GROVE_CREATED so recipes can
	// tell the two apart; which bucket a recipe lives in already decides
	// whether it runs (see config.Hooks), so this is informational.
	Created bool
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

// runOne runs a single recipe entry: a built-in if registered, else an
// external `grove-recipe-<type>` on PATH. Returns an error if the type is
// unknown or the handler/process fails; callers decide whether that's fatal.
func runOne(ctx Context, rc config.RecipeConfig) error {
	name := rc.Type
	if r, ok := registry[name]; ok {
		ui.Info("recipe: " + name)
		return r(ctx, rc)
	}
	bin := "grove-recipe-" + name
	path, err := exec.LookPath(bin)
	if err != nil {
		return fmt.Errorf("unknown recipe: %s (no built-in and no %s on PATH)", name, bin)
	}
	ui.Info("recipe: " + name + " (external)")
	cmd := exec.Command(path)
	cmd.Env = append(ctx.Env(), recipeEnv(ctx, rc)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Run executes each configured recipe in order, warning (but not aborting) on
// failure - including an unknown type. Used for the onOpen and afterFirstOpen
// buckets, which react to something that has already happened.
// Entries without a type are skipped (config.Load already warned).
func Run(recipes []config.RecipeConfig, ctx Context) {
	for _, rc := range recipes {
		if rc.Type == "" {
			continue
		}
		if err := runOne(ctx, rc); err != nil {
			ui.Warn(fmt.Sprintf("recipe %s failed: %v", rc.Type, err))
		}
	}
}

// RunBefore executes each configured recipe in order, stopping at and
// returning the first error. Used for the beforeCreateBranch bucket: it gates
// branch creation, so a non-zero exit (or an unknown type) must abort before
// anything is created. Entries without a type are skipped.
func RunBefore(recipes []config.RecipeConfig, ctx Context) error {
	for _, rc := range recipes {
		if rc.Type == "" {
			continue
		}
		if err := runOne(ctx, rc); err != nil {
			return fmt.Errorf("recipe %s failed: %w", rc.Type, err)
		}
	}
	return nil
}

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
