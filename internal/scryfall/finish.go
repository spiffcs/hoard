package scryfall

// The finish-to-price-column rules. They live here, beside the Card type both
// the API client and the catalog produce, so every consumer values a finish
// the same way.

// PricedAsFoil reports whether a finish is priced from a foil column rather
// than the plain one — true for both foil and etched.
//
// It says which side of the foil/nonfoil line a finish falls on, not which
// column to read: etched has had its own price since schema v21 and falls back
// to foil only when the sources carry no etched figure. Callers choosing a
// column want EffectiveFoilPrice or the entryValue SQL; callers asking "is this
// a foil-ish finish at all" want this.
func PricedAsFoil(finish string) bool {
	return finish == "foil" || finish == "etched"
}

// FoilPrice is the ordinary foil price when there is one, else the etched
// price.
//
// The fallback is for printings sold only as etched: they have no foil figure,
// and leaving the foil column empty would report a card hoard can price as
// unpriced. It is not a way to value an etched copy — for that see
// EffectiveFoilPrice, which reads the etched figure first.
func FoilPrice(foil, etched *float64) *float64 {
	if foil != nil {
		return foil
	}
	return etched
}

// EffectiveFoilPrice is the price to value one holding at, given a printing's
// foil and etched figures.
//
// An etched copy reads the etched price and falls back to foil, since not every
// source splits the product; anything else reads foil. This mirrors the
// entryValue SQL in the store, so a value computed in Go and one computed in
// SQLite cannot disagree.
func EffectiveFoilPrice(finish string, foil, etched *float64) *float64 {
	if finish == "etched" && etched != nil {
		return etched
	}
	return foil
}

// Finishes returns the finishes a card comes in, in a stable
// nonfoil→foil→etched order. No translation happens here anymore: since
// schema v8 the whole tool speaks Scryfall's own finish vocabulary, so this
// only imposes an order and drops anything unrecognized.
func Finishes(c Card) []string {
	has := map[string]bool{}
	for _, f := range c.Finishes {
		has[f] = true
	}
	var out []string
	for _, f := range []string{"nonfoil", "foil", "etched"} {
		if has[f] {
			out = append(out, f)
		}
	}
	return out
}
