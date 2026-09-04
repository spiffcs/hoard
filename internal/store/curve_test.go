package store

import (
	"fmt"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func documented(id, name, typeLine string, cmc float64) scryfall.Card {
	return scryfall.Card{
		ID: id, Set: "tst", CollectorNumber: "1", Name: name,
		PriceUSD:    f(1.00),
		ScryfallURL: "https://scryfall.com/card/tst/1",
		Raw:         []byte(fmt.Sprintf(`{"type_line":%q,"cmc":%g}`, typeLine, cmc)),
	}
}

// curvedDeck is a deck with one of everything the curve has to tell apart: a
// stack of spells, a land, a card hoard holds no document for, and a
// sideboard card that the main deck's curve must not count.
func curvedDeck(t *testing.T) (*Store, int64) {
	t.Helper()
	s := newTestStore(t)
	if err := s.UpsertPrintings([]scryfall.Card{
		documented("bolt-id", "Lightning Bolt", "Instant", 1),
		documented("hierarch-id", "Noble Hierarch", "Creature — Human Druid", 1),
		documented("verdict-id", "Supreme Verdict", "Sorcery", 3),
		documented("waste-id", "Wasteland", "Land", 0),
		documented("tomb-id", "Ancient Tomb", "Land", 0),
		documented("ulamog-id", "Ulamog, the Infinite Gyre", "Legendary Creature — Eldrazi", 11),
		documented("pyro-id", "Pyroblast", "Instant", 1),
		{ID: "mystery-id", Set: "tst", CollectorNumber: "9", Name: "Mystery Card",
			ScryfallURL: "https://scryfall.com/card/tst/9"},
	}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	id, err := s.UpsertDeck(
		DeckMeta{Name: "Curve", Source: "text", SourceID: "curve"},
		[]Entry{
			{ScryfallID: "bolt-id", Finish: finish.Nonfoil, Board: "main", Quantity: 4},
			{ScryfallID: "hierarch-id", Finish: finish.Nonfoil, Board: "main", Quantity: 2},
			{ScryfallID: "verdict-id", Finish: finish.Nonfoil, Board: "main", Quantity: 3},
			{ScryfallID: "waste-id", Finish: finish.Nonfoil, Board: "main", Quantity: 4},
			{ScryfallID: "tomb-id", Finish: finish.Nonfoil, Board: "main", Quantity: 2},
			{ScryfallID: "ulamog-id", Finish: finish.Nonfoil, Board: "main", Quantity: 1},
			{ScryfallID: "mystery-id", Finish: finish.Nonfoil, Board: "main", Quantity: 2},
			{ScryfallID: "pyro-id", Finish: finish.Nonfoil, Board: "side", Quantity: 3},
		})
	if err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}
	return s, id
}

func copiesAt(c Curve, mana int) int {
	for _, r := range c.Rows {
		if r.Mana == mana {
			return r.Copies
		}
	}
	return 0
}

func TestDeckCurveCountsCopiesNotRows(t *testing.T) {
	s, deck := curvedDeck(t)

	c, err := s.DeckCurve(deck, BoardMain)
	if err != nil {
		t.Fatalf("DeckCurve: %v", err)
	}

	if got := copiesAt(c, 1); got != 6 {
		t.Errorf("mana 1 holds %d copies, want 6 — four Bolts and two Hierarchs", got)
	}
	if got := copiesAt(c, 3); got != 3 {
		t.Errorf("mana 3 holds %d copies, want the 3 Verdicts", got)
	}
	if got := copiesAt(c, 11); got != 1 {
		t.Errorf("mana 11 holds %d copies, want the single Ulamog", got)
	}
}

func TestDeckCurveKeepsLandsOutOfTheBars(t *testing.T) {
	s, deck := curvedDeck(t)

	c, err := s.DeckCurve(deck, BoardMain)
	if err != nil {
		t.Fatalf("DeckCurve: %v", err)
	}

	if c.Lands != 6 {
		t.Errorf("Lands = %d, want the 4 Wastelands plus 2 Ancient Tombs", c.Lands)
	}
	if got := copiesAt(c, 0); got != 0 {
		t.Errorf("mana 0 holds %d copies — the lands leaked into the bars", got)
	}
}

func TestDeckCurveCountsAnUndocumentedPrintingAsUnknown(t *testing.T) {
	s, deck := curvedDeck(t)

	c, err := s.DeckCurve(deck, BoardMain)
	if err != nil {
		t.Fatalf("DeckCurve: %v", err)
	}

	if c.Unknown != 2 {
		t.Errorf("Unknown = %d, want the 2 copies hoard holds no document for", c.Unknown)
	}
	if got := copiesAt(c, 0); got != 0 {
		t.Errorf("mana 0 holds %d copies — an undocumented card was read as a 0-drop", got)
	}
}

func TestDeckCurveCountsOnlyTheBoardItIsAsked(t *testing.T) {
	s, deck := curvedDeck(t)

	main, err := s.DeckCurve(deck, BoardMain)
	if err != nil {
		t.Fatalf("DeckCurve: %v", err)
	}
	if got := copiesAt(main, 1); got != 6 {
		t.Errorf("mana 1 holds %d on the main deck, want 6 — the sideboard leaked in", got)
	}

	side, err := s.DeckCurve(deck, BoardSide)
	if err != nil {
		t.Fatalf("DeckCurve: %v", err)
	}
	if got := copiesAt(side, 1); got != 3 {
		t.Errorf("mana 1 holds %d on the sideboard, want the 3 Pyroblasts", got)
	}
	if side.Lands != 0 {
		t.Errorf("the sideboard reports %d lands, want none", side.Lands)
	}
}

func TestDeckCurveRowsRunUpwards(t *testing.T) {
	s, deck := curvedDeck(t)

	c, err := s.DeckCurve(deck, BoardMain)
	if err != nil {
		t.Fatalf("DeckCurve: %v", err)
	}

	for i := 1; i < len(c.Rows); i++ {
		if c.Rows[i].Mana <= c.Rows[i-1].Mana {
			t.Fatalf("rows are not in ascending mana order: %+v", c.Rows)
		}
	}
	if len(c.Rows) == 0 {
		t.Fatal("no rows at all")
	}
}
