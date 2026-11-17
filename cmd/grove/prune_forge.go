package main

import (
	"encoding/json"
	"os/exec"

	"grove/internal/config"
	"grove/internal/project"
	"grove/internal/ui"
)

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
