package store

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func seedSets(t *testing.T, s *Store) {
	t.Helper()
	mh2 := scryfall.Card{
		ID: "sol-mh2", Set: "mh2", CollectorNumber: "1", Name: "Solitude",
		ScryfallURL: "http://x", PriceUSD: f(30),
		Raw: []byte(`{"set_name":"Modern Horizons 2","released_at":"2021-06-18"}`),
	}
	uma := scryfall.Card{
		ID: "bb-uma", Set: "uma", CollectorNumber: "2", Name: "Bitterblossom",
		ScryfallURL: "http://x", PriceUSD: f(20), PriceUSDFoil: f(60),
		Raw: []byte(`{"set_name":"Ultimate Masters","released_at":"2018-12-07"}`),
	}
	bare := scryfall.Card{
		ID: "myst-zzz", Set: "zzz", CollectorNumber: "3", Name: "Mystery",
		ScryfallURL: "http://x", PriceUSD: f(1),
	}
	if err := s.AddCardFinish(mh2, finish.Nonfoil, 2); err != nil {
		t.Fatalf("adding mh2: %v", err)
	}
	if err := s.AddCardFinish(uma, finish.Nonfoil, 1); err != nil {
		t.Fatalf("adding uma nonfoil: %v", err)
	}
	if err := s.AddCardFinish(uma, finish.Foil, 1); err != nil {
		t.Fatalf("adding uma foil: %v", err)
	}
	if err := s.AddCardFinish(bare, finish.Nonfoil, 3); err != nil {
		t.Fatalf("adding bare: %v", err)
	}

	bid, err := s.CreateBinder("Trades")
	if err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	if err := s.AddCardFinishTo(bid, mh2, finish.Nonfoil, 1); err != nil {
		t.Fatalf("adding mh2 to binder: %v", err)
	}
}

func TestSetsHeld(t *testing.T) {
	s := newTestStore(t)
	seedSets(t, s)

	sets, err := s.SetsHeld()
	if err != nil {
		t.Fatalf("SetsHeld: %v", err)
	}
	if len(sets) != 3 {
		t.Fatalf("sets = %+v, want 3", sets)
	}
	if sets[0].Code != "mh2" || sets[1].Code != "uma" || sets[2].Code != "zzz" {
		t.Fatalf("order = %s %s %s, want mh2 uma zzz (newest first, undated last)",
			sets[0].Code, sets[1].Code, sets[2].Code)
	}
	if sets[0].Name != "Modern Horizons 2" || sets[0].ReleasedAt != "2021-06-18" {
		t.Errorf("mh2 = %+v, want the pretty name and date", sets[0])
	}
	if sets[2].Name != "ZZZ" || sets[2].ReleasedAt != "" {
		t.Errorf("bare set = %+v, want the upper-cased code and no date", sets[2])
	}
	if sets[0].Copies != 3 || sets[0].Value != 90 {
		t.Errorf("mh2 rollup = %d copies $%.0f, want 3 copies $90 across both binders",
			sets[0].Copies, sets[0].Value)
	}
	if sets[1].Copies != 2 || sets[1].Value != 80 {
		t.Errorf("uma rollup = %d copies $%.0f, want 2 copies $80 (nonfoil + foil)",
			sets[1].Copies, sets[1].Value)
	}
}

func TestSetByFinish(t *testing.T) {
	s := newTestStore(t)
	seedSets(t, s)

	rows, err := s.SetByFinish("uma")
	if err != nil {
		t.Fatalf("SetByFinish: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("uma rows = %+v, want the two finishes", rows)
	}
	for _, r := range rows {
		if r.SetCode != "uma" {
			t.Errorf("row %s from set %s leaked into uma", r.Name, r.SetCode)
		}
	}

	rows, err = s.SetByFinish("mh2")
	if err != nil {
		t.Fatalf("SetByFinish: %v", err)
	}
	if len(rows) != 1 || rows[0].Quantity != 3 || rows[0].Value != 90 {
		t.Fatalf("mh2 rows = %+v, want one row with both binders' copies summed", rows)
	}
}

func TestFoilTreatment(t *testing.T) {
	ns := func(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
	for in, want := range map[string]string{
		`["ripplefoil"]`:                "ripple",
		`["ripplefoil","boosterfun"]`:   "ripple",
		`["boosterfun","ripplefoil"]`:   "ripple",
		`["surgefoil"]`:                 "surge",
		`["boosterfun"]`:                "",
		`["universesbeyond","romance"]`: "",
		`[]`:                            "",
	} {
		if got := FoilTreatment(ns(in)); got != want {
			t.Errorf("FoilTreatment(%s) = %q, want %q", in, got, want)
		}
	}
	if got := FoilTreatment(sql.NullString{}); got != "" {
		t.Errorf("FoilTreatment(NULL) = %q, want empty", got)
	}
}

func TestFoilTreatmentDerivesFromTheSuffix(t *testing.T) {
	ns := func(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }
	for in, want := range map[string]string{

		`["silverfoil"]`:       "silver",
		`["fracturefoil"]`:     "fracture",
		`["manafoil"]`:         "mana",
		`["dragonscalefoil"]`:  "dragonscale",
		`["singularityfoil"]`:  "singularity",
		`["textured"]`:         "textured",
		`["embossed"]`:         "embossed",
		`["firstplacefoil"]`:   "1st place",
		`["chocobotrackfoil"]`: "chocobo",

		`["galaxyfoil"]`:      "galaxy",
		`["halofoil"]`:        "halo",
		`["confettifoil"]`:    "confetti",
		`["rainbowfoil"]`:     "rainbow",
		`["neonink"]`:         "neon",
		`["oilslick"]`:        "oilslick",
		`["gilded"]`:          "gilded",
		`["doublerainbow"]`:   "dbl rainbow",
		`["stepandcompleat"]`: "compleat",

		`["thick"]`:                    "",
		`["serialized"]`:               "",
		`["magnified"]`:                "",
		`["plastic"]`:                  "",
		`["metal"]`:                    "",
		`["prerelease","datestamped"]`: "",
	} {
		if got := FoilTreatment(ns(in)); got != want {
			t.Errorf("FoilTreatment(%s) = %q, want %q", in, got, want)
		}
	}

	if got := FoilTreatment(ns(`["texturedfoil"]`)); got != "textured" {
		t.Errorf("FoilTreatment(texturedfoil) = %q, want the suffix rule to answer", got)
	}

	if got := FoilTreatment(ns(`["foil"]`)); got != "" {
		t.Errorf("FoilTreatment(foil) = %q, want empty", got)
	}
}

func TestTreatmentSurfacesOnRows(t *testing.T) {
	s := newTestStore(t)
	ripple := scryfall.Card{
		ID: "ec-m3c", Set: "m3c", CollectorNumber: "32", Name: "Eldrazi Confluence",
		ScryfallURL: "http://x", PriceUSD: f(11.86),
		Raw: []byte(`{"promo_types":["ripplefoil"],"released_at":"2024-06-14"}`),
	}
	if err := s.AddCardFinish(ripple, finish.Foil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	rows, err := s.AllByFinish()
	if err != nil {
		t.Fatalf("AllByFinish: %v", err)
	}
	if len(rows) != 1 || rows[0].Treatment != "ripple" {
		t.Fatalf("rows = %+v, want the ripple treatment surfaced", rows)
	}
	owned, err := s.OwnedByFinish()
	if err != nil {
		t.Fatalf("OwnedByFinish: %v", err)
	}
	if len(owned) != 1 || owned[0].Treatment != "ripple" {
		t.Fatalf("owned = %+v, want the ripple treatment surfaced", owned)
	}
	d, err := s.CardDetail("ec-m3c")
	if err != nil {
		t.Fatalf("CardDetail: %v", err)
	}
	if d.Treatment != "ripple" {
		t.Errorf("detail treatment = %q, want ripple", d.Treatment)
	}
}

func TestSetByFinishLeavesOutUncountedBinders(t *testing.T) {
	s, _ := wantsStore(t)

	rows, err := s.SetByFinish("c21")
	if err != nil {
		t.Fatalf("SetByFinish(c21): %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("SetByFinish(c21) = %v, want nothing — Sol Ring is only in the uncounted Want binder",
			collNames(rows))
	}

	rows, err = s.SetByFinish("uma")
	if err != nil {
		t.Fatalf("SetByFinish(uma): %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "Ulamog, the Infinite Gyre" {
		t.Fatalf("SetByFinish(uma) = %v, want just Ulamog", collNames(rows))
	}
	if rows[0].Quantity != 2 || rows[0].Value != 20 {
		t.Errorf("Ulamog = %d copies worth %v, want 2 worth 20",
			rows[0].Quantity, rows[0].Value)
	}
}

func TestSetUnownedListsWantedAndNeverHeldPrintings(t *testing.T) {
	s, _ := wantsStore(t)

	missing := solRing()
	missing.ID, missing.CollectorNumber, missing.Name = "arcane-id", "2", "Arcane Signet"
	missing.PriceUSD = f(1)
	if err := s.UpsertPrintings([]scryfall.Card{missing}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}

	rows, err := s.SetUnowned("c21")
	if err != nil {
		t.Fatalf("SetUnowned(c21): %v", err)
	}
	byName := map[string]UnownedRow{}
	for _, r := range rows {
		byName[r.Name] = r
	}
	if len(byName) != 2 {
		t.Fatalf("SetUnowned(c21) = %v, want Sol Ring (wanted) and Arcane Signet (never held)",
			unownedNames(rows))
	}
	if got := byName["Sol Ring"].Where; got != "Want" {
		t.Errorf("Sol Ring is waiting in %q, want %q named on the row", got, "Want")
	}
	if got := byName["Arcane Signet"].Where; got != "" {
		t.Errorf("Arcane Signet names %q, want no list — hoard knows the printing, nobody listed it", got)
	}
	if got := byName["Sol Ring"].Quantity; got != 3 {
		t.Errorf("Sol Ring = %d copies, want the 3 waiting in the Want binder", got)
	}
	if got := byName["Arcane Signet"].Quantity; got != 0 {
		t.Errorf("Arcane Signet = %d copies, want 0 — hoard knows the printing, you hold none", got)
	}

	rows, err = s.SetUnowned("uma")
	if err != nil {
		t.Fatalf("SetUnowned(uma): %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("SetUnowned(uma) = %v, want nothing — the only uma printing is owned",
			unownedNames(rows))
	}
}

func TestSetByFinishCountsDeckCardsAsOwned(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.UpsertPrintings([]scryfall.Card{solRing()}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	if _, err := s.UpsertDeck(
		DeckMeta{Name: "Eldrazi", Source: "manual", SourceID: "deck:eldrazi"},
		[]Entry{{ScryfallID: "ulamog-id", Finish: finish.Nonfoil, Board: BoardMain, Quantity: 1}},
	); err != nil {
		t.Fatalf("UpsertDeck(Eldrazi): %v", err)
	}
	borrowed, err := s.UpsertDeck(
		DeckMeta{Name: "Borrowed", Source: "manual", SourceID: "deck:borrowed"},
		[]Entry{{ScryfallID: "sol-id", Finish: finish.Nonfoil, Board: BoardMain, Quantity: 4}},
	)
	if err != nil {
		t.Fatalf("UpsertDeck(Borrowed): %v", err)
	}
	if err := s.SetContainerCounted(borrowed, false); err != nil {
		t.Fatalf("SetContainerCounted: %v", err)
	}

	rows, err := s.SetByFinish("uma")
	if err != nil {
		t.Fatalf("SetByFinish(uma): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("SetByFinish(uma) = %v, want the one Ulamog printing", collNames(rows))
	}
	if rows[0].Quantity != 3 || rows[0].Value != 30 {
		t.Errorf("Ulamog = %d copies worth %v, want 3 worth 30 — 2 in the binder plus 1 in the deck",
			rows[0].Quantity, rows[0].Value)
	}

	rows, err = s.SetByFinish("c21")
	if err != nil {
		t.Fatalf("SetByFinish(c21): %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("SetByFinish(c21) = %v, want nothing — the Borrowed deck is not counted",
			collNames(rows))
	}

	missing, err := s.SetUnowned("c21")
	if err != nil {
		t.Fatalf("SetUnowned(c21): %v", err)
	}
	if len(missing) != 1 || missing[0].Name != "Sol Ring" {
		t.Fatalf("SetUnowned(c21) = %v, want Sol Ring — borrowed is not owned",
			unownedNames(missing))
	}
	if missing[0].Quantity != 4 {
		t.Errorf("Sol Ring = %d copies, want the 4 sitting in the uncounted deck", missing[0].Quantity)
	}
	if missing[0].Where != "Borrowed" {
		t.Errorf("Sol Ring is waiting in %q, want the deck %q named — a list is a list",
			missing[0].Where, "Borrowed")
	}

	if missing, err = s.SetUnowned("uma"); err != nil {
		t.Fatalf("SetUnowned(uma): %v", err)
	} else if len(missing) != 0 {
		t.Errorf("SetUnowned(uma) = %v, want nothing — the deck copy makes it owned",
			unownedNames(missing))
	}
}

func TestSetUnownedIsPerPrintingNotPerFinish(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Foil, 1); err != nil {
		t.Fatalf("AddCardFinish(foil): %v", err)
	}
	if err := s.UpsertPrintings([]scryfall.Card{solRing()}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	want, err := s.CreateBinder("Want")
	if err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	if err := s.AddCardFinishTo(want, solRing(), finish.Nonfoil, 2); err != nil {
		t.Fatalf("AddCardFinishTo(nonfoil): %v", err)
	}
	if err := s.AddCardFinishTo(want, solRing(), finish.Foil, 1); err != nil {
		t.Fatalf("AddCardFinishTo(foil): %v", err)
	}
	if err := s.SetContainerCounted(want, false); err != nil {
		t.Fatalf("SetContainerCounted: %v", err)
	}
	loaner, err := s.UpsertDeck(
		DeckMeta{Name: "Loaner", Source: "manual", SourceID: "deck:loaner"},
		[]Entry{{ScryfallID: "sol-id", Finish: finish.Nonfoil, Board: BoardMain, Quantity: 1}},
	)
	if err != nil {
		t.Fatalf("UpsertDeck(Loaner): %v", err)
	}
	if err := s.SetContainerCounted(loaner, false); err != nil {
		t.Fatalf("SetContainerCounted(Loaner): %v", err)
	}

	rows, err := s.SetUnowned("uma")
	if err != nil {
		t.Fatalf("SetUnowned(uma): %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("SetUnowned(uma) = %v, want nothing — one foil copy owns the printing, "+
			"and the missing nonfoil is tracked on the owned view", unownedNames(rows))
	}

	owned, err := s.SetByFinish("uma")
	if err != nil {
		t.Fatalf("SetByFinish(uma): %v", err)
	}
	if len(owned) != 1 || owned[0].Finish != finish.Foil || owned[0].Quantity != 1 {
		t.Errorf("SetByFinish(uma) = %+v, want the single foil row — the owned view still splits by finish",
			owned)
	}

	rows, err = s.SetUnowned("c21")
	if err != nil {
		t.Fatalf("SetUnowned(c21): %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("SetUnowned(c21) = %v, want one row for the printing, not one per finish",
			unownedNames(rows))
	}
	if rows[0].Name != "Sol Ring" || rows[0].Quantity != 4 {
		t.Errorf("Sol Ring = %d copies, want all 4 — both finishes in Want plus the Loaner deck",
			rows[0].Quantity)
	}
	for _, list := range []string{"Want", "Loaner"} {
		if !strings.Contains(rows[0].Where, list) {
			t.Errorf("Sol Ring is waiting in %q, want %q named too", rows[0].Where, list)
		}
	}
}

func unownedNames(rows []UnownedRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out
}
