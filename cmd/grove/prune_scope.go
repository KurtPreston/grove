package main

import (
	"fmt"
	"strings"

	"grove/internal/config"
	"grove/internal/project"
	"grove/internal/ui"
)

// resolvePruneScope removes scope flags, with the last overriding configuration.
// Validate before any fetch or deletion; a typo must not widen a local cleanup.
func resolvePruneScope(args []string, cfg config.Config) ([]string, string, error) {
	scope := cfg.PruneScope()
	var rest []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--scope":
			if i+1 == len(args) || strings.HasPrefix(args[i+1], "-") {
				return nil, "", fmt.Errorf("--scope requires local or all")
			}
			i++
			scope = args[i]
		case strings.HasPrefix(arg, "--scope="):
			scope = strings.TrimPrefix(arg, "--scope=")
		default:
			rest = append(rest, arg)
		}
	}
	if scope != "local" && scope != "all" {
		return nil, "", fmt.Errorf("invalid prune scope %q: --scope/prune.scope must be local or all", scope)
	}
	return rest, scope, nil
}

// refreshPrune fetches the selected scope and returns origin candidate names.
// nil means all origin branches; a non-nil empty slice means none.
func refreshPrune(p *project.Project, scope string) ([]string, error) {
	if scope == "all" {
		ui.Info("Fetching and pruning remotes...")
		p.Prune()
		return nil, nil
	}
	names, err := p.LocalOriginBranches()
	if err != nil {
		return nil, err
	}
	ui.Info("Local scope: fetching local-related origin branches; skipping the global pull request mirror.")
	if err := p.PruneLocal(names, p.DefaultBranch()); err != nil {
		ui.Warn(fmt.Sprintf("%v; using available local state.", err))
	}
	// Keep an empty local scope distinct from the full-scope nil sentinel.
	return append([]string{}, names...), nil
}

// scopedOriginBranchCandidates avoids even the all-origin ancestry scan. Names
// were captured before deleting local refs so renamed upstreams remain in scope.
func (in pruneInputs) scopedOriginBranchCandidates() []pruneCandidate {
	names := in.originNames
	reasons := make([]string, len(names))
	done := runParallel(len(names), statusWorkers(), func(i int) {
		if names[i] == "HEAD" {
			return
		}
		ref := "refs/remotes/origin/" + names[i]
		if !project.GitQuiet(in.p.Base, "--git-dir=.", "show-ref", "--verify", ref) {
			return
		}
		ancestor := in.p.CommitMerged(ref, "origin/"+in.def)
		reasons[i] = in.refReason(names[i], ref, ancestor)
	})
	return collectBranchCandidates(names, reasons, done, "origin/")
}
