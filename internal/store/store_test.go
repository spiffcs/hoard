package store

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cphillips918/hoard/internal/scryfall"
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

// unpricedFoil is a card Scryfall can price in non-foil but not in foil, like
// the Modern Horizons 3 ripple foils that motivated the fallback.
func unpricedFoil() scryfall.Card {
	return scryfall.Card{
		ID:              "ripple-id",
		Set:             "m3c",
		CollectorNumber: "218",
		Name:            "Acidic Slime",
		PriceUSD:        f(0.34),
		PriceUSDFoil:    nil,
		ScryfallURL:     "https://scryfall.com/card/m3c/218",
	}
}

func TestAltPriceFallback(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(unpricedFoil(), "foil", 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}

	// Before the fallback: owned in foil, no foil price, so worth nothing.
	if v, _ := s.CollectionValue(); v != 0 {
		t.Fatalf("setup: value = %v, want 0 before any fallback", v)
	}
	gaps, err := s.UnpricedByOwnedFinish()
	if err != nil {
		t.Fatalf("UnpricedByOwnedFinish: %v", err)
	}
	if len(gaps) != 1 || gaps[0].SetCode != "m3c" {
		t.Fatalf("gaps = %+v, want the one foil-unpriced card", gaps)
	}

	if err := s.UpsertAltPrices([]AltPrice{{
		ScryfallID: "ripple-id", MTGJSONUUID: "uuid-1",
		PriceUSD: f(0.34), PriceUSDFoil: f(0.49),
		SourceUSD: "tcgplayer", SourceUSDFoil: "cardkingdom",
	}}); err != nil {
		t.Fatalf("UpsertAltPrices: %v", err)
	}

	// 2 x $0.49 foil, from the fallback.
	if v, _ := s.CollectionValue(); v != 0.98 {
		t.Errorf("CollectionValue = %v, want 0.98 from the fallback", v)
	}
	totals, _ := s.CollectionTotals()
	if totals.Value != 0.98 {
		t.Errorf("CollectionTotals = %v, want 0.98", totals.Value)
	}
	// The row view must carry the effective price and name its source, so the
	// CLI can mark it as an estimate.
	cards, _ := s.ListCollection()
	if len(cards) != 1 {
		t.Fatalf("want 1 collection card, got %d", len(cards))
	}
	if cards[0].PriceUSDFoil == nil || *cards[0].PriceUSDFoil != 0.49 {
		t.Errorf("PriceUSDFoil = %v, want the fallback 0.49", cards[0].PriceUSDFoil)
	}
	if cards[0].AltSource != "cardkingdom" {
		t.Errorf("AltSource = %q, want the foil vendor", cards[0].AltSource)
	}

	// Once filled, it is no longer a gap, so a second run downloads nothing.
	if gaps, _ := s.UnpricedByOwnedFinish(); len(gaps) != 0 {
		t.Errorf("gaps after fill = %+v, want none", gaps)
	}

	// A Scryfall price must always win over the fallback.
	priced := unpricedFoil()
	priced.PriceUSDFoil = f(9.00)
	if err := s.UpsertCatalogCards([]scryfall.Card{priced}); err != nil {
		t.Fatalf("UpsertCatalogCards: %v", err)
	}
	if v, _ := s.CollectionValue(); v != 18.0 {
		t.Errorf("CollectionValue = %v, want 18 from Scryfall, not the fallback", v)
	}
	cards, _ = s.ListCollection()
	if cards[0].AltSource != "" {
		t.Errorf("AltSource = %q, want empty once Scryfall prices it", cards[0].AltSource)
	}
}

func TestUnpricedListing(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(unpricedFoil(), "foil", 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	// A fully priced card must not appear.
	if err := s.AddCard(ulamog(), false, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	rows, err := s.Unpriced()
	if err != nil {
		t.Fatalf("Unpriced: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want only the foil-unpriced card: %+v", len(rows), rows)
	}
	r := rows[0]
	if r.Name != "Acidic Slime" || r.Finish != "foil" || r.Copies != 2 {
		t.Errorf("row = %+v, want Acidic Slime foil x2", r)
	}
	if r.SetCode != "m3c" || r.CollectorNumber != "218" {
		t.Errorf("printing = %s/%s, want m3c/218", r.SetCode, r.CollectorNumber)
	}
	if r.HeldIn == "" {
		t.Error("HeldIn should name the container holding it")
	}

	// The listing and the fill must agree on what counts as unpriced, so a
	// fallback price removes the card from both.
	if err := s.UpsertAltPrices([]AltPrice{{
		ScryfallID: "ripple-id", MTGJSONUUID: "u", PriceUSDFoil: f(0.49), SourceUSDFoil: "cardkingdom",
	}}); err != nil {
		t.Fatalf("UpsertAltPrices: %v", err)
	}
	rows, _ = s.Unpriced()
	gaps, _ := s.UnpricedByOwnedFinish()
	if len(rows) != 0 || len(gaps) != 0 {
		t.Errorf("after a fallback price: %d listed, %d gaps; want none of either",
			len(rows), len(gaps))
	}
}

// One rule, applied both at deck import and by repair-finishes, so a freshly
// imported deck cannot need repairing.
func TestCorrectFinish(t *testing.T) {
	cases := []struct {
		name      string
		finish    string
		available []string
		want      string
		changed   bool
	}{
		// The bug this exists for: a decklist with no *F* marker parses as
		// normal, but the printing has no non-foil.
		{"foil-only printing listed as normal", "normal", []string{"foil"}, "foil", true},
		{"nonfoil-only listed as foil", "foil", []string{"nonfoil"}, "normal", true},
		// Scryfall says "nonfoil" where entries say "normal".
		{"already valid", "normal", []string{"nonfoil", "foil"}, "normal", false},
		{"foil is valid", "foil", []string{"nonfoil", "foil"}, "foil", false},
		{"etched is valid", "etched", []string{"nonfoil", "etched"}, "etched", false},
		// Several finishes and none matches: no single right answer, so leave it.
		{"ambiguous", "etched", []string{"nonfoil", "foil"}, "etched", false},
		// Nothing to check against.
		{"unknown printing", "normal", nil, "normal", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, changed := CorrectFinish(c.finish, c.available)
			if got != c.want || changed != c.changed {
				t.Errorf("CorrectFinish(%q, %v) = %q, %v; want %q, %v",
					c.finish, c.available, got, changed, c.want, c.changed)
			}
		})
	}
}

func TestRepairFinishes(t *testing.T) {
	s := newTestStore(t)
	// A foil-only printing imported as "normal", which is the bug: a decklist
	// with no *F* marker defaults to normal even when no such card exists.
	if err := s.AddCardFinish(unpricedFoil(), "normal", 3); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	// A correctly recorded card, to confirm it is left alone.
	if err := s.AddCard(ulamog(), false, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	available := map[string][]string{
		"ripple-id": {"foil"},            // exists only in foil
		"ulamog-id": {"nonfoil", "foil"}, // both, and recorded as normal: fine
	}
	fixed, ambiguous, err := s.RepairFinishes(available)
	if err != nil {
		t.Fatalf("RepairFinishes: %v", err)
	}
	if len(ambiguous) != 0 {
		t.Errorf("ambiguous = %+v, want none", ambiguous)
	}
	if len(fixed) != 1 {
		t.Fatalf("fixed = %+v, want just the foil-only card", fixed)
	}
	if fixed[0].From != "normal" || fixed[0].To != "foil" || fixed[0].Quantity != 3 {
		t.Errorf("fix = %+v, want normal->foil x3", fixed[0])
	}

	cards, _ := s.ListCollection()
	var slime CollectionCard
	for _, c := range cards {
		if c.ScryfallID == "ripple-id" {
			slime = c
		}
	}
	if slime.QtyNormal != 0 || slime.QtyFoil != 3 {
		t.Errorf("after repair: %d normal / %d foil, want 0/3", slime.QtyNormal, slime.QtyFoil)
	}
	// Idempotent: a second pass has nothing left to do.
	fixed, _, err = s.RepairFinishes(available)
	if err != nil || len(fixed) != 0 {
		t.Errorf("second pass fixed %+v (err %v), want nothing", fixed, err)
	}
}

// Correcting a finish can collide with an entry that already uses it. The
// primary key includes finish, so the quantities have to merge rather than one
// row overwriting or rejecting the other.
func TestRepairFinishesMergesWithExistingEntry(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(unpricedFoil(), "normal", 2); err != nil {
		t.Fatalf("AddCardFinish normal: %v", err)
	}
	if err := s.AddCardFinish(unpricedFoil(), "foil", 1); err != nil {
		t.Fatalf("AddCardFinish foil: %v", err)
	}

	fixed, _, err := s.RepairFinishes(map[string][]string{"ripple-id": {"foil"}})
	if err != nil {
		t.Fatalf("RepairFinishes: %v", err)
	}
	if len(fixed) != 1 {
		t.Fatalf("fixed = %+v, want the normal entry moved", fixed)
	}
	cards, _ := s.ListCollection()
	if len(cards) != 1 {
		t.Fatalf("want 1 card, got %d", len(cards))
	}
	// 2 mistakenly-normal plus the 1 already foil.
	if cards[0].QtyNormal != 0 || cards[0].QtyFoil != 3 {
		t.Errorf("merged to %d normal / %d foil, want 0/3",
			cards[0].QtyNormal, cards[0].QtyFoil)
	}
}

// When a printing comes in several finishes and none matches, there is no single
// right answer, so the entry is reported and left untouched rather than guessed.
func TestRepairFinishesLeavesAmbiguousAlone(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(unpricedFoil(), "etched", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	fixed, ambiguous, err := s.RepairFinishes(map[string][]string{
		"ripple-id": {"nonfoil", "foil"},
	})
	if err != nil {
		t.Fatalf("RepairFinishes: %v", err)
	}
	if len(fixed) != 0 {
		t.Errorf("fixed = %+v, want nothing changed", fixed)
	}
	if len(ambiguous) != 1 || ambiguous[0].From != "etched" {
		t.Errorf("ambiguous = %+v, want the etched entry reported", ambiguous)
	}
}

// A card owned only in non-foil must not be reported as needing a foil price.
func TestUnpricedIgnoresUnownedFinish(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(unpricedFoil(), "normal", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	gaps, err := s.UnpricedByOwnedFinish()
	if err != nil {
		t.Fatalf("UnpricedByOwnedFinish: %v", err)
	}
	if len(gaps) != 0 {
		t.Errorf("gaps = %+v, want none: the foil price is not needed", gaps)
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

	totals, err := s.CollectionTotals()
	if err != nil {
		t.Fatalf("CollectionTotals: %v", err)
	}
	// One printing, six copies across both finishes, valued like CollectionValue.
	if totals.DistinctCards != 1 || totals.TotalCopies != 6 || totals.Value != 75.0 {
		t.Errorf("CollectionTotals = %+v, want {1 6 75}", totals)
	}
}

func TestCollectionTotalsEmpty(t *testing.T) {
	s := newTestStore(t)
	totals, err := s.CollectionTotals()
	if err != nil {
		t.Fatalf("CollectionTotals on empty collection: %v", err)
	}
	if totals != (CollectionTotals{}) {
		t.Errorf("CollectionTotals = %+v, want all zeros", totals)
	}
}

func TestAddCardFinishEtched(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "etched", 2); err != nil {
		t.Fatalf("AddCardFinish etched: %v", err)
	}
	if err := s.AddCardFinish(ulamog(), "foil", 1); err != nil {
		t.Fatalf("AddCardFinish foil: %v", err)
	}
	// etched + foil are distinct entry rows but pivot together into QtyFoil.
	cards, _ := s.ListCollection()
	if len(cards) != 1 || cards[0].QtyFoil != 3 || cards[0].QtyNormal != 0 {
		t.Fatalf("pivot wrong: %+v", cards)
	}
	if err := s.AddCardFinish(ulamog(), "bogus", 1); err == nil {
		t.Error("expected error for invalid finish")
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

func TestDeckByRef(t *testing.T) {
	s := newTestStore(t)
	if err := s.UpsertCatalogCards([]scryfall.Card{solRing()}); err != nil {
		t.Fatalf("UpsertCatalogCards: %v", err)
	}
	entries := []Entry{{ScryfallID: "sol-id", Finish: "normal", Board: "main", Quantity: 1}}
	var ids []int64
	for _, name := range []string{
		"Vampiric Bloodlust (Commander 2017)",
		"Duel Decks Anthology: Elves vs. Goblins (Elves)",
		"Duel Decks Anthology: Elves vs. Goblins (Goblins)",
		"Elves", // an exact name that is also a fragment of the two above
		"50% Off (Weird_Name)",
	} {
		id, err := s.UpsertDeck(DeckMeta{Name: name, Source: "text", SourceID: name}, entries)
		if err != nil {
			t.Fatalf("UpsertDeck %q: %v", name, err)
		}
		ids = append(ids, id)
	}

	t.Run("by id", func(t *testing.T) {
		got, err := s.DeckByRef(strconv.FormatInt(ids[0], 10))
		if err != nil || got.Name != "Vampiric Bloodlust (Commander 2017)" {
			t.Fatalf("DeckByRef(id) = %v, %v", got, err)
		}
	})

	t.Run("unique fragment", func(t *testing.T) {
		got, err := s.DeckByRef("vampiric")
		if err != nil || got.Name != "Vampiric Bloodlust (Commander 2017)" {
			t.Fatalf("DeckByRef(fragment) = %v, %v", got, err)
		}
	})

	// An exact name must win even though it also appears inside two other names.
	t.Run("exact name beats fragment", func(t *testing.T) {
		got, err := s.DeckByRef("elves")
		if err != nil {
			t.Fatalf("DeckByRef(exact) error: %v", err)
		}
		if got.Name != "Elves" {
			t.Errorf("DeckByRef(%q) = %q, want the exact match", "elves", got.Name)
		}
	})

	t.Run("ambiguous fragment lists the candidates", func(t *testing.T) {
		_, err := s.DeckByRef("Goblins")
		if err == nil {
			t.Fatal("want an error for an ambiguous fragment")
		}
		if !strings.Contains(err.Error(), "matches 2 decks") {
			t.Errorf("error = %q, want a count of the matches", err)
		}
	})

	// % and _ are LIKE wildcards; a name containing them must match literally.
	t.Run("wildcards are escaped", func(t *testing.T) {
		got, err := s.DeckByRef("50% Off")
		if err != nil || got.Name != "50% Off (Weird_Name)" {
			t.Fatalf("DeckByRef(wildcard) = %v, %v", got, err)
		}
		if _, err := s.DeckByRef("50%%%Off"); err == nil {
			t.Error("a literal wildcard string should not match")
		}
	})

	t.Run("no match", func(t *testing.T) {
		if _, err := s.DeckByRef("nothing-like-this"); err == nil {
			t.Error("want an error for an unmatched ref")
		}
	})
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
		// A genuinely legacy database predates versioning, so it reports 0.
		// Without this the fixture would be stamped current while holding the
		// old shape, which is a state that cannot occur in the wild.
		`PRAGMA user_version = 0`,
		`DROP TABLE card_prices_alt`,
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
