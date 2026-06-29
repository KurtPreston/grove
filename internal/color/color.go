// Package color assigns a deterministic, terminal-friendly color to a branch and
// derives a readable foreground for it. A branch name is hashed (POSIX `cksum`)
// into a hue on the OKLCH color wheel, which
// spreads branches across the full hue circle to minimize color collisions.
package color

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// branchLightness and branchChroma are the fixed OKLCH lightness and chroma used
// when generating a branch color from its hashed hue. They are tuned to stay
// (mostly) within the sRGB gamut across all hues while remaining vivid and
// legible as terminal backgrounds.
const (
	branchLightness = 0.70
	branchChroma    = 0.14
)

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

// ForBranch returns the color assigned to a branch name: the branch hash is
// mapped to an OKLCH hue and rendered to an sRGB hex string, avoiding the
// collisions inherent in a small flat palette.
func ForBranch(branch string) string {
	hue := float64(cksum([]byte(branch)) % 360)
	return oklch(branchLightness, branchChroma, hue)
}

// oklch converts an OKLCH color (lightness l in [0,1], chroma c, hue h in
// degrees) to an sRGB hex string, clamping any out-of-gamut channels.
func oklch(l, c, hDeg float64) string {
	h := hDeg * math.Pi / 180
	a := c * math.Cos(h)
	b := c * math.Sin(h)

	// OKLab -> linear sRGB (Björn Ottosson's coefficients).
	lp := l + 0.3963377774*a + 0.2158037573*b
	mp := l - 0.1055613458*a - 0.0638541728*b
	sp := l - 0.0894841775*a - 1.2914855480*b

	lc := lp * lp * lp
	mc := mp * mp * mp
	sc := sp * sp * sp

	r := 4.0767416621*lc - 3.3077115913*mc + 0.2309699292*sc
	g := -1.2684380046*lc + 2.6097574011*mc - 0.3413193965*sc
	bl := -0.0041960863*lc - 0.7034186147*mc + 1.7076147010*sc

	return fmt.Sprintf("#%02x%02x%02x", linearToByte(r), linearToByte(g), linearToByte(bl))
}

// linearToByte applies the sRGB transfer function to a linear channel and clamps
// the result to a 0-255 byte.
func linearToByte(c float64) int {
	switch {
	case c <= 0:
		return 0
	case c >= 1:
		return 255
	case c <= 0.0031308:
		c = 12.92 * c
	default:
		c = 1.055*math.Pow(c, 1.0/2.4) - 0.055
	}
	v := int(math.Round(c * 255))
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
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
