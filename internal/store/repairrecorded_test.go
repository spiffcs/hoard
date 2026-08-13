package store

import (
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// priceUlamog restates the card at a new non-foil price, the way a refresh
// does.
func priceUlamog(t *testing.T, s *Store, usd float64) {
	t.Helper()
	c := ulamog()
	c.PriceUSD = f(usd)
	if err := s.UpsertPrintings([]scryfall.Card{c}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
}

func refuse(t *testing.T, s *Store, price, refused float64) {
	t.Helper()
	if err := s.ReplacePriceOverrides([]PriceOverride{{
		ScryfallID: "ulamog-id", Finish: "nonfoil", Price: price, Refused: refused,
		Source: "tcgplayer-low", Reason: "below-cheapest-ask",
	}}, []string{"ulamog-id"}); err != nil {
		t.Fatalf("ReplacePriceOverrides: %v", err)
	}
}

// seriesFor reads one card-and-finish's whole recorded series, oldest first.
func seriesFor(t *testing.T, s *Store, finish string) []PricePoint {
	t.Helper()
	got, err := s.PriceSeries("ulamog-id", finish)
	if err != nil {
		t.Fatalf("PriceSeries: %v", err)
	}
	return got
}

// The case the repair exists for: a price recorded before the sweep had a
// chance to refuse it. The degenerate figure must not be left in the series —
// it would show in movers as a crash the card never had.
func TestRepairRewritesARefusedObservation(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 2); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil { // baseline at $10
		t.Fatal(err)
	}
	backdate(t, s, stamps(t, s)[0], "2026-08-01T00:00:00Z")

	// The feed goes degenerate and the recording happens first.
	priceUlamog(t, s, 0.56)
	if _, err := s.RecordPrices(); err != nil {
		t.Fatal(err)
	}
	if got := seriesFor(t, s, "nonfoil"); len(got) != 2 || got[1].Price != 0.56 {
		t.Fatalf("series before the repair = %+v, want the degenerate figure recorded", got)
	}

	// The sweep catches up.
	refuse(t, s, 97.55, 0.56)
	moved, repaired, err := s.RepairRecordedPrices()
	if err != nil {
		t.Fatalf("RepairRecordedPrices: %v", err)
	}
	if repaired != 1 {
		t.Errorf("repaired = %d, want 1", repaired)
	}
	got := seriesFor(t, s, "nonfoil")
	if len(got) != 2 {
		t.Fatalf("series = %+v, want two observations", got)
	}
	if got[1].Price != 97.55 || got[1].Source != "tcgplayer-low" {
		t.Errorf("newest observation = %+v, want the ask that replaced the refused figure", got[1])
	}
	if len(moved) != 1 || moved[0].New != 97.55 || moved[0].Old != 10 {
		t.Errorf("moved = %+v, want the corrected rise reported", moved)
	}
}

// The case a delete-and-upsert would get wrong, and the reason the repair
// re-decides the recording instead of patching rows: the correction lands back
// on the price that was already recorded. Nothing moved, so there must be no
// observation at all — a row here is a round trip the card never made.
func TestRepairWithdrawsAnObservationThatNoLongerMoved(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 2); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil { // baseline at $10
		t.Fatal(err)
	}
	backdate(t, s, stamps(t, s)[0], "2026-08-01T00:00:00Z")

	priceUlamog(t, s, 0.50)
	if _, err := s.RecordPrices(); err != nil {
		t.Fatal(err)
	}
	if got := seriesFor(t, s, "nonfoil"); len(got) != 2 {
		t.Fatalf("series before the repair = %+v, want the drop recorded", got)
	}

	// The ask says the card is still worth exactly what it was.
	refuse(t, s, 10.00, 0.50)
	moved, repaired, err := s.RepairRecordedPrices()
	if err != nil {
		t.Fatalf("RepairRecordedPrices: %v", err)
	}
	if repaired != 1 {
		t.Errorf("repaired = %d, want 1", repaired)
	}
	got := seriesFor(t, s, "nonfoil")
	if len(got) != 1 || got[0].Price != 10 {
		t.Errorf("series = %+v, want the baseline alone — nothing moved", got)
	}
	if len(moved) != 0 {
		t.Errorf("moved = %+v, want nothing reported as having moved", moved)
	}
}

// The other direction: a correction the previous run earned and this one does
// not, because the feed came back to its senses. The recorded figure is
// yesterday's ask and has to become the feed's own price.
func TestRepairFollowsARetiredCorrection(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 2); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	refuse(t, s, 97.55, 0.56)
	if _, err := s.RecordPrices(); err != nil { // baseline at the ask
		t.Fatal(err)
	}
	backdate(t, s, stamps(t, s)[0], "2026-08-01T00:00:00Z")

	// Today the feed is sane again, but the old correction is still in force
	// when the recording runs.
	priceUlamog(t, s, 42.00)
	if _, err := s.RecordPrices(); err != nil {
		t.Fatal(err)
	}
	if got := seriesFor(t, s, "nonfoil"); len(got) != 1 {
		t.Fatalf("series = %+v, want no movement while the override stands", got)
	}

	// The sweep finds nothing to refuse and retires the correction.
	if err := s.ReplacePriceOverrides(nil, []string{"ulamog-id"}); err != nil {
		t.Fatalf("ReplacePriceOverrides: %v", err)
	}
	_, repaired, err := s.RepairRecordedPrices()
	if err != nil {
		t.Fatalf("RepairRecordedPrices: %v", err)
	}
	if repaired != 1 {
		t.Errorf("repaired = %d, want 1", repaired)
	}
	got := seriesFor(t, s, "nonfoil")
	if len(got) != 2 || got[1].Price != 42 || got[1].Source != "scryfall" {
		t.Errorf("series = %+v, want the feed's own price recorded once the ask retired", got)
	}
}

// The ordinary run, which is nearly every run: the sweep refuses nothing and
// retires nothing, and the repair must leave the recording exactly as it found
// it. A repair that rewrote rows on a quiet day would churn every series.
func TestRepairLeavesAnUnchangedRecordingAlone(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 2); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatal(err)
	}
	backdate(t, s, stamps(t, s)[0], "2026-08-01T00:00:00Z")
	priceUlamog(t, s, 11.25)
	if _, err := s.RecordPrices(); err != nil {
		t.Fatal(err)
	}
	before := seriesFor(t, s, "nonfoil")

	moved, repaired, err := s.RepairRecordedPrices()
	if err != nil {
		t.Fatalf("RepairRecordedPrices: %v", err)
	}
	if repaired != 0 {
		t.Errorf("repaired = %d on a run that corrected nothing, want 0", repaired)
	}
	after := seriesFor(t, s, "nonfoil")
	if len(after) != len(before) {
		t.Fatalf("series went from %d to %d observations", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Errorf("observation %d changed: %+v -> %+v", i, before[i], after[i])
		}
	}
	// What moved is still reported, so a caller that prints movers after a
	// quiet sweep prints the same list the recording did.
	if len(moved) != 1 || moved[0].New != 11.25 || moved[0].Old != 10 {
		t.Errorf("moved = %+v, want the recording's own movement", moved)
	}
}

// The value snapshot rides the same instant as the observations and is computed
// from the same prices, so a repair that fixed the series and left the total
// standing would leave the chart asserting a number no card supports.
func TestRepairRebuildsTheValueSnapshot(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 2); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	priceUlamog(t, s, 0.56)
	if _, err := s.RecordPrices(); err != nil {
		t.Fatal(err)
	}
	snaps, err := s.ValueSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(snaps); n != 1 || snaps[0].Total != 1.12 {
		t.Fatalf("snapshots = %+v, want one totalling the degenerate price", snaps)
	}

	refuse(t, s, 97.55, 0.56)
	if _, _, err := s.RepairRecordedPrices(); err != nil {
		t.Fatalf("RepairRecordedPrices: %v", err)
	}
	snaps, err = s.ValueSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 {
		t.Fatalf("snapshots = %+v, want the point rebuilt in place, not a second one", snaps)
	}
	if snaps[0].Total != 195.10 {
		t.Errorf("total = %v, want 2 × the corrected 97.55", snaps[0].Total)
	}
}

// A store that has never recorded has no instant to repair. It must say so
// quietly rather than inventing one — the browser runs this after every price
// refresh, including the first.
func TestRepairOnAStoreThatNeverRecorded(t *testing.T) {
	s := newTestStore(t)
	moved, repaired, err := s.RepairRecordedPrices()
	if err != nil {
		t.Fatalf("RepairRecordedPrices: %v", err)
	}
	if len(moved) != 0 || repaired != 0 {
		t.Errorf("moved = %+v, repaired = %d; want nothing", moved, repaired)
	}
}
