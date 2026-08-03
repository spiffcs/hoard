package browse

// The detail overlay's vendor half: bid sparklines, the spread trend, and
// the per-card comp sheet.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/store"
)

func pp(asOf string, price float64) store.PricePoint {
	return store.PricePoint{AsOf: asOf, Price: price, Source: "test"}
}

// Opening a detail loads the bid series alongside the price series.
func TestDetailOpenLoadsBidSeries(t *testing.T) {
	st := testStore()
	st.bidSeries = map[string][]store.PricePoint{
		"Bitterblossom-id|nonfoil": {pp("2026-07-01T00:00:00Z", 20), pp("2026-07-20T00:00:00Z", 24)},
	}
	m := newTestModel(t, st)
	m = key(m, "tab") // into the card pane
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("enter did not open the detail")
	}
	if got := m.detail.bids["nonfoil"]; len(got) != 2 {
		t.Fatalf("bids = %+v, want the seeded series", m.detail.bids)
	}
	out := strings.Join(m.hoardLines(*m.detail, 100), "\n")
	if !strings.Contains(out, "bid") || !strings.Contains(out, "$24.00") {
		t.Errorf("bid row missing:\n%s", out)
	}
}

// The spread row appears when the two series overlap, tracks the trend
// direction, and stays hidden when they never share a window.
func TestDetailBidAndSpreadRows(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	d := detail{
		series: map[string][]store.PricePoint{
			"nonfoil": {pp("2026-06-01T00:00:00Z", 10), pp("2026-07-01T00:00:00Z", 10)},
		},
		bids: map[string][]store.PricePoint{
			"nonfoil": {pp("2026-06-01T00:00:00Z", 5), pp("2026-07-01T00:00:00Z", 8)},
		},
	}
	out := strings.Join(m.hoardLines(d, 120), "\n")
	if !strings.Contains(out, "bid") || !strings.Contains(out, "$8.00") {
		t.Fatalf("bid row missing:\n%s", out)
	}
	// Flat $10 retail against a bid rising 5 → 8: the spread halves and
	// then some, 50% down to 20%.
	if !strings.Contains(out, "spread") || !strings.Contains(out, "50.0% → 20.0%") ||
		!strings.Contains(out, "tightening") {
		t.Errorf("spread trend missing or wrong:\n%s", out)
	}

	// Disjoint windows: a bid series that ends before the retail one
	// begins has no shared instant to compare at.
	d.bids["nonfoil"] = []store.PricePoint{pp("2026-01-01T00:00:00Z", 5), pp("2026-02-01T00:00:00Z", 6)}
	out = strings.Join(m.hoardLines(d, 120), "\n")
	if strings.Contains(out, "spread") {
		t.Errorf("spread row rendered without overlapping windows:\n%s", out)
	}
	if !strings.Contains(out, "bid") {
		t.Errorf("the bid row should survive alone:\n%s", out)
	}
}

// A bid series with no retail series still renders — the two tables have
// independent eras.
func TestDetailBidRowWithoutRetail(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	d := detail{
		series: map[string][]store.PricePoint{},
		bids: map[string][]store.PricePoint{
			"foil": {pp("2026-07-01T00:00:00Z", 3)},
		},
	}
	out := strings.Join(m.hoardLines(d, 120), "\n")
	if !strings.Contains(out, "bid") || !strings.Contains(out, "$3.00") {
		t.Errorf("orphan bid row missing:\n%s", out)
	}
}

// The COMPS section renders the sheet in the market view's vocabulary,
// notes the missing day cache, and stays absent without the capability.
func TestDetailCompsSection(t *testing.T) {
	sheet := market.Comp{
		Market: 10, HasMarket: true,
		Manapool: 11, HasManapool: true,
		CK: 12, HasCK: true,
		Buylist: 7, BuylistTo: "cardkingdom", HasBuylist: true,
		Low: 10, LowFrom: "tcgplayer",
	}
	m := atAllCards(t, newTestModel(t, testStore()))

	// Uninjected: no section at all.
	d := detail{comps: map[string]market.Comp{"nonfoil": sheet}, compsOK: true}
	if out := strings.Join(m.hoardLines(d, 140), "\n"); strings.Contains(out, "COMPS") {
		t.Fatalf("COMPS rendered without the capability:\n%s", out)
	}

	m.cardComps = func(string) (map[string]market.Comp, bool) { return nil, false }
	// No day cache: the section says how to get one.
	d = detail{compsOK: false}
	out := strings.Join(m.hoardLines(d, 140), "\n")
	if !strings.Contains(out, "COMPS") || !strings.Contains(out, "press F on the MARKET view") {
		t.Fatalf("absent-cache note missing:\n%s", out)
	}

	// A full sheet, in the market view's own words.
	d = detail{
		comps: map[string]market.Comp{"nonfoil": sheet}, compsOK: true,
		holdings: []store.Holding{{ContainerName: "Binder", Finish: "nonfoil", Quantity: 1}},
	}
	out = strings.Join(m.hoardLines(d, 140), "\n")
	for _, want := range []string{
		"tcg last sold $10.00", "mp asks $11.00", "ck asks $12.00", "ck pays $7.00", "spread 30.0%",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("comps line missing %q:\n%s", want, out)
		}
	}
	// 70% of last-sold: the held finish earns the EASY TO SELL verdict.
	if !strings.Contains(out, "EASY TO SELL") || !strings.Contains(out, "70.0% of tcg last-sold") {
		t.Errorf("verdict line missing:\n%s", out)
	}
	// PRICE always precedes COMPS.
	if strings.Index(out, "PRICE") > strings.Index(out, "COMPS") {
		t.Errorf("COMPS rendered before PRICE:\n%s", out)
	}
}

// An unheld finish's sheet renders without a verdict — the verdict is
// about your copies, the numbers are about the card.
func TestDetailCompsVerdictNeedsAHolding(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	m.cardComps = func(string) (map[string]market.Comp, bool) { return nil, true }
	d := detail{
		comps: map[string]market.Comp{"foil": {
			Market: 10, HasMarket: true, Buylist: 11, BuylistTo: "cardkingdom", HasBuylist: true,
			Low: 10, LowFrom: "tcgplayer",
		}},
		compsOK:  true,
		holdings: []store.Holding{{ContainerName: "Binder", Finish: "nonfoil", Quantity: 1}},
	}
	out := strings.Join(m.hoardLines(d, 140), "\n")
	if !strings.Contains(out, "ck pays $11.00") {
		t.Fatalf("foil sheet missing:\n%s", out)
	}
	if strings.Contains(out, "ARBITRAGE") {
		t.Errorf("verdict granted for an unheld finish:\n%s", out)
	}
}
