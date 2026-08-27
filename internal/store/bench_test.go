package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/scryfall"
)

const (
	benchPrintings = 20000
	benchSets      = 200
	benchDays      = 30
)

func benchRaw(i int, oracle string) []byte {
	return fmt.Appendf(nil, `{"object":"card","id":"id-%06d","name":"Benchmark Card %06d",
"set":"s%03d","set_name":"Benchmark Set %03d","collector_number":"%d",
"released_at":"2021-06-18","rarity":"%s","lang":"en","layout":"normal",
"artist":"Benchmark Artist","cmc":%d.0,"mana_cost":"{2}{U}","color_identity":["U","B"],
"type_line":"Creature — Human Wizard","power":"2","toughness":"3",
"oracle_text":%q,"flavor_text":%q,"games":["paper"],"finishes":["nonfoil","foil"],
"promo_types":["boosterfun"],"tcgplayer_id":%d,
"image_uris":{"normal":"https://example.invalid/%06d.jpg"},
"scryfall_uri":"https://example.invalid/card/%06d"}`,
		i, i, i%benchSets, i%benchSets, i%400,
		[]string{"common", "uncommon", "rare", "mythic"}[i%4], i%8,
		oracle, oracle, i, i, i)
}

func benchStore(b *testing.B, printings int) *Store {
	b.Helper()
	st, err := Open(filepath.Join(b.TempDir(), "bench.db"))
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { st.Close() })

	cid, err := st.CollectionID()
	if err != nil {
		b.Fatalf("CollectionID: %v", err)
	}

	oracle := strings.Repeat("Whenever this creature attacks, draw a card and scry two. ", 70)

	const batch = 2000
	for lo := 0; lo < printings; lo += batch {
		hi := min(lo+batch, printings)
		ps := make([]CompendiumPrinting, 0, hi-lo)
		for i := lo; i < hi; i++ {
			price := float64(i%400) / 4
			ps = append(ps, CompendiumPrinting{
				Card: scryfall.Card{
					ID:              fmt.Sprintf("id-%06d", i),
					Name:            fmt.Sprintf("Benchmark Card %06d", i),
					Set:             fmt.Sprintf("s%03d", i%benchSets),
					SetName:         fmt.Sprintf("Benchmark Set %03d", i%benchSets),
					CollectorNumber: fmt.Sprintf("%d", i%400),
					ReleasedAt:      "2021-06-18",
					Lang:            "en",
					ScryfallURL:     fmt.Sprintf("https://example.invalid/card/%06d", i),
					Finishes:        []string{"nonfoil", "foil"},
					PriceUSD:        &price,
					PriceUSDFoil:    &price,
					Raw:             benchRaw(i, oracle),
				},
				Finishes: []finish.Finish{finish.Nonfoil, finish.Foil},
			})
		}
		if _, _, err := st.SeedCompendiumPrintings(cid, ps); err != nil {
			b.Fatalf("SeedCompendiumPrintings: %v", err)
		}
	}

	seedBenchHistory(b, st, printings)
	return st
}

func seedBenchHistory(b *testing.B, st *Store, printings int) {
	b.Helper()
	retail := make(map[string][]mtgjson.Observation, printings)
	now := time.Now()
	for i := range printings {
		id := fmt.Sprintf("id-%06d", i)
		obs := make([]mtgjson.Observation, 0, benchDays)
		for d := benchDays - 1; d >= 0; d-- {
			obs = append(obs, mtgjson.Observation{
				Finish: finish.Nonfoil,
				Date:   now.AddDate(0, 0, -d).Format(time.RFC3339),
				Price:  float64(i%400)/4 + float64(d)/10,
				Source: "tcgplayer",
			})
		}
		retail[id] = obs
	}
	if _, _, err := st.BackfillPrices(retail); err != nil {
		b.Fatalf("BackfillPrices: %v", err)
	}
}

func benchAvgRawBytes(b *testing.B, st *Store) int {
	b.Helper()
	var avg float64
	if err := st.db.QueryRow(`SELECT AVG(LENGTH(raw_json)) FROM cards`).Scan(&avg); err != nil {
		b.Fatalf("measuring raw_json: %v", err)
	}
	return int(avg)
}

func BenchmarkAllByFinish(b *testing.B) {
	st := benchStore(b, benchPrintings)
	if n := benchAvgRawBytes(b, st); n < 3000 {
		b.Fatalf("raw_json averages %d bytes, too small to reproduce the "+
			"json_extract cost this benchmark exists to measure", n)
	}
	b.ResetTimer()
	for b.Loop() {
		if _, err := st.AllByFinish(); err != nil {
			b.Fatalf("AllByFinish: %v", err)
		}
	}
}

func BenchmarkSetsHeld(b *testing.B) {
	st := benchStore(b, benchPrintings)
	b.ResetTimer()
	for b.Loop() {
		if _, err := st.SetsHeld(); err != nil {
			b.Fatalf("SetsHeld: %v", err)
		}
	}
}

func BenchmarkSetByFinish(b *testing.B) {
	st := benchStore(b, benchPrintings)
	b.ResetTimer()
	for b.Loop() {
		if _, err := st.SetByFinish("s001"); err != nil {
			b.Fatalf("SetByFinish: %v", err)
		}
	}
}

func BenchmarkOwnedByFinish(b *testing.B) {
	st := benchStore(b, benchPrintings)
	b.ResetTimer()
	for b.Loop() {
		if _, err := st.OwnedByFinish(); err != nil {
			b.Fatalf("OwnedByFinish: %v", err)
		}
	}
}

func BenchmarkMovers(b *testing.B) {
	st := benchStore(b, benchPrintings)
	since := time.Now().AddDate(0, 0, -benchDays).Format(time.RFC3339)
	b.ResetTimer()
	for b.Loop() {
		if _, err := st.Movers(since); err != nil {
			b.Fatalf("Movers: %v", err)
		}
	}
}

func BenchmarkMatchingCardIDs(b *testing.B) {
	st := benchStore(b, benchPrintings)
	f := TraitFilter{Types: []string{"creature"}}
	b.ResetTimer()
	for b.Loop() {
		if _, err := st.MatchingCardIDs(f); err != nil {
			b.Fatalf("MatchingCardIDs: %v", err)
		}
	}
}

// BenchmarkRealDB measures against an existing large database instead of the
// generated fixture, which is small enough to sit entirely in the page cache.
// Point HOARD_BENCH_DB at a copy — never at a database you care about, since
// opening one runs migrations.
func BenchmarkRealDB(b *testing.B) {
	path := os.Getenv("HOARD_BENCH_DB")
	if path == "" {
		b.Skip("set HOARD_BENCH_DB to a copy of a large database")
	}
	st, err := Open(path)
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { st.Close() })

	var cards int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM cards`).Scan(&cards); err != nil {
		b.Fatalf("counting cards: %v", err)
	}
	if cards < 50000 {
		b.Fatalf("%s holds %d cards, too few to exercise cache pressure", path, cards)
	}
	b.Logf("%s: %d cards", path, cards)

	for _, c := range []struct {
		name string
		run  func() error
	}{
		{"AllByFinish", func() error { _, err := st.AllByFinish(); return err }},
		{"SetsHeld", func() error { _, err := st.SetsHeld(); return err }},
		{"OwnedByFinish", func() error { _, err := st.OwnedByFinish(); return err }},
		{"SetByFinish", func() error { _, err := st.SetByFinish("mh2"); return err }},
		{"SetUnowned", func() error { _, err := st.SetUnowned("mh2"); return err }},
	} {
		b.Run(c.name, func(b *testing.B) {
			for b.Loop() {
				if err := c.run(); err != nil {
					b.Fatalf("%s: %v", c.name, err)
				}
			}
		})
	}
}
