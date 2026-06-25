// Package recipe defines grove's recipe contract: a unit of "trigger a dev
// environment / side effect for a branch". Built-in recipes register themselves
// here; anything not built in is looked up as an external `grove-recipe-<name>`
// executable on PATH, so users can add their own without touching grove.
package recipe

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"grove/internal/ui"
)

// Context is everything a recipe might need about the branch being opened. It is
// passed to built-in recipes directly and exported to external recipes as env.
type Context struct {
	Branch        string
	Dir           string
	Color         string
	Fg            string
	Project       string
	ProjectDir    string
	Base          string
	DefaultBranch string
	SSHHost       string
	InSSH         bool

	// Created reports whether the worktree was created on this invocation (vs.
	// an existing one being reopened). Recipes like bootstrap use it to run
	// one-time setup only on fresh worktrees.
	Created bool

	// Recipe-specific configuration sourced from the environment.
	TmuxLayout   string
	WebhookURL   string
	WebhookToken string
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
		{"GROVE_DIR", c.Dir},
		{"GROVE_COLOR", c.Color},
		{"GROVE_FG", c.Fg},
		{"GROVE_PROJECT", c.Project},
		{"GROVE_PROJECT_DIR", c.ProjectDir},
		{"GROVE_BASE", c.Base},
		{"GROVE_DEFAULT_BRANCH", c.DefaultBranch},
		{"GROVE_SSH_HOST", c.SSHHost},
		{"GROVE_IN_SSH", inSSH},
		{"GROVE_CREATED", created},
	} {
		e = append(e, kv[0]+"="+kv[1])
	}
	return e
}

// Recipe is a built-in recipe handler.
type Recipe func(Context) error

var registry = map[string]Recipe{}

// Register adds a built-in recipe. Called from builtin package init().
func Register(name string, r Recipe) { registry[name] = r }

// Names returns the registered built-in recipe names (unordered).
func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	return out
}

// Parse splits a comma-separated recipe list, trimming blanks.
func Parse(csv string) []string {
	var out []string
	for _, n := range strings.Split(csv, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// Run executes each named recipe in order: a built-in if registered, else an
// external `grove-recipe-<name>` on PATH. Unknown recipes warn but don't abort.
func Run(names []string, ctx Context) {
	for _, name := range names {
		if r, ok := registry[name]; ok {
			ui.Info("recipe: " + name)
			if err := r(ctx); err != nil {
				ui.Warn(fmt.Sprintf("recipe %s failed: %v", name, err))
			}
			continue
		}
		bin := "grove-recipe-" + name
		if path, err := exec.LookPath(bin); err == nil {
			ui.Info("recipe: " + name + " (external)")
			cmd := exec.Command(path)
			cmd.Env = ctx.Env()
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
