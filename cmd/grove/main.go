// Command grove is a branch-centric worktree + workflow launcher.
//
// Responsibilities are split cleanly:
//  1. manage a worktree folder per branch (git is the source of truth),
//  2. assign each branch a deterministic color,
//  3. trigger a "recipe" (dev environment / side effect) for the branch.
//
// (1) and (2) are core; (3) is pluggable. The default recipe set is GROVE_RECIPES.
//
// Shell integration: a `grove` shell function (and `wt` alias) sets $GROVE_CD_FILE
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
	"grove/internal/project"
	"grove/internal/recipe"
	_ "grove/internal/recipe/builtin" // register built-in recipes
	"grove/internal/tmux"
	"grove/internal/ui"
)

// Runtime configuration sourced from the environment (populated in main).
var (
	codeHome       string
	recipesDefault string
	tmuxLayout     string
	copyFiles      []string
	palette        []string
	webhookURL     string
	webhookToken   string
	sshHost        string
	inSSH          bool
)

func main() {
	loadConfig()

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
	case "help", "-h", "--help":
		usage()
	case "":
		cmdSwitch(nil)
	default:
		// Bare `grove BRANCH`: treat the token as a branch (uses GROVE_RECIPES).
		cmdSwitch([]string{cmd})
	}
}

func loadConfig() {
	home, _ := os.UserHomeDir()
	codeHome = getenv("CODE_HOME", filepath.Join(home, "Code"))
	recipesDefault = getenv("GROVE_RECIPES", "tmux")
	tmuxLayout = getenv("GROVE_TMUX_LAYOUT", "shell=,claude=claude")
	copyFiles = splitColon(getenv("GROVE_COPY", ".env"))
	palette = color.ParsePalette(os.Getenv("GROVE_PALETTE"))
	webhookURL = os.Getenv("GROVE_WEBHOOK_URL")
	webhookToken = os.Getenv("GROVE_WEBHOOK_TOKEN")
	sshHost = os.Getenv("GROVE_SSH_HOST")
	inSSH = os.Getenv("SSH_CONNECTION") != ""
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
	p, dir, branch, err := project.Clone(url, folder, codeHome, copyFiles)
	if err != nil {
		ui.Die(err.Error())
	}
	emitCD(dir)
	recipe.Run(recipe.Parse(recipesDefault), buildContext(p, branch, dir))
}

// cmdOpen: grove open [BRANCH] [RECIPES]. BRANCH omitted or "." infers the
// current worktree's branch; RECIPES omitted falls back to GROVE_RECIPES.
func cmdOpen(args []string) {
	p := mustResolve()
	branch := ""
	recipesArg := ""
	if len(args) >= 1 {
		branch = trimSlash(args[0])
	}
	if len(args) >= 2 {
		recipesArg = args[1]
	}
	if branch == "" || branch == "." {
		b, ok := currentBranch(mustGetwd())
		if !ok {
			ui.Die("could not infer branch from current directory; pass a BRANCH")
		}
		branch = b
	}
	doOpen(p, branch, recipesArg)
}

// cmdSwitch: bare grove / grove switch / grove BRANCH. Always uses GROVE_RECIPES.
func cmdSwitch(args []string) {
	p := mustResolve()
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
	doOpen(p, branch, "")
}

func doOpen(p *project.Project, branch, recipesArg string) {
	dir, err := p.EnsureWorktree(branch, copyFiles)
	if err != nil {
		ui.Die(err.Error())
	}
	emitCD(dir)
	names := recipesArg
	if names == "" {
		names = recipesDefault
	}
	recipe.Run(recipe.Parse(names), buildContext(p, branch, dir))
}

// cmdPath: resolve (creating if needed) BRANCH's worktree; print path to stdout.
func cmdPath(args []string) {
	p := mustResolve()
	if len(args) < 1 {
		ui.Die("usage: grove path BRANCH")
	}
	branch := trimSlash(args[0])
	dir, err := p.EnsureWorktree(branch, copyFiles)
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
	session := project.Sanitize(p.Name())
	tmux.EnsureSession(session, p.Dir)
	wts, _ := p.Worktrees()
	for _, w := range wts {
		if w.Bare || w.Branch == "" {
			continue
		}
		hex := color.ForBranch(w.Branch, palette)
		tmux.EnsureWorktreeWindow(session, project.Sanitize(w.Branch), w.Path, hex, color.FgForHex(hex), tmuxLayout)
	}
	tmux.KillPlaceholder(session)
	tmux.AttachOrSwitch(session, project.Sanitize(p.DefaultBranch()))
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
	hex := color.ForBranch(branch, palette)
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
			color.Swatch(color.ForBranch(c.branch, palette)), c.branch, ui.Dim, c.path, ui.Reset)
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
	hex := color.ForBranch(args[0], palette)
	fmt.Printf("%s %s\n", color.Swatch(hex), hex)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func buildContext(p *project.Project, branch, dir string) recipe.Context {
	hex := color.ForBranch(branch, palette)
	return recipe.Context{
		Branch:        branch,
		Dir:           dir,
		Color:         hex,
		Fg:            color.FgForHex(hex),
		Project:       p.Name(),
		ProjectDir:    p.Dir,
		Base:          p.Base,
		DefaultBranch: p.DefaultBranch(),
		SSHHost:       sshHost,
		InSSH:         inSSH,
		TmuxLayout:    tmuxLayout,
		WebhookURL:    webhookURL,
		WebhookToken:  webhookToken,
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

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func splitColon(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ":") {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
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
	fmt.Fprint(os.Stderr, `grove - branch-centric worktree + workflow launcher (alias: wt)

Usage:
  grove clone GIT_URL [FOLDER]   Clone a repo as a bare .base + default worktree
  grove BRANCH                   Switch to (or create) BRANCH's worktree; run GROVE_RECIPES
  grove open [BRANCH] [RECIPES]  Open BRANCH (or current) and run RECIPES (default: GROVE_RECIPES)
  grove switch [BRANCH]          Like a bare BRANCH; no BRANCH opens an fzf picker
  grove path BRANCH              Resolve (creating if needed) BRANCH's worktree; print its path
  grove tmux                     Attach the project session, building a window per worktree
  grove list | ls [--porcelain]  List worktrees; --porcelain prints branch<TAB>path to stdout
  grove prune                    Remove merged/gone worktrees (keeps branch refs)
  grove rm BRANCH [--force]      Remove a single worktree (keeps branch ref)
  grove color BRANCH             Print the deterministic color for BRANCH
  grove help                     Show this help

Recipes drive the dev environment for a branch. Built-ins: tmux, vscode-color-config,
webhook. Anything else resolves to grove-recipe-<name> on PATH.

Environment:
  CODE_HOME           Base dir for new projects (default: ~/Code)
  GROVE_RECIPES       Comma-separated recipes for open/switch (default: tmux)
  GROVE_TMUX_LAYOUT   tmux panes as name=cmd,name=cmd (default: shell=,claude=claude)
  GROVE_COPY          Colon-separated untracked files copied into new worktrees (default: .env)
  GROVE_PALETTE       Override the branch color palette (space/comma-separated hex)
  GROVE_WEBHOOK_URL   Target URL for the 'webhook' recipe (e.g. http://127.0.0.1:39787/open via a reverse SSH tunnel)
  GROVE_WEBHOOK_TOKEN Shared secret sent as 'Authorization: Bearer' to docent (optional)
  GROVE_SSH_HOST      Remote-SSH host alias embedded in webhook payloads
`)
}
