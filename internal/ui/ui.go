// Package ui holds the small set of terminal output helpers shared across grove.
// Everything here writes to stderr so that stdout stays reserved for machine
// consumers (e.g. `grove path` and `grove list --porcelain`).
package ui

import (
	"fmt"
	"os"
)

const (
	Reset  = "\033[0m"
	Bold   = "\033[1m"
	Dim    = "\033[2m"
	Red    = "\033[0;31m"
	Green  = "\033[0;32m"
	Yellow = "\033[1;33m"
)

// Log prints a plain line to stderr.
func Log(s string) { fmt.Fprintln(os.Stderr, s) }

// Info prints a green status line to stderr.
func Info(s string) { fmt.Fprintf(os.Stderr, "%s%s%s\n", Green, s, Reset) }

// Warn prints a yellow warning to stderr.
func Warn(s string) { fmt.Fprintf(os.Stderr, "%s%s%s\n", Yellow, s, Reset) }

// Die prints a red error and exits non-zero.
func Die(s string) {
	fmt.Fprintf(os.Stderr, "%serror:%s %s\n", Red, Reset, s)
	os.Exit(1)
}
