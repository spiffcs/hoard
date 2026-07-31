package scryfall

// The finish-to-price-column rules. They live here, beside the Card type both
// the API client and the catalog produce, so every consumer values a finish
// the same way.

// PricedAsFoil reports whether a finish is valued from the foil price column.
// hoard's model has no separate etched price: Scryfall's etched figure is
// folded into the foil column (see FoilPrice), so etched holdings read the
// same column foils do.
func PricedAsFoil(finish string) bool {
	return finish == "foil" || finish == "etched"
}

// FoilPrice is the value of a Card's single foil price column: the ordinary
// foil price when there is one, else the etched price, so etched-only
// printings can still be valued.
func FoilPrice(foil, etched *float64) *float64 {
	if foil != nil {
		return foil
	}
	return etched
}

// Finishes returns the finishes a card comes in, translated to the tool's
// vocabulary (normal|foil|etched) in that stable order. Scryfall says
// "nonfoil" where the rest of the tool says "normal"; this is the shared
// translation table, so no consumer needs a private copy that can drift.
func Finishes(c Card) []string {
	has := map[string]bool{}
	for _, f := range c.Finishes {
		if f == "nonfoil" {
			f = "normal"
		}
		has[f] = true
	}
	var out []string
	for _, f := range []string{"normal", "foil", "etched"} {
		if has[f] {
			out = append(out, f)
		}
	}
	return out
}
