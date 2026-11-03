package main

import (
	"strings"

	"grove/internal/config"
	"grove/internal/project"
	"grove/internal/ui"
)

// sparseFlags holds the --sparse and --full flags of an open/switch/path.
type sparseFlags struct {
	// Paths lists the folders named with --sparse; nil when not passed.
	Paths []string
	// Full asks for a full checkout, overriding grove.json's "sparse".
	Full bool
}

// popSparse removes --sparse PATHS / --sparse=PATHS (repeatable, each
// comma-separated) and --full from args. Passing both, or --sparse with no
// folders, is an error.
func popSparse(args []string) ([]string, sparseFlags) {
	var out, raw []string
	var f sparseFlags
	named := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--full":
			f.Full = true
		case a == "--sparse":
			named = true
			if i+1 < len(args) {
				raw = append(raw, args[i+1])
				i++
			}
		case strings.HasPrefix(a, "--sparse="):
			named = true
			raw = append(raw, strings.TrimPrefix(a, "--sparse="))
		default:
			out = append(out, a)
		}
	}

	// Validate the combination before handing it back
	f.Paths = project.NormalizeSparse(raw)
	if named && f.Paths == nil {
		ui.Die("--sparse needs at least one folder, e.g. --sparse apps/web")
	}
	if f.Full && f.Paths != nil {
		ui.Die("pass either --sparse or --full, not both")
	}
	return out, f
}

// forCreate returns the folders a new worktree checks out: --full wins, then
// --sparse, then grove.json's "sparse". Nil means a full checkout.
func (f sparseFlags) forCreate(cfg config.Config) []string {
	if f.Full {
		return nil
	}
	if f.Paths != nil {
		return f.Paths
	}
	return project.NormalizeSparse(cfg.Sparse)
}

// reshape applies --sparse or --full to a worktree that already existed. With
// neither flag it leaves the worktree as it is, whatever grove.json says.
func (f sparseFlags) reshape(p *project.Project, dir string) {
	if !f.Full && f.Paths == nil {
		return
	}
	if err := p.SetSparse(dir, f.Paths); err != nil {
		ui.Die(err.Error())
	}
}
