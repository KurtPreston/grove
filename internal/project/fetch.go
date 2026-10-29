package project

import (
	"os/exec"
	"strings"

	"grove/internal/ui"
)

// originFetcher brings single branches up to date from origin while a worktree
// is being created. Fetching only the refs creation needs, instead of all of
// origin, keeps creation fast in repositories with thousands of branches. Once
// a fetch fails it stops trying, so an offline create warns only once.
type originFetcher struct {
	base string
	off  bool
}

// branch updates refs/remotes/origin/<name> if origin has that branch. A
// branch origin doesn't have is skipped silently; an unreachable origin warns
// and leaves the local refs as they are.
func (f *originFetcher) branch(name string) {
	if f.off {
		return
	}
	if !fetchOriginBranch(f.base, name) {
		ui.Warn("fetch failed; using local refs.")
		f.off = true
	}
}

// fetchOriginBranch fetches origin's refs/heads/<name> into
// refs/remotes/origin/<name>, reporting false when origin can't be reached or
// the fetch fails. A branch origin doesn't have counts as success. It fetches
// directly rather than asking `git ls-remote` first, because ls-remote can't
// narrow its request to one ref and lists every branch and tag on origin. The
// explicit refspec writes the tracking ref whatever the remote's configured
// fetch refspec is, and --no-tags keeps git from also downloading every tag
// that points into the fetched history.
func fetchOriginBranch(base, name string) bool {
	ref := "refs/heads/" + name
	refspec := "+" + ref + ":refs/remotes/origin/" + name
	ui.Info("Fetching '" + name + "' from origin...")

	// Run the fetch with git's messages untranslated so a missing ref is recognizable
	ctx, cancel := gitContext()
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", base, "--git-dir=.", "fetch", "--quiet", "--no-tags", "origin", refspec)
	cmd.Env = append(gitBaseEnv(), "LC_ALL=C")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if cmd.Run() == nil {
		return true
	}
	return strings.Contains(stderr.String(), "couldn't find remote ref "+ref)
}
