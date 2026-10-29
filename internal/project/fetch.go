package project

import (
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
// the fetch fails. A branch origin doesn't have counts as success. It asks
// origin first with ls-remote because fetching a missing ref is an error, and
// it passes an explicit refspec so the tracking ref is written whatever the
// remote's configured fetch refspec is. --no-tags keeps git from also
// downloading every tag that points into the fetched history.
func fetchOriginBranch(base, name string) bool {
	ref := "refs/heads/" + name

	// Ask origin whether it has the branch at all
	ui.Info("Fetching '" + name + "' from origin...")
	out, err := GitOut(base, "--git-dir=.", "ls-remote", "origin", ref)
	if err != nil {
		return false
	}
	if !lsRemoteHas(out, ref) {
		return true
	}

	// Fetch just that branch into its remote-tracking ref
	refspec := "+" + ref + ":refs/remotes/origin/" + name
	return GitQuiet(base, "--git-dir=.", "fetch", "--quiet", "--no-tags", "origin", refspec)
}

// lsRemoteHas reports whether `git ls-remote` output lists exactly ref. Patterns
// match ref-name tails, so the output can include other refs ending in it.
func lsRemoteHas(out, ref string) bool {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == ref {
			return true
		}
	}
	return false
}
