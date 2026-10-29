package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"grove/internal/config"
	"grove/internal/project"
	"grove/internal/recipe"
	"grove/internal/ui"
)

// cmdOpen: grove open [BRANCH] [TYPES] [--force]. BRANCH omitted or "." infers
// the current worktree's branch; TYPES (a comma-separated list of recipe
// types) filters grove.json's hooks to only those types. --force re-runs the
// afterFirstOpen bucket (one-time setup) on an existing worktree.
func cmdOpen(args []string) {
	p := mustResolve()
	args, force := popForce(args)
	args, from := popFrom(args)
	branch := ""
	filter := ""
	if len(args) >= 1 {
		branch = trimSlash(args[0])
	}
	if len(args) >= 2 {
		filter = args[1]
	}
	if branch == "" || branch == "." {
		b, ok := currentBranch(mustGetwd())
		if !ok {
			ui.Die("could not infer branch from current directory; pass a BRANCH")
		}
		branch = b
	}
	doOpen(p, openOptions{Branch: branch, Filter: filter, From: from, Force: force})
}

// cmdSwitch: bare grove / grove switch / grove BRANCH. Runs grove.json's recipes.
// Outside a grove project, it falls back to launching the current directory with
// the user-level recipes (see cmdLaunch).
func cmdSwitch(args []string) {
	if _, ok := project.FindRoot(mustGetwd()); !ok {
		ui.Warn("not a grove project; launching current directory with user recipes.")
		cmdLaunch(nil)
		return
	}
	p := mustResolve()
	args, force := popForce(args)
	args, from := popFrom(args)
	branch := ""
	if len(args) >= 1 {
		branch = trimSlash(args[0])
	}
	if branch == "" {
		if hasBin("fzf") {
			branch = fzfPick(p)
			if branch == "" {
				return
			}
		} else {
			ui.Die("usage: grove BRANCH   (install fzf for an interactive picker)")
		}
	}
	doOpen(p, openOptions{Branch: branch, From: from, Force: force})
}

// openOptions holds the parsed arguments of an open/switch.
type openOptions struct {
	Branch string
	// Filter is a comma-separated list of recipe types to run; empty runs all.
	Filter string
	// From names the base branch for a brand-new branch.
	From string
	// Force re-runs the afterFirstOpen bucket on an existing worktree.
	Force bool
}

// doOpen ensures the branch's worktree exists and runs grove.json's hooks for it.
func doOpen(p *project.Project, o openOptions) {
	branch, filter, force := o.Branch, o.Filter, o.Force
	cfg := loadCfg(p)
	beforeCtx := buildBeforeContext(p, branch)
	beforeCreate := func() error {
		return recipe.RunBefore(filterRecipes(cfg.BeforeCreateBranch(), filter), beforeCtx)
	}
	dir, created, err := p.EnsureWorktree(project.EnsureWorktreeOptions{
		Branch:       branch,
		Copy:         cfg.Copy,
		PickBase:     baseResolver(p, o.From),
		BeforeCreate: beforeCreate,
	})
	if err != nil {
		ui.Die(err.Error())
	}
	ctx := buildContext(p, branch, dir, created)

	// Update a reused worktree while onOpen runs, so the editor opens without
	// waiting on the network.
	waitPull := func() {}
	if !created {
		waitPull = startPull(dir, branch)
	}
	recipe.Run(filterRecipes(cfg.OnOpen(), filter), ctx)
	waitPull()
	if created || force {
		recipe.Run(filterRecipes(cfg.AfterFirstOpen(), filter), ctx)
	}
}

// startPull fast-forwards the worktree at dir in the background and returns a
// function that waits for it and reports the outcome. Reporting is left to the
// waiter so output never interleaves with whatever runs in the meantime.
func startPull(dir, branch string) func() {
	done := make(chan bool, 1)
	go func() { done <- project.PullLatest(dir, branch) }()
	return func() {
		if <-done {
			ui.Info("Pulled latest.")
		} else {
			ui.Warn("skipped pull (no upstream, offline, or not fast-forward).")
		}
	}
}

// filterRecipes restricts recipes to those whose type appears in the
// comma-separated csv. An empty csv keeps all recipes.
func filterRecipes(recipes []config.RecipeConfig, csv string) []config.RecipeConfig {
	csv = strings.TrimSpace(csv)
	if csv == "" {
		return recipes
	}
	want := map[string]bool{}
	for _, n := range strings.Split(csv, ",") {
		if n = strings.TrimSpace(n); n != "" {
			want[n] = true
		}
	}
	var out []config.RecipeConfig
	for _, r := range recipes {
		if want[r.Type] {
			out = append(out, r)
		}
	}
	return out
}

// wantsHelp reports whether args request help via -h or --help.
func wantsHelp(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return true
		}
	}
	return false
}

// popForce removes --force/-f from args, reporting whether it was present.
func popForce(args []string) ([]string, bool) {
	var out []string
	force := false
	for _, a := range args {
		if a == "--force" || a == "-f" {
			force = true
			continue
		}
		out = append(out, a)
	}
	return out, force
}

// popFrom removes --from REF / --from=REF from args, returning the ref ("" when
// absent). It names the base branch for a brand-new branch, bypassing the
// interactive base-branch picker.
func popFrom(args []string) ([]string, string) {
	var out []string
	from := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--from":
			if i+1 < len(args) {
				from = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--from="):
			from = strings.TrimPrefix(a, "--from=")
		default:
			out = append(out, a)
		}
	}
	return out, from
}

// cmdPath: resolve (creating if needed) BRANCH's worktree; print path to stdout.
func cmdPath(args []string) {
	p := mustResolve()
	args, from := popFrom(args)
	if len(args) < 1 {
		ui.Die("usage: grove path BRANCH")
	}
	branch := trimSlash(args[0])
	// nil beforeCreate: grove path is used by scripts/tooling, so it never
	// runs the (interactive-leaning, abortable) beforeCreateBranch bucket.
	dir, created, err := p.EnsureWorktree(project.EnsureWorktreeOptions{
		Branch:   branch,
		Copy:     loadCfg(p).Copy,
		PickBase: baseResolver(p, from),
	})
	if err != nil {
		ui.Die(err.Error())
	}
	if !created {
		startPull(dir, branch)()
	}
	fmt.Println(dir)
}

// baseResolver builds the pickBase callback handed to EnsureWorktree. It only
// runs when a brand-new branch is being created (existing branches are reused
// without consulting it). Resolution order:
//   - an explicit --from REF wins;
//   - when stdin is not a TTY, base off the default branch;
//   - otherwise prompt for the base branch, whatever branch we are on.
func baseResolver(p *project.Project, from string) func(def string) (string, error) {
	return func(def string) (string, error) {
		if from != "" {
			return from, nil
		}
		if !isInteractive() {
			ui.Info(fmt.Sprintf("Basing new branch off default '%s' (non-interactive; pass --from to choose).", def))
			return def, nil
		}
		cur, _ := currentBranch(mustGetwd())
		return pickBaseBranch(p, def, cur)
	}
}

// pickBaseBranch asks which branch to base a new branch off, offering every
// branch in the project with the default and current branches surfaced first.
// It uses fzf when available; otherwise it prints a numbered menu that also
// accepts a typed branch name.
func pickBaseBranch(p *project.Project, def, cur string) (string, error) {
	order := orderedBases(p, def, cur)
	if hasBin("fzf") {
		choice := fzfPickFrom(order, fmt.Sprintf("base for new branch off (default %s)> ", def))
		if choice == "" {
			return "", fmt.Errorf("no base branch selected")
		}
		return choice, nil
	}
	return numberedBasePrompt(order, def, cur), nil
}

// orderedBases returns the base-branch candidates with def first and cur second
// (when distinct), followed by the project's remaining branches.
func orderedBases(p *project.Project, def, cur string) []string {
	seen := map[string]bool{}
	var order []string
	push := func(b string) {
		if b == "" || seen[b] {
			return
		}
		seen[b] = true
		order = append(order, b)
	}
	push(def)
	push(cur)
	for _, b := range p.BranchList() {
		push(b)
	}
	return order
}

// numberedBasePrompt renders a numbered menu of base branches on stderr and
// reads the choice from stdin. A number selects that entry; a non-empty
// non-numeric line is treated as a typed branch name; a bare Enter selects the
// default branch. This is the fallback when fzf is not installed.
func numberedBasePrompt(order []string, def, cur string) string {
	fmt.Fprintln(os.Stderr, "Base the new branch off which branch?")
	for i, b := range order {
		tag := ""
		switch b {
		case def:
			tag = ui.Dim + " (default)" + ui.Reset
		case cur:
			tag = ui.Dim + " (current)" + ui.Reset
		}
		fmt.Fprintf(os.Stderr, "  %s[%d]%s %s%s\n", ui.Bold, i+1, ui.Reset, b, tag)
	}
	fmt.Fprintf(os.Stderr, "Enter a number or branch name [%s]: ", def)
	line := strings.TrimSpace(readLine())
	if line == "" {
		return def
	}
	if n, err := strconv.Atoi(line); err == nil {
		if n >= 1 && n <= len(order) {
			return order[n-1]
		}
		return def
	}
	return line
}
