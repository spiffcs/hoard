package ui

import (
	"os"
	"strconv"
)

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
