package store

import (
	"slices"
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// enriched is a card carrying a Scryfall document, so its generated columns
// resolve the way a refreshed catalog's do.
func enriched() scryfall.Card {
	c := ulamog()
	c.Raw = []byte(`{"id":"ulamog-id","name":"Ulamog, the Infinite Gyre",
	  "set":"uma","set_name":"Ultimate Masters","collector_number":"7",
	  "rarity":"mythic","type_line":"Legendary Creature — Eldrazi",
	  "mana_cost":"{11}","cmc":11.0,"oracle_text":"Annihilator 4",
	  "artist":"Mark Tedin","released_at":"2018-12-07","layout":"normal",
	  "color_identity":[]}`)
	return c
}

func TestCardDetailResolvesDescriptiveFields(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCard(enriched(), false, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	d, err := s.CardDetail("ulamog-id")
	if err != nil {
		t.Fatalf("CardDetail: %v", err)
	}
	if !d.Enriched {
		t.Fatal("Enriched = false for a card with a stored document")
	}
	if d.Rarity == nil || *d.Rarity != "mythic" {
		t.Errorf("Rarity = %v, want mythic", d.Rarity)
	}
	if d.TypeLine == nil || *d.TypeLine != "Legendary Creature — Eldrazi" {
		t.Errorf("TypeLine = %v", d.TypeLine)
	}
	if d.CMC == nil || *d.CMC != 11.0 {
		t.Errorf("CMC = %v, want 11", d.CMC)
	}
	if d.Artist == nil || *d.Artist != "Mark Tedin" {
		t.Errorf("Artist = %v", d.Artist)
	}
	// Identity carries through from the base columns, not the document.
	if d.Name != "Ulamog, the Infinite Gyre" || d.SetCode != "uma" {
		t.Errorf("identity wrong: %+v", d.Card)
	}
}

// A card with no stored document must report unknown, not empty. A detail pane
// that prints "" for rarity is indistinguishable from one printing a real value
// that happens to be blank, and hides the fact that a refresh would fix it.
func TestCardDetailReportsUnenrichedCards(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCard(ulamog(), false, 1); err != nil { // no Raw
		t.Fatalf("AddCard: %v", err)
	}

	d, err := s.CardDetail("ulamog-id")
	if err != nil {
		t.Fatalf("CardDetail: %v", err)
	}
	if d.Enriched {
		t.Error("Enriched = true without a stored document")
	}
	if d.Rarity != nil || d.TypeLine != nil || d.CMC != nil {
		t.Errorf("want nil descriptive fields, got %v %v %v", d.Rarity, d.TypeLine, d.CMC)
	}
	// The prices and identity still work; only the derived fields are missing.
	if d.PriceUSD == nil || *d.PriceUSD != 10.00 {
		t.Errorf("PriceUSD = %v, want 10", d.PriceUSD)
	}
}

func TestParseColorIdentity(t *testing.T) {
	s := newTestStore(t)
	for _, tc := range []struct {
		name string
		json string
		want []string
	}{
		{"mono", `["B"]`, []string{"B"}},
		{"two colours", `["W","U"]`, []string{"W", "U"}},
		{"colourless", `[]`, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := ulamog()
			c.ID = "c-" + tc.name
			c.Raw = []byte(`{"color_identity":` + tc.json + `}`)
			if err := s.UpsertCatalogCards([]scryfall.Card{c}); err != nil {
				t.Fatalf("upsert: %v", err)
			}
			d, err := s.CardDetail(c.ID)
			if err != nil {
				t.Fatalf("CardDetail: %v", err)
			}
			if !slices.Equal(d.ColorIdentity, tc.want) {
				t.Errorf("ColorIdentity = %v, want %v", d.ColorIdentity, tc.want)
			}
		})
	}
}

// The question neither `list` nor `deck show` could answer: four copies of a
// card spread across the collection and two decks.
func TestHoldingsOfSpansContainers(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCard(ulamog(), false, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	for _, name := range []string{"Deck A", "Deck B"} {
		if _, err := s.UpsertDeck(
			DeckMeta{Name: name, Source: "text", SourceID: name},
			[]Entry{{ScryfallID: "ulamog-id", Finish: "normal", Board: "main", Quantity: 2}},
		); err != nil {
			t.Fatalf("UpsertDeck %s: %v", name, err)
		}
	}

	holdings, err := s.HoldingsOf("ulamog-id")
	if err != nil {
		t.Fatalf("HoldingsOf: %v", err)
	}
	if len(holdings) != 3 {
		t.Fatalf("got %d holdings, want 3 (collection + two decks): %+v", len(holdings), holdings)
	}
	var total int
	var kinds []string
	for _, h := range holdings {
		total += h.Quantity
		kinds = append(kinds, h.ContainerKind)
	}
	if total != 5 {
		t.Errorf("total copies = %d, want 5", total)
	}
	// The collection sorts first, so a reader sees what they hold loose before
	// what is committed to decks.
	if kinds[0] != KindCollection {
		t.Errorf("kinds = %v, want the collection first", kinds)
	}
}

func TestHoldingsOfUnheldCard(t *testing.T) {
	s := newTestStore(t)
	if err := s.UpsertCatalogCards([]scryfall.Card{ulamog()}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	holdings, err := s.HoldingsOf("ulamog-id")
	if err != nil {
		t.Fatalf("HoldingsOf: %v", err)
	}
	if len(holdings) != 0 {
		t.Errorf("got %+v, want none for a catalogued but unheld card", holdings)
	}
}

func TestPriceSeriesReturnsTheWholeSeries(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCard(ulamog(), false, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	backdate(t, s, stamps(t, s)[0], "2026-07-01T00:00:00Z")

	risen := ulamog()
	risen.PriceUSD = f(12.00)
	if err := s.UpdatePrices([]scryfall.Card{risen}); err != nil {
		t.Fatalf("UpdatePrices: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}

	series, err := s.PriceSeries("ulamog-id", "normal")
	if err != nil {
		t.Fatalf("PriceSeries: %v", err)
	}
	if len(series) != 2 {
		t.Fatalf("got %d points, want 2: %+v", len(series), series)
	}
	// Oldest first, so a sparkline reads left to right without reversing.
	if series[0].Price != 10.00 || series[1].Price != 12.00 {
		t.Errorf("series = %+v, want 10 then 12", series)
	}
	if series[0].AsOf >= series[1].AsOf {
		t.Errorf("series not in chronological order: %+v", series)
	}
	// The foil series is its own; the non-foil move must not appear in it.
	foil, err := s.PriceSeries("ulamog-id", "foil")
	if err != nil {
		t.Fatalf("PriceSeries foil: %v", err)
	}
	if len(foil) != 1 {
		t.Errorf("foil series = %+v, want just its baseline", foil)
	}
}
