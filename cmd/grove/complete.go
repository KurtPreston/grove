package main

import (
	"fmt"
	"os"
	"strings"

	"grove/internal/project"
)

const filesSentinel = "__grove_files__"

var completeSubcommands = []string{
	"clone", "open", "switch", "sw", "path", "tmux", "list", "ls",
	"prune", "rm", "remove", "color", "launch", "here", "version", "update", "help",
}

// branchArgIndex maps a subcommand to the positional index (0-based, after the
// subcommand) that expects a branch name. Bare `grove BRANCH` uses index -1.
var branchArgIndex = map[string]int{
	"open": 0, "switch": 0, "sw": 0, "path": 0, "rm": 0, "remove": 0, "color": 0,
}

// fileArgIndex maps a subcommand to positional indices that expect a filesystem path.
var fileArgIndex = map[string][]int{
	"launch": {0}, "here": {0}, "clone": {1},
}

// completeFlags maps a subcommand to its supported flags for completion.
var completeFlags = map[string][]string{
	"open":     {"--force", "-f", "--from"},
	"switch":   {"--force", "-f", "--from"},
	"sw":       {"--force", "-f", "--from"},
	"path":     {"--from"},
	"prune":    {"--dry-run", "-n"},
	"rm":       {"--force", "-f"},
	"remove":   {"--force", "-f"},
	"list":     {"--porcelain"},
	"ls":       {"--porcelain"},
	"update":   {"--force", "-f"},
}

// cmdComplete implements `grove __complete WORD...` for shell tab completion.
// It prints newline-separated candidates filtered by the last word's prefix.
// Errors and non-grove contexts produce no output.
func cmdComplete(args []string) {
	if len(args) == 0 {
		return
	}
	cur := args[len(args)-1]
	prior := args[:len(args)-1]

	if len(prior) > 0 && prior[len(prior)-1] == "--from" {
		printCompleteBranches(cur)
		return
	}

	if strings.HasPrefix(cur, "-") {
		cmd, _, _ := parseCompleteWords(prior)
		printCompleteFiltered(completeFlagsFor(cmd), cur)
		return
	}

	cmd, argIdx, _ := parseCompleteWords(prior)
	if cmd == "" {
		var candidates []string
		candidates = append(candidates, completeSubcommands...)
		candidates = append(candidates, completeBranches("")...)
		printCompleteFiltered(candidates, cur)
		return
	}

	if idx, ok := branchArgIndex[cmd]; ok && argIdx == idx {
		printCompleteBranches(cur)
		return
	}

	for _, idx := range fileArgIndex[cmd] {
		if argIdx == idx {
			fmt.Println(filesSentinel)
			return
		}
	}

	if flags := completeFlagsFor(cmd); len(flags) > 0 && argIdx > 0 {
		printCompleteFiltered(flags, cur)
	}
}

// parseCompleteWords walks words before the current completion token, skipping
// flags and their values, and returns the subcommand plus the 0-based index of
// the positional argument being completed.
func parseCompleteWords(words []string) (cmd string, argIdx int, positional []string) {
	i := 0
	for i < len(words) {
		w := words[i]
		if w == "--from" {
			i += 2
			continue
		}
		if w == "--force" || w == "-f" || w == "--dry-run" || w == "-n" || w == "--porcelain" {
			i++
			continue
		}
		if cmd == "" {
			cmd = w
			i++
			continue
		}
		positional = append(positional, w)
		i++
	}
	return cmd, len(positional), positional
}

func completeFlagsFor(cmd string) []string {
	return completeFlags[cmd]
}

func completeBranches(prefix string) []string {
	wd, err := os.Getwd()
	if err != nil {
		return nil
	}
	p, err := project.Resolve(wd)
	if err != nil {
		return nil
	}
	return filterCompletePrefix(p.BranchList(), prefix)
}

func printCompleteBranches(prefix string) {
	printCompleteFiltered(completeBranches(prefix), prefix)
}

func printCompleteFiltered(items []string, prefix string) {
	for _, item := range filterCompletePrefix(items, prefix) {
		fmt.Println(item)
	}
}

func filterCompletePrefix(items []string, prefix string) []string {
	if prefix == "" {
		return items
	}
	var out []string
	for _, item := range items {
		if strings.HasPrefix(item, prefix) {
			out = append(out, item)
		}
	}
	return out
}
