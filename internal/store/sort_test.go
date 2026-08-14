package store

import (
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
)

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

	if in[0].Name != "cheap" {
		t.Errorf("decksByValue mutated its argument: %v", names(in))
	}
}

func TestCollectionByValue(t *testing.T) {
	mk := func(name string, fin finish.Finish, qty int, value float64) CollectionRow {
		r := CollectionRow{Finish: fin, Quantity: qty, Value: value}
		r.Name = name
		return r
	}
	in := []CollectionRow{
		mk("cheap", finish.Nonfoil, 1, 1),

		mk("bulk-but-many", finish.Nonfoil, 10, 50),
		mk("unpriced-b", finish.Nonfoil, 3, 0),
		mk("one-expensive", finish.Nonfoil, 1, 30),
		mk("unpriced-a", finish.Nonfoil, 1, 0),

		mk("foil-heavy", finish.Foil, 2, 120),
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
	mk := func(name, board string, fin finish.Finish, qty int, usd, usdFoil *float64) EntryView {
		e := EntryView{Finish: fin, Board: board, Quantity: qty}
		e.Card.Name = name
		e.Card.PriceUSD = usd
		e.Card.PriceUSDFoil = usdFoil
		return e
	}
	in := []EntryView{

		mk("commander-cheap", "commander", finish.Nonfoil, 1, price(2), nil),
		mk("main-mid", "main", finish.Nonfoil, 1, price(40), nil),

		mk("main-many", "main", finish.Nonfoil, 10, price(9), nil),

		mk("side-foil", "side", finish.Foil, 1, price(1), price(75)),
		mk("side-unpriced", "side", finish.Nonfoil, 1, nil, nil),
	}
	got := EntriesByValue(in)

	want := []string{"main-many", "side-foil", "main-mid", "commander-cheap", "side-unpriced"}
	for i, name := range want {
		if got[i].Card.Name != name {
			t.Errorf("position %d = %q, want %q", i, got[i].Card.Name, name)
		}
	}

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
