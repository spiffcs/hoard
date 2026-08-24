package store

import (
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

const (
	testNow   = "2026-08-10T21:00:00Z"
	longAgo   = "2026-06-01T00:00:00Z"
	beforeNow = "2026-08-09T00:00:00Z"
)

const fixtureSource = "scryfall"

func observe(t *testing.T, s *Store, fin finish.Finish, price float64, asOf string, source ...string) {
	t.Helper()
	src := fixtureSource
	if len(source) > 0 {
		src = source[0]
	}
	if _, err := s.db.Exec(
		`INSERT INTO card_price_history (scryfall_id, finish, price_usd, source, as_of)
		 VALUES ('ulamog-id', ?, ?, ?, ?)`, fin, price, src, asOf); err != nil {
		t.Fatalf("seeding history at %s: %v", asOf, err)
	}

	if src == fixtureSource {
		priceIs(t, s, fin, price)
	}
}

func priceIs(t *testing.T, s *Store, fin finish.Finish, price float64) {
	t.Helper()
	c := ulamog()
	if fin == finish.Foil {
		c.PriceUSDFoil = &price
	} else {
		c.PriceUSD = &price
	}
	if err := s.UpsertPrintings([]scryfall.Card{c}); err != nil {
		t.Fatalf("setting the price: %v", err)
	}
}

func percentWatch(t *testing.T, s *Store, fin finish.Finish, op string, pct, minMove float64) {
	t.Helper()
	if err := s.AddCardFinish(ulamog(), fin, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddWatchInput(WatchInput{
		ScryfallID: "ulamog-id", Display: "Ulamog", Finish: fin,
		Op: op, Pct: pct, MinMove: minMove, WindowDays: DefaultWindowDays,
	}); err != nil {
		t.Fatalf("AddWatchInput: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE watches SET created_at = '2026-01-01T00:00:00Z'`); err != nil {
		t.Fatalf("backdating the watch: %v", err)
	}
}

func watchAt(t *testing.T, s *Store, at string) WatchStatus {
	t.Helper()
	all, err := s.listWatchesAt(at)
	if err != nil {
		t.Fatalf("listWatchesAt: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("watches = %d, want 1", len(all))
	}
	return all[0]
}

func checkAt(t *testing.T, s *Store, at string) []WatchStatus {
	t.Helper()
	fired, _, err := s.checkWatchesAt(at)
	if err != nil {
		t.Fatalf("checkWatchesAt %s: %v", at, err)
	}
	return fired
}

func TestPercentDropAnchorsOnTheWindowHigh(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, finish.Nonfoil, "drop", 0.10, 0)
	observe(t, s, finish.Nonfoil, 30.00, longAgo)
	observe(t, s, finish.Nonfoil, 38.43, "2026-07-25T00:00:00Z")
	observe(t, s, finish.Nonfoil, 34.57, beforeNow)

	w := watchAt(t, s, testNow)
	if w.Anchor == nil || *w.Anchor != 38.43 {
		t.Fatalf("anchor = %v, want the window high 38.43", w.Anchor)
	}
	if w.AnchorAt != "2026-07-25T00:00:00Z" {
		t.Errorf("anchorAt = %q, want the moment the high was observed", w.AnchorAt)
	}
	if !w.Met() {
		t.Errorf("a 10.1%% fall from the high did not meet a 10%% drop watch: %+v", w)
	}

	if w.PriceUSD == nil || *w.PriceUSD != 34.57 {
		t.Errorf("price = %v, want hoard's effective price", w.PriceUSD)
	}
}

func TestPercentRiseAnchorsOnTheWindowLow(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, finish.Nonfoil, "rise", 0.10, 0)
	observe(t, s, finish.Nonfoil, 60.00, longAgo)
	observe(t, s, finish.Nonfoil, 46.54, "2026-07-25T00:00:00Z")
	observe(t, s, finish.Nonfoil, 51.96, beforeNow)

	w := watchAt(t, s, testNow)
	if w.Anchor == nil || *w.Anchor != 46.54 {
		t.Fatalf("anchor = %v, want the window low 46.54", w.Anchor)
	}
	if !w.Met() {
		t.Errorf("an 11.6%% rise from the low did not meet a 10%% rise watch: %+v", w)
	}
}

func TestPercentWindowBoundIsRFC3339(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, finish.Nonfoil, "drop", 0.10, 0)
	observe(t, s, finish.Nonfoil, 50.00, longAgo)
	observe(t, s, finish.Nonfoil, 100.00, "2026-07-11T05:00:00Z")
	observe(t, s, finish.Nonfoil, 50.00, "2026-07-11T20:00:00Z")
	observe(t, s, finish.Nonfoil, 47.00, beforeNow)

	w := watchAt(t, s, testNow)
	if w.Anchor == nil {
		t.Fatal("no anchor")
	}
	if *w.Anchor != 50.00 {
		t.Fatalf("anchor = %.2f, want 50.00 — the cutoff day leaked into the window", *w.Anchor)
	}
	if w.Met() {
		t.Error("a 6% fall fired a 10% drop watch: the bound admitted a pre-window spike")
	}

	var rfc, dt int
	if err := s.db.QueryRow(`
SELECT (SELECT COUNT(*) FROM card_price_history
         WHERE as_of >= strftime('%Y-%m-%dT%H:%M:%SZ', ?, '-30 days')),
       (SELECT COUNT(*) FROM card_price_history
         WHERE as_of >= datetime(?, '-30 days'))`, testNow, testNow).Scan(&rfc, &dt); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if rfc != 1 || dt != 3 {
		t.Errorf("rows admitted: rfc3339 %d, datetime %d — want 1 and 3", rfc, dt)
	}
}

func TestPercentFiringReAnchors(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, finish.Nonfoil, "drop", 0.10, 0)
	observe(t, s, finish.Nonfoil, 38.00, longAgo)
	observe(t, s, finish.Nonfoil, 38.43, "2026-07-20T00:00:00Z")
	observe(t, s, finish.Nonfoil, 34.57, "2026-07-25T00:00:00Z")

	fired := checkAt(t, s, "2026-07-25T01:00:00Z")
	if len(fired) != 1 {
		t.Fatalf("the slide fired %d alerts, want 1", len(fired))
	}
	if fired[0].Anchor == nil || *fired[0].Anchor != 38.43 {
		t.Errorf("the alert reported anchor %v, want 38.43", fired[0].Anchor)
	}

	observe(t, s, finish.Nonfoil, 36.50, "2026-07-26T00:00:00Z")
	if fired := checkAt(t, s, "2026-07-26T01:00:00Z"); len(fired) != 0 {
		t.Fatalf("the bounce fired %d alerts, want 0", len(fired))
	}
	observe(t, s, finish.Nonfoil, 34.41, "2026-07-27T00:00:00Z")
	if fired := checkAt(t, s, "2026-07-27T01:00:00Z"); len(fired) != 0 {
		t.Fatalf("the same slide fired again (%d alerts): the anchor did not move with it", len(fired))
	}

	observe(t, s, finish.Nonfoil, 30.50, "2026-07-28T00:00:00Z")
	if fired := checkAt(t, s, "2026-07-28T01:00:00Z"); len(fired) != 1 {
		t.Fatalf("a further 16%% fall fired %d alerts, want 1", len(fired))
	}
}

func TestWouldFireLeavesTheAnchorAlone(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, finish.Nonfoil, "drop", 0.10, 0)
	observe(t, s, finish.Nonfoil, 38.00, longAgo)
	observe(t, s, finish.Nonfoil, 38.43, "2026-07-20T00:00:00Z")
	observe(t, s, finish.Nonfoil, 34.57, "2026-07-25T00:00:00Z")

	for i := range 2 {
		would, err := s.wouldFireAt(testNow)
		if err != nil {
			t.Fatalf("WouldFire: %v", err)
		}
		if len(would) != 1 {
			t.Fatalf("preview %d returned %d watches, want 1", i, len(would))
		}
	}
	var lastFired string
	if err := s.db.QueryRow(`SELECT last_fired_at FROM watches`).Scan(&lastFired); err != nil {
		t.Fatalf("reading last_fired_at: %v", err)
	}
	if lastFired != "" {
		t.Errorf("last_fired_at = %q after two previews, want it unwritten", lastFired)
	}
	if fired := checkAt(t, s, testNow); len(fired) != 1 {
		t.Error("the preview consumed the alert a cron depends on")
	}
}

func TestPercentWaitsOnAYoungSeries(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, finish.Nonfoil, "drop", 0.10, 0)
	observe(t, s, finish.Nonfoil, 50.00, "2026-08-04T00:00:00Z")
	observe(t, s, finish.Nonfoil, 40.00, beforeNow)

	w := watchAt(t, s, testNow)
	if !w.WaitingOnHistory() {
		t.Fatalf("a 7-day-old series did not read as waiting on history: %+v", w)
	}
	if w.Met() {
		t.Error("a 20% fall fired on a series younger than its own window")
	}
	if fired := checkAt(t, s, testNow); len(fired) != 0 {
		t.Errorf("the check fired %d alerts on a series too young to have a high", len(fired))
	}

	observe(t, s, finish.Nonfoil, 50.00, longAgo)
	priceIs(t, s, finish.Nonfoil, 40.00)
	w = watchAt(t, s, testNow)
	if w.WaitingOnHistory() {
		t.Fatalf("a series older than the window still read as waiting: %+v", w)
	}
	if !w.Met() {
		t.Errorf("the 20%% fall did not meet the watch once the record reached back: %+v", w)
	}
}

func TestPercentCarriesTheStablePriceIntoTheWindow(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, finish.Nonfoil, "drop", 0.10, 0)
	observe(t, s, finish.Nonfoil, 132.14, "2026-05-12T00:00:00Z")

	w := watchAt(t, s, testNow)
	if w.Anchor == nil || *w.Anchor != 132.14 {
		t.Fatalf("anchor = %v, want the price still in effect (132.14)", w.Anchor)
	}
	if w.WaitingOnHistory() {
		t.Error("a 90-day-old record read as too young: stable is not thin")
	}
	if w.Met() {
		t.Error("a flat price met a drop watch")
	}

	observe(t, s, finish.Nonfoil, 110.00, beforeNow)
	w = watchAt(t, s, testNow)
	if w.Anchor == nil || *w.Anchor != 132.14 {
		t.Fatalf("anchor = %v, want 132.14 — the window forgot the price it opened at", w.Anchor)
	}
	if !w.Met() {
		t.Error("the fall from a long-stable price was lost")
	}
}

func TestPercentAnchorIgnoresOtherSeries(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, finish.Foil, "drop", 0.10, 0)
	observe(t, s, finish.Foil, 100.00, longAgo)
	observe(t, s, finish.Nonfoil, 900.00, "2026-07-21T00:00:00Z")
	observe(t, s, finish.Foil, 800.00, "2026-07-22T00:00:00Z", "cardkingdom")
	observe(t, s, finish.Foil, 95.00, beforeNow)

	w := watchAt(t, s, testNow)
	if w.Anchor == nil || *w.Anchor != 100.00 {
		t.Fatalf("anchor = %v, want 100.00 from the foil %s series alone", w.Anchor, fixtureSource)
	}
	if w.Met() {
		t.Error("a 5% fall fired: the anchor read a series the watch did not name")
	}
}

func TestPercentMinMoveSuppresses(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, finish.Nonfoil, "drop", 0.10, DefaultMinMove)
	observe(t, s, finish.Nonfoil, 1.00, longAgo)
	observe(t, s, finish.Nonfoil, 0.80, beforeNow)

	if w := watchAt(t, s, testNow); w.Met() {
		t.Error("a twenty cent move fired a watch with a twenty-five cent floor")
	}

	if _, err := s.db.Exec(`DELETE FROM card_price_history`); err != nil {
		t.Fatalf("clearing: %v", err)
	}
	observe(t, s, finish.Nonfoil, 40.00, longAgo)
	observe(t, s, finish.Nonfoil, 32.00, beforeNow)
	if w := watchAt(t, s, testNow); !w.Met() {
		t.Error("an eight dollar move was suppressed by a twenty-five cent floor")
	}
}

func TestAbsoluteWatchesAreUnchanged(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddWatch("ulamog-id", "Ulamog", finish.Nonfoil, "under", 12); err != nil {
		t.Fatalf("AddWatch: %v", err)
	}
	observe(t, s, finish.Nonfoil, 999.00, longAgo)
	priceIs(t, s, finish.Nonfoil, 10.00)

	w := watchAt(t, s, testNow)
	if w.Anchor != nil {
		t.Errorf("anchor = %v on an absolute watch, want none", *w.Anchor)
	}
	if w.WaitingOnHistory() {
		t.Error("an absolute watch read as waiting on history")
	}
	if w.Pct != 0 || w.WindowDays != DefaultWindowDays {
		t.Errorf("pct = %v, window = %d — want the inert defaults", w.Pct, w.WindowDays)
	}

	if w.PriceUSD == nil || *w.PriceUSD != 10.00 {
		t.Errorf("price = %v, want the effective price 10.00", w.PriceUSD)
	}
	if !w.Met() {
		t.Error("the absolute branch stopped working")
	}
	if got := w.Rule(); got != "under $12.00" {
		t.Errorf("Rule() = %q, want the dollar phrasing", got)
	}
}

func TestPercentAndAbsoluteCoexist(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, finish.Nonfoil, "drop", 0.10, 0)
	if err := s.AddWatch("ulamog-id", "Ulamog", finish.Nonfoil, "over", 149.86); err != nil {
		t.Fatalf("AddWatch over: %v", err)
	}
	all, err := s.listWatchesAt(testNow)
	if err != nil {
		t.Fatalf("listWatchesAt: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("watches = %d, want the percent and the absolute one side by side", len(all))
	}
	rules := []string{all[0].Rule(), all[1].Rule()}
	if !strings.Contains(strings.Join(rules, " "), "drop 10%") ||
		!strings.Contains(strings.Join(rules, " "), "over $149.86") {
		t.Errorf("rules = %v, want both questions kept whole", rules)
	}
}

func TestPercentValidationRefusesCrossedUnits(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	for _, tc := range []struct {
		name string
		in   WatchInput
		want string
	}{
		{"dollars on a movement", WatchInput{Op: "drop", Pct: 0.1, Threshold: 30}, "takes no dollar threshold"},
		{"a percentage on a line", WatchInput{Op: "under", Threshold: 30, Pct: 0.1}, "takes no percentage"},
		{"a movement with no size", WatchInput{Op: "rise"}, "needs a percentage"},
		{"a percent mistaken for a fraction", WatchInput{Op: "drop", Pct: 10}, "needs a percentage"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.in
			in.ScryfallID, in.Display, in.Finish = "ulamog-id", "Ulamog", finish.Nonfoil
			err := s.AddWatchInput(in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

func TestReAddingAPercentWatchClearsTheFireMoment(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, finish.Nonfoil, "drop", 0.10, 0)
	observe(t, s, finish.Nonfoil, 38.00, longAgo)
	observe(t, s, finish.Nonfoil, 38.43, "2026-07-20T00:00:00Z")
	observe(t, s, finish.Nonfoil, 34.57, "2026-07-25T00:00:00Z")
	if fired := checkAt(t, s, "2026-07-25T01:00:00Z"); len(fired) != 1 {
		t.Fatalf("setup: fired %d, want 1", len(fired))
	}

	percentWatch(t, s, finish.Nonfoil, "drop", 0.20, 0)
	var lastFired, lastState string
	if err := s.db.QueryRow(`SELECT last_fired_at, last_state FROM watches`).
		Scan(&lastFired, &lastState); err != nil {
		t.Fatalf("reading the re-added row: %v", err)
	}
	if lastFired != "" || lastState != "" {
		t.Errorf("last_fired_at = %q, last_state = %q — want a re-added watch re-armed",
			lastFired, lastState)
	}
}

func TestRecordPricesSkipsUnchangedPrices(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	countRows := func() int {
		t.Helper()
		var n int
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM card_price_history WHERE finish = 'nonfoil'`).Scan(&n); err != nil {
			t.Fatalf("counting history: %v", err)
		}
		return n
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}
	first := countRows()
	if first != 1 {
		t.Fatalf("the first observation wrote %d rows, want 1", first)
	}

	for range 3 {
		if _, err := s.db.Exec(`UPDATE card_price_history
			SET as_of = strftime('%Y-%m-%dT%H:%M:%SZ', as_of, '-1 day')`); err != nil {
			t.Fatalf("walking the observations back: %v", err)
		}
		if _, err := s.RecordPrices(); err != nil {
			t.Fatalf("RecordPrices: %v", err)
		}
		if got := countRows(); got != first {
			t.Fatalf("a refresh at an unchanged price wrote %d rows, want %d — "+
				"history is a change log and every percent watch depends on it", got, first)
		}
	}

	c := ulamog()
	c.PriceUSD = f(11.00)
	if err := s.UpsertPrintings([]scryfall.Card{c}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}
	if got := countRows(); got != first+1 {
		t.Fatalf("a changed price wrote %d rows, want %d", got, first+1)
	}
}

func TestPercentReArmsOnFiringAlone(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, finish.Nonfoil, "drop", 0.10, 0)
	observe(t, s, finish.Nonfoil, 50.00, longAgo)
	observe(t, s, finish.Nonfoil, 40.00, beforeNow)

	if fired := checkAt(t, s, testNow); len(fired) != 1 {
		t.Fatalf("the fall fired %d alerts, want 1", len(fired))
	}
	if w := watchAt(t, s, testNow); w.LastState != "met" || w.LastFiredAt == "" {
		t.Fatalf("after firing: last_state=%q last_fired_at=%q", w.LastState, w.LastFiredAt)
	}

	if fired := checkAt(t, s, testNow); len(fired) != 0 {
		t.Fatalf("the second check fired %d alerts, want 0", len(fired))
	}
	w := watchAt(t, s, testNow)
	if w.State() != "waiting" {
		t.Errorf("state = %q with no price change, want waiting: firing re-anchors", w.State())
	}
	if w.Anchor == nil || *w.Anchor != 40.00 {
		t.Errorf("anchor = %v, want it collapsed to the price that fired (40.00)", w.Anchor)
	}
	if w.LastState != "unmet" {
		t.Errorf("last_state = %q, want unmet — re-armed with no crossing back", w.LastState)
	}

	observe(t, s, finish.Nonfoil, 35.00, "2026-08-10T23:00:00Z")
	if fired := checkAt(t, s, "2026-08-11T00:00:00Z"); len(fired) != 1 {
		t.Error("a further 12.5% fall did not fire: the watch did not re-arm")
	}
}

func TestAbsoluteDoesNotReArmOnFiringAlone(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	priceIs(t, s, finish.Nonfoil, 40.00)
	if err := s.AddWatch("ulamog-id", "Ulamog", finish.Nonfoil, "under", 45); err != nil {
		t.Fatalf("AddWatch: %v", err)
	}
	if fired := checkAt(t, s, testNow); len(fired) != 1 {
		t.Fatalf("the first check fired %d, want 1", len(fired))
	}
	if fired := checkAt(t, s, testNow); len(fired) != 0 {
		t.Fatalf("the second check fired %d, want 0", len(fired))
	}
	if w := watchAt(t, s, testNow); w.State() != "met" || w.LastState != "met" {
		t.Errorf("state=%q last_state=%q, want an absolute watch still latched at met",
			w.State(), w.LastState)
	}
}
