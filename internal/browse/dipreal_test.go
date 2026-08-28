package browse

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDipPagingWithMomentumSizedLikeTheRealCatalog(t *testing.T) {
	f := testStore()
	f.dips, f.momentum = manyDips(35), manyMomentum(614)

	m := atAllCards(t, newTestModel(t, f))
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	m = next.(Model)
	m = showDipView(t, m)

	if len(m.dips) != 35 {
		t.Errorf("dip table paged %d rows, want all 35 (under one page)", len(m.dips))
	}
	if len(m.momentum) != pageSize {
		t.Errorf("momentum table showed %d rows, want one page of %d", len(m.momentum), pageSize)
	}

	body := strings.Join(m.dipLines(110), "\n")
	if strings.Count(body, "\n") > 40 {
		t.Errorf("view rendered %d lines into a 40-row pane", strings.Count(body, "\n"))
	}
	if !strings.Contains(body, "of 614") {
		t.Errorf("momentum heading lost its total:\n%s", firstLines(body, 3))
	}

	m = key(m, "]")
	m = key(m, ">")
	if m.momentum[0].Name != "Mom 050" {
		t.Errorf("after ] then > momentum starts at %q, want Mom 050", m.momentum[0].Name)
	}
	if len(m.dips) != 35 {
		t.Errorf("turning the momentum page disturbed the dip table (%d rows)", len(m.dips))
	}
}
