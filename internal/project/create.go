package project

import (
	"fmt"
	"path/filepath"
	"strings"

	"grove/internal/ui"
)

// ---------------------------------------------------------------------------
// Worktree creation
// ---------------------------------------------------------------------------

// setUpstream configures branch.<name>.remote/merge so a plain `git push` (or
// `git pull`) on branch targets origin/<branch> without needing --set-upstream.
// git only wires this up automatically when a branch is created from an existing
// remote-tracking ref (e.g. `worktree add --track`); grove sets it explicitly for
// its other creation paths — brand-new branches, pre-existing local branches, and
// the default branch materialized by clone — so every grove branch is pushable out
// of the box.
//
// grove's model is that a branch tracks its own same-named branch on origin, so
// it also repairs a stale upstream that points at a *different* branch on origin —
// typically the ref the branch was forked from (e.g. origin/release/next). Left in
// place, such an upstream silently redirects a bare `git push` to that base branch
// when the user's push.default is `upstream`/`tracking`, which is how a
// feature-branch push ends up rejected against a protected base. A deliberately
// configured upstream on a *different remote* (e.g. a fork's `upstream` remote) is
// left untouched — grove only manages same-repo tracking.
func setUpstream(base, branch string) {
	want := "refs/heads/" + branch
	remoteOut, _ := GitOut(base, "--git-dir=.", "config", "--get", "branch."+branch+".remote")
	mergeOut, _ := GitOut(base, "--git-dir=.", "config", "--get", "branch."+branch+".merge")
	remote := strings.TrimSpace(remoteOut)
	merge := strings.TrimSpace(mergeOut)

	// Already tracking its own origin branch: nothing to do.
	if remote == "origin" && merge == want {
		return
	}
	// A deliberate upstream on another remote (fork workflow): leave it alone.
	if remote != "" && remote != "origin" {
		return
	}
	_ = Git(base, "--git-dir=.", "config", "branch."+branch+".remote", "origin")
	_ = Git(base, "--git-dir=.", "config", "branch."+branch+".merge", want)
}

// PullLatest fast-forwards the worktree at dir (checked out on branch) to its
// upstream, reporting whether it succeeded. Unlike `git pull`, which fetches
// every branch on the remote, it fetches only the upstream branch, which keeps
// reopening a worktree fast in repositories with thousands of active branches.
// It is quiet, so it is safe to run alongside other work.
func PullLatest(dir, branch string) bool {
	// Resolve the upstream; without one there's nothing to pull.
	remote, _ := GitOut(dir, "config", "--get", "branch."+branch+".remote")
	merge, _ := GitOut(dir, "config", "--get", "branch."+branch+".merge")
	remote, merge = strings.TrimSpace(remote), strings.TrimSpace(merge)
	if remote == "" || merge == "" {
		return false
	}

	// A "." remote is a local upstream: nothing to fetch, merge it directly.
	if remote == "." {
		return GitQuiet(dir, "merge", "--ff-only", "--quiet", merge)
	}

	// Fetch just that branch, then fast-forward to it as `git pull` would. The
	// remote's fetch refspec still updates its tracking ref (e.g. origin/main).
	if !GitQuiet(dir, "fetch", "--quiet", remote, merge) {
		return false
	}
	return GitQuiet(dir, "merge", "--ff-only", "--quiet", "FETCH_HEAD")
}

// EnsureWorktreeOptions configures EnsureWorktree.
type EnsureWorktreeOptions struct {
	// Branch is the branch whose worktree to find or create.
	Branch string

	// Copy lists untracked files copied from the default-branch worktree into
	// freshly created ones.
	Copy []string

	// PickBase chooses which branch a brand-new branch is based off. It is
	// called only when the branch exists neither locally nor on origin —
	// reusing an existing branch never consults it. It receives the project
	// default branch and returns the branch name to base off (return def to
	// keep the historical "off the default branch" behavior). A nil PickBase
	// defaults to def.
	PickBase func(def string) (string, error)

	// BeforeCreate, if non-nil, is called only when the branch exists neither
	// locally nor on origin (the same brand-new-branch condition that consults
	// PickBase), after the base branch is resolved but before anything is
	// created. Its error aborts creation — nothing is created and the error is
	// returned to the caller. This is the hook point for grove.json's
	// beforeCreateBranch bucket.
	BeforeCreate func() error

	// NoFetch skips contacting origin, so creation uses local refs only. By
	// default grove fetches just the branch itself and, for a brand-new branch,
	// its base branch.
	NoFetch bool
}

// EnsureWorktree returns the worktree dir for opts.Branch, creating it if
// needed. The returned bool reports whether the worktree was created on this
// call (vs. reused), so callers can drive one-time afterFirstOpen behavior. A
// reused worktree is returned as-is; callers that want it updated call
// PullLatest.
func (p *Project) EnsureWorktree(opts EnsureWorktreeOptions) (string, bool, error) {
	branch := opts.Branch

	// A leading dash means an unrecognized flag, never a branch name.
	if strings.HasPrefix(branch, "-") {
		return "", false, fmt.Errorf("unknown option '%s' (branch names cannot start with '-'); see 'grove help'", branch)
	}
	if dir, ok := p.WorktreePathFor(branch); ok {
		ui.Info("Using existing worktree: " + dir)
		return dir, false, nil
	}

	dir := filepath.Join(p.Dir, Sanitize(branch))
	if pathExists(dir) {
		return "", false, fmt.Errorf("%s already exists but is not a worktree for '%s'", dir, branch)
	}

	// A branch that isn't local may be on origin, including one never fetched
	// before, so ask origin for it unless fetching is off
	fetcher := &originFetcher{base: p.Base, off: opts.NoFetch}
	local := GitQuiet(p.Base, "--git-dir=.", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if !local && opts.NoFetch {
		ui.Info("Skipping fetch; using local refs.")
	}
	if !local {
		fetcher.branch(branch)
	}

	switch {
	case local:
		ui.Info(fmt.Sprintf("Creating worktree for existing local branch '%s' at %s", branch, dir))
		if err := Git(p.Base, "--git-dir=.", "worktree", "add", dir, branch); err != nil {
			return "", false, fmt.Errorf("worktree add failed")
		}
		setUpstream(p.Base, branch)
	case GitQuiet(p.Base, "--git-dir=.", "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+branch):
		ui.Info(fmt.Sprintf("Creating worktree tracking origin/%s at %s", branch, dir))
		if err := Git(p.Base, "--git-dir=.", "worktree", "add", "--track", "-b", branch, dir, "origin/"+branch); err != nil {
			return "", false, fmt.Errorf("worktree add failed")
		}
	default:
		if err := p.createBranch(opts, dir, fetcher); err != nil {
			return "", false, err
		}
	}

	p.setupWorktree(dir, opts.Copy)
	return dir, true, nil
}

// createBranch creates opts.Branch as a brand-new branch with a worktree at
// dir. It asks opts.PickBase for the base branch, brings that base up to date
// from origin, then runs opts.BeforeCreate, which can abort before anything is
// created.
func (p *Project) createBranch(opts EnsureWorktreeOptions, dir string, fetcher *originFetcher) error {
	// Choose the base branch, defaulting to the project default
	def := p.DefaultBranch()
	baseBranch := def
	if opts.PickBase != nil {
		chosen, err := opts.PickBase(def)
		if err != nil {
			return err
		}
		if chosen != "" {
			baseBranch = chosen
		}
	}

	// Update the base from origin, then resolve it to a concrete ref
	fetcher.branch(baseBranch)
	baseRef, err := p.resolveBaseRef(baseBranch)
	if err != nil {
		return err
	}
	if opts.BeforeCreate != nil {
		if err := opts.BeforeCreate(); err != nil {
			return err
		}
	}

	// --no-track so autoSetupMerge doesn't point upstream at the base ref; we
	// then track the same-named branch so a plain push/pull targets origin/<branch>.
	ui.Info(fmt.Sprintf("Creating new branch '%s' off %s at %s", opts.Branch, baseRef, dir))
	if err := Git(p.Base, "--git-dir=.", "worktree", "add", "--no-track", "-b", opts.Branch, dir, baseRef); err != nil {
		return fmt.Errorf("worktree add failed")
	}
	setUpstream(p.Base, opts.Branch)
	return nil
}

// resolveBaseRef turns a base-branch name into a concrete ref to branch from.
// It prefers origin/<branch> when that remote-tracking ref exists so new
// branches start from the latest fetched upstream (matching grove's historical
// behavior for the default branch), falling back to the local branch when there
// is no remote counterpart. An unknown name is an error so a bad --from (or an
// empty picker selection) fails loudly instead of silently basing off HEAD.
func (p *Project) resolveBaseRef(branch string) (string, error) {
	if GitQuiet(p.Base, "--git-dir=.", "show-ref", "--verify", "--quiet", "refs/remotes/origin/"+branch) {
		return "origin/" + branch, nil
	}
	if GitQuiet(p.Base, "--git-dir=.", "rev-parse", "--verify", "--quiet", branch+"^{commit}") {
		return branch, nil
	}
	return "", fmt.Errorf("base branch %q not found locally or on origin", branch)
}
