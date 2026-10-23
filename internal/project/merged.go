package project

import (
	"regexp"
	"strings"

	"grove/internal/ui"
)

// Prune fetches (with --prune) so merge detection sees the latest
// origin/<default> and remote-tracking refs before classifying worktrees.
func (p *Project) Prune() {
	if !GitQuiet(p.Base, "--git-dir=.", "fetch", "--prune", "origin") {
		ui.Warn("fetch failed; using local state.")
	}
}

// BranchSquashMerged reports whether branch's net changes are already present in
// into (e.g. "origin/main") even when the branch was squash- or rebase-merged.
// Those merges rewrite history, so the branch tip is not an ancestor of into and
// `git branch --merged` never lists it.
//
// It synthesizes a commit holding branch's tree parented on the merge-base of
// branch and into, then asks `git cherry` whether that combined diff is already
// upstream: a leading "-" means an equivalent patch exists in into. The
// synthesized commit is dangling (no ref points at it) and is reclaimed by gc.
func (p *Project) BranchSquashMerged(branch, into string) bool {
	base, err := GitOut(p.Base, "--git-dir=.", "merge-base", into, branch)
	if err != nil {
		return false
	}
	base = strings.TrimSpace(base)
	if base == "" {
		return false
	}
	tree, err := GitOut(p.Base, "--git-dir=.", "rev-parse", branch+"^{tree}")
	if err != nil {
		return false
	}
	tree = strings.TrimSpace(tree)
	if tree == "" {
		return false
	}
	virt, err := GitOut(p.Base, "--git-dir=.", "commit-tree", tree, "-p", base, "-m", "_")
	if err != nil {
		return false
	}
	virt = strings.TrimSpace(virt)
	if virt == "" {
		return false
	}
	out, err := GitOut(p.Base, "--git-dir=.", "cherry", into, virt)
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(out), "-")
}

// CommitMerged reports whether rev is an ancestor of into (e.g. "origin/main").
// It is the detached-HEAD counterpart of `git branch --merged`, which lists only
// branches.
func (p *Project) CommitMerged(rev, into string) bool {
	return GitQuiet(p.Base, "--git-dir=.", "merge-base", "--is-ancestor", rev, into)
}

// prSuffix matches the " (#123)" that forges append to a squash commit's subject.
var prSuffix = regexp.MustCompile(`\s+\(#\d+\)$`)

// SubjectsUpstream reports whether every non-merge commit that only head holds
// (reachable from no branch, remote-tracking ref, or tag) has a commit with the
// same subject in into but not in head, ignoring a trailing " (#N)".
//
// It catches a detached worktree left at a commit that was later squash-merged
// against a newer default branch: the squash's diff then differs from the
// commit's (conflict resolutions, a "Merge main" folded in), so the
// patch-equivalence check misses it, and no ref still holds the original commit.
// It is a heuristic, so it reports false when head holds no commits of its own:
// a HEAD that some ref still holds is not evidence of anything upstream.
func (p *Project) SubjectsUpstream(head, into string) bool {
	// Subjects of the commits only head holds
	own, err := GitOut(p.Base, "--git-dir=.", "log", "--no-merges", "--format=%s", head, "--not", "--branches", "--remotes", "--tags")
	if err != nil || strings.TrimSpace(own) == "" {
		return false
	}

	// Subjects that landed upstream since head forked, without their PR numbers
	theirs, err := GitOut(p.Base, "--git-dir=.", "log", "--no-merges", "--format=%s", into, "--not", head)
	if err != nil {
		return false
	}
	landed := make(map[string]bool)
	for _, s := range strings.Split(theirs, "\n") {
		landed[prSuffix.ReplaceAllString(strings.TrimSpace(s), "")] = true
	}

	// Every commit of head's own must have landed
	for _, s := range strings.Split(strings.TrimSpace(own), "\n") {
		s = prSuffix.ReplaceAllString(strings.TrimSpace(s), "")
		if s == "" || !landed[s] {
			return false
		}
	}
	return true
}
