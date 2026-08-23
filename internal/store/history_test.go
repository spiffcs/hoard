package store

import (
	"github.com/spiffcs/hoard/internal/finish"
	"path/filepath"
	"slices"
	"testing"

	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func changeFor(t *testing.T, changes []PriceChange, id string, fin finish.Finish) PriceChange {
	t.Helper()
	for _, c := range changes {
		if c.ScryfallID == id && c.Finish == fin {
			return c
		}
	}
	t.Fatalf("no change for %s/%s in %+v", id, fin, changes)
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

func TestRecordPricesFirstRunIsBaseline(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 2); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	changes, err := s.RecordPrices()
	if err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}
	if len(changes) != 0 {
		t.Errorf("first run reported %+v, want no movement", changes)
	}

	if n := observationCount(t, s); n != 2 {
		t.Errorf("recorded %d observations, want 2 (one per priced finish)", n)
	}
}

func TestRecordPricesReportsMovement(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 3); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}

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

	if n := observationCount(t, s); n != 3 {
		t.Errorf("recorded %d observations, want 3 (2 baseline + 1 move)", n)
	}
}

func TestRecordPricesSkipsUnchanged(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
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

func TestRecordPricesCountsCopiesAcrossContainersAndFinishes(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(solRing(), finish.Foil, 2); err != nil {
		t.Fatalf("AddCardFinish foil: %v", err)
	}
	if err := s.AddCardFinish(solRing(), finish.Etched, 1); err != nil {
		t.Fatalf("AddCardFinish etched: %v", err)
	}
	if _, err := s.UpsertDeck(
		DeckMeta{Name: "EDH", Source: "text", SourceID: "edh"},
		[]Entry{{ScryfallID: "sol-id", Finish: finish.Foil, Board: "main", Quantity: 4}},
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

	c := changeFor(t, changes, "sol-id", finish.Foil)
	if c.Copies != 7 {
		t.Errorf("Copies = %d, want 7 (2 foil + 1 etched loose + 4 foil in a deck)", c.Copies)
	}
	if got := c.TotalDelta(); got != -7.00 {
		t.Errorf("TotalDelta = %v, want -7.00", got)
	}
}

func TestRecordPricesIgnoresUnownedCards(t *testing.T) {
	s := newTestStore(t)

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

	if n := observationCount(t, s); n != 3 {
		t.Errorf("recorded %d observations, want 3", n)
	}
}

func TestRecordPricesUsesFallbackPrices(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(unpricedFoil(), finish.Foil, 2); err != nil {
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
	c := changeFor(t, changes, "ripple-id", finish.Foil)
	if c.Old != 30.00 || c.New != 36.00 {
		t.Errorf("moved %v -> %v, want 30 -> 36", c.Old, c.New)
	}
	if c.Source != "cardkingdom" {
		t.Errorf("Source = %q, want the vendor behind the fallback", c.Source)
	}
}

func TestMoversWindow(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}

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
		c := changeFor(t, got, "ulamog-id", finish.Nonfoil)
		if c.Old != 10.00 || c.New != 20.00 {
			t.Errorf("moved %v -> %v, want 10 -> 20", c.Old, c.New)
		}
	})

	t.Run("window older than the history sees the move", func(t *testing.T) {
		got, err := s.Movers("2020-01-01T00:00:00Z")
		if err != nil {
			t.Fatalf("Movers: %v", err)
		}
		c := changeFor(t, got, "ulamog-id", finish.Nonfoil)
		if c.Old != 10.00 || c.New != 20.00 {
			t.Errorf("moved %v -> %v, want 10 -> 20", c.Old, c.New)
		}
		if c.OldAsOf != "2026-06-01T00:00:00Z" {
			t.Errorf("OldAsOf = %q, want the record's own first observation", c.OldAsOf)
		}
	})
}

func TestRecordPricesWithinOneSecond(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}

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

	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
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

func obs(date string, fin finish.Finish, price float64) mtgjson.Observation {
	return mtgjson.Observation{Date: date, Finish: fin, Price: price, Source: "tcgplayer"}
}

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

func TestBackfillPricesStoresOnlyTheDaysThatMoved(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	inserted, cards, err := s.BackfillPrices(map[string][]mtgjson.Observation{
		"ulamog-id": {
			obs("2026-07-01", finish.Nonfoil, 5.00),
			obs("2026-07-02", finish.Nonfoil, 5.00),
			obs("2026-07-03", finish.Nonfoil, 7.00),
			obs("2026-07-04", finish.Nonfoil, 7.00),
			obs("2026-07-05", finish.Nonfoil, 6.00),
		},
	})
	if err != nil {
		t.Fatalf("BackfillPrices: %v", err)
	}

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

func TestBackfillPricesKeepsFinishesSeparate(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	inserted, _, err := s.BackfillPrices(map[string][]mtgjson.Observation{
		"ulamog-id": {
			obs("2026-07-01", finish.Nonfoil, 5.00),
			obs("2026-07-02", finish.Nonfoil, 6.00),
			obs("2026-07-01", finish.Foil, 20.00),
			obs("2026-07-02", finish.Foil, 20.00),
		},
	})
	if err != nil {
		t.Fatalf("BackfillPrices: %v", err)
	}

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

func TestBackfillPricesLeavesObservedRowsAlone(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	backdate(t, s, stamps(t, s)[0], "2026-07-10T00:00:00Z")

	inserted, _, err := s.BackfillPrices(map[string][]mtgjson.Observation{
		"ulamog-id": {obs("2026-07-10", finish.Nonfoil, 99.00)},
	})
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

func TestBackfillPricesStopsAtExistingHistory(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	backdate(t, s, stamps(t, s)[0], "2026-07-10T05:09:38Z")

	inserted, _, err := s.BackfillPrices(map[string][]mtgjson.Observation{
		"ulamog-id": {
			obs("2026-07-08", finish.Nonfoil, 4.00),
			obs("2026-07-09", finish.Nonfoil, 6.00),
			obs("2026-07-10", finish.Nonfoil, 9.90),
			obs("2026-07-11", finish.Nonfoil, 9.95),
		},
	})
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

func TestBackfillPricesInsertsOnlyDaysBeforeTheLiveEra(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	backdate(t, s, stamps(t, s)[0], "2026-07-10T05:09:38Z")

	inserted, _, err := s.BackfillPrices(map[string][]mtgjson.Observation{
		"ulamog-id": {
			obs("2026-07-08", finish.Nonfoil, 4.00),
			obs("2026-07-10", finish.Nonfoil, 9.90),
		},
	})
	if err != nil {
		t.Fatalf("BackfillPrices: %v", err)
	}
	if inserted != 1 {
		t.Fatalf("inserted %d, want only the day before the live era", inserted)
	}
	if p, _ := priceAt(t, s, "ulamog-id", "nonfoil", "2026-07-08T00:00:00Z"); p != 4.00 {
		t.Errorf("8 July nonfoil = %v, want 4.00", p)
	}
}

func TestBackfillPricesBaselinesTheSurvivingWindow(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	backdate(t, s, stamps(t, s)[0], "2026-07-03T00:00:00Z")

	inserted, _, err := s.BackfillPrices(map[string][]mtgjson.Observation{
		"ulamog-id": {
			obs("2026-07-01", finish.Nonfoil, 5.00),
			obs("2026-07-02", finish.Nonfoil, 5.00),
			obs("2026-07-03", finish.Nonfoil, 5.00),
			obs("2026-07-09", finish.Nonfoil, 8.00),
		},
	})
	if err != nil {
		t.Fatalf("BackfillPrices: %v", err)
	}
	if inserted != 1 {
		t.Fatalf("inserted %d, want 1", inserted)
	}
	want := []string{"2026-07-01T00:00:00Z", "2026-07-03T00:00:00Z"}
	if got := stamps(t, s); !slices.Equal(got, want) {
		t.Errorf("stamps = %v, want the 1st as the window's baseline under the live row", got)
	}
}

func TestBackfillBoundIsPerCard(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	backdate(t, s, stamps(t, s)[0], "2026-07-10T05:09:38Z")

	if err := s.AddCardFinish(solRing(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	inserted, cards, err := s.BackfillPrices(map[string][]mtgjson.Observation{
		"ulamog-id": {
			obs("2026-07-08", finish.Nonfoil, 4.00),
			obs("2026-07-09", finish.Nonfoil, 6.00),
			obs("2026-07-10", finish.Nonfoil, 9.90),
		},
		"sol-id": {
			obs("2026-07-08", finish.Nonfoil, 1.00),
			obs("2026-07-09", finish.Nonfoil, 1.00),
			obs("2026-07-10", finish.Nonfoil, 2.00),
			obs("2026-07-11", finish.Nonfoil, 2.00),
		},
	})
	if err != nil {
		t.Fatalf("BackfillPrices: %v", err)
	}

	if inserted != 4 || cards != 2 {
		t.Fatalf("inserted %d rows across %d cards, want 4 across 2", inserted, cards)
	}
	if p, _ := priceAt(t, s, "sol-id", "nonfoil", "2026-07-10T00:00:00Z"); p != 2.00 {
		t.Errorf("sol ring 10 July = %v, want the imported 2.00", p)
	}
}

func TestBackfillPricesGivesMoversItsDepth(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 4); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	backdate(t, s, stamps(t, s)[0], "2026-07-29T05:09:38Z")

	before, err := s.Movers("2026-06-29T00:00:00Z")
	if err != nil {
		t.Fatalf("Movers: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("got %+v before backfilling, want nothing", before)
	}

	if _, _, err := s.BackfillPrices(map[string][]mtgjson.Observation{
		"ulamog-id": {obs("2026-06-20", finish.Nonfoil, 4.00), obs("2026-07-15", finish.Nonfoil, 8.00)},
	}); err != nil {
		t.Fatalf("BackfillPrices: %v", err)
	}

	got, err := s.Movers("2026-06-29T00:00:00Z")
	if err != nil {
		t.Fatalf("Movers: %v", err)
	}
	c := changeFor(t, got, "ulamog-id", finish.Nonfoil)

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

func TestBackfillPricesIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	series := map[string][]mtgjson.Observation{
		"ulamog-id": {obs("2026-07-01", finish.Nonfoil, 5.00), obs("2026-07-02", finish.Nonfoil, 6.00)},
	}
	if _, _, err := s.BackfillPrices(series); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	first := observationCount(t, s)

	inserted, cards, err := s.BackfillPrices(series)
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

func TestBackfillPricesEmptyInputWritesNothing(t *testing.T) {
	s := newTestStore(t)
	inserted, cards, err := s.BackfillPrices(nil)
	if err != nil || inserted != 0 || cards != 0 {
		t.Errorf("got %d/%d, %v; want 0/0 and no error", inserted, cards, err)
	}
}

func TestRecordPricesWritesValueSnapshot(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if _, err := s.UpsertDeck(DeckMeta{Name: "Fish", Source: "manual", SourceID: "deck:fish"},
		[]Entry{{ScryfallID: "ulamog-id", Finish: finish.Foil, Board: "main", Quantity: 1}}); err != nil {
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

	if got.Binder != 20 || got.Decks != 25 || got.Total != 45 {
		t.Errorf("snapshot = binder %v / decks %v / total %v, want 20/25/45",
			got.Binder, got.Decks, got.Total)
	}

	tx, err := s.db.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := snapshotValue(tx, "2099-01-01T00:00:00Z"); err != nil {
		t.Fatalf("snapshotValue: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if snaps, _ = s.ValueSnapshots(); len(snaps) != 2 {
		t.Errorf("snapshots = %d, want the unchanged value recorded again", len(snaps))
	}
}

func TestValueSnapshotsSeededFromHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seed.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if _, err := s.UpsertDeck(DeckMeta{Name: "Fish", Source: "manual", SourceID: "deck:fish"},
		[]Entry{{ScryfallID: "ulamog-id", Finish: finish.Foil, Board: "main", Quantity: 1}}); err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO card_price_history VALUES ('ulamog-id','nonfoil',10,'scryfall','2026-05-01T00:00:00Z')`,
		`INSERT INTO card_price_history VALUES ('ulamog-id','foil',20,'scryfall','2026-05-01T00:00:00Z')`,
		`INSERT INTO card_price_history VALUES ('ulamog-id','nonfoil',12,'scryfall','2026-05-10T00:00:00Z')`,

		`DROP TABLE value_snapshots`,
		`DROP TABLE watches`,
		`DROP TABLE card_bid_history`,
		`DROP TABLE settings`,
		`DROP TABLE finish_guesses`,
		`ALTER TABLE cards DROP COLUMN tcgplayer_id`,
		`ALTER TABLE cards DROP COLUMN ck_url`,
		`ALTER TABLE cards DROP COLUMN ck_foil_url`,
		`ALTER TABLE cards DROP COLUMN promo_types`,
		`ALTER TABLE cards DROP COLUMN tcg_alt_product_id`,
		`ALTER TABLE cards DROP COLUMN ck_foil_id`,
		`ALTER TABLE cards DROP COLUMN ck_etched_id`,
		`ALTER TABLE cards DROP COLUMN tcg_product_id`,
		`ALTER TABLE cards DROP COLUMN price_usd_etched`,
		`ALTER TABLE cards DROP COLUMN tcg_etched_product_id`,
		`ALTER TABLE cards DROP COLUMN lang`,
		`ALTER TABLE cards DROP COLUMN printed_name`,
		`ALTER TABLE cards DROP COLUMN power`,
		`ALTER TABLE cards DROP COLUMN toughness`,
		`ALTER TABLE cards DROP COLUMN loyalty`,
		`ALTER TABLE cards DROP COLUMN flavor_text`,
		`ALTER TABLE cards DROP COLUMN image_uri`,
		`DROP TRIGGER card_entries_folder_insert`,
		`DROP TRIGGER card_entries_folder_update`,
		`DROP INDEX containers_parent`,
		`ALTER TABLE containers DROP COLUMN parent_id`,
		`ALTER TABLE containers DROP COLUMN counted`,
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

	if first.Binder != 20 || first.Decks != 20 || first.Total != 40 {
		t.Errorf("first = %+v, want binder 20 / decks 20 / total 40", first)
	}

	if second.Binder != 24 || second.Decks != 20 || second.Total != 44 {
		t.Errorf("second = %+v, want binder 24 / decks 20 / total 44", second)
	}
}

func srcObs(date string, fin finish.Finish, price float64, source string) mtgjson.Observation {
	return mtgjson.Observation{Date: date, Finish: fin, Price: price, Source: source}
}

func TestBackfillReplacesVendorSwitchedSeries(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Foil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	if _, _, err := s.BackfillPrices(map[string][]mtgjson.Observation{
		"ulamog-id": {
			srcObs("2026-07-01", finish.Foil, 21.00, "manapool"),
			srcObs("2026-07-05", finish.Foil, 21.78, "manapool"),
		},
	}); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	if _, err := s.db.Exec(`INSERT INTO card_price_history VALUES
		('ulamog-id','foil',21.90,'manapool','2026-07-06T09:15:00Z')`); err != nil {
		t.Fatal(err)
	}

	inserted, cards, err := s.BackfillPrices(map[string][]mtgjson.Observation{
		"ulamog-id": {
			srcObs("2026-07-01", finish.Foil, 17.10, "tcgplayer"),
			srcObs("2026-07-03", finish.Foil, 17.56, "tcgplayer"),
		},
	})
	if err != nil {
		t.Fatalf("BackfillPrices: %v", err)
	}
	if inserted != 2 || cards != 1 {
		t.Errorf("inserted %d across %d cards, want the whole new series in", inserted, cards)
	}
	if p, src := priceAt(t, s, "ulamog-id", "foil", "2026-07-01T00:00:00Z"); p != 17.10 || src != "tcgplayer" {
		t.Errorf("replaced series reads %v from %q, want tcgplayer 17.10", p, src)
	}
	want := []string{"2026-07-01T00:00:00Z", "2026-07-03T00:00:00Z", "2026-07-06T09:15:00Z"}
	if got := stamps(t, s); !slices.Equal(got, want) {
		t.Errorf("stamps = %v, want the old reconstruction gone and the live row standing", got)
	}
	if p, src := priceAt(t, s, "ulamog-id", "foil", "2026-07-06T09:15:00Z"); p != 21.90 || src != "manapool" {
		t.Errorf("live row = %v from %q, want untouched", p, src)
	}

	inserted, _, err = s.BackfillPrices(map[string][]mtgjson.Observation{
		"ulamog-id": {srcObs("2026-07-01", finish.Foil, 17.10, "tcgplayer")},
	})
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if inserted != 0 {
		t.Errorf("re-run inserted %d, want 0", inserted)
	}
	if got := stamps(t, s); !slices.Equal(got, want) {
		t.Errorf("stamps after re-run = %v, want unchanged", got)
	}
}

func TestBackfillRetiresOnlyTheIncomingFinish(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Foil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, _, err := s.BackfillPrices(map[string][]mtgjson.Observation{
		"ulamog-id": {srcObs("2026-07-01", finish.Foil, 21.00, "manapool")},
	}); err != nil {
		t.Fatalf("seeding: %v", err)
	}

	if _, _, err := s.BackfillPrices(map[string][]mtgjson.Observation{
		"ulamog-id": {srcObs("2026-06-20", finish.Nonfoil, 9.00, "tcgplayer")},
	}); err != nil {
		t.Fatalf("BackfillPrices: %v", err)
	}
	if p, src := priceAt(t, s, "ulamog-id", "foil", "2026-07-01T00:00:00Z"); p != 21.00 || src != "manapool" {
		t.Errorf("foil series = %v from %q, want untouched by the nonfoil import", p, src)
	}
}

func TestRecordPricesSkipsFinishesThePrintingLacks(t *testing.T) {
	s := newTestStore(t)

	c := ulamog()
	c.Raw = []byte(`{"finishes":["foil"]}`)
	if err := s.AddCardFinish(c, finish.Foil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}

	rows, err := s.db.Query(`SELECT finish FROM card_price_history`)
	if err != nil {
		t.Fatalf("reading history: %v", err)
	}
	defer rows.Close()
	var finishes []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			t.Fatalf("scan: %v", err)
		}
		finishes = append(finishes, f)
	}
	if slices.Contains(finishes, "nonfoil") {
		t.Errorf("recorded finishes %v; the printing has no non-foil copy", finishes)
	}
	if !slices.Contains(finishes, "foil") {
		t.Errorf("recorded finishes %v, want the foil series it does have", finishes)
	}
}

func TestRecordPricesKeepsSeriesForUnknownFinishes(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}
	if n := observationCount(t, s); n != 2 {
		t.Errorf("recorded %d observations, want 2 — a printing with no stored "+
			"document must keep both priced series", n)
	}
}
