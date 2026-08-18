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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
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
			// Bare `grove BRANCH`: treat the token as a branch (uses grove.json recipes).
			cmdSwitch([]string{cmd})
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
	doOpen(p, branch, filter, from, force)
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
	doOpen(p, branch, "", from, force)
}

func doOpen(p *project.Project, branch, filter, from string, force bool) {
	cfg := loadCfg(p)
	beforeCtx := buildBeforeContext(p, branch)
	beforeCreate := func() error {
		return recipe.RunBefore(filterRecipes(cfg.BeforeCreateBranch(), filter), beforeCtx)
	}
	dir, created, err := p.EnsureWorktree(branch, cfg.Copy, baseResolver(p, from), beforeCreate)
	if err != nil {
		ui.Die(err.Error())
	}
	ctx := buildContext(p, branch, dir, created)
	recipe.Run(filterRecipes(cfg.OnOpen(), filter), ctx)
	if created || force {
		recipe.Run(filterRecipes(cfg.AfterFirstOpen(), filter), ctx)
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
	dir, _, err := p.EnsureWorktree(branch, loadCfg(p).Copy, baseResolver(p, from), nil)
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
	p := mustResolve()
	porcelain, byTime := parseListFlags(args)
	wts, _ := p.Worktrees()
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
	if byTime {
		sortWorktreesByCommitTime(rows, times)
	}
	if porcelain {
		for _, w := range rows {
			fmt.Printf("%s\t%s\n", w.Branch, w.Path)
		}
		return
	}
	fmt.Fprintf(os.Stderr, "%sProject:%s %s  %s(%s)%s\n",
		ui.Bold, ui.Reset, p.Name(), ui.Dim, p.Dir, ui.Reset)
	session := project.Sanitize(p.Name())
	now := time.Now()
	ages := make([]string, len(rows))
	ageWidth := 0
	for i, w := range rows {
		ages[i] = formatCommitAge(times[w.Head], now)
		if n := len(ages[i]); n > ageWidth {
			ageWidth = n
		}
	}
	for i, w := range rows {
		printRow(p, session, w, ages[i], ageWidth)
	}
}

func parseListFlags(args []string) (porcelain, byTime bool) {
	for _, a := range args {
		switch a {
		case "--porcelain":
			porcelain = true
		case "-t", "--time":
			byTime = true
		}
	}
	return porcelain, byTime
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

func printRow(p *project.Project, session string, w project.Worktree, age string, ageWidth int) {
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

	fmt.Fprintf(os.Stderr, "%s  %s  %s %s%s%s  %s\n",
		sw, tmuxMark, dirty, ui.Dim, fmt.Sprintf("%-*s", ageWidth, age), ui.Reset, branch)
}

func cmdPrune(args []string) {
	dry := false
	for _, a := range args {
		if a == "--dry-run" || a == "-n" {
			dry = true
		}
	}
	p := mustResolve()
	cfg := loadCfg(p)
	def := p.DefaultBranch()
	ui.Info("Fetching and pruning remotes...")
	p.Prune()

	partial := p.IsPartialClone()
	if cfg.SquashDetectionEnabled() && partial {
		ui.Warn("Partial (blobless) clone detected; skipping squash/rebase-merge detection (ancestry-only) to avoid on-demand fetches.")
	}

	wts, _ := p.Worktrees()
	cwd := mustGetwd()
	var forgeMerged map[string]bool
	if cfg.ForgeEnabled() {
		forgeMerged = forgeMergedBranches(p, cfg)
	}
	type cand struct {
		branch, path, reason string
		dirty                bool
	}
	var candidates []cand
	dirtyCount := 0
	for _, w := range wts {
		if r := pruneReason(p, w, def, cwd, cfg, forgeMerged, partial); r != "" {
			dirty := !p.WorktreeClean(w.Path)
			if dirty {
				dirtyCount++
			}
			candidates = append(candidates, cand{w.Branch, w.Path, r, dirty})
		}
	}
	if len(candidates) == 0 {
		ui.Info("Nothing to prune.")
		return
	}

	ui.Log("The following worktrees are merged (branch refs are kept):")
	for _, c := range candidates {
		fmt.Fprintf(os.Stderr, "  %s %-28s %s%s%s",
			color.Swatch(color.ForBranch(c.branch)), c.branch, ui.Dim, c.reason, ui.Reset)
		if c.dirty {
			fmt.Fprintf(os.Stderr, " %slocal changes will be discarded%s", ui.Yellow, ui.Reset)
		}
		fmt.Fprintln(os.Stderr)
	}
	if dry {
		ui.Info("Dry run; no worktrees removed.")
		return
	}
	prompt := "Remove these worktree directories? [y/N] "
	if dirtyCount > 0 {
		prompt = fmt.Sprintf("Remove these worktree directories? %d have local changes that will be discarded. [y/N] ", dirtyCount)
	}
	fmt.Fprint(os.Stderr, prompt)
	if !readYes() {
		ui.Info("Aborted.")
		return
	}

	session := project.Sanitize(p.Name())
	for _, c := range candidates {
		if tmux.Has() {
			tmux.KillWindow(session, project.Sanitize(c.branch))
		}
		// The user confirmed removal at the prompt, so force past any local
		// changes; the branch ref is kept regardless.
		switch err := p.RemoveWorktree(c.path, true); {
		case err != nil:
			ui.Warn(fmt.Sprintf("Could not remove %s: %v", c.path, err))
		default:
			ui.Info(fmt.Sprintf("Removed worktree %s (branch '%s' kept).", c.path, c.branch))
		}
	}
	_ = project.Git(p.Base, "worktree", "prune")
}

// pruneReason returns why a worktree is a prune candidate ("merged", "squashed",
// or "forge"), or "" when it should be kept. A worktree is never a candidate when
// it is bare, has no branch, is the default branch, or is the current directory.
func pruneReason(p *project.Project, w project.Worktree, def, cwd string, cfg config.Config, forgeMerged map[string]bool, partial bool) string {
	if w.Path == "" || w.Branch == "" || w.Bare {
		return ""
	}
	if w.Branch == def || w.Path == cwd {
		return ""
	}
	into := "origin/" + def
	if branchMerged(p, w.Branch, into) {
		return "merged"
	}
	// Squash/rebase merges rewrite history, so ancestry misses them; fall back
	// to patch-equivalence against origin/default. Skip on partial clones, where
	// the patch-id diff would lazy-fetch blobs from the remote.
	if cfg.SquashDetectionEnabled() && !partial && p.BranchSquashMerged(w.Branch, into) {
		return "squashed"
	}
	// Authoritative merged-PR state from the forge, when configured.
	if forgeMerged[w.Branch] {
		return "forge"
	}
	// Otherwise keep it. A "gone" upstream (branch.<name>.remote set with no
	// origin/<name> ref) is deliberately NOT a prune signal. grove points every
	// branch at origin/<branch> for out-of-the-box push/pull (see setUpstream), so
	// a brand-new branch that was never pushed lands in the exact same git state as
	// one whose upstream was pushed and later deleted: `git branch -vv` reports
	// "[origin/<name>: gone]" for both, and no local ref, reflog, or track field
	// tells them apart. Treating that as a candidate discarded never-pushed work,
	// so grove prunes only branches whose work is provably upstream (the
	// merged/squashed/forge checks above).
	return ""
}

// forgeMergedBranches returns the set of head-branch names whose PRs are merged,
// according to the forge (via gh). It returns nil (warning once) when the repo
// can't be resolved, gh is unavailable, or the query fails, so prune falls back
// to git-only detection.
func forgeMergedBranches(p *project.Project, cfg config.Config) map[string]bool {
	repo := cfg.ForgeRepo()
	if repo == "" {
		repo = p.OriginRepoSlug()
	}
	if repo == "" {
		ui.Warn("prune.forge: could not determine origin repo; skipping forge check.")
		return nil
	}
	if _, err := exec.LookPath("gh"); err != nil {
		ui.Warn("prune.forge: gh not found on PATH; skipping forge check.")
		return nil
	}
	out, err := exec.Command("gh", "pr", "list",
		"--repo", repo,
		"--state", "merged",
		"--limit", "200",
		"--json", "headRefName",
	).Output()
	if err != nil {
		ui.Warn("prune.forge: 'gh pr list' failed; skipping forge check.")
		return nil
	}
	var prs []struct {
		HeadRefName string `json:"headRefName"`
	}
	if err := json.Unmarshal(out, &prs); err != nil {
		ui.Warn("prune.forge: could not parse gh output; skipping forge check.")
		return nil
	}
	merged := make(map[string]bool, len(prs))
	for _, pr := range prs {
		if pr.HeadRefName != "" {
			merged[pr.HeadRefName] = true
		}
	}
	return merged
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

// cmdLaunch: grove launch [DIR] / grove here. Runs the user-level onOpen hooks
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
		ui.Die("no user hooks configured; create " + path +
			` with a "hooks": {"onOpen": [...]} object (e.g. vscode-color-config, webhook).`)
	}

	name := filepath.Base(abs)
	recipe.Run(cfg.OnOpen(), buildLaunchContext(name, abs))
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
	p, err := project.Resolve(mustGetwd())
	if err != nil {
		ui.Die(err.Error())
	}
	reconcileStale(p)
	return p
}

// reconcileStale clears git's leftover bookkeeping for worktree folders deleted
// outside grove (e.g. `rm -rf`), so every command works from what's actually on
// disk. It runs on project resolution rather than only in `grove list` so that
// reused-branch flows (switch/open/path) don't trip over a vanished worktree
// that git still thinks is checked out. Branch refs are kept.
func reconcileStale(p *project.Project) {
	pruned := p.ReconcileStale()
	if len(pruned) == 0 {
		return
	}
	noun := "worktree"
	if len(pruned) > 1 {
		noun = "worktrees"
	}
	ui.Info(fmt.Sprintf("Reconciled %d removed %s: %s (branch refs kept).",
		len(pruned), noun, strings.Join(pruned, ", ")))
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

// isInteractive reports whether stdin is a terminal, so grove only prompts when
// a human can answer. Scripts, pipes, and non-TTY SSH commands fall through to
// non-interactive defaults instead of blocking on a prompt.
func isInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// readLine reads a single line from stdin (without the trailing newline).
func readLine() string {
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimRight(line, "\r\n")
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
  grove list | ls [-t] [--porcelain]  List worktrees with last commit time; -t newest first; --porcelain prints branch<TAB>path
  grove prune                    Remove merged worktrees (keeps branch refs)
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
