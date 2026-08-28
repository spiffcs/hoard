package browse

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func colouredDipModel(t *testing.T) Model {
	t.Helper()
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	f := testStore()
	f.dips = []store.TrendRow{
		dipRow("Shallow Dip", 100, 82, 82),
		dipRow("Deep Dip", 100, 35, 35),
	}
	f.momentum = []store.TrendRow{
		momentumRow("Short Run", 10, 12, 3),
		momentumRow("Long Run", 10, 30, 18),
	}
	m := atAllCards(t, newTestModel(t, f))
	next, cmd := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = pump(t, next.(Model), cmd)
	m = showDipView(t, m)
	return m
}

func TestDipOffHighUsesTheMarketGradeSpectrum(t *testing.T) {
	m := colouredDipModel(t)
	m.focus = paneContainers
	e := ui.Env{Color: true}
	out := strings.Join(m.dipLines(120), "\n")

	deep := e.Grade(market.DipGrade(0.65))(ui.SignedPercent(-0.65))
	if !strings.Contains(out, deep) {
		t.Errorf("the -65%% dip is not at the full-green end:\n%q", out)
	}
	shallow := e.Grade(market.DipGrade(0.18))(ui.SignedPercent(-0.18))
	if !strings.Contains(out, shallow) {
		t.Errorf("the -18%% dip is not near the dull end:\n%q", out)
	}
	if deep == shallow {
		t.Error("a deep and a shallow dip must not render the same shade")
	}
}

func TestDipStreakAndChangeAreColoured(t *testing.T) {
	m := colouredDipModel(t)
	m.focus = paneContainers
	e := ui.Env{Color: true}
	out := strings.Join(m.dipLines(120), "\n")

	long := e.Grade(market.StreakGrade(18))("18 up")
	if !strings.Contains(out, long) {
		t.Errorf("an 18-check streak is not graded:\n%q", out)
	}
	short := e.Grade(market.StreakGrade(3))("3 up")
	if !strings.Contains(out, short) {
		t.Errorf("a 3-check streak is not graded:\n%q", out)
	}
	if long == short {
		t.Error("streak length must change the shade")
	}

	change := e.Diverge(ui.DivergeFrac(2.0, 2.0))(ui.SignedPercent(2.0))
	if !strings.Contains(out, change) {
		t.Errorf("momentum CHANGE does not use the movers diverge ramp:\n%q", out)
	}
}

func TestEnterOpensDetailFromBothDipTables(t *testing.T) {
	m := colouredDipModel(t)

	sec, _ := m.dipCursorPos()
	if sec != int(secDip) {
		t.Fatalf("cursor starts in section %d, want the dip table", sec)
	}
	m = key(m, "enter")
	if m.detail == nil {
		t.Fatal("enter on a dip row opened nothing; the help line promises a detail page")
	}
	if got := m.detail.card.ScryfallID; got != "Deep Dip" {
		t.Errorf("detail opened %q, want the selected dip row", got)
	}

	m = key(m, "esc")
	if m.detail != nil {
		t.Fatal("esc did not close the detail page")
	}

	m = key(m, "]")
	if sec, _ := m.dipCursorPos(); sec != int(secMomentum) {
		t.Fatalf("] did not reach the momentum table")
	}
	m = key(m, "enter")
	if m.detail == nil {
		t.Fatal("enter on a momentum row opened nothing")
	}
	if got := m.detail.card.ScryfallID; got != "Long Run" {
		t.Errorf("detail opened %q, want the selected momentum row", got)
	}
}

func TestEscFromDipDetailKeepsTheSection(t *testing.T) {
	m := colouredDipModel(t)

	m = key(m, "]")
	before, beforeIdx := m.dipCursorPos()
	m = key(m, "enter")
	if m.detail == nil {
		t.Fatal("enter opened nothing")
	}
	m = key(m, "esc")
	if m.detail != nil {
		t.Fatal("esc did not close the detail page")
	}
	sec, idx := m.dipCursorPos()
	if sec != before || idx != beforeIdx {
		t.Errorf("esc returned to section %d/%d, want %d/%d — the cursor must not jump tables",
			sec, idx, before, beforeIdx)
	}
}

func TestDipDetailOfAnUnheldPrintingIsFine(t *testing.T) {
	m := colouredDipModel(t)
	m = key(m, "enter")
	if m.detail == nil {
		t.Fatal("enter opened nothing")
	}
	if len(m.detail.holdings) != 0 {
		t.Skip("fixture holds this printing; nothing to assert")
	}
	if out := strings.Join(m.detailLines(*m.detail, 120), "\n"); out == "" {
		t.Error("detail page rendered nothing for an unheld printing")
	}
}
