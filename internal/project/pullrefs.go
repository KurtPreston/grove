package project

import (
	"os/exec"
	"regexp"
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

// PullLanded reports whether the pull request whose fetched head is ref landed
// in into. Either its head is an ancestor of into (a merge commit), or a commit
// in into names the request (as forges title squash merges, e.g. "(#50)") and
// carries exactly the request's net diff. Matching the diff alone is not
// enough: an open request whose changes happen to equal an unrelated upstream
// commit would otherwise vouch for every branch inside it.
func (p *Project) PullLanded(ref, into string) bool {
	if p.CommitMerged(ref, into) {
		return true
	}
	label := PullLabel(ref)
	if label == ref {
		return false
	}

	// The request's net diff since it forked from into
	base, err := GitOut(p.Base, "--git-dir=.", "merge-base", into, ref)
	if err != nil || strings.TrimSpace(base) == "" {
		return false
	}
	want := p.patchID(strings.TrimSpace(base), ref)
	if want == "" {
		return false
	}

	// Commits that landed since then naming the request, not a longer number
	pattern := regexp.QuoteMeta(label) + "([^0-9]|$)"
	out, err := GitOut(p.Base, "--git-dir=.", "log", "-E", "--format=%H", "--grep="+pattern, into, "--not", ref, "--")
	if err != nil {
		return false
	}
	for _, c := range strings.Fields(out) {
		if p.patchID(c+"^", c) == want {
			return true
		}
	}
	return false
}

// patchID returns the stable patch ID of the diff from one commit to another,
// or "" when the diff is empty or git fails.
func (p *Project) patchID(from, to string) string {
	diff, err := GitOut(p.Base, "--git-dir=.", "diff-tree", "-p", "--no-color", "--no-ext-diff", from, to, "--")
	if err != nil || strings.TrimSpace(diff) == "" {
		return ""
	}

	// git patch-id reads the diff on stdin and prints "<id> <commit>"
	ctx, cancel := gitContext()
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", p.Base, "--git-dir=.", "patch-id", "--stable")
	cmd.Env = gitBaseEnv()
	cmd.Stdin = strings.NewReader(diff)
	out, err := cmd.Output()
	if fields := strings.Fields(string(out)); err == nil && len(fields) > 0 {
		return fields[0]
	}
	return ""
}
