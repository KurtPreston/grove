package project

import (
	"fmt"
	"strings"

	"grove/internal/ui"
)

// ---------------------------------------------------------------------------
// Sparse checkout
// ---------------------------------------------------------------------------

// NormalizeSparse cleans a list of sparse-checkout folders: it trims spaces and
// surrounding slashes, splits comma-separated entries, and drops empty and
// duplicate ones. The result is nil when nothing is left, which means a full
// checkout.
func NormalizeSparse(paths []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, entry := range paths {
		for _, p := range strings.Split(entry, ",") {
			p = strings.Trim(strings.TrimSpace(p), "/")
			if p == "" || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// SparsePaths returns the folders a worktree's cone-mode sparse checkout keeps,
// or nil when the worktree is a full checkout.
func SparsePaths(dir string) []string {
	out, err := GitOut(dir, "sparse-checkout", "list")
	if err != nil {
		return nil
	}
	return NormalizeSparse(strings.Split(strings.TrimSpace(out), "\n"))
}

// populateSparse limits a worktree created with --no-checkout to paths, then
// checks out its files. Without the checkout the index stays empty and every
// file would show as deleted.
func populateSparse(dir string, paths []string) error {
	ui.Info("Sparse checkout: " + strings.Join(paths, ", "))
	args := append([]string{"sparse-checkout", "set", "--cone", "--"}, paths...)
	if err := Git(dir, args...); err != nil {
		return fmt.Errorf("sparse-checkout set failed")
	}
	if err := Git(dir, "checkout", "--quiet"); err != nil {
		return fmt.Errorf("checkout after sparse-checkout failed")
	}
	return nil
}

// SetSparse reshapes an existing worktree. A nil paths widens a sparse worktree
// to a full checkout, which is always safe. A non-nil paths narrows it to those
// folders, which requires a clean worktree unless it only widens the current
// sparse set, so local changes are never hidden from view.
func (p *Project) SetSparse(dir string, paths []string) error {
	current := SparsePaths(dir)

	// Widen to a full checkout
	if paths == nil {
		if current == nil {
			return nil
		}
		ui.Info("Switching to a full checkout...")
		if err := Git(dir, "sparse-checkout", "disable"); err != nil {
			return fmt.Errorf("sparse-checkout disable failed")
		}
		return nil
	}

	// Leave the worktree alone when it already matches
	if sameSet(current, paths) {
		return nil
	}

	// Narrowing could hide local changes, so require a clean worktree
	widening := current != nil && coversAll(paths, current)
	if !widening && !p.WorktreeClean(dir) {
		return fmt.Errorf("%s has local changes; commit or stash them before switching to a sparse checkout", dir)
	}
	ui.Info("Sparse checkout: " + strings.Join(paths, ", "))
	args := append([]string{"sparse-checkout", "set", "--cone", "--"}, paths...)
	if err := Git(dir, args...); err != nil {
		return fmt.Errorf("sparse-checkout set failed")
	}
	return nil
}

// sameSet reports whether a and b hold the same folders in any order.
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	want := map[string]bool{}
	for _, p := range a {
		want[p] = true
	}
	for _, p := range b {
		if !want[p] {
			return false
		}
	}
	return true
}

// coversAll reports whether every folder in inner sits inside some folder in
// outer, so switching from inner to outer only adds files.
func coversAll(outer, inner []string) bool {
	for _, in := range inner {
		covered := false
		for _, out := range outer {
			if in == out || strings.HasPrefix(in, out+"/") {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}
