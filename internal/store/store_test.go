package store

import (
	"database/sql"
	"github.com/spiffcs/hoard/internal/finish"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
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

func heldByFinish(t *testing.T, s *Store, scryfallID string) map[string]int {
	t.Helper()
	rows, err := s.ListCollectionByFinish()
	if err != nil {
		t.Fatalf("ListCollectionByFinish: %v", err)
	}
	out := map[string]int{}
	for _, r := range rows {
		if r.ScryfallID == scryfallID {
			out[r.Finish.String()] = r.Quantity
		}
	}
	return out
}

func collectionRow(t *testing.T, s *Store, scryfallID string, fin finish.Finish) CollectionRow {
	t.Helper()
	rows, err := s.ListCollectionByFinish()
	if err != nil {
		t.Fatalf("ListCollectionByFinish: %v", err)
	}
	for _, r := range rows {
		if r.ScryfallID == scryfallID && r.Finish == fin {
			return r
		}
	}
	t.Fatalf("no %s row for %s in %+v", fin, scryfallID, rows)
	return CollectionRow{}
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
	if err := s.AddCardFinish(unpricedFoil(), finish.Foil, 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}

	if tot, _ := s.CollectionTotals(); tot.Value != 0 {
		t.Fatalf("setup: value = %v, want 0 before any fallback", tot.Value)
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

	totals, _ := s.CollectionTotals()
	if totals.Value != 0.98 {
		t.Errorf("CollectionTotals = %v, want 0.98 from the fallback", totals.Value)
	}

	row := collectionRow(t, s, "ripple-id", finish.Foil)
	if row.PriceUSDFoil == nil || *row.PriceUSDFoil != 0.49 {
		t.Errorf("PriceUSDFoil = %v, want the fallback 0.49", row.PriceUSDFoil)
	}
	if row.AltSource != "cardkingdom" {
		t.Errorf("AltSource = %q, want the foil vendor", row.AltSource)
	}

	if gaps, _ := s.UnpricedByOwnedFinish(); len(gaps) != 0 {
		t.Errorf("gaps after fill = %+v, want none", gaps)
	}

	priced := unpricedFoil()
	priced.PriceUSDFoil = f(9.00)
	if err := s.UpsertPrintings([]scryfall.Card{priced}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	if tot, _ := s.CollectionTotals(); tot.Value != 18.0 {
		t.Errorf("value = %v, want 18 from Scryfall, not the fallback", tot.Value)
	}
	if row := collectionRow(t, s, "ripple-id", finish.Foil); row.AltSource != "" {
		t.Errorf("AltSource = %q, want empty once Scryfall prices it", row.AltSource)
	}
}

func TestUnpricedListing(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(unpricedFoil(), finish.Foil, 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}

	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
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
	if r.Name != "Acidic Slime" || r.Finish != finish.Foil || r.Copies != 2 {
		t.Errorf("row = %+v, want Acidic Slime foil x2", r)
	}
	if r.SetCode != "m3c" || r.CollectorNumber != "218" {
		t.Errorf("printing = %s/%s, want m3c/218", r.SetCode, r.CollectorNumber)
	}
	if r.HeldIn == "" {
		t.Error("HeldIn should name the container holding it")
	}

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

func TestCorrectFinish(t *testing.T) {
	cases := []struct {
		name      string
		finish    finish.Finish
		available []finish.Finish
		want      finish.Finish
		changed   bool
	}{

		{"foil-only printing listed as nonfoil", finish.Nonfoil, []finish.Finish{finish.Foil}, finish.Foil, true},
		{"nonfoil-only listed as foil", finish.Foil, []finish.Finish{finish.Nonfoil}, finish.Nonfoil, true},

		{"already valid", finish.Nonfoil, []finish.Finish{finish.Nonfoil, finish.Foil}, finish.Nonfoil, false},
		{"foil is valid", finish.Foil, []finish.Finish{finish.Nonfoil, finish.Foil}, finish.Foil, false},
		{"etched is valid", finish.Etched, []finish.Finish{finish.Nonfoil, finish.Etched}, finish.Etched, false},

		{"ambiguous", finish.Etched, []finish.Finish{finish.Nonfoil, finish.Foil}, finish.Etched, false},

		{"unknown printing", finish.Nonfoil, nil, finish.Nonfoil, false},
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

	if err := s.AddCardFinish(unpricedFoil(), finish.Nonfoil, 3); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}

	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	available := map[string][]finish.Finish{
		"ripple-id": {finish.Foil},
		"ulamog-id": {finish.Nonfoil, finish.Foil},
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
	if fixed[0].From != finish.Nonfoil || fixed[0].To != finish.Foil || fixed[0].Quantity != 3 {
		t.Errorf("fix = %+v, want normal->foil x3", fixed[0])
	}

	held := heldByFinish(t, s, "ripple-id")
	if held["nonfoil"] != 0 || held["foil"] != 3 {
		t.Errorf("after repair: %d normal / %d foil, want 0/3", held["nonfoil"], held["foil"])
	}

	fixed, _, err = s.RepairFinishes(available)
	if err != nil || len(fixed) != 0 {
		t.Errorf("second pass fixed %+v (err %v), want nothing", fixed, err)
	}
}

func TestRepairFinishesMergesWithExistingEntry(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(unpricedFoil(), finish.Nonfoil, 2); err != nil {
		t.Fatalf("AddCardFinish normal: %v", err)
	}
	if err := s.AddCardFinish(unpricedFoil(), finish.Foil, 1); err != nil {
		t.Fatalf("AddCardFinish foil: %v", err)
	}

	fixed, _, err := s.RepairFinishes(map[string][]finish.Finish{"ripple-id": {finish.Foil}})
	if err != nil {
		t.Fatalf("RepairFinishes: %v", err)
	}
	if len(fixed) != 1 {
		t.Fatalf("fixed = %+v, want the normal entry moved", fixed)
	}

	held := heldByFinish(t, s, "ripple-id")
	if held["nonfoil"] != 0 || held["foil"] != 3 {
		t.Errorf("merged to %d normal / %d foil, want 0/3", held["nonfoil"], held["foil"])
	}
}

func TestRepairFinishesLeavesAmbiguousAlone(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(unpricedFoil(), finish.Etched, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	fixed, ambiguous, err := s.RepairFinishes(map[string][]finish.Finish{
		"ripple-id": {finish.Nonfoil, finish.Foil},
	})
	if err != nil {
		t.Fatalf("RepairFinishes: %v", err)
	}
	if len(fixed) != 0 {
		t.Errorf("fixed = %+v, want nothing changed", fixed)
	}
	if len(ambiguous) != 1 || ambiguous[0].From != finish.Etched {
		t.Errorf("ambiguous = %+v, want the etched entry reported", ambiguous)
	}
}

func TestUnpricedIgnoresUnownedFinish(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(unpricedFoil(), finish.Nonfoil, 1); err != nil {
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

	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 2); err != nil {
		t.Fatalf("AddCard normal: %v", err)
	}
	if err := s.AddCardFinish(ulamog(), finish.Foil, 1); err != nil {
		t.Fatalf("AddCard foil: %v", err)
	}
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 3); err != nil {
		t.Fatalf("AddCard normal again: %v", err)
	}

	held := heldByFinish(t, s, "ulamog-id")
	if held["nonfoil"] != 5 || held["foil"] != 1 {
		t.Errorf("quantities = %d/%d, want 5/1", held["nonfoil"], held["foil"])
	}

	totals, err := s.CollectionTotals()
	if err != nil {
		t.Fatalf("CollectionTotals: %v", err)
	}

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
	if err := s.AddCardFinish(ulamog(), finish.Etched, 2); err != nil {
		t.Fatalf("AddCardFinish etched: %v", err)
	}
	if err := s.AddCardFinish(ulamog(), finish.Foil, 1); err != nil {
		t.Fatalf("AddCardFinish foil: %v", err)
	}

	held := heldByFinish(t, s, "ulamog-id")
	if held["etched"] != 2 || held["foil"] != 1 || held["nonfoil"] != 0 {
		t.Fatalf("holdings = %v, want 2 etched / 1 foil / 0 normal", held)
	}
	if err := s.AddCardFinish(ulamog(), finish.Finish{}, 1); err == nil {
		t.Error("expected error for a holding with no finish set")
	}
}

func TestDeckByRef(t *testing.T) {
	s := newTestStore(t)
	if err := s.UpsertPrintings([]scryfall.Card{solRing()}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	entries := []Entry{{ScryfallID: "sol-id", Finish: finish.Nonfoil, Board: "main", Quantity: 1}}
	var ids []int64
	for _, name := range []string{
		"Vampiric Bloodlust (Commander 2017)",
		"Duel Decks Anthology: Elves vs. Goblins (Elves)",
		"Duel Decks Anthology: Elves vs. Goblins (Goblins)",
		"Elves",
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

	if err := s.UpsertPrintings([]scryfall.Card{ulamog(), solRing()}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}

	meta := DeckMeta{Name: "My EDH", Source: "archidekt", SourceID: "999", SourceURL: "http://x", Format: "Commander"}
	entries := []Entry{
		{ScryfallID: "ulamog-id", Finish: finish.Nonfoil, Board: "commander", Quantity: 1},
		{ScryfallID: "sol-id", Finish: finish.Nonfoil, Board: "main", Quantity: 1},
	}
	id, err := s.UpsertDeck(meta, entries)
	if err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}

	entries2 := []Entry{{ScryfallID: "sol-id", Finish: finish.Foil, Board: "main", Quantity: 2}}
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
	if decks[0].Value != 10.0 {
		t.Errorf("deck Value = %v, want 10", decks[0].Value)
	}

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
	if err := s.UpsertPrintings([]scryfall.Card{ulamog(), solRing()}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}

	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.UpsertDeck(
		DeckMeta{Name: "D", Source: "text", SourceID: "d1"},
		[]Entry{{ScryfallID: "ulamog-id", Finish: finish.Nonfoil, Board: "main", Quantity: 1}},
	); err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}

	totals, err := s.OwnedByFinish()
	if err != nil {
		t.Fatalf("OwnedByFinish: %v", err)
	}
	var ulamogTotal int
	for _, o := range totals {
		if o.ScryfallID == "ulamog-id" {
			ulamogTotal += o.Copies
		}
	}
	if ulamogTotal != 2 {
		t.Errorf("Ulamog total owned = %d, want 2", ulamogTotal)
	}

	for _, o := range totals {
		if o.ScryfallID == "sol-id" {
			t.Error("Sol Ring appears in totals despite 0 copies owned")
		}
	}
}

func TestLegacyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")

	legacy, err := Open(path)
	if err != nil {
		t.Fatalf("initial open: %v", err)
	}

	for _, stmt := range []string{

		`DROP TABLE card_bid_history`, `DROP TABLE card_price_history`,
		`DROP TABLE value_snapshots`, `DROP TABLE watches`, `DROP TABLE settings`,
		`DROP TABLE finish_guesses`,
		`DROP TABLE card_entries`, `DROP TABLE containers`, `DROP TABLE cards`,
		`CREATE TABLE cards (
            scryfall_id TEXT PRIMARY KEY, set_code TEXT NOT NULL, collector_number TEXT NOT NULL,
            name TEXT NOT NULL, qty_normal INTEGER NOT NULL DEFAULT 0, qty_foil INTEGER NOT NULL DEFAULT 0,
            price_usd REAL, price_usd_foil REAL, scryfall_url TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`INSERT INTO cards VALUES ('ulamog-id','uma','7','Ulamog, the Infinite Gyre',3,1,10.0,25.0,'http://x','2020-01-01T00:00:00Z')`,

		`PRAGMA user_version = 0`,
		`DROP TABLE card_prices_alt`,
	} {
		if _, err := legacy.db.Exec(stmt); err != nil {
			t.Fatalf("seed legacy: %v", err)
		}
	}
	legacy.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("reopen (migrate): %v", err)
	}
	defer s.Close()

	held := heldByFinish(t, s, "ulamog-id")
	if held["nonfoil"] != 3 || held["foil"] != 1 {
		t.Errorf("migrated quantities = %d/%d, want 3/1", held["nonfoil"], held["foil"])
	}
	if row := collectionRow(t, s, "ulamog-id", finish.Nonfoil); row.Name != "Ulamog, the Infinite Gyre" {
		t.Errorf("migrated name = %q", row.Name)
	}
}

func TestUpsertKeepsRawJSONWhenTheNewCardHasNone(t *testing.T) {
	s := newTestStore(t)

	withRaw := ulamog()
	withRaw.Raw = []byte(`{"rarity":"mythic","type_line":"Legendary Creature — Eldrazi"}`)
	if err := s.UpsertPrintings([]scryfall.Card{withRaw}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	bare := ulamog()
	bare.PriceUSD = f(11.00)
	if err := s.UpsertPrintings([]scryfall.Card{bare}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	var rarity sql.NullString
	var price float64
	if err := s.db.QueryRow(
		`SELECT rarity, price_usd FROM cards WHERE scryfall_id = 'ulamog-id'`).
		Scan(&rarity, &price); err != nil {
		t.Fatalf("reading card: %v", err)
	}
	if !rarity.Valid || rarity.String != "mythic" {
		t.Errorf("rarity = %+v, want the document from the first upsert to survive", rarity)
	}
	if price != 11.00 {
		t.Errorf("price = %v, want the second upsert's 11", price)
	}
}

func TestUpsertReplacesRawJSONWhenTheNewCardHasOne(t *testing.T) {
	s := newTestStore(t)

	first := ulamog()
	first.Raw = []byte(`{"rarity":"rare"}`)
	if err := s.UpsertPrintings([]scryfall.Card{first}); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	second := ulamog()
	second.Raw = []byte(`{"rarity":"mythic"}`)
	if err := s.UpsertPrintings([]scryfall.Card{second}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	var rarity string
	if err := s.db.QueryRow(
		`SELECT rarity FROM cards WHERE scryfall_id = 'ulamog-id'`).Scan(&rarity); err != nil {
		t.Fatalf("reading rarity: %v", err)
	}
	if rarity != "mythic" {
		t.Errorf("rarity = %q, want the newer document", rarity)
	}
}

func TestPriceGapChecksAreRemembered(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(unpricedFoil(), finish.Foil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}

	gaps, err := s.UnpricedByOwnedFinish()
	if err != nil {
		t.Fatalf("UnpricedByOwnedFinish: %v", err)
	}
	if len(gaps) != 1 {
		t.Fatalf("gaps = %+v, want the one foil-unpriced card", gaps)
	}
	if gaps[0].CheckedAt != nil {
		t.Errorf("CheckedAt = %v, want nil before anything asked", gaps[0].CheckedAt)
	}

	if err := s.RecordPriceGapChecks([]string{gaps[0].ScryfallID}); err != nil {
		t.Fatalf("RecordPriceGapChecks: %v", err)
	}
	gaps, _ = s.UnpricedByOwnedFinish()
	if len(gaps) != 1 || gaps[0].CheckedAt == nil {
		t.Fatalf("gaps = %+v, want the check recorded", gaps)
	}
	first := *gaps[0].CheckedAt

	if err := s.RecordPriceGapChecks([]string{gaps[0].ScryfallID}); err != nil {
		t.Fatalf("second RecordPriceGapChecks: %v", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM card_price_gaps`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d rows, want one per card", n)
	}
	gaps, _ = s.UnpricedByOwnedFinish()
	if *gaps[0].CheckedAt < first {
		t.Error("the recorded check went backwards")
	}
}

func TestPricedCardLeavesTheGapList(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(unpricedFoil(), finish.Foil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	gaps, _ := s.UnpricedByOwnedFinish()
	if err := s.RecordPriceGapChecks([]string{gaps[0].ScryfallID}); err != nil {
		t.Fatal(err)
	}

	if err := s.UpsertAltPrices([]AltPrice{{
		ScryfallID: "ripple-id", MTGJSONUUID: "u",
		PriceUSDFoil: f(0.49), SourceUSDFoil: "cardkingdom",
	}}); err != nil {
		t.Fatalf("UpsertAltPrices: %v", err)
	}
	if gaps, _ := s.UnpricedByOwnedFinish(); len(gaps) != 0 {
		t.Errorf("gaps = %+v, want none once a price exists", gaps)
	}
}

func TestPriceGapChecksCascade(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(unpricedFoil(), finish.Foil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	gaps, _ := s.UnpricedByOwnedFinish()
	if err := s.RecordPriceGapChecks([]string{gaps[0].ScryfallID}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.RemoveFromCollection("ripple-id"); err != nil {
		t.Fatalf("RemoveFromCollection: %v", err)
	}
	if _, err := s.db.Exec(`DELETE FROM cards WHERE scryfall_id = 'ripple-id'`); err != nil {
		t.Fatalf("deleting the card: %v", err)
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM card_price_gaps`).Scan(&n)
	if n != 0 {
		t.Errorf("%d orphaned gap rows remain", n)
	}
}
