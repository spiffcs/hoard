package scryfall

import (
	"strings"

	"github.com/spiffcs/hoard/internal/finish"
)

func FoilPrice(foil, etched *float64) *float64 {
	if foil != nil {
		return foil
	}
	return etched
}

func Finishes(c Card) []finish.Finish {
	has := map[string]bool{}
	for _, f := range c.Finishes {
		has[f] = true
	}
	var out []finish.Finish
	for _, f := range finish.All() {
		if has[f.String()] {
			out = append(out, f)
		}
	}
	return out
}

const VariationMarkers = "★†Φ"

func BaseNumber(number string) string {
	return strings.TrimRight(number, VariationMarkers)
}
