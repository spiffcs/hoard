package ui

import (
	"os"
	"strconv"
)

// CellAspectOverride reads HOARD_CELL_ASPECT: the terminal cell's
// height:width ratio kitty image heights are computed from, a plain
// number like 2.8. Zero when unset or implausible, letting the caller
// fall back to its default. An override rather than a measurement: the
// kernel's reported pixel grid proved unreliable (Ghostty's gave a ratio
// that still letterboxed a gap under the card, observed live), so the
// tuned default wins unless the user says otherwise.
func CellAspectOverride() float64 {
	v := os.Getenv("HOARD_CELL_ASPECT")
	if v == "" {
		return 0
	}
	a, err := strconv.ParseFloat(v, 64)
	if err != nil || a < 1 || a > 4 {
		return 0
	}
	return a
}
