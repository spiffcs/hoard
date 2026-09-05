package report

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func testCollection() store.CollectionTotals {
	return store.CollectionTotals{DistinctCards: 2, TotalCopies: 10, Value: 100}
}

func testDecks() []store.DeckSummary {
	mk := func(name string, copies int, value float64) store.DeckSummary {
		d := store.DeckSummary{TotalCopies: copies, Value: value, Counted: true}
		d.Name = name
		d.Kind = store.KindDeck
		return d
	}
	return []store.DeckSummary{
		mk("Beta Deck (Precon)", 60, 100),
		mk("Unpriced Deck (Precon)", 40, 0),
		mk("Alpha Deck (Precon)", 100, 300),
	}
}

func renderSummary(env ui.Env) string {
	return Summary(env, testCollection(), testDecks())
}

func TestSummaryTable(t *testing.T) {
	got := renderSummary(ui.Env{Width: 80, Clamp: true, Bars: true})
	want := strings.Join([]string{
		"BINDER                     10  $100.00  ██",
		"DECKS · 3                 200  $400.00  ████████",
		"",
		"  Alpha Deck (Precon)     100  $300.00  ██████",
		"  Beta Deck (Precon)       60  $100.00  ██",
		"  Unpriced Deck (Precon)   40    $0.00",
		"",
		"TOTAL                     210  $500.00",
		"",
	}, "\n")
	if got != want {
		t.Errorf("summary =\n%s\nwant\n%s", got, want)
	}
}

func TestSummaryTableSortsByValue(t *testing.T) {
	out := renderSummary(ui.Env{Width: 80, Clamp: true, Bars: true})
	alpha := strings.Index(out, "Alpha")
	beta := strings.Index(out, "Beta")
	unpriced := strings.Index(out, "Unpriced")
	if !(alpha < beta && beta < unpriced) {
		t.Errorf("decks not ordered by value desc:\n%s", out)
	}
}

func TestSummaryTableTieBreaksByName(t *testing.T) {
	mk := func(name string) store.DeckSummary {
		d := store.DeckSummary{TotalCopies: 1, Counted: true}
		d.Name = name
		return d
	}
	decks := []store.DeckSummary{mk("Zeta"), mk("Alpha"), mk("Mid")}
	out := Summary(ui.Env{Width: 80, Clamp: true}, store.CollectionTotals{}, decks)
	a, m, z := strings.Index(out, "Alpha"), strings.Index(out, "Mid"), strings.Index(out, "Zeta")
	if !(a < m && m < z) {
		t.Errorf("equal-value decks not name-ordered:\n%s", out)
	}
}

func TestSummaryTablePiped(t *testing.T) {
	out := renderSummary(ui.Env{Width: 80, Clamp: false, Bars: false})
	if strings.Contains(out, "\x1b") {
		t.Error("piped summary contains ANSI escapes")
	}
	if strings.ContainsAny(out, "█▏▎▍▌▋▊▉") {
		t.Error("piped summary contains share bars")
	}
	if strings.Contains(out, "…") {
		t.Error("piped summary truncated a name")
	}
	if !strings.Contains(out, "Unpriced Deck (Precon)") {
		t.Errorf("piped summary lost a full deck name:\n%s", out)
	}
}

func TestSummaryTableNarrow(t *testing.T) {
	for _, width := range []int{60, 50, 40, 30} {
		env := ui.Env{Width: width, Clamp: true, Bars: width >= 50}
		for line := range strings.SplitSeq(renderSummary(env), "\n") {
			if w := len([]rune(line)); w > width {
				t.Errorf("width %d: line %q is %d wide", width, line, w)
			}
		}
	}
}

func TestSummaryTableEmpty(t *testing.T) {
	out := Summary(ui.Env{Width: 80, Clamp: true, Bars: true},
		store.CollectionTotals{}, nil)

	want := strings.Join([]string{
		"BINDER     0  $0.00",
		"DECKS · 0  0  $0.00",
		"",
		"TOTAL      0  $0.00",
		"",
	}, "\n")
	if out != want {
		t.Errorf("empty summary =\n%s\nwant\n%s", out, want)
	}
}

func TestMoverSectionsSortByTotalImpact(t *testing.T) {
	changes := []store.PriceChange{
		change("One Dollar Each", 1, 10.00, 11.00),
		change("A Dime, Forty Times", 40, 1.00, 1.10),
		change("Crashed", 2, 20.00, 15.00),
		change("Slipped", 1, 5.00, 4.50),
	}

	secs := moverSections(changes, 10)
	if len(secs) != 2 || secs[0].Title != "RISERS" || secs[1].Title != "SINKERS" {
		t.Fatalf("sections = %+v, want RISERS then SINKERS", secs)
	}

	var risers []string
	for _, c := range secs[0].Rows {
		risers = append(risers, c.Name)
	}
	if strings.Join(risers, ",") != "A Dime, Forty Times,One Dollar Each" {
		t.Errorf("risers = %v, want the forty dimes first", risers)
	}

	var sinkers []string
	for _, c := range secs[1].Rows {
		sinkers = append(sinkers, c.Name)
	}
	if strings.Join(sinkers, ",") != "Crashed,Slipped" {
		t.Errorf("sinkers = %v, want the biggest loss first", sinkers)
	}
}

func TestMoverSectionsTruncate(t *testing.T) {
	var changes []store.PriceChange
	for i := range 25 {
		changes = append(changes, change("Riser", 1, 1.00, float64(2+i)))
		changes = append(changes, change("Sinker", 1, 100.00, float64(50-i)))
	}
	for _, sec := range moverSections(changes, 3) {
		if len(sec.Rows) != 3 {
			t.Errorf("%s has %d rows, want the limit of 3", sec.Title, len(sec.Rows))
		}
	}

	for _, sec := range moverSections(changes, 0) {
		if len(sec.Rows) != DefaultMoverRows {
			t.Errorf("%s has %d rows, want the default %d", sec.Title, len(sec.Rows), DefaultMoverRows)
		}
	}
}

func TestMoverSectionsExcludeUnmoved(t *testing.T) {
	secs := moverSections([]store.PriceChange{change("Flat", 3, 5.00, 5.00)}, 10)
	for _, sec := range secs {
		if len(sec.Rows) != 0 {
			t.Errorf("%s = %+v, want nothing for a price that did not move", sec.Title, sec.Rows)
		}
	}
}

func TestMoversTable(t *testing.T) {
	rows := []store.PriceChange{
		{Name: "Ulamog, the Infinite Gyre", SetCode: "uma", CollectorNumber: "7",
			Finish: finish.Foil, Copies: 3, Old: 10.00, New: 12.50},
	}
	got := moversTable(ui.Env{Width: 100, Clamp: true}, moverSections(rows, 10), time.Time{}).Render()

	for _, want := range []string{
		"RISERS",
		"Ulamog, the Infinite Gyre", "uma/7", "foil",
		"$10.00", "→", "$12.50",
		"+25.0%",
		"×3",
		"+$7.50",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered row is missing %q:\n%s", want, got)
		}
	}

	if strings.Contains(got, "SINKERS") {
		t.Errorf("empty section still printed its heading:\n%s", got)
	}
}

func TestMoversTableMarksOnlyInterestingFinishes(t *testing.T) {
	got := moversTable(ui.Env{Width: 100, Clamp: true},
		moverSections([]store.PriceChange{change("Sol Ring", 1, 2.00, 3.00)}, 10), time.Time{}).Render()
	if strings.Contains(got, "nonfoil") {
		t.Errorf("non-foil rows should not spell out the finish:\n%s", got)
	}
}

func TestMoversTableSharesOneLayoutAcrossSections(t *testing.T) {
	changes := []store.PriceChange{
		{Name: "Sol Ring", SetCode: "c21", CollectorNumber: "1", Finish: finish.Nonfoil,
			Copies: 40, Old: 1.00, New: 1.10},
		{Name: "Black Lotus", SetCode: "lea", CollectorNumber: "232", Finish: finish.Nonfoil,
			Copies: 1, Old: 20000.00, New: 18500.00},
	}
	out := moversTable(ui.Env{Width: 70, Clamp: true}, moverSections(changes, 10), time.Time{}).Render()

	var riser, sinker string
	for line := range strings.SplitSeq(out, "\n") {
		switch {
		case strings.Contains(line, "Sol Ring"):
			riser = line
		case strings.Contains(line, "Black Lotus"):
			sinker = line
		}
	}
	if riser == "" || sinker == "" {
		t.Fatalf("expected a row in each section:\n%s", out)
	}

	if got, want := len(strings.Fields(riser)), len(strings.Fields(sinker)); got != want {
		t.Errorf("sections rendered different column sets (%d vs %d fields):\n%s", got, want, out)
	}
}

func TestMarketIdentityAndProfitColors(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	sec := market.Section{Kind: market.KindProfit, Rows: []market.Opportunity{{
		Card: store.OwnedFinish{Name: "Absorb", SetCode: "rna", CollectorNumber: "151",
			Finish: finish.Nonfoil, Copies: 1, ColorIdentity: []string{"W", "U"}},
		Market: 1.00, BuyAt: 1.00, BuyFrom: "tcgplayer", LowAsk: 1.00,
		SellAt: 3.00, SellTo: "cardkingdom",
		HasMarket: true, HasRetail: true, HasBuy: true, HasLowAsk: true,
	}}}

	out := Market(ui.Env{Width: 100, Color: true, Clamp: true}, sec)

	if strings.Contains(out, "mW\x1b[0m\x1b[38") {
		t.Errorf("market row still renders a pips column:\n%q", out)
	}
	if !strings.Contains(out, "38;2;") {
		t.Errorf("market row lost its identity tint:\n%q", out)
	}
	if !strings.Contains(out, "\x1b[92m+$2.00\x1b[0m") {
		t.Errorf("profit not gain-colored:\n%q", out)
	}

	plain := Market(ui.Env{Width: 100, Clamp: true}, sec)
	if strings.Contains(plain, "\x1b[") {
		t.Errorf("piped market output carries escapes:\n%q", plain)
	}
}

func TestMoversGradientColors(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	changes := []store.PriceChange{
		{Name: "Riser", SetCode: "a", CollectorNumber: "1", Finish: finish.Nonfoil,
			Copies: 2, Old: 1.00, New: 5.00},
		{Name: "Sinker", SetCode: "b", CollectorNumber: "2", Finish: finish.Nonfoil,
			Copies: 1, Old: 50.00, New: 10.00},
	}
	e := ui.Env{Color: true}
	out := moversTable(ui.Env{Width: 100, Color: true, Clamp: true},
		moverSections(changes, 10), time.Time{}).Render()

	pctMax, impactMax := 4.0, 40.0
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "Riser"):
			if want := e.Diverge(1)(ui.SignedPercent(4.0)); !strings.Contains(line, want) {
				t.Errorf("riser CHANGE not at the gain endpoint:\n%q", line)
			}
			if want := e.Diverge(ui.DivergeFrac(8, impactMax))(ui.SignedMoney(8)); !strings.Contains(line, want) {
				t.Errorf("riser IMPACT not mid-ramp:\n%q", line)
			}
		case strings.Contains(line, "Sinker"):
			if want := e.Diverge(-1)(ui.SignedMoney(-40)); !strings.Contains(line, want) {
				t.Errorf("sinker IMPACT not at the loss endpoint:\n%q", line)
			}
			if want := e.Diverge(ui.DivergeFrac(-0.8, pctMax))(ui.SignedPercent(-0.8)); !strings.Contains(line, want) {
				t.Errorf("sinker CHANGE not mid-ramp:\n%q", line)
			}
		}
	}
	if strings.Contains(strings.SplitN(out, "\n", 2)[0], "38;2;") {
		t.Errorf("the header row must never be colored:\n%q", out)
	}

	plainOut := moversTable(ui.Env{Width: 100, Clamp: true}, moverSections(changes, 10), time.Time{}).Render()
	if strings.Contains(plainOut, "\x1b[") {
		t.Errorf("piped movers output carries escapes:\n%q", plainOut)
	}
}

func TestMoversTableFitsNarrowTerminal(t *testing.T) {
	rows := []store.PriceChange{
		{Name: "Ulamog, the Infinite Gyre", SetCode: "uma", CollectorNumber: "7",
			Finish: finish.Foil, Copies: 3, Old: 10.00, New: 12.50},
		{Name: "Black Lotus", SetCode: "lea", CollectorNumber: "232", Finish: finish.Nonfoil,
			Copies: 1, Old: 20000.00, New: 18500.00},
	}
	for _, width := range []int{44, 60, 80, 120} {
		out := moversTable(ui.Env{Width: width, Clamp: true}, moverSections(rows, 10), time.Time{}).Render()
		for line := range strings.SplitSeq(strings.TrimRight(out, "\n"), "\n") {
			if len([]rune(line)) > width {
				t.Errorf("at width %d a line is %d cells wide: %q", width, len([]rune(line)), line)
			}
		}

		for _, want := range []string{"+$7.50", "-$1,500.00"} {
			if !strings.Contains(out, want) {
				t.Errorf("at width %d the impact %s was dropped:\n%s", width, want, out)
			}
		}
	}
}

func TestMoversTableDropsTheArrowBeforeTheOldPrice(t *testing.T) {
	rows := []store.PriceChange{
		{Name: "Black Lotus", SetCode: "lea", CollectorNumber: "232", Finish: finish.Nonfoil,
			Copies: 1, Old: 20000.00, New: 18500.00},
	}
	for width := 40; width <= 120; width++ {
		out := moversTable(ui.Env{Width: width, Clamp: true}, moverSections(rows, 10), time.Time{}).Render()
		if strings.Contains(out, "→") && !strings.Contains(out, "$20,000.00") {
			t.Fatalf("at width %d the arrow outlived the price it points from:\n%s", width, out)
		}
	}
}

func TestSignedMoney(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{
		{7.5, "+$7.50"},
		{-7.5, "-$7.50"},
		{0, "$0.00"},
		{1234.5, "+$1,234.50"},
	} {
		if got := ui.SignedMoney(tc.in); got != tc.want {
			t.Errorf("ui.SignedMoney(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSignedPercent(t *testing.T) {
	for _, tc := range []struct {
		in   float64
		want string
	}{
		{0.25, "+25.0%"},
		{-0.25, "-25.0%"},
		{0.005, "+0.5%"},
		{0, ""},
	} {
		if got := ui.SignedPercent(tc.in); got != tc.want {
			t.Errorf("ui.SignedPercent(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func change(name string, copies int, old, now float64) store.PriceChange {
	return store.PriceChange{
		Name: name, SetCode: "uma", CollectorNumber: "7", Finish: finish.Nonfoil,
		Copies: copies, Old: old, New: now,
	}
}

func TestValuationRendersEverySection(t *testing.T) {
	d := ValuationData{
		AsOf:   "2026-07-30T09:00:00Z",
		Binder: testCollection(),
		Binders: []store.DeckSummary{
			func() store.DeckSummary {
				b := store.DeckSummary{DistinctCards: 1, TotalCopies: 4, Value: 60}
				b.Name = "Binder"
				return b
			}(),
			func() store.DeckSummary {
				b := store.DeckSummary{DistinctCards: 1, TotalCopies: 6, Value: 40}
				b.Name = "Trade"
				return b
			}(),
		},
		Decks: testDecks(),
		Top: []store.OwnedFinish{
			{Name: "Ancient Tomb", SetCode: "uma", CollectorNumber: "236",
				Finish: finish.Nonfoil, Copies: 2, Value: 60},
		},
		Sources:  []store.SourceCount{{Source: "scryfall", Printings: 3, Copies: 10}},
		Unpriced: store.SourceCount{Printings: 1, Copies: 2},
	}
	out := Valuation(ui.Env{Width: 100, Clamp: true}, d)
	for _, want := range []string{
		"VALUATION · prices as of 30 Jul 2026",
		"BINDERS", "Trade",
		"TOP 1 HOLDINGS", "Ancient Tomb", "$30.00", "$60.00",
		"PRICE SOURCES", "scryfall", "unpriced", "counted as $0.00",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("valuation is missing %q:\n%s", want, out)
		}
	}
}

func TestValuationSkipsSingleBinderBreakdown(t *testing.T) {
	b := store.DeckSummary{DistinctCards: 2, TotalCopies: 10, Value: 100}
	b.Name = "Binder"
	out := Valuation(ui.Env{Width: 100, Clamp: true}, ValuationData{
		Binder:  testCollection(),
		Binders: []store.DeckSummary{b},
	})
	if strings.Contains(out, "BINDERS") {
		t.Errorf("single-binder valuation still shows the breakdown:\n%s", out)
	}
	if strings.Contains(out, "prices as of") {
		t.Errorf("valuation with no AsOf claims a date:\n%s", out)
	}
}

func TestValuationCSV(t *testing.T) {
	var sb strings.Builder
	err := ValuationCSV(&sb, "2026-07-30T09:00:00Z", []store.OwnedFinish{
		{Name: "Mystic Remora", SetCode: "ice", CollectorNumber: "78",
			Finish: finish.Nonfoil, Copies: 1, Value: 0},
		{Name: "Ancient Tomb", SetCode: "uma", CollectorNumber: "236",
			Finish: finish.Nonfoil, Copies: 2, Value: 60},
	})
	if err != nil {
		t.Fatalf("ValuationCSV: %v", err)
	}
	want := strings.Join([]string{
		"Name,Set,Collector Number,Finish,Copies,Unit Price USD,Value USD,As Of",
		"Ancient Tomb,uma,236,nonfoil,2,30.00,60.00,30 Jul 2026",
		"Mystic Remora,ice,78,nonfoil,1,,,30 Jul 2026",
		"",
	}, "\n")
	if sb.String() != want {
		t.Errorf("valuation CSV:\n%s\nwant:\n%s", sb.String(), want)
	}
}

func TestCompsSpreadColorsAndDashes(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	comps := []market.Comp{
		{
			Card: store.OwnedFinish{Name: "Ancient Tomb", SetCode: "uma", CollectorNumber: "236",
				Finish: finish.Nonfoil, Copies: 1, Value: 60, ColorIdentity: []string{}},
			Market: 60, HasMarket: true, CK: 65, HasCK: true,
			Low: 60, LowFrom: "tcgplayer",
			Buylist: 48, BuylistTo: "cardkingdom", HasBuylist: true,
		},
		{
			Card: store.OwnedFinish{Name: "Sol Ring", SetCode: "c21", CollectorNumber: "125",
				Finish: finish.Nonfoil, Copies: 4, Value: 8},
			Low: 1.99, LowFrom: "manapool", Manapool: 1.99, HasManapool: true,
		},
	}

	out := Comps(ui.Env{Width: 110, Color: true, Clamp: true}, comps)
	if !strings.Contains(out, "COMPS") || !strings.Contains(out, "SPREAD") {
		t.Fatalf("comps section missing its furniture:\n%s", out)
	}
	if !strings.Contains(out, "20.0%") {
		t.Errorf("spread percent missing:\n%s", out)
	}

	wantGreen := ui.Env{Color: true}.Heat(market.MarkupGrade(0.20))("20.0%")
	if !strings.Contains(out, wantGreen) {
		t.Errorf("tight spread not graded green: want %q in:\n%q", wantGreen, out)
	}

	plain := Comps(ui.Env{Width: 110, Clamp: true}, comps)
	if strings.Contains(plain, "\x1b[") {
		t.Errorf("piped comps output carries escapes:\n%q", plain)
	}
	if !strings.Contains(plain, "—") {
		t.Errorf("missing vendors must render the dash:\n%s", plain)
	}
}
