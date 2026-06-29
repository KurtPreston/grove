// Command grove is a branch-centric worktree + workflow launcher.
//
// Responsibilities are split cleanly:
//  1. manage a worktree folder per branch (git is the source of truth),
//  2. assign each branch a deterministic color,
//  3. trigger a "recipe" (dev environment / side effect) for the branch.
//
// (1) and (2) are core; (3) is pluggable, configured per project in grove.json.
//
// Shell integration: a `grove` shell function sets $GROVE_CD_FILE
// before calling this binary. When a command should move the caller's shell, we
// write the target directory there; the function reads it and performs the cd.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"grove/internal/color"
	"grove/internal/config"
	"grove/internal/project"
	"grove/internal/recipe"
	"grove/internal/recipe/builtin" // register built-in recipes + layout helpers
	"grove/internal/tmux"
	"grove/internal/ui"
)

// inSSH is the one piece of runtime state still sourced from the environment:
// it is set by sshd, not user config. Everything else lives in grove.json.
var inSSH bool

func main() {
	inSSH = os.Getenv("SSH_CONNECTION") != ""

	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}

	switch cmd {
	case "clone":
		cmdClone(args[1:])
	case "open":
		cmdOpen(args[1:])
	case "switch", "sw":
		cmdSwitch(args[1:])
	case "path":
		cmdPath(args[1:])
	case "tmux":
		cmdTmux()
	case "list", "ls":
		cmdList(args[1:])
	case "prune":
		cmdPrune()
	case "rm", "remove":
		cmdRm(args[1:])
	case "color":
		cmdColor(args[1:])
	case "launch", "here":
		cmdLaunch(args[1:])
	case "help", "-h", "--help":
		usage()
	case "":
		cmdSwitch(nil)
	default:
		// Bare `grove BRANCH`: treat the token as a branch (uses grove.json recipes).
		cmdSwitch([]string{cmd})
	}
}

// loadCfg reads the project's grove.json, warning (and falling back to defaults)
// on any read/parse error so a malformed file never blocks a command.
func loadCfg(p *project.Project) config.Config {
	cfg, err := config.Load(p.Dir)
	if err != nil {
		ui.Warn("grove.json: " + err.Error() + "; using defaults.")
	}
	return cfg
}

// ---------------------------------------------------------------------------
// Commands
// ---------------------------------------------------------------------------

func cmdClone(args []string) {
	if len(args) < 1 {
		ui.Die("usage: grove clone GIT_URL [FOLDER]")
	}
	url := args[0]
	folder := ""
	if len(args) >= 2 {
		folder = args[1]
	}
	p, dir, branch, err := project.Clone(url, folder, config.Defaults().Copy)
	if err != nil {
		ui.Die(err.Error())
	}
	if err := config.Seed(p.Dir); err != nil {
		ui.Warn("could not write starter grove.json: " + err.Error())
	} else {
		ui.Info("Wrote starter " + config.Filename + " (edit it to configure recipes).")
	}
	cfg := loadCfg(p)
	emitCD(dir)
	recipe.Run(cfg.Recipes, buildContext(p, branch, dir, true, false))
}

// cmdOpen: grove open [BRANCH] [RECIPES] [--force]. BRANCH omitted or "." infers
// the current worktree's branch; RECIPES (a comma-separated list of recipe
// types) filters grove.json's recipes to only those types. --force re-runs
// one-time recipes (bootstrap) on an existing worktree.
func cmdOpen(args []string) {
	p := mustResolve()
	args, force := popForce(args)
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
	doOpen(p, branch, filter, force)
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
	doOpen(p, branch, "", force)
}

func doOpen(p *project.Project, branch, filter string, force bool) {
	cfg := loadCfg(p)
	dir, created, err := p.EnsureWorktree(branch, cfg.Copy)
	if err != nil {
		ui.Die(err.Error())
	}
	emitCD(dir)
	recipe.Run(filterRecipes(cfg.Recipes, filter), buildContext(p, branch, dir, created, force))
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

// cmdPath: resolve (creating if needed) BRANCH's worktree; print path to stdout.
func cmdPath(args []string) {
	p := mustResolve()
	if len(args) < 1 {
		ui.Die("usage: grove path BRANCH")
	}
	branch := trimSlash(args[0])
	dir, _, err := p.EnsureWorktree(branch, loadCfg(p).Copy)
	if err != nil {
		ui.Die(err.Error())
	}
	fmt.Println(dir)
}

// cmdTmux: attach the project session, building a window for every worktree.
func cmdTmux() {
	p := mustResolve()
	if !tmux.Has() {
		ui.Die("tmux is not installed.")
	}
	cfg := loadCfg(p)
	layout := tmuxLayout(cfg)
	session := project.Sanitize(p.Name())
	tmux.EnsureSession(session, p.Dir)
	wts, _ := p.Worktrees()
	for _, w := range wts {
		if w.Bare || w.Branch == "" {
			continue
		}
		hex := color.ForBranch(w.Branch)
		tmux.EnsureWorktreeWindow(session, project.Sanitize(w.Branch), w.Path, hex, color.FgForHex(hex), layout)
	}
	tmux.KillPlaceholder(session)
	tmux.AttachOrSwitch(session, project.Sanitize(p.DefaultBranch()))
}

// tmuxLayout returns the layout from the config's tmux recipe entry, or the
// built-in default when there is no tmux recipe.
func tmuxLayout(cfg config.Config) string {
	for _, r := range cfg.Recipes {
		if r.Type == "tmux" {
			return builtin.LayoutOr(r.Layout)
		}
	}
	return builtin.DefaultLayout
}

func cmdList(args []string) {
	p := mustResolve()
	if len(args) >= 1 && args[0] == "--porcelain" {
		listPorcelain(p)
		return
	}
	wts, _ := p.Worktrees()
	fmt.Fprintf(os.Stderr, "%sProject:%s %s  %s(%s)%s\n",
		ui.Bold, ui.Reset, p.Name(), ui.Dim, p.Dir, ui.Reset)
	session := project.Sanitize(p.Name())
	for _, w := range wts {
		if w.Bare {
			continue
		}
		printRow(p, session, w)
	}
}

func listPorcelain(p *project.Project) {
	wts, _ := p.Worktrees()
	for _, w := range wts {
		if w.Bare || w.Branch == "" {
			continue
		}
		fmt.Printf("%s\t%s\n", w.Branch, w.Path)
	}
}

func printRow(p *project.Project, session string, w project.Worktree) {
	branch := w.Branch
	if branch == "" {
		branch = "(no branch)"
	}
	hex := color.ForBranch(branch)
	sw := color.Swatch(hex)

	tmuxMark := ui.Dim + " -- " + ui.Reset
	if w.Branch != "" && tmux.Has() && tmux.WindowExists(session, project.Sanitize(w.Branch)) {
		tmuxMark = ui.Green + "tmux" + ui.Reset
	}

	dirty := " "
	if out, err := project.GitOut(w.Path, "status", "--porcelain"); err == nil && strings.TrimSpace(out) != "" {
		dirty = ui.Yellow + "*" + ui.Reset
	}

	fmt.Fprintf(os.Stderr, "%s  %s  %s %-26s %s%s%s\n",
		sw, tmuxMark, dirty, branch, ui.Dim, w.Path, ui.Reset)
}

func cmdPrune() {
	p := mustResolve()
	def := p.DefaultBranch()
	ui.Info("Fetching and pruning remotes...")
	p.Prune()

	wts, _ := p.Worktrees()
	cwd := mustGetwd()
	type cand struct{ branch, path string }
	var candidates []cand
	for _, w := range wts {
		if pruneConsider(p, w, def, cwd) {
			candidates = append(candidates, cand{w.Branch, w.Path})
		}
	}
	if len(candidates) == 0 {
		ui.Info("Nothing to prune.")
		return
	}

	ui.Log("The following worktrees are merged or gone (branch refs are kept):")
	for _, c := range candidates {
		fmt.Fprintf(os.Stderr, "  %s %-28s %s%s%s\n",
			color.Swatch(color.ForBranch(c.branch)), c.branch, ui.Dim, c.path, ui.Reset)
	}
	fmt.Fprint(os.Stderr, "Remove these worktree directories? [y/N] ")
	if !readYes() {
		ui.Info("Aborted.")
		return
	}

	session := project.Sanitize(p.Name())
	for _, c := range candidates {
		if tmux.Has() {
			tmux.KillWindow(session, project.Sanitize(c.branch))
		}
		if err := p.RemoveWorktree(c.path, false); err != nil {
			ui.Warn(fmt.Sprintf("%s is dirty; use 'grove rm %s --force' to remove it.", c.path, c.branch))
		} else {
			ui.Info(fmt.Sprintf("Removed worktree %s (branch '%s' kept).", c.path, c.branch))
		}
	}
	_ = project.Git(p.Base, "worktree", "prune")
}

// pruneConsider reports whether a worktree is a prune candidate: not default, not
// the cwd, and either merged into origin/default or its upstream is gone.
func pruneConsider(p *project.Project, w project.Worktree, def, cwd string) bool {
	if w.Path == "" || w.Branch == "" || w.Bare {
		return false
	}
	if w.Branch == def || w.Path == cwd {
		return false
	}
	if branchMerged(p, w.Branch, "origin/"+def) {
		return true
	}
	// Upstream still present? not a candidate.
	if project.GitQuiet(p.Base, "rev-parse", "--verify", "--quiet", w.Branch+"@{upstream}") {
		return false
	}
	// Was configured to track a remote (now gone)? candidate.
	return project.GitQuiet(p.Base, "config", "--get", "branch."+w.Branch+".remote")
}

func branchMerged(p *project.Project, branch, into string) bool {
	out, err := project.GitOut(p.Base, "branch", "--merged", into)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[len(fields)-1] == branch {
			return true
		}
	}
	return false
}

func cmdRm(args []string) {
	p := mustResolve()
	if len(args) < 1 {
		ui.Die("usage: grove rm BRANCH [--force]")
	}
	branch := args[0]
	force := false
	for _, a := range args[1:] {
		if a == "--force" || a == "-f" {
			force = true
		}
	}
	dir, ok := p.WorktreePathFor(branch)
	if !ok {
		ui.Die("no worktree for branch '" + branch + "'.")
	}
	if tmux.Has() {
		tmux.KillWindow(project.Sanitize(p.Name()), project.Sanitize(branch))
	}
	if err := p.RemoveWorktree(dir, force); err != nil {
		ui.Die(dir + " has changes; re-run with --force to discard them.")
	}
	ui.Info("Removed worktree for '" + branch + "' (branch ref kept).")
}

func cmdColor(args []string) {
	if len(args) < 1 {
		ui.Die("usage: grove color BRANCH")
	}
	hex := color.ForBranch(args[0])
	fmt.Printf("%s %s\n", color.Swatch(hex), hex)
}

// cmdLaunch: grove launch [DIR] / grove here. Runs the user-level recipes
// (~/.config/grove/config.json) against DIR (or cwd) without requiring a grove
// project or creating a worktree. Used directly and as the fallback for bare
// grove invocations outside a grove project.
func cmdLaunch(args []string) {
	dir := mustGetwd()
	if len(args) >= 1 && args[0] != "" {
		dir = trimSlash(args[0])
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		ui.Die("cannot resolve directory: " + err.Error())
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		ui.Die("not a directory: " + abs)
	}

	cfg, found, err := config.LoadUser()
	if err != nil {
		ui.Warn("user config: " + err.Error() + "; ignoring recipes.")
	}
	if !found {
		path, _ := config.UserConfigPath()
		ui.Die("no user recipes configured; create " + path +
			` with a "recipes" array (e.g. vscode-color-config, webhook).`)
	}

	name := filepath.Base(abs)
	recipe.Run(cfg.Recipes, buildLaunchContext(name, abs))
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func buildContext(p *project.Project, branch, dir string, created, force bool) recipe.Context {
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
		Force:         force,
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

func mustResolve() *project.Project {
	p, err := project.Resolve(mustGetwd())
	if err != nil {
		ui.Die(err.Error())
	}
	return p
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		ui.Die("cannot determine current directory: " + err.Error())
	}
	return wd
}

func currentBranch(cwd string) (string, bool) {
	out, err := project.GitOut(cwd, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", false
	}
	b := strings.TrimSpace(out)
	if b == "" || b == "HEAD" {
		return "", false
	}
	return b, true
}

func fzfPick(p *project.Project) string {
	wts, _ := p.Worktrees()
	var branches []string
	for _, w := range wts {
		if !w.Bare && w.Branch != "" {
			branches = append(branches, w.Branch)
		}
	}
	cmd := exec.Command("fzf", "--prompt=worktree> ", "--height=40%", "--reverse")
	cmd.Stdin = strings.NewReader(strings.Join(branches, "\n"))
	cmd.Stderr = os.Stderr
	var b strings.Builder
	cmd.Stdout = &b
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(b.String())
}

func emitCD(dir string) {
	if f := os.Getenv("GROVE_CD_FILE"); f != "" {
		_ = os.WriteFile(f, []byte(dir), 0o644)
	}
}

func readYes() bool {
	var reply string
	_, _ = fmt.Scanln(&reply)
	reply = strings.ToLower(strings.TrimSpace(reply))
	return strings.HasPrefix(reply, "y")
}

// trimSlash drops trailing slashes left by directory tab-completion (e.g. "feat/").
func trimSlash(s string) string {
	for strings.HasSuffix(s, "/") {
		s = strings.TrimSuffix(s, "/")
	}
	return s
}

func hasBin(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func usage() {
	fmt.Fprint(os.Stderr, `grove - branch-centric worktree + workflow launcher

Usage:
  grove clone GIT_URL [FOLDER]   Clone a repo (under FOLDER in the current dir) as a bare .base + default worktree
  grove BRANCH                   Switch to (or create) BRANCH's worktree; run grove.json recipes
  grove open [BRANCH] [TYPES]    Open BRANCH (or current); TYPES filters grove.json recipes by type
  grove switch [BRANCH]          Like a bare BRANCH; no BRANCH opens an fzf picker
  grove path BRANCH              Resolve (creating if needed) BRANCH's worktree; print its path
  grove tmux                     Attach the project session, building a window per worktree
  grove list | ls [--porcelain]  List worktrees; --porcelain prints branch<TAB>path to stdout
  grove prune                    Remove merged/gone worktrees (keeps branch refs)
  grove rm BRANCH [--force]      Remove a single worktree (keeps branch ref)
  grove color BRANCH             Print the deterministic color for BRANCH
  grove launch | here [DIR]      Run user-level recipes for DIR (or cwd) without a worktree
  grove help                     Show this help

Pass --force to open/switch to re-run one-time recipes (bootstrap) on an existing worktree.

Configuration lives in grove.json at the project root (beside .base), validated by
grove.schema.json. It declares an ordered "recipes" array; each entry has a "type"
plus that type's settings. Built-in types: tmux, vscode-color-config, webhook,
bootstrap. Any other type resolves to grove-recipe-<type> on PATH (settings exported
as GROVE_RECIPE_*). The top-level "copy" array tunes which files are copied.
Branch colors are derived automatically from a hash of the branch name.
'grove clone' seeds a starter grove.json.

Outside a grove project, 'grove' (or 'grove launch [DIR]') runs the recipes from a
user-level config at $XDG_CONFIG_HOME/grove/config.json (default ~/.config/grove/config.json)
against the directory, using the folder name for the color and webhook view. No
default recipe is assumed: with no user config, the launch is a no-op error.
`)
}
