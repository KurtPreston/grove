// Command grove is a branch-centric worktree + workflow launcher.
//
// Responsibilities are split cleanly:
//  1. manage a worktree folder per branch (git is the source of truth),
//  2. assign each branch a deterministic color,
//  3. trigger a "recipe" (dev environment / side effect) for the branch.
//
// (1) and (2) are core; (3) is pluggable, configured per project in grove.json.
//
// Shell integration is optional and drives a single opt-in recipe: a `grove`
// shell function sets $GROVE_CD_FILE before calling this binary, and the
// built-in `cd` recipe writes the worktree path there so the function can cd the
// caller's shell after grove exits. Without the `cd` recipe (or the sourced
// function) grove never moves your shell.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"grove/internal/color"
	"grove/internal/config"
	"grove/internal/project"
	"grove/internal/recipe"
	"grove/internal/recipe/builtin" // register built-in recipes + layout helpers
	"grove/internal/selfupdate"
	"grove/internal/tmux"
	"grove/internal/ui"

	"golang.org/x/term"
)

// inSSH is the one piece of runtime state still sourced from the environment:
// it is set by sshd, not user config. Everything else lives in grove.json.
var inSSH bool

// Build metadata, injected via -ldflags at release time (see .goreleaser.yaml).
// Defaults apply to `go build`/`go install` and source checkouts.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	inSSH = os.Getenv("SSH_CONNECTION") != ""

	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}

	// `grove SUBCOMMAND --help` shows help rather than treating the flag as an
	// argument (e.g. creating a branch named "help").
	if cmd != "__complete" && len(args) > 1 && wantsHelp(args[1:]) {
		usage()
		return
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
		cmdPrune(args[1:])
	case "rm", "remove":
		cmdRm(args[1:])
	case "color":
		cmdColor(args[1:])
	case "launch", "here":
		cmdLaunch(args[1:])
	case "version", "--version", "-v":
		cmdVersion()
	case "update":
		cmdUpdate(args[1:])
	case "help", "-h", "--help":
		usage()
	case "__complete":
		cmdComplete(args[1:])
	case "":
		cmdSwitch(nil)
	default:
		if dir, ok := existingDir(cmd); ok {
			// `grove PATH` where PATH is an existing directory: run user-level
			// recipes for it, equivalent to `cd PATH && grove here`.
			cmdLaunch([]string{dir})
		} else {
			// Bare `grove BRANCH`: treat the token as a branch (uses grove.json
			// recipes). Pass every argument so flags such as --from reach it.
			cmdSwitch(args)
		}
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
		ui.Warn("could not write starter " + config.SeedFilename + ": " + err.Error())
	} else {
		ui.Info("Wrote starter " + config.SeedFilename + " (edit it to configure hooks).")
	}
	cfg := loadCfg(p)
	ctx := buildContext(p, branch, dir, true)
	recipe.Run(cfg.OnOpen(), ctx)
	recipe.Run(cfg.AfterFirstOpen(), ctx)
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

// tmuxLayout returns the layout from the config's tmux recipe entry (checked
// across all three hooks buckets, since nothing stops a tmux recipe from
// living in any of them), or the built-in default when there is no tmux
// recipe.
func tmuxLayout(cfg config.Config) string {
	if cfg.Hooks == nil {
		return builtin.DefaultLayout
	}
	for _, bucket := range [][]config.RecipeConfig{
		cfg.Hooks.BeforeCreateBranch,
		cfg.Hooks.OnOpen,
		cfg.Hooks.AfterFirstOpen,
	} {
		for _, r := range bucket {
			if r.Type == "tmux" {
				return builtin.LayoutOr(r.Layout)
			}
		}
	}
	return builtin.DefaultLayout
}

func cmdList(args []string) {
	p, wts := mustResolveWorktrees()
	flags := parseListFlags(args)
	porcelain := flags.porcelain
	var rows []project.Worktree
	for _, w := range wts {
		if w.Bare {
			continue
		}
		if porcelain && w.Branch == "" {
			continue
		}
		rows = append(rows, w)
	}
	heads := make([]string, 0, len(rows))
	for _, w := range rows {
		heads = append(heads, w.Head)
	}
	times := p.CommitTimes(heads)
	if flags.byTime {
		sortWorktreesByCommitTime(rows, times)
	}
	if porcelain {
		for _, w := range rows {
			fmt.Printf("%s\t%s\n", w.Branch, w.Path)
		}
		return
	}
	// Start the dirty checks now so they run while the header and ages are built
	cfg := loadCfg(p)
	checkStatus := cfg.ListStatusEnabled() && !flags.noStatus
	timeout := cfg.ListStatusTimeout()
	statusCtx, cancelStatus := listStatusContext(timeout)
	defer cancelStatus()
	states := make([]project.Dirtiness, len(rows))
	var statusDone []chan struct{}
	if checkStatus {
		statusDone = runParallel(len(rows), statusWorkers(), func(i int) {
			states[i] = project.WorktreeDirtiness(statusCtx, rows[i].Path)
		})
	}

	// Print the header and compute the age column
	styled := stdoutIsTerminal()
	fmt.Println(style(fmt.Sprintf("%sProject:%s %s  %s(%s)%s",
		ui.Bold, ui.Reset, p.Name(), ui.Dim, p.Dir, ui.Reset), styled))
	session := project.Sanitize(p.Name())
	windows := tmux.WindowNames(session)
	now := time.Now()
	ages := make([]string, len(rows))
	ageWidth := 0
	for i, w := range rows {
		ages[i] = formatCommitAge(times[w.Head], now)
		if n := len(ages[i]); n > ageWidth {
			ageWidth = n
		}
	}

	// Stream rows in order as each one's status lands
	unknown := false
	for i, w := range rows {
		mark := " "
		if checkStatus {
			<-statusDone[i]
			unknown = unknown || states[i] == project.DirtyUnknown
			mark = dirtyMark(states[i])
		}
		printRow(w, windows, mark, ages[i], ageWidth, styled)
	}
	if unknown && statusCtx.Err() == context.DeadlineExceeded {
		fmt.Fprintf(os.Stderr, "%s? = git status took over %s; raise list.statusTimeout in grove.json, or skip the check with --no-status.%s\n",
			ui.Dim, timeout, ui.Reset)
	}
}

// listStatusContext bounds all of a listing's dirty checks together. On a huge
// repo a single `git status` can take seconds (e.g. while git starts the
// fsmonitor daemon after a reboot); past the timeout, rows show "?" instead.
// A zero timeout waits for every check.
func listStatusContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(context.Background(), timeout)
	}
	return context.WithCancel(context.Background())
}

// dirtyMark renders a row's local-change column.
func dirtyMark(d project.Dirtiness) string {
	switch d {
	case project.Dirty:
		return ui.Yellow + "*" + ui.Reset
	case project.DirtyUnknown:
		return ui.Dim + "?" + ui.Reset
	}
	return " "
}

// listFlags holds `grove list`'s command-line options.
type listFlags struct {
	porcelain, byTime, noStatus bool
}

func parseListFlags(args []string) listFlags {
	var f listFlags
	for _, a := range args {
		switch a {
		case "--porcelain":
			f.porcelain = true
		case "-t", "--time":
			f.byTime = true
		case "--no-status":
			f.noStatus = true
		}
	}
	return f
}

func sortWorktreesByCommitTime(wts []project.Worktree, times map[string]time.Time) {
	sort.SliceStable(wts, func(i, j int) bool {
		ti, tj := times[wts[i].Head], times[wts[j].Head]
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return wts[i].Branch < wts[j].Branch
	})
}

func formatCommitAge(at, now time.Time) string {
	if at.IsZero() {
		return "--"
	}
	d := now.Sub(at)
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return "just now"
	} else if d < time.Hour {
		return ageUnits(int(d.Minutes()), "minute")
	} else if d < 24*time.Hour {
		return ageUnits(int(d.Hours()), "hour")
	} else if d < 30*24*time.Hour {
		return ageUnits(int(d.Hours()/24), "day")
	} else if d < 365*24*time.Hour {
		n := int(d.Hours() / 24 / 30)
		if n < 1 {
			n = 1
		}
		return ageUnits(n, "month")
	}
	n := int(d.Hours() / 24 / 365)
	if n < 1 {
		n = 1
	}
	return ageUnits(n, "year")
}

func ageUnits(n int, unit string) string {
	if n == 1 {
		return "1 " + unit + " ago"
	}
	return fmt.Sprintf("%d %ss ago", n, unit)
}

func printRow(w project.Worktree, windows map[string]bool, dirty, age string, ageWidth int, styled bool) {
	branch := w.Branch
	if branch == "" {
		branch = "(no branch)"
	}

	tmuxMark := ui.Dim + " -- " + ui.Reset
	if w.Branch != "" && windows[project.Sanitize(w.Branch)] {
		tmuxMark = ui.Green + "tmux" + ui.Reset
	}

	row := fmt.Sprintf("%s  %s %s%s%s  %s",
		tmuxMark, dirty, ui.Dim, fmt.Sprintf("%-*s", ageWidth, age), ui.Reset, branch)
	if styled {
		// The swatch is nothing but color, so it only earns its space on a terminal.
		row = color.Swatch(color.ForBranch(branch)) + "  " + row
	}
	fmt.Println(style(row, styled))
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
	switch err := p.RemoveWorktree(dir, force); {
	case errors.Is(err, project.ErrWorktreeDirty):
		ui.Die(dir + " has local changes; re-run with --force to discard them.")
	case err != nil:
		ui.Die("could not remove " + dir + ": " + err.Error())
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

// cmdVersion prints the build version and metadata baked in at release time.
func cmdVersion() {
	fmt.Printf("grove %s (commit %s, built %s)\n", version, commit, date)
}

// cmdUpdate replaces the running grove binary in place with the latest published
// release (see internal/selfupdate). --force reinstalls even when already
// current; GROVE_VERSION / GROVE_REPO mirror install.sh to pin the tag or source.
func cmdUpdate(args []string) {
	_, force := popForce(args)
	res, err := selfupdate.Run(selfupdate.Options{
		CurrentVersion: version,
		Force:          force,
	})
	if err != nil {
		ui.Die(err.Error())
	}
	if res.UpToDate {
		ui.Info(fmt.Sprintf("grove is already up to date (%s).", displayVer(res.ToVersion)))
		return
	}
	ui.Info(fmt.Sprintf("Updated grove %s -> %s (%s).",
		displayVer(res.FromVersion), displayVer(res.ToVersion), res.BinaryPath))
	if len(res.ShellUpdated) > 0 {
		ui.Info("Refreshed shell integration: " + strings.Join(res.ShellUpdated, ", ") + ".")
	}
}

// displayVer trims a leading "v" so version output reads consistently whether it
// came from the baked-in build string ("0.3.2") or a release tag ("v0.3.3").
func displayVer(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "unknown"
	}
	return strings.TrimPrefix(v, "v")
}

// cmdLaunch: grove launch [DIR] / grove here. Runs onOpen hooks against DIR (or
// cwd) without creating a worktree. Used directly and as the fallback for bare
// grove invocations outside a grove project.
//
// A directory inside a grove project is configured by that project's grove.json,
// so the user-level config (~/.config/grove/config.json) is never consulted for
// it - only a directory outside any grove project falls back to that file.
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

	if root, ok := project.FindRoot(abs); ok {
		launchInProject(root, abs)
		return
	}

	cfg, err := config.LoadUser()
	if err != nil {
		ui.Die(noUserConfigMessage(abs, err))
	}
	recipe.Run(cfg.OnOpen(), buildLaunchContext(filepath.Base(abs), abs))
}

// otherUserConfigNames lists the accepted user-config file names apart from the
// canonical config.json already named in the error message.
func otherUserConfigNames() []string {
	var names []string
	for _, name := range config.UserConfigNames {
		if name != "config.json" {
			names = append(names, name)
		}
	}
	return names
}

// launchInProject runs the project's onOpen hooks for a directory that already
// lives under a grove project root, with the same context a switch/open would
// build (branch, project, base) so project hooks see the variables they expect.
func launchInProject(root, abs string) {
	p := &project.Project{Base: filepath.Join(root, ".base"), Dir: root}
	branch, ok := currentBranch(abs)
	if !ok {
		branch = filepath.Base(abs)
	}
	recipe.Run(loadCfg(p).OnOpen(), buildContext(p, branch, abs, false))
}

// noUserConfigMessage explains a failed user-level config load for dir, naming
// the file grove tried and what was wrong with it. dir is outside every grove
// project here (cmdLaunch checked), which is the reason grove looked at the
// user-level file at all - so say that first: a user who expected their project
// config to apply needs to know grove never saw one.
func noUserConfigMessage(dir string, err error) string {
	var cfgErr *config.UserConfigError
	if !errors.As(err, &cfgErr) {
		return dir + " is not a grove project, and grove could not read its user-level config: " + err.Error()
	}
	msg := dir + " is not a grove project. Attempted to use " + cfgErr.Path +
		" for default behavior, but the file was " + cfgErr.Problem + "."
	if cfgErr.Err != nil {
		msg += "\n  " + cfgErr.Err.Error()
	}
	msg += "\n  It needs a " + `"hooks": {"onOpen": [...]}` +
		" object (e.g. vscode-color-config, webhook)."
	// Only worth listing the alternatives when there is nothing to fix: a file
	// that exists but is broken should be fixed where it is, not renamed.
	if cfgErr.Problem == config.ProblemNotFound {
		msg += "\n  That directory also accepts " + strings.Join(otherUserConfigNames(), ", ") + "."
	}
	return msg
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

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

func mustResolve() *project.Project {
	p, _ := mustResolveWorktrees()
	return p
}

// mustResolveWorktrees is mustResolve plus the reconciled worktree list, for
// commands that enumerate worktrees anyway and can skip a second listing.
func mustResolveWorktrees() (*project.Project, []project.Worktree) {
	p, err := project.Resolve(mustGetwd())
	if err != nil {
		ui.Die(err.Error())
	}
	return p, reconcileStale(p)
}

// reconcileStale clears git's leftover bookkeeping for worktree folders deleted
// outside grove (e.g. `rm -rf`), so every command works from what's actually on
// disk. It runs on project resolution rather than only in `grove list` so that
// reused-branch flows (switch/open/path) don't trip over a vanished worktree
// that git still thinks is checked out. Branch refs are kept. It returns the
// worktree list as it stands after reconciling.
func reconcileStale(p *project.Project) []project.Worktree {
	pruned, wts := p.ReconcileStale()
	if len(pruned) == 0 {
		return wts
	}
	noun := "worktree"
	if len(pruned) > 1 {
		noun = "worktrees"
	}
	ui.Info(fmt.Sprintf("Reconciled %d removed %s: %s (branch refs kept).",
		len(pruned), noun, strings.Join(pruned, ", ")))
	return wts
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
	return fzfPickFrom(branches, "worktree> ")
}

// fzfPickFrom runs fzf over items with the given prompt, returning the selection
// (or "" when fzf is cancelled/fails).
func fzfPickFrom(items []string, prompt string) string {
	cmd := exec.Command("fzf", "--prompt="+prompt, "--height=40%", "--reverse")
	cmd.Stdin = strings.NewReader(strings.Join(items, "\n"))
	cmd.Stderr = os.Stderr
	var b strings.Builder
	cmd.Stdout = &b
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(b.String())
}

// isInteractive reports whether stdin is a terminal, so grove only prompts when
// a human can answer. Scripts, pipes, and non-TTY SSH commands fall through to
// non-interactive defaults instead of blocking on a prompt.
func isInteractive() bool {
	return isTerminal(os.Stdin)
}

// stdoutIsTerminal reports whether stdout is a terminal. Commands that print
// results to stdout use it to decide whether color is worth emitting: escape
// codes help a human and only corrupt a pipe or file.
func stdoutIsTerminal() bool {
	return isTerminal(os.Stdout)
}

// isTerminal reports whether f is a real terminal. Checking for a character
// device is not enough: /dev/null is one too, and agents and scripts often run
// with stdin redirected there.
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

// style returns s unchanged when styled, and with its SGR escapes removed
// otherwise, so one formatting path serves both terminals and pipes.
func style(s string, styled bool) string {
	if styled {
		return s
	}
	return sgr.ReplaceAllString(s, "")
}

// readLine reads a single line from stdin (without the trailing newline).
func readLine() string {
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}

func readYes() bool {
	return readYesDefault(false)
}

// readYesDefault reads a y/n reply, returning def when the reply is empty.
func readYesDefault(def bool) bool {
	var reply string
	_, _ = fmt.Scanln(&reply)
	reply = strings.ToLower(strings.TrimSpace(reply))
	if reply == "" {
		return def
	}
	return strings.HasPrefix(reply, "y")
}

// trimSlash drops trailing slashes left by directory tab-completion (e.g. "feat/").
func trimSlash(s string) string {
	for strings.HasSuffix(s, "/") {
		s = strings.TrimSuffix(s, "/")
	}
	return s
}

// expandTilde expands a leading ~ or ~/ to the user's home directory. Shells
// normally expand these, but grove may still see a literal ~ (e.g. when quoted).
func expandTilde(p string) string {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	} else if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// existingDir reports whether token names an existing directory (after tilde
// expansion and trailing-slash trimming), returning the resolved path to use.
func existingDir(token string) (string, bool) {
	p := expandTilde(trimSlash(token))
	if p == "" {
		return "", false
	}
	if fi, err := os.Stat(p); err == nil && fi.IsDir() {
		return p, true
	}
	return "", false
}

func hasBin(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func usage() {
	fmt.Fprint(os.Stderr, `grove - branch-centric worktree + workflow launcher

Usage:
  grove clone GIT_URL [FOLDER]   Clone a repo (under FOLDER in the current dir) as a bare .base + default worktree
  grove BRANCH [--from REF]      Switch to (or create) BRANCH's worktree; run grove.json recipes
  grove DIR                      If DIR is an existing directory, run user-level recipes for it (like 'cd DIR && grove here')
  grove open [BRANCH] [TYPES]    Open BRANCH (or current); TYPES filters grove.json recipes by type
  grove switch [BRANCH]          Like a bare BRANCH; no BRANCH opens an fzf picker
  grove path BRANCH              Resolve (creating if needed) BRANCH's worktree; print its path
  grove tmux                     Attach the project session, building a window per worktree
  grove list | ls [-t] [--porcelain] [--no-status]
                                 List worktrees with last commit time and local changes (*);
                                 -t newest first; --porcelain prints branch<TAB>path;
                                 --no-status skips the local-changes check
  grove prune [--dry-run] [--force] [--keep-branches] [--remote | --no-remote]
                                 Remove merged worktrees and delete merged branches, locally and
                                 (after asking) on origin; worktrees with local changes are kept
                                 unless --force discards them; --keep-branches only removes worktrees
  grove rm BRANCH [--force]      Remove a single worktree (keeps branch ref); --force discards local changes
  grove color BRANCH             Print the deterministic color for BRANCH
  grove launch | here [DIR]      Run user-level recipes for DIR (or cwd) without a worktree
  grove version                  Print the grove version and build metadata
  grove update [--force]         Update grove in place to the latest published release
  grove help                     Show this help
  grove __complete WORD...       Hidden: print tab-completion candidates (used by shell scripts)

Pass --force to open/switch to re-run the afterFirstOpen bucket (one-time setup)
on an existing worktree.

When a branch doesn't exist yet, grove creates it. Before creating it, grove runs
the beforeCreateBranch hooks (if any); a non-zero exit aborts creation entirely.
If stdin is a TTY, grove then asks which branch to base the new branch off (fzf
when available, else a numbered menu). Pass --from REF to choose the base without
being asked (also used by scripts and non-TTY sessions); otherwise the new branch
is based off the default branch.

Worktree folders deleted outside grove (e.g. 'rm -rf') are reconciled automatically
on the next command: grove drops git's stale bookkeeping so the worktree stops
showing up and the branch can be checked out again. Branch refs are always kept.

Configuration lives in grove.json (or grove.jsonc, with comments/trailing commas)
at the project root (beside .base), validated by grove.schema.json. It declares a
"hooks" object with three buckets, each an ordered array of recipes (a "type" plus
that type's settings):
  beforeCreateBranch  gates a brand-new branch; a non-zero exit aborts creation
  onOpen              runs on every open: creating, reopening, or a plain launch
  afterFirstOpen      runs once, after the first open of a fresh worktree (one-time setup)
Built-in recipe types: tmux, vscode-color-config, webhook, command, cd. Any other
type resolves to grove-recipe-<type> on PATH (settings exported as GROVE_RECIPE_*).
The top-level "copy" array tunes which files are copied. The optional "cd" recipe
moves your shell into the worktree and requires the shell integration to be sourced.
Branch colors are derived automatically from a hash of the branch name.
'grove clone' seeds a starter grove.jsonc.

Outside a grove project, 'grove' (or 'grove launch [DIR]') runs the onOpen hooks from
a user-level config at $XDG_CONFIG_HOME/grove/config.json (default ~/.config/grove/config.json)
against the directory, using the folder name for the color and webhook view. No
default hooks are assumed: with no user config, the launch is a no-op error.
`)
}
