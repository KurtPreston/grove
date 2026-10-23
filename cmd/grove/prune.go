package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"grove/internal/color"
	"grove/internal/config"
	"grove/internal/project"
	"grove/internal/tmux"
	"grove/internal/ui"
)

// remoteMode records how prune handles merged branches on origin: ask (the
// default), or a --remote/--no-remote answer given up front.
type remoteMode int

const (
	remoteAsk remoteMode = iota
	remoteYes
	remoteNo
)

func cmdPrune(args []string) {
	args, force := popForce(args)
	dry, keepBranches, remote := false, false, remoteAsk
	for _, a := range args {
		switch a {
		case "--dry-run", "-n":
			dry = true
		case "--keep-branches":
			keepBranches = true
		case "--remote":
			remote = remoteYes
		case "--no-remote":
			remote = remoteNo
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

	// Mirror pull request heads so branches older than their squash-merged
	// request still count; a failed fetch falls back to the last mirror
	var pulls *pullHeads
	if cfg.PullRefsEnabled() && !partial {
		if !p.FetchPullHeads() {
			ui.Warn("Fetching pull request heads failed; using local state.")
		}
		pulls = newPullHeads(p, "origin/"+def)
	}

	wts, _ := p.Worktrees()
	cwd := mustGetwd()
	var forgeMerged map[string]string
	if cfg.ForgeEnabled() {
		forgeMerged = forgeMergedBranches(p, cfg)
	}
	in := pruneInputs{
		p: p, def: def, cwd: cwd, cfg: cfg, partial: partial,
		merged:      mergedBranches(p, "origin/"+def),
		forgeMerged: forgeMerged,
		pulls:       pulls,
	}

	// Classify every worktree in parallel; each check may run git (squash
	// detection, status), which is slow on large repos.
	results := make([]*pruneCandidate, len(wts))
	done := runParallel(len(wts), statusWorkers(), func(i int) {
		w := wts[i]
		if r := in.reason(w); r != "" {
			results[i] = &pruneCandidate{w.Branch, w.Path, r, !p.WorktreeClean(w.Path)}
		}
	})

	// Collect candidates in worktree order
	var candidates []pruneCandidate
	for i := range wts {
		<-done[i]
		if results[i] != nil {
			candidates = append(candidates, *results[i])
		}
	}
	remove, kept := splitPruneCandidates(candidates, force)

	// Plan branch deletions too, unless asked to keep every ref
	var locals, origins []pruneCandidate
	if !keepBranches {
		locals = in.localBranchCandidates(wts, remove)
		if remote != remoteNo {
			origins = in.originBranchCandidates()
		}
	}

	if len(kept) > 0 {
		ui.Warn("Merged, but kept because of local changes (commit or stash them, or re-run with --force to discard):")
		for _, c := range kept {
			printPruneCandidate(c, ui.Yellow+"local changes"+ui.Reset)
		}
	}
	if len(remove)+len(locals)+len(origins) == 0 {
		ui.Info("Nothing to prune.")
		return
	}

	// List the worktrees going away, flagging any whose changes are discarded
	dirtyCount := 0
	if len(remove) > 0 {
		header := "The following worktrees are merged (their branches are deleted too):"
		if keepBranches {
			header = "The following worktrees are merged (branch refs are kept):"
		}
		ui.Log(header)
		for _, c := range remove {
			note := ""
			if c.dirty {
				dirtyCount++
				note = ui.Yellow + "local changes will be discarded" + ui.Reset
			}
			printPruneCandidate(c, note)
		}
	}

	// List the other branch refs going away, locally and on origin
	if others := withoutWorktreeBranches(locals, remove); len(others) > 0 {
		ui.Log("The following other local branches are merged:")
		for _, c := range others {
			printPruneCandidate(c, "")
		}
	}
	if len(origins) > 0 {
		ui.Log("The following branches on origin are merged:")
		for _, c := range origins {
			printPruneCandidate(c, "")
		}
	}
	if dry {
		ui.Info("Dry run; nothing removed.")
		return
	}

	// Worktrees and local branches share one confirmation; declining stops here
	if len(remove)+len(locals) > 0 {
		fmt.Fprint(os.Stderr, localPrompt(len(remove), len(locals), dirtyCount))
		if !readYes() {
			ui.Info("Aborted.")
			return
		}
		removeWorktrees(p, remove)
		deleteLocalBranches(p, locals)
	}

	// Deleting from origin affects everyone, so it gets its own confirmation
	if len(origins) > 0 && confirmOriginDelete(remote, len(origins)) {
		deleteOriginBranches(p, origins)
	}
}

// withoutWorktreeBranches drops the branches of removed worktrees, which the
// worktree listing already shows.
func withoutWorktreeBranches(branches, remove []pruneCandidate) []pruneCandidate {
	shown := map[string]bool{}
	for _, c := range remove {
		shown[c.branch] = true
	}
	var out []pruneCandidate
	for _, c := range branches {
		if !shown[c.branch] {
			out = append(out, c)
		}
	}
	return out
}

// localPrompt builds the confirmation for removing worktrees and deleting local
// branches, naming only what is actually about to happen.
func localPrompt(worktrees, branches, dirty int) string {
	var parts []string
	if worktrees > 0 {
		parts = append(parts, fmt.Sprintf("remove %d %s", worktrees, plural(worktrees, "worktree")))
	}
	if branches > 0 {
		parts = append(parts, fmt.Sprintf("delete %d local %s", branches, plural(branches, "branch")))
	}
	action := strings.Join(parts, " and ")
	prompt := strings.ToUpper(action[:1]) + action[1:] + "?"
	if dirty > 0 {
		prompt += fmt.Sprintf(" %d %s local changes that will be discarded.", dirty, plural(dirty, "has", "have"))
	}
	return prompt + " [y/N] "
}

// plural returns one when n is 1 and many otherwise. With no many given it
// appends "s" (or "es" after a trailing "h") to one.
func plural(n int, one string, many ...string) string {
	if n == 1 {
		return one
	}
	if len(many) > 0 {
		return many[0]
	}
	if strings.HasSuffix(one, "h") {
		return one + "es"
	}
	return one + "s"
}

// confirmOriginDelete decides whether to delete merged branches from origin: a
// --remote/--no-remote flag answers up front, otherwise a terminal user is asked
// (defaulting to yes). Without a terminal there is nobody to ask, so the remote
// is left alone rather than changed on a default.
func confirmOriginDelete(mode remoteMode, n int) bool {
	switch mode {
	case remoteYes:
		return true
	case remoteNo:
		return false
	}
	if !isTerminal(os.Stdin) {
		ui.Info("Leaving origin's branches alone without a terminal to confirm; pass --remote to delete them.")
		return false
	}
	fmt.Fprintf(os.Stderr, "Delete %d %s from origin? [Y/n] ", n, plural(n, "branch"))
	return readYesDefault(true)
}

// removeWorktrees removes each pruned worktree and its tmux window. Anything
// reaching it is either clean or was opted in with --force and confirmed at the
// prompt, so it forces past git's submodule guard.
func removeWorktrees(p *project.Project, remove []pruneCandidate) {
	session := project.Sanitize(p.Name())
	for _, c := range remove {
		// Only branch worktrees get a tmux window
		if c.branch != "" && tmux.Has() {
			tmux.KillWindow(session, project.Sanitize(c.branch))
		}
		switch err := p.RemoveWorktree(c.path, true); {
		case err != nil:
			ui.Warn(fmt.Sprintf("Could not remove %s: %v", c.path, err))
		case c.branch == "":
			ui.Info(fmt.Sprintf("Removed detached worktree %s.", c.path))
		default:
			ui.Info(fmt.Sprintf("Removed worktree %s.", c.path))
		}
	}
	_ = project.Git(p.Base, "--git-dir=.", "worktree", "prune")
}

// deleteLocalBranches deletes each branch ref, one at a time so a single
// refusal (e.g. a worktree that failed to remove still has it checked out)
// doesn't stop the rest.
func deleteLocalBranches(p *project.Project, branches []pruneCandidate) {
	for _, c := range branches {
		if err := p.DeleteBranch(c.branch); err != nil {
			ui.Warn(fmt.Sprintf("Could not delete branch '%s': %v", c.branch, err))
		} else {
			ui.Info(fmt.Sprintf("Deleted branch '%s'.", c.branch))
		}
	}
}

// deleteOriginBranches deletes each branch from origin, one push per branch so
// one failure doesn't abort the rest. The pushes run one at a time: each also
// deletes the local origin/<name> ref, and concurrent pushes would contend for
// the packed-refs lock.
func deleteOriginBranches(p *project.Project, branches []pruneCandidate) {
	for _, c := range branches {
		if err := p.DeleteOriginBranch(strings.TrimPrefix(c.branch, "origin/")); err != nil {
			ui.Warn(fmt.Sprintf("Could not delete '%s': %v", c.branch, err))
		} else {
			ui.Info(fmt.Sprintf("Deleted '%s'.", c.branch))
		}
	}
}

// pruneCandidate is a worktree or branch prune classified as merged, plus the
// working-tree state that decides whether removing a worktree is safe.
type pruneCandidate struct {
	branch, path, reason string
	dirty                bool
}

// splitPruneCandidates separates the candidates prune removes from the ones it
// keeps. A dirty worktree is kept unless force was given: committed work is
// already upstream (that is what made it a candidate), but uncommitted work has
// no ref to survive on and untracked files are not recoverable from git at all.
// --force mirrors `grove rm --force` and opts those worktrees back in.
func splitPruneCandidates(cands []pruneCandidate, force bool) (remove, kept []pruneCandidate) {
	for _, c := range cands {
		if c.dirty && !force {
			kept = append(kept, c)
		} else {
			remove = append(remove, c)
		}
	}
	return remove, kept
}

// printPruneCandidate writes one listing row: swatch, branch, prune reason, and
// an optional trailing note about the worktree's local changes.
func printPruneCandidate(c pruneCandidate, note string) {
	// A detached worktree has no branch, so it is shown by its path
	label := c.branch
	if label == "" {
		label = "(no branch) " + c.path
	}
	fmt.Fprintf(os.Stderr, "  %s %-28s %s%s%s",
		color.Swatch(color.ForBranch(label)), label, ui.Dim, c.reason, ui.Reset)
	if note != "" {
		fmt.Fprintf(os.Stderr, " %s", note)
	}
	fmt.Fprintln(os.Stderr)
}

// pruneInputs holds the project-wide facts prune classifies worktrees against,
// gathered once so per-worktree checks don't repeat them.
type pruneInputs struct {
	p           *project.Project
	def, cwd    string
	cfg         config.Config
	merged      map[string]bool   // branches whose tips are ancestors of origin/<def>
	forgeMerged map[string]string // head branch -> head commit of its merged PR, per the forge
	pulls       *pullHeads        // fetched pull request heads; nil when that check is off
	partial     bool
}

// reason returns why a worktree is a prune candidate ("merged", "squashed",
// "forge", "pr #N", or "subject"), or "" when it should be kept. A worktree is never a
// candidate when it is bare, is on the default or a protected branch, or is the
// current directory. Detached worktrees are judged by their HEAD commit (see
// detachedReason).
func (in pruneInputs) reason(w project.Worktree) string {
	if w.Path == "" || w.Bare || w.Path == in.cwd {
		return ""
	}
	if w.Branch == "" {
		return in.detachedReason(w, "origin/"+in.def)
	}
	return in.refReason(w.Branch, w.Branch, in.merged[w.Branch])
}

// refReason classifies one branch, named name and resolved through ref (the
// branch itself, or origin/<name> for a remote one), as "merged", "squashed",
// "forge", or "pr #N"; or "" to keep it. ancestor reports whether ref's tip is already an
// ancestor of origin/<def>, as listed once up front by `git branch --merged`.
func (in pruneInputs) refReason(name, ref string, ancestor bool) string {
	p, cfg := in.p, in.cfg
	if name == in.def || cfg.PruneProtected(name) {
		return ""
	}
	if ancestor {
		return "merged"
	}
	// Squash/rebase merges rewrite history, so ancestry misses them; fall back
	// to patch-equivalence against origin/default. Skip on partial clones, where
	// the patch-id diff would lazy-fetch blobs from the remote.
	into := "origin/" + in.def
	if cfg.SquashDetectionEnabled() && !in.partial && p.BranchSquashMerged(ref, into) {
		return "squashed"
	}
	// Authoritative merged-PR state from the forge, when configured. The branch
	// must not have moved past the PR's head, so commits added after the merge
	// (or a reused branch name) never count as landed.
	if head, ok := in.forgeMerged[name]; ok && head != "" && p.CommitMerged(ref, head) {
		return "forge"
	}
	// A pull request can gain commits after a branch's last fetch, or be
	// superseded by a later one, so its squash no longer matches the branch's
	// own diff. Any landed request holding all of the branch's commits counts.
	if r := in.pulls.reason(ref); r != "" {
		return r
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

// localBranchCandidates returns the local branches prune deletes: the branch of
// every worktree it removes, plus every other merged local branch that no
// remaining worktree has checked out (git refuses to delete those anyway).
func (in pruneInputs) localBranchCandidates(wts []project.Worktree, remove []pruneCandidate) []pruneCandidate {
	// Reuse the verdicts already reached for removed worktrees
	removed := map[string]string{}
	for _, c := range remove {
		if c.branch != "" {
			removed[c.branch] = c.reason
		}
	}
	checkedOut := map[string]bool{}
	for _, w := range wts {
		if _, ok := removed[w.Branch]; w.Branch != "" && !ok {
			checkedOut[w.Branch] = true
		}
	}

	// Classify the rest in parallel, since squash detection runs git per branch
	var names []string
	for _, b := range in.p.LocalBranches() {
		if !checkedOut[b] {
			names = append(names, b)
		}
	}
	reasons := make([]string, len(names))
	done := runParallel(len(names), statusWorkers(), func(i int) {
		if r, ok := removed[names[i]]; ok {
			reasons[i] = r
		} else {
			reasons[i] = in.refReason(names[i], names[i], in.merged[names[i]])
		}
	})
	return collectBranchCandidates(names, reasons, done, "")
}

// originBranchCandidates returns origin's merged branches, labeled origin/<name>.
func (in pruneInputs) originBranchCandidates() []pruneCandidate {
	names := in.p.OriginBranches()
	merged := mergedOriginBranches(in.p, "origin/"+in.def)
	reasons := make([]string, len(names))
	done := runParallel(len(names), statusWorkers(), func(i int) {
		reasons[i] = in.refReason(names[i], "origin/"+names[i], merged[names[i]])
	})
	return collectBranchCandidates(names, reasons, done, "origin/")
}

// collectBranchCandidates waits for each parallel classification in order and
// keeps the branches with a prune reason, labeling each with prefix.
func collectBranchCandidates(names, reasons []string, done []chan struct{}, prefix string) []pruneCandidate {
	var out []pruneCandidate
	for i, name := range names {
		<-done[i]
		if reasons[i] != "" {
			out = append(out, pruneCandidate{branch: prefix + name, reason: reasons[i]})
		}
	}
	return out
}

// detachedReason classifies a detached worktree by its HEAD commit, which no
// branch names, so the forge can't be asked about it. Beyond ancestry and
// patch-equivalence it accepts "subject": every commit only HEAD holds landed
// upstream under the same subject, as when a commit checked out to test a merge
// was then squash-merged against a newer default branch.
func (in pruneInputs) detachedReason(w project.Worktree, into string) string {
	p, cfg, partial := in.p, in.cfg, in.partial
	if !w.Detached || w.Head == "" {
		return ""
	}
	if p.CommitMerged(w.Head, into) {
		return "merged"
	}
	// Squash checks share the branch path's partial-clone guard
	if !cfg.SquashDetectionEnabled() || partial {
		return ""
	}
	if p.BranchSquashMerged(w.Head, into) {
		return "squashed"
	}
	if p.SubjectsUpstream(w.Head, into) {
		return "subject"
	}
	return in.pulls.reason(w.Head)
}

// pullHeads judges a branch against the fetched pull request heads that contain
// it. Each head is judged once and the verdict shared, since branches are
// classified in parallel and several may sit inside the same request.
type pullHeads struct {
	p      *project.Project
	into   string
	mu     sync.Mutex
	landed map[string]bool
}

func newPullHeads(p *project.Project, into string) *pullHeads {
	return &pullHeads{p: p, into: into, landed: map[string]bool{}}
}

// reason returns "pr #N" (or "pr !N" for a GitLab merge request) when a pull
// request containing rev landed in into by merge or squash, or "" otherwise.
// A nil receiver means the check is off.
func (h *pullHeads) reason(rev string) string {
	if h == nil {
		return ""
	}
	for _, ref := range h.p.PullHeadsContaining(rev) {
		if h.headLanded(ref) {
			return "pr " + project.PullLabel(ref)
		}
	}
	return ""
}

// headLanded reports, caching the verdict, whether the pull request head ref
// landed in into (see project.PullLanded). A head both workers judge at once is
// just judged twice, with the same result.
func (h *pullHeads) headLanded(ref string) bool {
	h.mu.Lock()
	landed, seen := h.landed[ref]
	h.mu.Unlock()
	if seen {
		return landed
	}
	landed = h.p.PullLanded(ref, h.into)
	h.mu.Lock()
	h.landed[ref] = landed
	h.mu.Unlock()
	return landed
}

// forgeMergedBranches maps each merged PR's head-branch name to its head commit,
// according to the forge (via gh). It returns nil (warning once) when the repo
// can't be resolved, gh is unavailable, or the query fails, so prune falls back
// to git-only detection.
func forgeMergedBranches(p *project.Project, cfg config.Config) map[string]string {
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
		"--json", "headRefName,headRefOid",
	).Output()
	if err != nil {
		ui.Warn("prune.forge: 'gh pr list' failed; skipping forge check.")
		return nil
	}
	var prs []struct {
		HeadRefName string `json:"headRefName"`
		HeadRefOid  string `json:"headRefOid"`
	}
	if err := json.Unmarshal(out, &prs); err != nil {
		ui.Warn("prune.forge: could not parse gh output; skipping forge check.")
		return nil
	}

	// gh lists newest first, so the first PR seen for a branch name wins
	merged := make(map[string]string, len(prs))
	for _, pr := range prs {
		if _, seen := merged[pr.HeadRefName]; pr.HeadRefName != "" && !seen {
			merged[pr.HeadRefName] = pr.HeadRefOid
		}
	}
	return merged
}

// mergedBranches returns the local branches whose tips are reachable from into,
// listed with a single `git branch --merged`. It returns nil if git fails, so no
// branch counts as merged by ancestry.
func mergedBranches(p *project.Project, into string) map[string]bool {
	return mergedRefs(p, "branch", "--merged", into, "--format=%(refname:short)")
}

// mergedOriginBranches is mergedBranches for origin's branches, keyed without
// the origin/ prefix.
func mergedOriginBranches(p *project.Project, into string) map[string]bool {
	// Match full ref names so another remote's same-named branch can't collide
	out := map[string]bool{}
	for ref := range mergedRefs(p, "branch", "-r", "--merged", into, "--format=%(refname)") {
		const prefix = "refs/remotes/origin/"
		if name := strings.TrimPrefix(ref, prefix); strings.HasPrefix(ref, prefix) && name != "HEAD" {
			out[name] = true
		}
	}
	return out
}

// mergedRefs runs a `git branch --merged` listing and returns its names as a set,
// or nil if git fails.
func mergedRefs(p *project.Project, args ...string) map[string]bool {
	out, err := project.GitOut(p.Base, append([]string{"--git-dir=."}, args...)...)
	if err != nil {
		return nil
	}
	merged := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if b := strings.TrimSpace(line); b != "" {
			merged[b] = true
		}
	}
	return merged
}
