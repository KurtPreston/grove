package project

import (
	"fmt"
	"sort"
	"strings"
)

// LocalOriginBranches returns origin branch names related to local heads:
// same-named branches and any differently named upstreams on origin.
func (p *Project) LocalOriginBranches() ([]string, error) {
	out, err := GitOut(p.Base, "--git-dir=.", "for-each-ref",
		"--format=%(refname:lstrip=2)%09%(upstream:remotename)%09%(upstream:remoteref)", "refs/heads/")
	if err != nil {
		return nil, fmt.Errorf("list local branches and upstreams: %w", err)
	}
	names := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Split(line, "\t")
		if fields[0] == "" {
			continue
		}
		names[fields[0]] = true
		if len(fields) == 3 && fields[1] == "origin" && strings.HasPrefix(fields[2], "refs/heads/") {
			names[strings.TrimPrefix(fields[2], "refs/heads/")] = true
		}
	}
	var result []string
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, nil
}

// PruneLocal refreshes only names and the default branch, without fetching tags
// or unrelated branches. Missing branches lose only their selected tracking ref.
// A fetch failure stops the refresh so an unreachable origin is reported once.
func (p *Project) PruneLocal(names []string, def string) error {
	seen := map[string]bool{}
	for _, name := range append([]string{def}, names...) {
		if seen[name] || name == "HEAD" {
			continue
		}
		seen[name] = true
		exists, err := fetchOriginBranchState(p.Base, name)
		if err != nil {
			return err
		}
		if !exists {
			ref := "refs/remotes/origin/" + name
			if err := Git(p.Base, "--git-dir=.", "update-ref", "--no-deref", "-d", ref); err != nil {
				return fmt.Errorf("prune %s: %w", ref, err)
			}
		}
	}
	return nil
}
