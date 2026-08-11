package store

import (
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// The present these tests measure against, and a date comfortably before every
// window they open, so a series can be made to reach back past its own window
// the way a real printing's record does.
const (
	testNow   = "2026-08-10T21:00:00Z"
	longAgo   = "2026-06-01T00:00:00Z"
	beforeNow = "2026-08-09T00:00:00Z"
)

// fixtureSource is the vendor a percent watch on the shared fixture card
// anchors on: ulamog() carries Scryfall prices, so that is where its effective
// price comes from, and the anchor follows the effective price.
const fixtureSource = "scryfall"

// observe writes one price observation straight into history, which is what
// update-prices leaves behind. The source is the anchored one unless a test is
// specifically about a vendor the anchor should ignore.
func observe(t *testing.T, s *Store, finish string, price float64, asOf string, source ...string) {
	t.Helper()
	src := fixtureSource
	if len(source) > 0 {
		src = source[0]
	}
	if _, err := s.db.Exec(
		`INSERT INTO card_price_history (scryfall_id, finish, price_usd, source, as_of)
		 VALUES ('ulamog-id', ?, ?, ?, ?)`, finish, price, src, asOf); err != nil {
		t.Fatalf("seeding history at %s: %v", asOf, err)
	}
	// The catalog moves with it. A row exists in history *because* the
	// effective price took that value — RecordPrices writes one from the other —
	// so a fixture where the two disagree is not a state hoard can reach, and a
	// percent watch compares the catalog's price against the series' extreme.
	if src == fixtureSource {
		priceIs(t, s, finish, price)
	}
}

// priceIs sets the catalog's effective price for one finish, for the tests that
// have to say what the price is now independently of the order rows were
// seeded in.
func priceIs(t *testing.T, s *Store, finish string, price float64) {
	t.Helper()
	c := ulamog()
	if finish == "foil" {
		c.PriceUSDFoil = &price
	} else {
		c.PriceUSD = &price
	}
	if err := s.UpsertPrintings([]scryfall.Card{c}); err != nil {
		t.Fatalf("setting the price: %v", err)
	}
}

// percentWatch stands one movement watch on the shared fixture card, backdated
// so the watch's own creation is never the binding lower bound unless a test
// says so.
func percentWatch(t *testing.T, s *Store, finish, op string, pct, minMove float64) {
	t.Helper()
	if err := s.AddCardFinish(ulamog(), finish, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddWatchInput(WatchInput{
		ScryfallID: "ulamog-id", Display: "Ulamog", Finish: finish,
		Op: op, Pct: pct, MinMove: minMove, WindowDays: DefaultWindowDays,
	}); err != nil {
		t.Fatalf("AddWatchInput: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE watches SET created_at = '2026-01-01T00:00:00Z'`); err != nil {
		t.Fatalf("backdating the watch: %v", err)
	}
}

// watchAt reads the one watch as of a named present.
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

// A trailing drop measures from the window's high, not from the first price it
// ever saw: that difference is the whole feature.
func TestPercentDropAnchorsOnTheWindowHigh(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, "nonfoil", "drop", 0.10, 0)
	observe(t, s, "nonfoil", 30.00, longAgo)                // the record reaches back
	observe(t, s, "nonfoil", 38.43, "2026-07-25T00:00:00Z") // the high
	observe(t, s, "nonfoil", 34.57, beforeNow)              // -10.1% off it

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
	// A pinned anchor would have been the 30.00 and answered "up 15%".
	if w.PriceUSD == nil || *w.PriceUSD != 34.57 {
		t.Errorf("price = %v, want hoard's effective price", w.PriceUSD)
	}
}

// A rise anchors on the window's low, from the same series and the same rules
// read the other way up.
func TestPercentRiseAnchorsOnTheWindowLow(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, "nonfoil", "rise", 0.10, 0)
	observe(t, s, "nonfoil", 60.00, longAgo)
	observe(t, s, "nonfoil", 46.54, "2026-07-25T00:00:00Z") // the low
	observe(t, s, "nonfoil", 51.96, beforeNow)              // +11.6% off it

	w := watchAt(t, s, testNow)
	if w.Anchor == nil || *w.Anchor != 46.54 {
		t.Fatalf("anchor = %v, want the window low 46.54", w.Anchor)
	}
	if !w.Met() {
		t.Errorf("an 11.6%% rise from the low did not meet a 10%% rise watch: %+v", w)
	}
}

// NEGATIVE CONTROL: the window's lower bound must be RFC 3339.
//
// datetime(now,'-30 days') renders the same instant as "2026-07-11 21:00:00" —
// space separator, no Z — while as_of is "2026-07-11T05:00:00Z". SQLite
// compares them as text and 'T' (0x54) sorts above ' ' (0x20), so a bound that
// means to exclude most of the cutoff day admits all of it.
//
// The spike below sits in exactly that one day of slop. With the correct bound
// the anchor is the $50 the price stood at when the window opened and a 6%
// fall says nothing; under a datetime() bound the anchor becomes the $100
// spike and the watch reports a 53% collapse that never happened.
func TestPercentWindowBoundIsRFC3339(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, "nonfoil", "drop", 0.10, 0)
	observe(t, s, "nonfoil", 50.00, longAgo)
	observe(t, s, "nonfoil", 100.00, "2026-07-11T05:00:00Z") // before the cutoff, same day
	observe(t, s, "nonfoil", 50.00, "2026-07-11T20:00:00Z")  // the price the window opens at
	observe(t, s, "nonfoil", 47.00, beforeNow)               // -6% from 50, -53% from 100

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

	// The trap itself, so the reason this test exists cannot be argued away:
	// the two spellings of one instant do not select the same rows.
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

// NEGATIVE CONTROL: firing re-anchors, so one slide is one alert.
//
// Prismatic Vista's real shape on the owner's database: a fall through the
// line, a bounce back over it, then a further fall. Crossing semantics alone
// fire twice, because the anchor does not move when the alert does.
// last_fired_at as a third lower bound collapses it to the one alert a person
// would want, and leaves the watch armed at the new level.
func TestPercentFiringReAnchors(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, "nonfoil", "drop", 0.10, 0)
	observe(t, s, "nonfoil", 38.00, longAgo)
	observe(t, s, "nonfoil", 38.43, "2026-07-20T00:00:00Z")
	observe(t, s, "nonfoil", 34.57, "2026-07-25T00:00:00Z") // -10.0%: the alert

	fired := checkAt(t, s, "2026-07-25T01:00:00Z")
	if len(fired) != 1 {
		t.Fatalf("the slide fired %d alerts, want 1", len(fired))
	}
	if fired[0].Anchor == nil || *fired[0].Anchor != 38.43 {
		t.Errorf("the alert reported anchor %v, want 38.43", fired[0].Anchor)
	}

	// The bounce un-mets the watch; the further fall is 0.5% below the price
	// that fired. A *further* ten percent would be worth hearing — this is the
	// same slide, and without the re-anchor the old 38.43 is still in the
	// window and both of these fire.
	observe(t, s, "nonfoil", 36.50, "2026-07-26T00:00:00Z")
	if fired := checkAt(t, s, "2026-07-26T01:00:00Z"); len(fired) != 0 {
		t.Fatalf("the bounce fired %d alerts, want 0", len(fired))
	}
	observe(t, s, "nonfoil", 34.41, "2026-07-27T00:00:00Z")
	if fired := checkAt(t, s, "2026-07-27T01:00:00Z"); len(fired) != 0 {
		t.Fatalf("the same slide fired again (%d alerts): the anchor did not move with it", len(fired))
	}

	// A genuine further fall off the new level still speaks.
	observe(t, s, "nonfoil", 30.50, "2026-07-28T00:00:00Z")
	if fired := checkAt(t, s, "2026-07-28T01:00:00Z"); len(fired) != 1 {
		t.Fatalf("a further 16%% fall fired %d alerts, want 1", len(fired))
	}
}

// A glance at the browser is not an acknowledgment, and for a percent watch
// that matters more than for an absolute one: writing last_fired_at here would
// move the baseline the next alert is measured from, destroying the movement
// rather than merely previewing it.
func TestWouldFireLeavesTheAnchorAlone(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, "nonfoil", "drop", 0.10, 0)
	observe(t, s, "nonfoil", 38.00, longAgo)
	observe(t, s, "nonfoil", 38.43, "2026-07-20T00:00:00Z")
	observe(t, s, "nonfoil", 34.57, "2026-07-25T00:00:00Z")

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

// NEGATIVE CONTROL: a series younger than the window it claims to summarise
// cannot answer, and says so rather than firing.
//
// The whole series here is seven days old and falls 20%. A thirty-day high
// read off seven days of a printing's first fortnight is not a high, and the
// guard is about the reach of the record rather than the number of rows in it
// — which is why the second half of this test adds one old observation and
// nothing else, and the same shape then answers.
func TestPercentWaitsOnAYoungSeries(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, "nonfoil", "drop", 0.10, 0)
	observe(t, s, "nonfoil", 50.00, "2026-08-04T00:00:00Z")
	observe(t, s, "nonfoil", 40.00, beforeNow) // -20%

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

	// Only the record's reach changes; the price is still the fallen one.
	observe(t, s, "nonfoil", 50.00, longAgo)
	priceIs(t, s, "nonfoil", 40.00)
	w = watchAt(t, s, testNow)
	if w.WaitingOnHistory() {
		t.Fatalf("a series older than the window still read as waiting: %+v", w)
	}
	if !w.Met() {
		t.Errorf("the 20%% fall did not meet the watch once the record reached back: %+v", w)
	}
}

// The carry-forward: history is a change log, so a printing whose price has
// not moved writes nothing, and a perfectly well-known price can be absent
// from the window entirely.
//
// This is Talon Gates of Madara's real shape — five observations in May and
// nothing for the ninety days since. Anchoring only on rows inside the window
// would give it no anchor at all, and worse, the row that finally recorded a
// fall would be the only row in the window, so the anchor would equal the
// fallen price and the alert would be lost rather than merely delayed.
func TestPercentCarriesTheStablePriceIntoTheWindow(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, "nonfoil", "drop", 0.10, 0)
	observe(t, s, "nonfoil", 132.14, "2026-05-12T00:00:00Z") // then flat for 90 days

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

	// The fall arrives as a single row, the only one inside the window.
	observe(t, s, "nonfoil", 110.00, beforeNow) // -16.8%
	w = watchAt(t, s, testNow)
	if w.Anchor == nil || *w.Anchor != 132.14 {
		t.Fatalf("anchor = %v, want 132.14 — the window forgot the price it opened at", w.Anchor)
	}
	if !w.Met() {
		t.Error("the fall from a long-stable price was lost")
	}
}

// The anchor reads one printing, one finish and one vendor. A foil watch
// anchored on the nonfoil series would compare a $136 foil against a nonfoil
// high and report every foil watch as already met.
func TestPercentAnchorIgnoresOtherSeries(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, "foil", "drop", 0.10, 0)
	observe(t, s, "foil", 100.00, longAgo)
	observe(t, s, "nonfoil", 900.00, "2026-07-21T00:00:00Z")             // another finish
	observe(t, s, "foil", 800.00, "2026-07-22T00:00:00Z", "cardkingdom") // another vendor
	observe(t, s, "foil", 95.00, beforeNow)                              // -5% on the anchored series

	w := watchAt(t, s, testNow)
	if w.Anchor == nil || *w.Anchor != 100.00 {
		t.Fatalf("anchor = %v, want 100.00 from the foil %s series alone", w.Anchor, fixtureSource)
	}
	if w.Met() {
		t.Error("a 5% fall fired: the anchor read a series the watch did not name")
	}
}

// The dollar floor is what makes the feature usable on a collection that is
// mostly cheap, and it has to suppress the alert rather than merely annotate
// it.
func TestPercentMinMoveSuppresses(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, "nonfoil", "drop", 0.10, DefaultMinMove)
	observe(t, s, "nonfoil", 1.00, longAgo)
	observe(t, s, "nonfoil", 0.80, beforeNow) // -20%, but only twenty cents

	if w := watchAt(t, s, testNow); w.Met() {
		t.Error("a twenty cent move fired a watch with a twenty-five cent floor")
	}
	// The same percentage where it is worth saying still speaks.
	if _, err := s.db.Exec(`DELETE FROM card_price_history`); err != nil {
		t.Fatalf("clearing: %v", err)
	}
	observe(t, s, "nonfoil", 40.00, longAgo)
	observe(t, s, "nonfoil", 32.00, beforeNow) // -20%, eight dollars
	if w := watchAt(t, s, testNow); !w.Met() {
		t.Error("an eight dollar move was suppressed by a twenty-five cent floor")
	}
}

// Absolute watches are untouched by any of it: no anchor, no window, and the
// same branch of Met they have always taken.
func TestAbsoluteWatchesAreUnchanged(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := s.AddWatch("ulamog-id", "Ulamog", "nonfoil", "under", 12); err != nil {
		t.Fatalf("AddWatch: %v", err)
	}
	observe(t, s, "nonfoil", 999.00, longAgo) // would be a wild anchor
	priceIs(t, s, "nonfoil", 10.00)           // but the price today is the catalog's

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
	// Price is the catalog's effective 10.00, not the 999 sitting in history.
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

// A percent watch and an absolute one coexist on one printing, because drop
// and rise are new values of op rather than a new dimension — so the existing
// UNIQUE(scryfall_id, finish, op) keeps meaning what its comment says.
func TestPercentAndAbsoluteCoexist(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, "nonfoil", "drop", 0.10, 0)
	if err := s.AddWatch("ulamog-id", "Ulamog", "nonfoil", "over", 149.86); err != nil {
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

// The units cannot be crossed: threshold and pct are separate columns exactly
// so neither can stand in for the other, and a row filling the wrong one would
// evaluate against zero and either never fire or fire on every check.
func TestPercentValidationRefusesCrossedUnits(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
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
			in.ScryfallID, in.Display, in.Finish = "ulamog-id", "Ulamog", "nonfoil"
			err := s.AddWatchInput(in)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one naming %q", err, tc.want)
			}
		})
	}
}

// Re-adding a percent watch re-arms it: the moment the *old* rule fired is not
// a bound the new one may anchor from.
func TestReAddingAPercentWatchClearsTheFireMoment(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, "nonfoil", "drop", 0.10, 0)
	observe(t, s, "nonfoil", 38.00, longAgo)
	observe(t, s, "nonfoil", 38.43, "2026-07-20T00:00:00Z")
	observe(t, s, "nonfoil", 34.57, "2026-07-25T00:00:00Z")
	if fired := checkAt(t, s, "2026-07-25T01:00:00Z"); len(fired) != 1 {
		t.Fatalf("setup: fired %d, want 1", len(fired))
	}

	percentWatch(t, s, "nonfoil", "drop", 0.20, 0) // same direction, new size
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

// NEGATIVE CONTROL: RecordPrices must not write a price that has not changed.
//
// This is not an optimisation and it is the reason a derived anchor is exact.
// MAX(price_usd) over a range is the true running high only because every row
// in the range is a distinct transition. If an unchanged price were written on
// every tick, a re-anchored watch — whose window opens at the moment it fired —
// would find a fresh row carrying the old price and re-fire on it. Anything
// that "optimises" RecordPrices into writing every tick breaks this test first.
func TestRecordPricesSkipsUnchangedPrices(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
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
	// The stored rows are walked back a day between refreshes. appendPrices
	// deliberately lets two refreshes inside one second collide on the primary
	// key, so a test this fast would otherwise watch a spurious write overwrite
	// itself and report a pass — which is exactly what this control checked
	// for and did not catch until the rows were separated in time.
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
	// A real change is still recorded.
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

// FINDING #4, the half the demo report's suggested wording would have got
// wrong: a percent watch does NOT wait for the price to cross back.
//
// The report proposed documenting "it re-arms when the price crosses back",
// and flagged that it had not verified the rule. For under and over that is
// right. For drop and rise it is not: firing writes last_fired_at, which
// becomes the anchor's lower bound, so the anchor collapses to about the price
// that fired and the very next check reads unmet — with no new observation, no
// price change, and nothing for the user to do. The watch is re-armed at the
// new level and speaks again only on a further move of its own size.
//
// A single sentence covering both ops would therefore have been false about
// half of them.
func TestPercentReArmsOnFiringAlone(t *testing.T) {
	s := newTestStore(t)
	percentWatch(t, s, "nonfoil", "drop", 0.10, 0)
	observe(t, s, "nonfoil", 50.00, longAgo)
	observe(t, s, "nonfoil", 40.00, beforeNow) // -20%: the alert

	if fired := checkAt(t, s, testNow); len(fired) != 1 {
		t.Fatalf("the fall fired %d alerts, want 1", len(fired))
	}
	if w := watchAt(t, s, testNow); w.LastState != "met" || w.LastFiredAt == "" {
		t.Fatalf("after firing: last_state=%q last_fired_at=%q", w.LastState, w.LastFiredAt)
	}

	// Nothing happens. No observation, no price change, no user action — and
	// the watch is armed again, because the anchor moved with the alert.
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

	// And it speaks again on a further fall of its own size, measured from the
	// new level rather than from the old high. The observation has to be later
	// than the fire moment — an anchor cannot read a price recorded before the
	// bound it was re-anchored at, which is the same carry-forward rule that
	// makes the collapse above work.
	observe(t, s, "nonfoil", 35.00, "2026-08-10T23:00:00Z") // -12.5% from 40
	if fired := checkAt(t, s, "2026-08-11T00:00:00Z"); len(fired) != 1 {
		t.Error("a further 12.5% fall did not fire: the watch did not re-arm")
	}
}

// The negative control's mirror: an absolute watch on the same shape stays
// latched, so the difference between the two ops is the behaviour and not the
// fixture.
func TestAbsoluteDoesNotReArmOnFiringAlone(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), "nonfoil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	priceIs(t, s, "nonfoil", 40.00)
	if err := s.AddWatch("ulamog-id", "Ulamog", "nonfoil", "under", 45); err != nil {
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
