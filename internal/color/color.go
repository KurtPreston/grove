// Package color assigns a deterministic, terminal-friendly color to a branch and
// derives a readable foreground for it. The branch->color mapping intentionally
// matches the original bash `wt` (which hashed with POSIX `cksum`), so colors
// stay stable across the rewrite.
package color

import (
	"fmt"
	"strconv"
	"strings"
)

// DefaultPalette is a curated, visually distinct set of hues. Override at runtime
// via GROVE_PALETTE (space- or comma-separated hex values).
var DefaultPalette = []string{
	"#3b82f6", "#ef4444", "#22c55e", "#eab308",
	"#a855f7", "#ec4899", "#14b8a6", "#f97316",
	"#6366f1", "#84cc16", "#06b6d4", "#f43f5e",
}

var crctab [256]uint32

func init() {
	const poly = 0x04C11DB7
	for i := 0; i < 256; i++ {
		c := uint32(i) << 24
		for k := 0; k < 8; k++ {
			if c&0x80000000 != 0 {
				c = (c << 1) ^ poly
			} else {
				c <<= 1
			}
		}
		crctab[i] = c
	}
}

// cksum replicates the POSIX `cksum` CRC (polynomial 0x04C11DB7, length-folded,
// one's complement) so the branch->index mapping is identical to the bash tool.
func cksum(data []byte) uint32 {
	var crc uint32
	for _, b := range data {
		crc = (crc << 8) ^ crctab[byte(crc>>24)^b]
	}
	for n := len(data); n > 0; n >>= 8 {
		crc = (crc << 8) ^ crctab[byte(crc>>24)^byte(n)]
	}
	return ^crc
}

// ParsePalette turns a GROVE_PALETTE override into a slice; empty input yields the
// default palette. Accepts whitespace- and/or comma-separated hex values.
func ParsePalette(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return DefaultPalette
	}
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	if len(fields) == 0 {
		return DefaultPalette
	}
	return fields
}

// ForBranch returns the palette color assigned to a branch name.
func ForBranch(branch string, palette []string) string {
	if len(palette) == 0 {
		palette = DefaultPalette
	}
	idx := int(cksum([]byte(branch)) % uint32(len(palette)))
	return palette[idx]
}

func rgb(hex string) (int, int, int) {
	h := strings.TrimPrefix(hex, "#")
	if len(h) < 6 {
		return 0, 0, 0
	}
	r, _ := strconv.ParseInt(h[0:2], 16, 0)
	g, _ := strconv.ParseInt(h[2:4], 16, 0)
	b, _ := strconv.ParseInt(h[4:6], 16, 0)
	return int(r), int(g), int(b)
}

// FgForHex picks black or white for legible text on the given background.
func FgForHex(hex string) string {
	r, g, b := rgb(hex)
	lum := (r*299 + g*587 + b*114) / 1000
	if lum > 140 {
		return "#000000"
	}
	return "#ffffff"
}

// Swatch renders a small truecolor block for terminal display.
func Swatch(hex string) string {
	r, g, b := rgb(hex)
	return fmt.Sprintf("\033[48;2;%d;%d;%dm   \033[0m", r, g, b)
}
