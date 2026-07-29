package store

import (
	"path/filepath"
	"testing"

	"github.com/cphillips918/hoard/internal/scryfall"
)

// changeFor finds one card-and-finish in a set of changes.
func changeFor(t *testing.T, changes []PriceChange, id, finish string) PriceChange {
	t.Helper()
	for _, c := range changes {
		if c.ScryfallID == id && c.Finish == finish {
			return c
		}
	}
	t.Fatalf("no change for %s/%s in %+v", id, finish, changes)
	return PriceChange{}
}

func observationCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM card_price_history`).Scan(&n); err != nil {
		t.Fatalf("counting history: %v", err)
	}
	return n
}

// backdate rewrites every stored observation's timestamp, standing in for time
// passing between refreshes. Tests cannot wait a day, and the alternative —
// injecting a clock into the store for this alone — would be a bigger change to
// production code than the thing under test.
func backdate(t *testing.T, s *Store, from, to string) {
	t.Helper()
	if _, err := s.db.Exec(`UPDATE card_price_history SET as_of = ? WHERE as_of = ?`, to, from); err != nil {
		t.Fatalf("backdating history: %v", err)
	}
}

func stamps(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.db.Query(`SELECT DISTINCT as_of FROM card_price_history ORDER BY as_of`)
	if err != nil {
		t.Fatalf("reading stamps: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, v)
	}
	return out
}

// A first observation is a baseline, not a movement: there is nothing to have
// moved from, and reporting it would make every newly added card a riser.
func TestRecordPricesFirstRunIsBaseline(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCard(ulamog(), false, 2); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	changes, err := s.RecordPrices()
	if err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("first run reported %+v, want no movement", changes)
	}
	// Both finishes are priced, so both are worth a baseline even though only
	// the non-foil is held: the foil is what a later foil purchase compares to.
	if n := observationCount(t, s); n != 2 {
		t.Errorf("recorded %d observations, want 2 (one per priced finish)", n)
	}
}

func TestRecordPricesReportsMovement(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCard(ulamog(), false, 3); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	// Yesterday's refresh. Timestamps are per-second, so a baseline recorded in
	// the same second as the move would be overwritten by it rather than
	// preceding it.
	backdate(t, s, stamps(t, s)[0], "2026-07-28T00:00:00Z")

	risen := ulamog()
	risen.PriceUSD = f(12.50)
	if err := s.UpdatePrices([]scryfall.Card{risen}); err != nil {
		t.Fatalf("UpdatePrices: %v", err)
	}

	changes, err := s.RecordPrices()
	if err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %+v, want only the non-foil move", changes)
	}
	c := changes[0]
	if c.Old != 10.00 || c.New != 12.50 {
		t.Errorf("moved %v -> %v, want 10 -> 12.50", c.Old, c.New)
	}
	if c.Copies != 3 {
		t.Errorf("Copies = %d, want 3", c.Copies)
	}
	if got := c.TotalDelta(); got != 7.50 {
		t.Errorf("TotalDelta = %v, want 7.50 (3 copies × $2.50)", got)
	}
	if got := c.Pct(); got != 0.25 {
		t.Errorf("Pct = %v, want 0.25", got)
	}
	if c.Source != "scryfall" {
		t.Errorf("Source = %q, want scryfall", c.Source)
	}

	// The foil never moved, so nothing new was written for it.
	if n := observationCount(t, s); n != 3 {
		t.Errorf("recorded %d observations, want 3 (2 baseline + 1 move)", n)
	}
}

// Re-recording an unchanged catalog must write nothing. A row per card per
// refresh would grow the database by the size of the catalog every day.
func TestRecordPricesSkipsUnchanged(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCard(ulamog(), false, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	before := observationCount(t, s)

	for range 3 {
		changes, err := s.RecordPrices()
		if err != nil {
			t.Fatalf("RecordPrices: %v", err)
		}
		if len(changes) != 0 {
			t.Errorf("re-recording reported %+v, want nothing", changes)
		}
	}
	if got := observationCount(t, s); got != before {
		t.Errorf("observations grew from %d to %d with no price change", before, got)
	}
}

// Etched copies are valued from the foil price, so they must be counted against
// the foil's movement rather than silently dropped or given a series of their
// own that would only ever duplicate the foil's.
func TestRecordPricesCountsCopiesAcrossContainersAndFinishes(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(solRing(), "foil", 2); err != nil {
		t.Fatalf("AddCardFinish foil: %v", err)
	}
	if err := s.AddCardFinish(solRing(), "etched", 1); err != nil {
		t.Fatalf("AddCardFinish etched: %v", err)
	}
	if _, err := s.UpsertDeck(
		DeckMeta{Name: "EDH", Source: "text", SourceID: "edh"},
		[]Entry{{ScryfallID: "sol-id", Finish: "foil", Board: "main", Quantity: 4}},
	); err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	sunk := solRing()
	sunk.PriceUSDFoil = f(4.00)
	if err := s.UpdatePrices([]scryfall.Card{sunk}); err != nil {
		t.Fatalf("UpdatePrices: %v", err)
	}
	changes, err := s.RecordPrices()
	if err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}

	c := changeFor(t, changes, "sol-id", "foil")
	if c.Copies != 7 {
		t.Errorf("Copies = %d, want 7 (2 foil + 1 etched loose + 4 foil in a deck)", c.Copies)
	}
	if got := c.TotalDelta(); got != -7.00 {
		t.Errorf("TotalDelta = %v, want -7.00", got)
	}
}

// A card in the catalog but owned nowhere still accrues history — it can come
// back, and the gap would be permanent — but it is not a mover, because nothing
// held has changed value.
func TestRecordPricesIgnoresUnownedCards(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCard(ulamog(), false, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RemoveFromCollection("ulamog-id"); err != nil {
		t.Fatalf("RemoveFromCollection: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	backdate(t, s, stamps(t, s)[0], "2026-07-28T00:00:00Z")

	risen := ulamog()
	risen.PriceUSD = f(99.00)
	if err := s.UpdatePrices([]scryfall.Card{risen}); err != nil {
		t.Fatalf("UpdatePrices: %v", err)
	}
	changes, err := s.RecordPrices()
	if err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("changes = %+v, want nothing for a card owned nowhere", changes)
	}
	// Recorded all the same, so the series survives the card's absence.
	if n := observationCount(t, s); n != 3 {
		t.Errorf("recorded %d observations, want 3", n)
	}
}

// A price arriving from MTGJSON must be recorded as the effective price. Storing
// the raw Scryfall column instead would report the fallback's replacement by a
// real quote as an enormous move that never happened.
func TestRecordPricesUsesFallbackPrices(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(unpricedFoil(), "foil", 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.UpsertAltPrices([]AltPrice{{
		ScryfallID: "ripple-id", MTGJSONUUID: "uuid-1",
		PriceUSDFoil: f(30.00), SourceUSDFoil: "cardkingdom",
	}}); err != nil {
		t.Fatalf("UpsertAltPrices: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}

	if err := s.UpsertAltPrices([]AltPrice{{
		ScryfallID: "ripple-id", MTGJSONUUID: "uuid-1",
		PriceUSDFoil: f(36.00), SourceUSDFoil: "cardkingdom",
	}}); err != nil {
		t.Fatalf("UpsertAltPrices: %v", err)
	}
	changes, err := s.RecordPrices()
	if err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}
	c := changeFor(t, changes, "ripple-id", "foil")
	if c.Old != 30.00 || c.New != 36.00 {
		t.Errorf("moved %v -> %v, want 30 -> 36", c.Old, c.New)
	}
	if c.Source != "cardkingdom" {
		t.Errorf("Source = %q, want the vendor behind the fallback", c.Source)
	}
}

func TestMoversWindow(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCard(ulamog(), false, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	// The baseline is a month old; the move happened yesterday.
	backdate(t, s, stamps(t, s)[0], "2026-06-01T00:00:00Z")

	risen := ulamog()
	risen.PriceUSD = f(20.00)
	if err := s.UpdatePrices([]scryfall.Card{risen}); err != nil {
		t.Fatalf("UpdatePrices: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}

	t.Run("window reaching the baseline sees the move", func(t *testing.T) {
		got, err := s.Movers("2026-06-15T00:00:00Z")
		if err != nil {
			t.Fatalf("Movers: %v", err)
		}
		c := changeFor(t, got, "ulamog-id", "normal")
		if c.Old != 10.00 || c.New != 20.00 {
			t.Errorf("moved %v -> %v, want 10 -> 20", c.Old, c.New)
		}
	})

	// A cutoff older than anything recorded still has a baseline: the earliest
	// observation is at or before it.
	t.Run("window older than the history sees the move", func(t *testing.T) {
		got, err := s.Movers("2020-01-01T00:00:00Z")
		if err != nil {
			t.Fatalf("Movers: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %+v, want nothing: no observation at or before the cutoff", got)
		}
	})
}

// Two refreshes landing in the same second must not fail the run on the primary
// key, and the later price must win.
func TestRecordPricesWithinOneSecond(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCard(ulamog(), false, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	// Collapse the baseline onto the timestamp the next write will use.
	backdate(t, s, stamps(t, s)[0], now())

	risen := ulamog()
	risen.PriceUSD = f(11.00)
	if err := s.UpdatePrices([]scryfall.Card{risen}); err != nil {
		t.Fatalf("UpdatePrices: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}

	var got float64
	if err := s.db.QueryRow(`
SELECT price_usd FROM card_price_history WHERE scryfall_id='ulamog-id' AND finish='normal'`).
		Scan(&got); err != nil {
		t.Fatalf("reading price: %v", err)
	}
	if got != 11.00 {
		t.Errorf("stored %v, want the later price 11.00", got)
	}
}

// v4 seeds history from the prices already on disk. Without it the first refresh
// after upgrading would have nothing to compare against and would report a
// collection where nothing had ever moved.
func TestPriceHistorySeededFromExistingPrices(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hoard.db")
	seedRawDB(t, path, schemaV1+`
ALTER TABLE card_prices_alt ADD COLUMN source_usd TEXT;
ALTER TABLE card_prices_alt ADD COLUMN source_usd_foil TEXT;
ALTER TABLE card_prices_alt DROP COLUMN source;
ALTER TABLE cards ADD COLUMN mtgjson_uuid TEXT;
INSERT INTO cards VALUES
  ('ulamog-id','uma','7','Ulamog',10.0,25.0,'http://x','2026-07-01T00:00:00Z',NULL);
INSERT INTO cards VALUES
  ('ripple-id','m3c','218','Acidic Slime',0.34,NULL,'http://x','2026-07-01T00:00:00Z',NULL);
INSERT INTO card_prices_alt VALUES
  ('ripple-id','uuid-1',NULL,30.0,'2026-06-20T00:00:00Z',NULL,'cardkingdom');
PRAGMA user_version = 3;`)

	s, err := Open(path)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer s.Close()

	type point struct {
		price  float64
		source string
		asOf   string
	}
	got := map[string]point{}
	rows, err := s.db.Query(`SELECT scryfall_id, finish, price_usd, source, as_of FROM card_price_history`)
	if err != nil {
		t.Fatalf("reading history: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, finish string
		var p point
		if err := rows.Scan(&id, &finish, &p.price, &p.source, &p.asOf); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got[id+"/"+finish] = p
	}

	want := map[string]point{
		"ulamog-id/normal": {10.0, "scryfall", "2026-07-01T00:00:00Z"},
		"ulamog-id/foil":   {25.0, "scryfall", "2026-07-01T00:00:00Z"},
		"ripple-id/normal": {0.34, "scryfall", "2026-07-01T00:00:00Z"},
		// The foil Scryfall cannot price is seeded from the fallback, stamped
		// when that fallback was fetched rather than when the card was written.
		"ripple-id/foil": {30.0, "cardkingdom", "2026-06-20T00:00:00Z"},
	}
	if len(got) != len(want) {
		t.Fatalf("seeded %+v, want %d rows", got, len(want))
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s seeded as %+v, want %+v", k, got[k], w)
		}
	}

	// Seeded, so the very next refresh compares against these rather than
	// treating today's unchanged prices as brand new.
	changes, err := s.RecordPrices()
	if err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("refresh after seeding reported %+v, want no movement", changes)
	}
	if n := observationCount(t, s); n != len(want) {
		t.Errorf("observations = %d, want the %d seeded", n, len(want))
	}
}

func TestPriceHistoryDepth(t *testing.T) {
	s := newTestStore(t)
	n, oldest, err := s.PriceHistoryDepth()
	if err != nil {
		t.Fatalf("PriceHistoryDepth: %v", err)
	}
	if n != 0 || oldest != "" {
		t.Errorf("empty hoard reports %d observations since %q, want 0 and empty", n, oldest)
	}

	if err := s.AddCard(ulamog(), false, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}
	n, oldest, err = s.PriceHistoryDepth()
	if err != nil {
		t.Fatalf("PriceHistoryDepth: %v", err)
	}
	if n != 2 || oldest == "" {
		t.Errorf("depth = %d since %q, want 2 observations and a timestamp", n, oldest)
	}
}
