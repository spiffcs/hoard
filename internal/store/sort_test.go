package store

import "testing"

func TestDecksByValue(t *testing.T) {
	mk := func(name string, value float64) DeckSummary {
		d := DeckSummary{Value: value}
		d.Name = name
		return d
	}
	in := []DeckSummary{
		mk("cheap", 1), mk("rich", 300), mk("zero-b", 0), mk("mid", 50), mk("zero-a", 0),
	}
	got := DecksByValue(in)

	want := []string{"rich", "mid", "cheap", "zero-a", "zero-b"}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("position %d = %q, want %q (full order: %v)", i, got[i].Name, name, names(got))
		}
	}

	// The caller's slice must be left alone: cmdDeckList and summaryTable both
	// sort the same result of ListDecks.
	if in[0].Name != "cheap" {
		t.Errorf("decksByValue mutated its argument: %v", names(in))
	}
}

func TestCollectionByValue(t *testing.T) {
	mk := func(name, finish string, qty int, value float64) CollectionRow {
		r := CollectionRow{Finish: finish, Quantity: qty, Value: value}
		r.Name = name
		return r
	}
	in := []CollectionRow{
		mk("cheap", "normal", 1, 1),
		// Quantity is already folded into Value by the
		mk("bulk-but-many", "normal", 10, 50),
		mk("unpriced-b", "normal", 3, 0),
		mk("one-expensive", "normal", 1, 30),
		mk("unpriced-a", "normal", 1, 0),
		// A foil holding is its own row now, not a second column.
		mk("foil-heavy", "foil", 2, 120),
	}
	got := CollectionByValue(in)

	want := []string{"foil-heavy", "bulk-but-many", "one-expensive", "cheap", "unpriced-a", "unpriced-b"}
	for i, name := range want {
		if got[i].Name != name {
			t.Errorf("position %d = %q, want %q (full order: %v)", i, got[i].Name, name, cardNames(got))
		}
	}
	if in[0].Name != "cheap" {
		t.Errorf("collectionByValue mutated its argument: %v", cardNames(in))
	}
}

func TestEntriesByValue(t *testing.T) {
	price := func(v float64) *float64 { return &v }
	mk := func(name, board, finish string, qty int, usd, usdFoil *float64) EntryView {
		e := EntryView{Finish: finish, Board: board, Quantity: qty}
		e.Card.Name = name
		e.Card.PriceUSD = usd
		e.Card.PriceUSDFoil = usdFoil
		return e
	}
	in := []EntryView{
		// Grouped by board as the store returns them, cheapest first.
		mk("commander-cheap", "commander", "normal", 1, price(2), nil),
		mk("main-mid", "main", "normal", 1, price(40), nil),
		// Quantity counts: 10 x $9 beats one $40 card.
		mk("main-many", "main", "normal", 10, price(9), nil),
		// Foil entries take the foil price.
		mk("side-foil", "side", "foil", 1, price(1), price(75)),
		mk("side-unpriced", "side", "normal", 1, nil, nil),
	}
	got := EntriesByValue(in)

	want := []string{"main-many", "side-foil", "main-mid", "commander-cheap", "side-unpriced"}
	for i, name := range want {
		if got[i].Card.Name != name {
			t.Errorf("position %d = %q, want %q", i, got[i].Card.Name, name)
		}
	}
	// Board grouping is deliberately flattened; the BOARD column still carries it.
	if got[0].Board != "main" || got[1].Board != "side" {
		t.Errorf("expected boards to interleave by value, got %q then %q",
			got[0].Board, got[1].Board)
	}
	if in[0].Card.Name != "commander-cheap" {
		t.Error("entriesByValue mutated its argument")
	}
}

func cardNames(rows []CollectionRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out
}

func names(decks []DeckSummary) []string {
	out := make([]string, len(decks))
	for i, d := range decks {
		out[i] = d.Name
	}
	return out
}
