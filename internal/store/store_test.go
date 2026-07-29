package store

import (
	"path/filepath"
	"testing"

	"github.com/cphillips918/mtg_index/internal/scryfall"
)

func f(v float64) *float64 { return &v }

func newTestStore(t *testing.T) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func ulamog() scryfall.Card {
	return scryfall.Card{
		ID:              "ulamog-id",
		Set:             "uma",
		CollectorNumber: "7",
		Name:            "Ulamog, the Infinite Gyre",
		PriceUSD:        f(10.00),
		PriceUSDFoil:    f(25.00),
		ScryfallURL:     "https://scryfall.com/card/uma/7",
	}
}

func solRing() scryfall.Card {
	return scryfall.Card{
		ID:              "sol-id",
		Set:             "c21",
		CollectorNumber: "1",
		Name:            "Sol Ring",
		PriceUSD:        f(2.00),
		PriceUSDFoil:    f(5.00),
		ScryfallURL:     "https://scryfall.com/card/c21/1",
	}
}

func TestCollectionAddAndIncrement(t *testing.T) {
	s := newTestStore(t)

	if err := s.AddCard(ulamog(), false, 2); err != nil {
		t.Fatalf("AddCard normal: %v", err)
	}
	if err := s.AddCard(ulamog(), true, 1); err != nil {
		t.Fatalf("AddCard foil: %v", err)
	}
	if err := s.AddCard(ulamog(), false, 3); err != nil {
		t.Fatalf("AddCard normal again: %v", err)
	}

	cards, err := s.ListCollection()
	if err != nil {
		t.Fatalf("ListCollection: %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("want 1 collection card, got %d", len(cards))
	}
	if cards[0].QtyNormal != 5 || cards[0].QtyFoil != 1 {
		t.Errorf("quantities = %d/%d, want 5/1", cards[0].QtyNormal, cards[0].QtyFoil)
	}

	val, err := s.CollectionValue()
	if err != nil {
		t.Fatalf("CollectionValue: %v", err)
	}
	// 5*10 + 1*25 = 75
	if val != 75.0 {
		t.Errorf("CollectionValue = %v, want 75", val)
	}
}

func TestSetCollectionQuantities(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCard(ulamog(), false, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	existed, err := s.SetCollectionQuantities("ulamog-id", 4, 2)
	if err != nil {
		t.Fatalf("SetCollectionQuantities: %v", err)
	}
	if !existed {
		t.Error("existed = false, want true")
	}
	c, _ := s.FindCollectionCard("uma", "7")
	if c == nil {
		t.Fatal("card not found after set-qty")
	}
	cards, _ := s.ListCollection()
	if cards[0].QtyNormal != 4 || cards[0].QtyFoil != 2 {
		t.Errorf("quantities = %d/%d, want 4/2", cards[0].QtyNormal, cards[0].QtyFoil)
	}

	// Setting a finish to 0 removes it.
	if _, err := s.SetCollectionQuantities("ulamog-id", 0, 3); err != nil {
		t.Fatalf("SetCollectionQuantities zero-normal: %v", err)
	}
	cards, _ = s.ListCollection()
	if cards[0].QtyNormal != 0 || cards[0].QtyFoil != 3 {
		t.Errorf("quantities = %d/%d, want 0/3", cards[0].QtyNormal, cards[0].QtyFoil)
	}

	existed, _ = s.SetCollectionQuantities("missing-id", 1, 1)
	if existed {
		t.Error("existed = true for missing card, want false")
	}
}

func TestRemoveFromCollection(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCard(ulamog(), false, 2); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	n, err := s.RemoveFromCollection("ulamog-id")
	if err != nil {
		t.Fatalf("RemoveFromCollection: %v", err)
	}
	if n != 1 {
		t.Errorf("removed %d entry rows, want 1", n)
	}
	if c, _ := s.FindCollectionCard("uma", "7"); c != nil {
		t.Errorf("card still present after remove: %+v", c)
	}
}

func TestDeckUpsertReplaceAndCascade(t *testing.T) {
	s := newTestStore(t)
	// Catalog must contain the cards a deck references.
	if err := s.UpsertCatalogCards([]scryfall.Card{ulamog(), solRing()}); err != nil {
		t.Fatalf("UpsertCatalogCards: %v", err)
	}

	meta := DeckMeta{Name: "My EDH", Source: "archidekt", SourceID: "999", SourceURL: "http://x", Format: "Commander"}
	entries := []Entry{
		{ScryfallID: "ulamog-id", Finish: "normal", Board: "commander", Quantity: 1},
		{ScryfallID: "sol-id", Finish: "normal", Board: "main", Quantity: 1},
	}
	id, err := s.UpsertDeck(meta, entries)
	if err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}

	// Re-import the same deck with a different list — entries are replaced.
	entries2 := []Entry{{ScryfallID: "sol-id", Finish: "foil", Board: "main", Quantity: 2}}
	id2, err := s.UpsertDeck(meta, entries2)
	if err != nil {
		t.Fatalf("UpsertDeck re-import: %v", err)
	}
	if id != id2 {
		t.Fatalf("re-import created a new deck (%d != %d)", id, id2)
	}

	decks, err := s.ListDecks()
	if err != nil {
		t.Fatalf("ListDecks: %v", err)
	}
	if len(decks) != 1 {
		t.Fatalf("want 1 deck, got %d", len(decks))
	}
	if decks[0].TotalCopies != 2 {
		t.Errorf("TotalCopies = %d, want 2 (after replace)", decks[0].TotalCopies)
	}
	if decks[0].Value != 10.0 { // 2 foil Sol Rings @ 5
		t.Errorf("deck Value = %v, want 10", decks[0].Value)
	}

	// Cascade delete removes entries too.
	if _, err := s.RemoveContainer(id); err != nil {
		t.Fatalf("RemoveContainer: %v", err)
	}
	views, err := s.DeckEntries(id)
	if err != nil {
		t.Fatalf("DeckEntries: %v", err)
	}
	if len(views) != 0 {
		t.Errorf("entries remain after cascade delete: %d", len(views))
	}
}

func TestTotalsAcrossContainers(t *testing.T) {
	s := newTestStore(t)
	if err := s.UpsertCatalogCards([]scryfall.Card{ulamog(), solRing()}); err != nil {
		t.Fatalf("UpsertCatalogCards: %v", err)
	}
	// 1 Ulamog loose + 1 Ulamog in a deck = 2 owned.
	if err := s.AddCard(ulamog(), false, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.UpsertDeck(
		DeckMeta{Name: "D", Source: "text", SourceID: "d1"},
		[]Entry{{ScryfallID: "ulamog-id", Finish: "normal", Board: "main", Quantity: 1}},
	); err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}

	totals, err := s.TotalsByCard()
	if err != nil {
		t.Fatalf("TotalsByCard: %v", err)
	}
	var ulamogTotal int
	for _, o := range totals {
		if o.ScryfallID == "ulamog-id" {
			ulamogTotal = o.TotalCopies
		}
	}
	if ulamogTotal != 2 {
		t.Errorf("Ulamog total owned = %d, want 2", ulamogTotal)
	}
	// Sol Ring is in the catalog but owned nowhere → excluded from totals.
	for _, o := range totals {
		if o.ScryfallID == "sol-id" {
			t.Error("Sol Ring appears in totals despite 0 copies owned")
		}
	}
}

func TestLegacyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	// Build a legacy single-table database directly.
	legacy, err := Open(path)
	if err != nil {
		t.Fatalf("initial open: %v", err)
	}
	// Drop the modern schema and recreate the old one with a row.
	for _, stmt := range []string{
		`DROP TABLE card_entries`, `DROP TABLE containers`, `DROP TABLE cards`,
		`CREATE TABLE cards (
            scryfall_id TEXT PRIMARY KEY, set_code TEXT NOT NULL, collector_number TEXT NOT NULL,
            name TEXT NOT NULL, qty_normal INTEGER NOT NULL DEFAULT 0, qty_foil INTEGER NOT NULL DEFAULT 0,
            price_usd REAL, price_usd_foil REAL, scryfall_url TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`INSERT INTO cards VALUES ('ulamog-id','uma','7','Ulamog, the Infinite Gyre',3,1,10.0,25.0,'http://x','2020-01-01T00:00:00Z')`,
	} {
		if _, err := legacy.db.Exec(stmt); err != nil {
			t.Fatalf("seed legacy: %v", err)
		}
	}
	legacy.Close()

	// Reopen — migration should run.
	s, err := Open(path)
	if err != nil {
		t.Fatalf("reopen (migrate): %v", err)
	}
	defer s.Close()

	cards, err := s.ListCollection()
	if err != nil {
		t.Fatalf("ListCollection after migrate: %v", err)
	}
	if len(cards) != 1 {
		t.Fatalf("want 1 migrated card, got %d", len(cards))
	}
	if cards[0].QtyNormal != 3 || cards[0].QtyFoil != 1 {
		t.Errorf("migrated quantities = %d/%d, want 3/1", cards[0].QtyNormal, cards[0].QtyFoil)
	}
	if cards[0].Name != "Ulamog, the Infinite Gyre" {
		t.Errorf("migrated name = %q", cards[0].Name)
	}
}
