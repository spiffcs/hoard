package store

import (
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

var asOf = time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)

func day(n int) string {
	return asOf.AddDate(0, 0, -n).Format(time.DateOnly)
}

func TestSettlingBoundary(t *testing.T) {
	cases := []struct {
		name       string
		releasedAt string
		want       bool
	}{

		{"not yet released", day(-2), true},
		{"released today", day(0), true},
		{"one day short of the window", day(DefaultSettlingDays - 1), true},

		{"exactly the window", day(DefaultSettlingDays), false},
		{"past the window", day(DefaultSettlingDays + 1), false},
		{"long settled", day(3000), false},

		{"no release date", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Settling(c.releasedAt, asOf); got != c.want {
				t.Errorf("Settling(%q) = %v, want %v", c.releasedAt, got, c.want)
			}
			p := PriceChange{ReleasedAt: c.releasedAt}
			if got := p.Settling(asOf); got != c.want {
				t.Errorf("PriceChange.Settling(%q) = %v, want %v — the row's way "+
					"in must not disagree with the rule", c.releasedAt, got, c.want)
			}
		})
	}
}

func TestSettlingDaysFromEnvValue(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{"", DefaultSettlingDays},
		{"  ", DefaultSettlingDays},
		{"30", 30},
		{" 30 ", 30},
		{"0", 0},
		{"1", 1},
		{"3650", 3650},

		{"-1", DefaultSettlingDays},
		{"90d", DefaultSettlingDays},
		{"ninety", DefaultSettlingDays},
		{"9.5", DefaultSettlingDays},
	}
	for _, c := range cases {
		if got := settlingDaysFrom(c.raw); got != c.want {
			t.Errorf("settlingDaysFrom(%q) = %d, want %d", c.raw, got, c.want)
		}
	}
}

func TestSettlingDaysAskedSeparatesPresenceFromUsability(t *testing.T) {
	for _, raw := range []string{"0", "30", " 90 "} {
		if _, ok := settlingDaysAsked(raw); !ok {
			t.Errorf("settlingDaysAsked(%q) reported no ask, want one", raw)
		}
	}
	for _, raw := range []string{"", "  ", "-1", "90d", "ninety", "9.5"} {
		if n, ok := settlingDaysAsked(raw); ok {
			t.Errorf("settlingDaysAsked(%q) = (%d, true), want no ask", raw, n)
		}
	}

	if n, ok := settlingDaysAsked("0"); !ok || n != 0 {
		t.Errorf("settlingDaysAsked(\"0\") = (%d, %v), want (0, true)", n, ok)
	}
}

func TestSettlingWindowOfZeroHoldsNothingOut(t *testing.T) {
	for _, released := range []string{day(-2), day(0), day(1), day(3000), ""} {
		if settlingWithin(released, asOf, 0) {
			t.Errorf("settlingWithin(%q, 0 days) = true, want false", released)
		}
	}
	rows := []PriceChange{
		{SetCode: "hob", ReleasedAt: day(-2), Copies: 1, Old: 10, New: 20},
		{SetCode: "uma", ReleasedAt: day(3000), Copies: 1, Old: 10, New: 8},
	}
	var plain float64
	for _, r := range rows {
		plain += r.TotalDelta()
	}
	if plain != 8 {
		t.Fatalf("fixture drifted: plain sum = %v, want 8", plain)
	}
}

func TestSettlingWindowShortensWithTheOverride(t *testing.T) {
	released := day(45)
	if !settlingWithin(released, asOf, DefaultSettlingDays) {
		t.Fatal("45 days old is inside the default window")
	}
	if settlingWithin(released, asOf, 30) {
		t.Error("45 days old must have cleared a 30-day window")
	}
}

func TestNetMovedHoldsOutSettlingSets(t *testing.T) {
	rows := []PriceChange{
		{SetCode: "uma", ReleasedAt: day(3000), Copies: 1, Old: 10, New: 8},
		{SetCode: "hob", ReleasedAt: day(-2), Copies: 1, Old: 10, New: 20},
	}
	net, heldOut := NetMoved(rows, asOf)
	if net != -2 {
		t.Errorf("net = %v, want -2 (the settled row alone)", net)
	}
	if heldOut != 1 {
		t.Errorf("heldOut = %d, want 1", heldOut)
	}

	var plain float64
	for _, r := range rows {
		plain += r.TotalDelta()
	}
	if plain != 8 {
		t.Fatalf("fixture drifted: plain sum = %v, want 8", plain)
	}
	if (net < 0) == (plain < 0) {
		t.Errorf("net %v and plain sum %v share a sign, so this test cannot "+
			"detect the exclusion being dropped", net, plain)
	}
}

func TestNetMovedCountsDistinctSetsNotRows(t *testing.T) {
	rows := []PriceChange{
		{SetCode: "hob", ReleasedAt: day(1), Copies: 1, Old: 1, New: 2},
		{SetCode: "hob", ReleasedAt: day(1), Copies: 4, Old: 1, New: 3},
		{SetCode: "hoc", ReleasedAt: day(1), Copies: 1, Old: 5, New: 1},
	}
	net, heldOut := NetMoved(rows, asOf)
	if heldOut != 2 {
		t.Errorf("heldOut = %d, want 2 (hob and hoc)", heldOut)
	}
	if net != 0 {
		t.Errorf("net = %v, want 0 — every row was held out", net)
	}
}

func TestNetMovedWithNothingSettlingIsThePlainSum(t *testing.T) {
	rows := []PriceChange{
		{SetCode: "uma", ReleasedAt: day(3000), Copies: 2, Old: 10, New: 12},
		{SetCode: "c21", ReleasedAt: "", Copies: 1, Old: 5, New: 4},
	}
	net, heldOut := NetMoved(rows, asOf)
	if net != 3 {
		t.Errorf("net = %v, want 3", net)
	}
	if heldOut != 0 {
		t.Errorf("heldOut = %d, want 0", heldOut)
	}
}

func hobbit() scryfall.Card {
	return scryfall.Card{
		ID:              "hobbit-id",
		Set:             "hob",
		CollectorNumber: "7",
		Name:            "Celebrate the Mountain-king",
		PriceUSD:        f(10.00),
		ScryfallURL:     "https://scryfall.com/card/hob/7",
		Raw:             []byte(`{"set_name":"The Hobbit","released_at":"2026-08-14"}`),
	}
}

func TestMoversCarriesTheReleaseDate(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(hobbit(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	backdate(t, s, stamps(t, s)[0], "2026-08-01T00:00:00Z")

	fallen := hobbit()
	fallen.PriceUSD = f(4.00)
	if err := s.UpsertPrintings([]scryfall.Card{fallen}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	moved, err := s.RecordPrices()
	if err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}

	t.Run("RecordPrices", func(t *testing.T) {
		c := changeFor(t, moved, "hobbit-id", finish.Nonfoil)
		if c.ReleasedAt != "2026-08-14" {
			t.Errorf("ReleasedAt = %q, want 2026-08-14 — update-prices reports "+
				"its net from these rows", c.ReleasedAt)
		}
		if !c.Settling(asOf) {
			t.Error("a set released after asOf is not settling")
		}
	})

	t.Run("Movers", func(t *testing.T) {
		got, err := s.Movers("2026-07-01T00:00:00Z")
		if err != nil {
			t.Fatalf("Movers: %v", err)
		}
		c := changeFor(t, got, "hobbit-id", finish.Nonfoil)
		if c.ReleasedAt != "2026-08-14" {
			t.Errorf("ReleasedAt = %q, want 2026-08-14", c.ReleasedAt)
		}
		net, heldOut := NetMoved(got, asOf)
		if heldOut != 1 || net != 0 {
			t.Errorf("NetMoved = (%v, %d), want (0, 1)", net, heldOut)
		}
	})
}

func TestMoversTolerationOfAnUnknownReleaseDate(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddCardFinish(ulamog(), finish.Nonfoil, 1); err != nil {
		t.Fatalf("AddCard: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("baseline: %v", err)
	}
	backdate(t, s, stamps(t, s)[0], "2026-08-01T00:00:00Z")

	risen := ulamog()
	risen.PriceUSD = f(20.00)
	if err := s.UpsertPrintings([]scryfall.Card{risen}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	if _, err := s.RecordPrices(); err != nil {
		t.Fatalf("RecordPrices: %v", err)
	}
	got, err := s.Movers("2026-07-01T00:00:00Z")
	if err != nil {
		t.Fatalf("Movers: %v", err)
	}
	c := changeFor(t, got, "ulamog-id", finish.Nonfoil)
	if c.ReleasedAt != "" {
		t.Errorf("ReleasedAt = %q, want empty", c.ReleasedAt)
	}
	net, heldOut := NetMoved(got, asOf)
	if heldOut != 0 || net != 10 {
		t.Errorf("NetMoved = (%v, %d), want (10, 0) — an undated set counts", net, heldOut)
	}
}
