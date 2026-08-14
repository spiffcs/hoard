package store

import (
	"github.com/spiffcs/hoard/internal/finish"
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
)

func TestFinishRoundTripsThroughSQLite(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.db.Exec(`CREATE TABLE finish_probe (finish TEXT NOT NULL)`); err != nil {
		t.Fatalf("creating probe table: %v", err)
	}
	all := finish.All()
	if len(all) < 3 {
		t.Fatalf("finish.All() = %v, want at least nonfoil, foil and etched", all)
	}
	for _, want := range all {
		if _, err := s.db.Exec(`DELETE FROM finish_probe`); err != nil {
			t.Fatalf("clearing probe table: %v", err)
		}
		if _, err := s.db.Exec(`INSERT INTO finish_probe (finish) VALUES (?)`, want); err != nil {
			t.Fatalf("inserting %q: %v", want, err)
		}

		var text string
		if err := s.db.QueryRow(`SELECT finish FROM finish_probe`).Scan(&text); err != nil {
			t.Fatalf("reading %q back as text: %v", want, err)
		}
		if text != want.String() {
			t.Errorf("%q stored as %q, want its own spelling", want, text)
		}

		var got finish.Finish
		if err := s.db.QueryRow(`SELECT finish FROM finish_probe`).Scan(&got); err != nil {
			t.Fatalf("scanning %q back: %v", want, err)
		}
		if got != want {
			t.Errorf("scanned %q, want %q", got, want)
		}
	}
}

func TestScanRejectsAnyTextThatIsNotAFinish(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.db.Exec(`CREATE TABLE scan_probe (finish TEXT NOT NULL)`); err != nil {
		t.Fatalf("creating probe table: %v", err)
	}
	for _, text := range []string{"normal", "shiny", "", "Foil"} {
		if _, err := s.db.Exec(`DELETE FROM scan_probe`); err != nil {
			t.Fatalf("clearing probe table: %v", err)
		}
		if _, err := s.db.Exec(`INSERT INTO scan_probe (finish) VALUES (?)`, text); err != nil {
			t.Fatalf("inserting %q: %v", text, err)
		}
		var got finish.Finish
		if err := s.db.QueryRow(`SELECT finish FROM scan_probe`).Scan(&got); err == nil {
			t.Errorf("a %q row scanned as %q, want an error", text, got)
		}
	}
}

func TestEveryFinishIsValuedByThePriceSQL(t *testing.T) {
	wantValue := map[finish.Finish]float64{
		finish.Nonfoil: 1.50,
		finish.Foil:    4.00,
		finish.Etched:  30.00,
	}
	all := finish.All()
	if len(all) < 3 {
		t.Fatalf("finish.All() = %v, want at least nonfoil, foil and etched", all)
	}
	for _, fin := range all {
		t.Run(fin.String(), func(t *testing.T) {
			want, declared := wantValue[fin]
			if !declared {
				t.Fatalf("%q is enumerated but no price is expected for it here. "+
					"The price SQL in store.go ends in an ELSE that quietly values "+
					"anything it does not recognise at the plain USD price, so decide "+
					"which column prices %q and say so here", fin, fin)
			}
			s := newTestStore(t)
			c := scryfall.Card{
				ID: "kenrith-id", Set: "cmr", CollectorNumber: "332", Name: "Kenrith",
				PriceUSD:       f(1.50),
				PriceUSDFoil:   f(4.00),
				PriceUSDEtched: f(30.00),
				ScryfallURL:    "https://scryfall.com/card/cmr/332",
			}
			if err := s.AddCardFinish(c, fin, 1); err != nil {
				t.Fatalf("AddCardFinish(%q): %v", fin, err)
			}

			totals, err := s.CollectionTotals()
			if err != nil {
				t.Fatalf("CollectionTotals: %v", err)
			}
			if totals.Value != want {
				t.Errorf("a %q copy values at %v, want %v", fin, totals.Value, want)
			}

			un, err := s.Unpriced()
			if err != nil {
				t.Fatalf("Unpriced: %v", err)
			}
			if len(un) != 0 {
				t.Errorf("a fully priced %q printing reads as unpriced: %+v", fin, un)
			}
		})
	}
}
