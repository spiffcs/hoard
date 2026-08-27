package browse

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/store"
)

func manyDips(n int) []store.TrendRow {
	out := make([]store.TrendRow, n)
	for i := range out {
		out[i] = dipRow(fmt.Sprintf("Dip %03d", i), 100, float64(50+i), float64(50+i))
	}
	return out
}

func manyMomentum(n int) []store.TrendRow {
	out := make([]store.TrendRow, n)
	for i := range out {
		out[i] = momentumRow(fmt.Sprintf("Mom %03d", i), 10, float64(20+i), n-i)
	}
	return out
}

func onDip(t *testing.T, dips, momentum int) Model {
	t.Helper()
	f := testStore()
	f.dips, f.momentum = manyDips(dips), manyMomentum(momentum)

	m := atAllCards(t, newTestModel(t, f))
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	m = next.(Model)
	(&m).showView(viewDip)
	if m.view != viewDip {
		t.Fatalf("view = %v, want dip", m.view)
	}
	return m
}

func TestDipSectionsPageIndependently(t *testing.T) {
	m := onDip(t, 120, 70)

	if len(m.dips) != pageSize || len(m.momentum) != pageSize {
		t.Fatalf("first page = %d dips, %d momentum, want %d each",
			len(m.dips), len(m.momentum), pageSize)
	}
	if m.dips[0].Name != "Dip 000" || m.momentum[0].Name != "Mom 000" {
		t.Fatalf("first page starts at %q / %q, want the first row of each",
			m.dips[0].Name, m.momentum[0].Name)
	}

	m = key(m, ">")
	if m.dips[0].Name != "Dip 050" {
		t.Errorf("after > the dip table starts at %q, want Dip 050", m.dips[0].Name)
	}
	if m.momentum[0].Name != "Mom 000" {
		t.Errorf("> turned the momentum page too (%q); each table pages on its own",
			m.momentum[0].Name)
	}

	m = key(m, "<")
	if m.dips[0].Name != "Dip 000" {
		t.Errorf("after < the dip table starts at %q, want Dip 000", m.dips[0].Name)
	}
}

func TestDipPagingStopsAtTheEnds(t *testing.T) {
	m := onDip(t, 60, 10)

	m = key(m, "<")
	if m.dips[0].Name != "Dip 000" {
		t.Errorf("< on the first page moved to %q", m.dips[0].Name)
	}
	m = key(m, ">")
	m = key(m, ">")
	if got := len(m.dips); got != 10 {
		t.Errorf("last page has %d rows, want the 10 remaining", got)
	}
	if m.dips[0].Name != "Dip 050" {
		t.Errorf("last page starts at %q, want Dip 050", m.dips[0].Name)
	}
}

func TestDipJumpsBetweenTables(t *testing.T) {
	m := onDip(t, 20, 20)

	sec, _ := m.dipCursorPos()
	if sec != int(secDip) {
		t.Fatalf("cursor starts in section %d, want the dip table", sec)
	}

	m = key(m, "]")
	if sec, _ := m.dipCursorPos(); sec != int(secMomentum) {
		t.Errorf("] left the cursor in section %d, want momentum", sec)
	}
	m = key(m, "[")
	if sec, _ := m.dipCursorPos(); sec != int(secDip) {
		t.Errorf("[ left the cursor in section %d, want dip", sec)
	}
}

func TestDipHeadingsCarryThePageFragment(t *testing.T) {
	m := onDip(t, 120, 70)

	body := strings.Join(m.dipLines(110), "\n")
	if !strings.Contains(body, "of 120") {
		t.Errorf("dip heading is missing its row count:\n%s", firstLines(body, 4))
	}
	if !strings.Contains(body, "of 70") {
		t.Errorf("momentum heading is missing its row count:\n%s", firstLines(body, 4))
	}
}

func firstLines(s string, n int) string {
	parts := strings.SplitN(s, "\n", n+1)
	if len(parts) > n {
		parts = parts[:n]
	}
	return strings.Join(parts, "\n")
}

func TestDipHeaderCountsEveryRowNotJustThePage(t *testing.T) {
	m := onDip(t, 120, 70)

	_, totals := m.dipHeader()
	if !strings.Contains(totals, "120 at the floor") {
		t.Errorf("header = %q, want all 120 dips counted, not the 50 on screen", totals)
	}
	if !strings.Contains(totals, "70 climbing") {
		t.Errorf("header = %q, want all 70 momentum rows counted, not the 50 on screen", totals)
	}

	m.turnDipPage(1)
	_, paged := m.dipHeader()
	if paged != totals {
		t.Errorf("turning a page changed the header from %q to %q; the totals are not per-page",
			totals, paged)
	}
}

func TestDipHeaderFollowsTheFilter(t *testing.T) {
	m := onDip(t, 120, 70)

	m.pennyLimit = 60
	m.deriveView()

	_, totals := m.dipHeader()
	if !strings.Contains(totals, "109 at the floor") {
		t.Errorf("header = %q, want the 11 gated dips dropped from the count", totals)
	}
	if !strings.Contains(totals, "29 climbing") {
		t.Errorf("header = %q, want the 41 gated momentum rows dropped from the count", totals)
	}
}
