package project

import (
	"strings"
)

// pullRefspecs mirror the head ref forges publish for every pull request into
// grove's private refs/grove/ namespace: refs/pull/N/head on GitHub and Gitea,
// refs/merge-requests/N/head on GitLab. Each forge gets its own namespace so
// --prune maps every local ref back to exactly one remote ref.
var pullRefspecs = []string{
	"+refs/pull/*/head:refs/grove/pull/*",
	"+refs/merge-requests/*/head:refs/grove/merge-requests/*",
}

// pullRefPrefixes are the local namespaces pullRefspecs fill, with the notation
// each forge uses to name a request of that kind.
var pullRefPrefixes = []struct{ ref, label string }{
	{"refs/grove/pull/", "#"},
	{"refs/grove/merge-requests/", "!"},
}

// FetchPullHeads mirrors origin's pull request heads into refs/grove/, pruning
// those the forge no longer publishes. A forge that publishes none simply
// matches no refs. It reports whether the fetch succeeded; on failure the refs
// from the last successful fetch remain usable.
func (p *Project) FetchPullHeads() bool {
	args := append([]string{"--git-dir=.", "fetch", "--prune", "--no-tags", "origin"}, pullRefspecs...)
	return GitQuiet(p.Base, args...)
}

// PullHeadsContaining returns the fetched pull request head refs whose history
// contains rev, i.e. every request that carried all of rev's commits.
func (p *Project) PullHeadsContaining(rev string) []string {
	// List matching refs under every forge namespace in one call
	args := []string{"--git-dir=.", "for-each-ref", "--contains", rev, "--format=%(refname)"}
	for _, n := range pullRefPrefixes {
		args = append(args, n.ref)
	}
	out, err := GitOut(p.Base, args...)
	if err != nil {
		return nil
	}
	return strings.Fields(out)
}

// PullLabel names a fetched pull request head ref the way its forge does, such
// as "#50" for refs/grove/pull/50 or "!7" for a GitLab merge request.
func PullLabel(ref string) string {
	for _, n := range pullRefPrefixes {
		if strings.HasPrefix(ref, n.ref) {
			return n.label + strings.TrimPrefix(ref, n.ref)
		}
	}
	return ref
}
