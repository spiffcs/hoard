package browse

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func namesCostBasis(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "cost basis") || strings.Contains(l, "costbasis")
}

func costBasisRows() []store.PriceChange {
	return []store.PriceChange{
		{ScryfallID: "gain-id", Name: "Grewell", SetCode: "aaa", CollectorNumber: "1",
			Finish: finish.Nonfoil, Copies: 2, Old: 4, New: 10},
		{ScryfallID: "loss-id", Name: "Fellow", SetCode: "bbb", CollectorNumber: "2",
			Finish: finish.Nonfoil, Copies: 1, Old: 50, New: 20},
	}
}

func cycleWindows(t *testing.T, m Model, presses int) ([]string, Model) {
	t.Helper()
	var seen []string
	for range presses {
		m = key(m, "W")
		seen = append(seen, m.status)
	}
	return seen, m
}

func TestMoversWindowCycleIsUnchangedWithoutACostBasis(t *testing.T) {
	st := windowStore("2026-06-20T12:00:00Z", "2026-08-02T12:00:00Z")
	if len(st.costBasis) != 0 {
		t.Fatal("fixture must have no cost basis")
	}
	m := onWindowMovers(t, st)

	seen, m := cycleWindows(t, m, 6)
	for i := range 3 {
		if seen[i] != seen[i+3] {
			t.Errorf("window cycle is not period 3 without a cost basis: %q", seen)
		}
	}
	for _, s := range seen {
		if namesCostBasis(s) {
			t.Errorf("a hoard with no cost basis offered a cost basis window: %q", s)
		}
	}
	if out := m.View(); !strings.Contains(out, "lookback 7/30/90 days") {
		t.Errorf("help no longer reads 7/30/90 for a hoard with no cost basis:\n%s", out)
	}
}

func TestMoversWindowGainsACostBasisStopOnceOneIsEntered(t *testing.T) {
	st := windowStore("2026-06-20T12:00:00Z", "2026-08-02T12:00:00Z")
	st.costBasis = costBasisRows()
	m := onWindowMovers(t, st)

	seen, _ := cycleWindows(t, m, 8)
	for i := range 4 {
		if seen[i] != seen[i+4] {
			t.Errorf("window cycle is not period 4 with a cost basis entered: %q", seen)
		}
	}
	var named int
	for _, s := range seen[:4] {
		if namesCostBasis(s) {
			named++
		}
	}
	if named != 1 {
		t.Errorf("want exactly one cost basis stop in the cycle, got %d: %q", named, seen[:4])
	}
}

func atCostBasisWindow(t *testing.T, m Model) Model {
	t.Helper()
	for range 6 {
		m = key(m, "W")
		if namesCostBasis(m.status) {
			return m
		}
	}
	t.Fatalf("never reached a cost basis window; last status %q", m.status)
	return m
}

func TestCostBasisWindowHidesCardsWithoutOne(t *testing.T) {
	st := windowStore("2026-06-20T12:00:00Z", "2026-08-02T12:00:00Z")
	st.costBasis = costBasisRows()
	m := atCostBasisWindow(t, onWindowMovers(t, st))

	out := m.View()
	for _, want := range []string{"Grewell", "Fellow"} {
		if !strings.Contains(out, want) {
			t.Errorf("%q has a cost basis but is missing from the pane:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"Longrecord", "Shortrecord"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("%q has no cost basis and must be hidden:\n%s", unwanted, out)
		}
	}
}

func TestCostBasisWindowShowsGrowthAgainstWhatYouPaid(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	st := windowStore("2026-06-20T12:00:00Z", "2026-08-02T12:00:00Z")
	st.costBasis = costBasisRows()
	m := atCostBasisWindow(t, onWindowMovers(t, st))
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = next.(Model)
	m.focus = paneContainers

	out := m.View()
	if !strings.Contains(out, ui.SignedPercent(1.5)) {
		t.Errorf("a card bought at 4 and now worth 10 must read %s:\n%s",
			ui.SignedPercent(1.5), out)
	}
	if !strings.Contains(out, ui.SignedPercent(-0.6)) {
		t.Errorf("a card bought at 50 and now worth 20 must read %s:\n%s",
			ui.SignedPercent(-0.6), out)
	}

	e := ui.Env{Color: true}
	gain := e.Diverge(ui.DivergeFrac(1.5, 1.5))(ui.SignedPercent(1.5))
	loss := e.Diverge(ui.DivergeFrac(-0.6, 1.5))(ui.SignedPercent(-0.6))
	if !strings.Contains(out, gain) {
		t.Errorf("the gain is not shaded like every other movers number:\n%q", out)
	}
	if !strings.Contains(out, loss) {
		t.Errorf("the loss is not shaded like every other movers number:\n%q", out)
	}
	if gain == loss {
		t.Error("a gain and a loss must not render the same shade")
	}
}

func TestCostBasisWindowSaysWhatItIsComparingAgainst(t *testing.T) {
	st := windowStore("2026-06-20T12:00:00Z", "2026-08-02T12:00:00Z")
	st.costBasis = costBasisRows()
	m := atCostBasisWindow(t, onWindowMovers(t, st))

	out := m.View()
	if strings.Contains(out, "MOVERS · SINCE") {
		t.Errorf("the cost basis window still dates itself to a lookback:\n%s", out)
	}
	if !namesCostBasis(m.status) {
		t.Errorf("status = %q, want it to name the cost basis window", m.status)
	}
	if !strings.Contains(out, "cost basis") {
		t.Errorf("nothing on screen says this window compares against what you paid:\n%s", out)
	}
}
