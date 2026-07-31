package store

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/scryfall"
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
	if err := s.AddCardFinish(ulamog(), "nonfoil", 2); err != nil {
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
	if err := s.AddCardFinish(ulamog(), "nonfoil", 3); err != nil {
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
	if err := s.UpsertPrintings([]scryfall.Card{risen}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
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
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
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
	if err := s.UpsertPrintings([]scryfall.Card{sunk}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
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
	// Catalogued but never added to a container, which is the state this is
	// about: a printing hoard knows the price of and owns none of.
	if err := s.UpsertPrintings([]scryfall.Card{ulamog()}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	backdate(t, s, stamps(t, s)[0], "2026-07-28T00:00:00Z")

	risen := ulamog()
	risen.PriceUSD = f(99.00)
	if err := s.UpsertPrintings([]scryfall.Card{risen}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
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
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	// The baseline is a month old; the move happened yesterday.
	backdate(t, s, stamps(t, s)[0], "2026-06-01T00:00:00Z")

	risen := ulamog()
	risen.PriceUSD = f(20.00)
	if err := s.UpsertPrintings([]scryfall.Card{risen}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}

	t.Run("window reaching the baseline sees the move", func(t *testing.T) {
		got, err := s.Movers("2026-06-15T00:00:00Z")
		if err != nil {
			t.Fatalf("Movers: %v", err)
		}
		c := changeFor(t, got, "ulamog-id", "nonfoil")
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
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	// Collapse the baseline onto the timestamp the next write will use.
	backdate(t, s, stamps(t, s)[0], now())

	risen := ulamog()
	risen.PriceUSD = f(11.00)
	if err := s.UpsertPrintings([]scryfall.Card{risen}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}

	var got float64
	if err := s.db.QueryRow(`
SELECT price_usd FROM card_price_history WHERE scryfall_id='ulamog-id' AND finish='nonfoil'`).
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
		"ulamog-id/nonfoil": {10.0, "scryfall", "2026-07-01T00:00:00Z"},
		"ulamog-id/foil":    {25.0, "scryfall", "2026-07-01T00:00:00Z"},
		"ripple-id/nonfoil": {0.34, "scryfall", "2026-07-01T00:00:00Z"},
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

	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
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

// obs builds one imported observation. Source is fixed: only TCGplayer's series
// is imported, because it is the one Scryfall's own prices come from.
func obs(date, finish string, price float64) mtgjson.Observation {
	return mtgjson.Observation{Date: date, Finish: finish, Price: price, Source: "tcgplayer"}
}

// priceAt reads one stored observation, so a test can assert that a row survived
// a collision rather than merely that some row exists.
func priceAt(t *testing.T, s *Store, id, finish, asOf string) (float64, string) {
	t.Helper()
	var price float64
	var source string
	err := s.db.QueryRow(`SELECT price_usd, source FROM card_price_history
	    WHERE scryfall_id = ? AND finish = ? AND as_of = ?`, id, finish, asOf).
		Scan(&price, &source)
	if err != nil {
		t.Fatalf("reading %s/%s at %s: %v", id, finish, asOf, err)
	}
	return price, source
}

// MTGJSON quotes a price every day whether or not it changed. Storing all ninety
// would add two orders of magnitude more rows than the collection has cards, and
// say nothing a repeat of the previous number does not already say.
func TestBackfillPricesStoresOnlyTheDaysThatMoved(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	inserted, cards, err := s.BackfillPrices(map[string][]mtgjson.Observation{
		"ulamog-id": {
			obs("2026-07-01", "nonfoil", 5.00),
			obs("2026-07-02", "nonfoil", 5.00),
			obs("2026-07-03", "nonfoil", 7.00),
			obs("2026-07-04", "nonfoil", 7.00),
			obs("2026-07-05", "nonfoil", 6.00),
		},
	}, "")
	if err != nil {
		t.Fatalf("BackfillPrices: %v", err)
	}
	// The 1st as baseline, then the two days it actually moved.
	if inserted != 3 || cards != 1 {
		t.Errorf("inserted %d rows across %d cards, want 3 across 1", inserted, cards)
	}
	want := []string{"2026-07-01T00:00:00Z", "2026-07-03T00:00:00Z", "2026-07-05T00:00:00Z"}
	if got := stamps(t, s); !slices.Equal(got, want) {
		t.Errorf("stamps = %v, want %v", got, want)
	}
	if p, src := priceAt(t, s, "ulamog-id", "nonfoil", want[1]); p != 7.00 || src != "tcgplayer" {
		t.Errorf("2026-07-03 = %v from %q, want 7 from tcgplayer", p, src)
	}
}

// Each finish is its own series. A foil that held steady while the non-foil moved
// must not inherit the non-foil's dates, or its history claims moves it never had.
func TestBackfillPricesKeepsFinishesSeparate(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	inserted, _, err := s.BackfillPrices(map[string][]mtgjson.Observation{
		"ulamog-id": {
			obs("2026-07-01", "nonfoil", 5.00),
			obs("2026-07-02", "nonfoil", 6.00),
			obs("2026-07-01", "foil", 20.00),
			obs("2026-07-02", "foil", 20.00),
		},
	}, "")
	if err != nil {
		t.Fatalf("BackfillPrices: %v", err)
	}
	// Two for the non-foil, one for the foil that never moved.
	if inserted != 3 {
		t.Errorf("inserted %d, want 3", inserted)
	}
	var foils int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM card_price_history WHERE finish = 'foil'`).
		Scan(&foils); err != nil {
		t.Fatalf("counting foils: %v", err)
	}
	if foils != 1 {
		t.Errorf("stored %d foil observations, want 1", foils)
	}
}

// An imported price must never displace an observed one. The live row is what the
// prices actually were; the import is a reconstruction made afterwards.
func TestBackfillPricesLeavesObservedRowsAlone(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	backdate(t, s, stamps(t, s)[0], "2026-07-10T00:00:00Z")

	// Same card, same finish, same day, different price — and unbounded, so the
	// only thing stopping the overwrite is the conflict clause.
	inserted, _, err := s.BackfillPrices(map[string][]mtgjson.Observation{
		"ulamog-id": {obs("2026-07-10", "nonfoil", 99.00)},
	}, "")
	if err != nil {
		t.Fatalf("BackfillPrices: %v", err)
	}
	if inserted != 0 {
		t.Errorf("inserted %d, want 0: the day was already observed", inserted)
	}
	if p, src := priceAt(t, s, "ulamog-id", "nonfoil", "2026-07-10T00:00:00Z"); p != 10.00 || src != "scryfall" {
		t.Errorf("stored %v from %q, want the observed 10 from scryfall", p, src)
	}
}

// The bound is not tidiness. MTGJSON's vendor snapshots are taken at a different
// hour than Scryfall's, so an imported point beside a real one for the same era
// would surface in Movers as a few cents of movement that never happened.
func TestBackfillPricesStopsAtExistingHistory(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	backdate(t, s, stamps(t, s)[0], "2026-07-10T05:09:38Z")
	_, oldest, err := s.PriceHistoryDepth()
	if err != nil {
		t.Fatalf("PriceHistoryDepth: %v", err)
	}

	inserted, _, err := s.BackfillPrices(map[string][]mtgjson.Observation{
		"ulamog-id": {
			obs("2026-07-08", "nonfoil", 4.00),
			obs("2026-07-09", "nonfoil", 6.00),
			obs("2026-07-10", "nonfoil", 9.90), // same day as the live row
			obs("2026-07-11", "nonfoil", 9.95), // after it
		},
	}, oldest)
	if err != nil {
		t.Fatalf("BackfillPrices: %v", err)
	}
	if inserted != 2 {
		t.Errorf("inserted %d, want 2: only the days before history began", inserted)
	}
	want := []string{"2026-07-08T00:00:00Z", "2026-07-09T00:00:00Z", "2026-07-10T05:09:38Z"}
	if got := stamps(t, s); !slices.Equal(got, want) {
		t.Errorf("stamps = %v, want %v", got, want)
	}
}

// Filtering happens before the repeats are dropped, not after: the surviving
// window needs a baseline of its own rather than one that got cut off.
func TestBackfillPricesBaselinesTheSurvivingWindow(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	// Compacting first would keep only the 1st, whose price then falls outside
	// the window, leaving the card with no baseline at all.
	inserted, _, err := s.BackfillPrices(map[string][]mtgjson.Observation{
		"ulamog-id": {
			obs("2026-07-01", "nonfoil", 5.00),
			obs("2026-07-02", "nonfoil", 5.00),
			obs("2026-07-03", "nonfoil", 5.00),
			obs("2026-07-09", "nonfoil", 8.00),
		},
	}, "2026-07-03T00:00:00Z")
	if err != nil {
		t.Fatalf("BackfillPrices: %v", err)
	}
	if inserted != 1 {
		t.Fatalf("inserted %d, want 1", inserted)
	}
	if got := stamps(t, s); !slices.Equal(got, []string{"2026-07-01T00:00:00Z"}) {
		t.Errorf("stamps = %v, want the 1st as the window's baseline", got)
	}
}

// The point of the whole exercise: a 30-day question answered on day one.
func TestBackfillPricesGivesMoversItsDepth(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 4); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	backdate(t, s, stamps(t, s)[0], "2026-07-29T05:09:38Z")
	_, oldest, err := s.PriceHistoryDepth()
	if err != nil {
		t.Fatalf("PriceHistoryDepth: %v", err)
	}

	// Before: a month-wide window has nothing at or before its cutoff to
	// compare against, so the card is invisible.
	before, err := s.Movers("2026-06-29T00:00:00Z")
	if err != nil {
		t.Fatalf("Movers: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("got %+v before backfilling, want nothing", before)
	}

	if _, _, err := s.BackfillPrices(map[string][]mtgjson.Observation{
		"ulamog-id": {obs("2026-06-20", "nonfoil", 4.00), obs("2026-07-15", "nonfoil", 8.00)},
	}, oldest); err != nil {
		t.Fatalf("BackfillPrices: %v", err)
	}

	got, err := s.Movers("2026-06-29T00:00:00Z")
	if err != nil {
		t.Fatalf("Movers: %v", err)
	}
	c := changeFor(t, got, "ulamog-id", "nonfoil")
	// Baseline is the imported 15 July price; current is the live one, not the
	// newest import, because midnight sorts before an observed timestamp.
	if c.Old != 4.00 || c.New != 10.00 {
		t.Errorf("moved %v -> %v, want 4 -> 10", c.Old, c.New)
	}
	if c.Source != "scryfall" {
		t.Errorf("Source = %q, want scryfall: the current price is the observed one", c.Source)
	}
	if c.Copies != 4 || c.TotalDelta() != 24.00 {
		t.Errorf("%d copies, total %v; want 4 and 24", c.Copies, c.TotalDelta())
	}

	_, depth, err := s.PriceHistoryDepth()
	if err != nil {
		t.Fatalf("PriceHistoryDepth: %v", err)
	}
	if depth != "2026-06-20T00:00:00Z" {
		t.Errorf("oldest = %q, want the imported 20 June point", depth)
	}
}

// Running it twice must not double the rows: the second pass is bounded by the
// history the first one created.
func TestBackfillPricesIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	series := map[string][]mtgjson.Observation{
		"ulamog-id": {obs("2026-07-01", "nonfoil", 5.00), obs("2026-07-02", "nonfoil", 6.00)},
	}
	if _, _, err := s.BackfillPrices(series, ""); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	first := observationCount(t, s)

	_, oldest, err := s.PriceHistoryDepth()
	if err != nil {
		t.Fatalf("PriceHistoryDepth: %v", err)
	}
	inserted, cards, err := s.BackfillPrices(series, oldest)
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if inserted != 0 || cards != 0 {
		t.Errorf("second pass inserted %d rows across %d cards, want none", inserted, cards)
	}
	if got := observationCount(t, s); got != first {
		t.Errorf("history grew from %d to %d", first, got)
	}
}

// Nothing to import must not open a transaction or report anything happened.
func TestBackfillPricesEmptyInputWritesNothing(t *testing.T) {
	s := newTestStore(t)
	inserted, cards, err := s.BackfillPrices(nil, "")
	if err != nil || inserted != 0 || cards != 0 {
		t.Errorf("got %d/%d, %v; want 0/0 and no error", inserted, cards, err)
	}
}

// RecordPrices must snapshot the hoard's value even when nothing moved: the
// chart's flat stretches mean "checked, unchanged", and skipping them would
// leave gaps indistinguishable from "never looked".
func TestRecordPricesWritesValueSnapshot(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if _, err := s.UpsertDeck(DeckMeta{Name: "Fish", Source: "manual", SourceID: "deck:fish"},
		[]Entry{{ScryfallID: "ulamog-id", Finish: "foil", Board: "main", Quantity: 1}}); err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}

	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}
	snaps, err := s.ValueSnapshots()
	if err != nil {
		t.Fatalf("ValueSnapshots: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("snapshots = %d, want 1 after a refresh", len(snaps))
	}
	got := snaps[0]
	if got.Seeded {
		t.Error("a refresh's snapshot is marked seeded")
	}
	// 2 non-foil at $10 loose, 1 foil at $25 in the deck.
	if got.Binder != 20 || got.Decks != 25 || got.Total != 45 {
		t.Errorf("snapshot = binder %v / decks %v / total %v, want 20/25/45",
			got.Binder, got.Decks, got.Total)
	}

	// A second observation at a distinct instant lands beside it, prices
	// unchanged — written directly so the test does not sleep out a
	// same-second collision.
	if err := s.snapshotValue("2099-01-01T00:00:00Z"); err != nil {
		t.Fatalf("snapshotValue: %v", err)
	}
	if snaps, _ = s.ValueSnapshots(); len(snaps) != 2 {
		t.Errorf("snapshots = %d, want the unchanged value recorded again", len(snaps))
	}
}

// Migrating to v9 seeds the value series from existing price history: one
// estimated point per recorded date, from today's quantities at that day's
// prices, carrying each price forward across dates it did not move.
func TestValueSnapshotsSeededFromHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seed.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.AddCardFinish(ulamog(), "nonfoil", 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if _, err := s.UpsertDeck(DeckMeta{Name: "Fish", Source: "manual", SourceID: "deck:fish"},
		[]Entry{{ScryfallID: "ulamog-id", Finish: "foil", Board: "main", Quantity: 1}}); err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO card_price_history VALUES ('ulamog-id','nonfoil',10,'scryfall','2026-05-01T00:00:00Z')`,
		`INSERT INTO card_price_history VALUES ('ulamog-id','foil',20,'scryfall','2026-05-01T00:00:00Z')`,
		`INSERT INTO card_price_history VALUES ('ulamog-id','nonfoil',12,'scryfall','2026-05-10T00:00:00Z')`,
		// Rewind to v8 so reopening replays the snapshot migration (and
		// v10's watches table, which must also not pre-exist).
		`DROP TABLE value_snapshots`,
		`DROP TABLE watches`,
		`PRAGMA user_version = 8`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatalf("reopen (migrate): %v", err)
	}
	defer s.Close()

	snaps, err := s.ValueSnapshots()
	if err != nil {
		t.Fatalf("ValueSnapshots: %v", err)
	}
	if len(snaps) != 2 {
		t.Fatalf("seeded snapshots = %+v, want one per history date", snaps)
	}
	first, second := snaps[0], snaps[1]
	if !first.Seeded || !second.Seeded {
		t.Error("migration-built snapshots are not marked seeded")
	}
	if first.AsOf != "2026-05-01T00:00:00Z" || second.AsOf != "2026-05-10T00:00:00Z" {
		t.Errorf("stamps = %s / %s", first.AsOf, second.AsOf)
	}
	// Day one: 2 non-foil at $10 loose, 1 foil at $20 in the deck.
	if first.Binder != 20 || first.Decks != 20 || first.Total != 40 {
		t.Errorf("first = %+v, want binder 20 / decks 20 / total 40", first)
	}
	// Day ten: the non-foil moved to $12; the foil price carries forward.
	if second.Binder != 24 || second.Decks != 20 || second.Total != 44 {
		t.Errorf("second = %+v, want binder 24 / decks 20 / total 44", second)
	}
}
