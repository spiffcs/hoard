package browse

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/store"
)

func onDipRows(t *testing.T, dips, momentum []store.TrendRow) Model {
	t.Helper()
	f := testStore()
	f.dips, f.momentum = dips, momentum
	m := atAllCards(t, newTestModel(t, f))
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 40})
	m = next.(Model)
	(&m).showView(viewDip)
	if m.view != viewDip {
		t.Fatalf("view = %v, want dip", m.view)
	}
	return m
}

func sortKeysOffered(m Model) []string {
	var out []string
	for range 12 {
		label := strings.TrimSuffix(m.sortLabel(), " (reversed)")
		if _, key, ok := strings.Cut(label, " · "); ok {
			label = key
		}
		if slices.Contains(out, label) {
			break
		}
		out = append(out, label)
		m = key(m, "s")
	}
	return out
}

func trendNames(rows []store.TrendRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
	}
	return out
}

func TestDipTableSortsOnlyItsOwnColumns(t *testing.T) {
	m := onDipRows(t,
		[]store.TrendRow{dipRow("Alpha", 100, 40, 40), dipRow("Bravo", 80, 70, 70)},
		[]store.TrendRow{momentumRow("Charlie", 10, 20, 5)})

	got := sortKeysOffered(m)
	want := []string{"off high", "name", "set/num", "finish", "high", "low", "now"}
	if !slices.Equal(got, want) {
		t.Errorf("dip table offers %v, want %v — only the columns the table shows", got, want)
	}
}

func TestMomentumTableSortsOnlyItsOwnColumns(t *testing.T) {
	m := onDipRows(t,
		[]store.TrendRow{dipRow("Alpha", 100, 40, 40)},
		[]store.TrendRow{momentumRow("Charlie", 10, 20, 5), momentumRow("Delta", 5, 30, 9)})

	m.cursor[paneCards] = m.dipSectionsInfo()[secMomentum].curStart
	if sec, _ := m.dipCursorPos(); sec != int(secMomentum) {
		t.Fatalf("cursor landed in section %d, want the momentum table", sec)
	}

	got := sortKeysOffered(m)
	want := []string{"streak", "name", "set/num", "finish", "was", "now", "change"}
	if !slices.Equal(got, want) {
		t.Errorf("momentum table offers %v, want %v — only the columns the table shows", got, want)
	}
}

func TestDipSortReordersOnlyTheDipTable(t *testing.T) {
	m := onDipRows(t,
		[]store.TrendRow{
			dipRow("Charlie", 100, 40, 40),
			dipRow("Alpha", 80, 70, 70),
			dipRow("Bravo", 60, 30, 30),
		},
		[]store.TrendRow{momentumRow("Zulu", 10, 20, 5), momentumRow("Yankee", 5, 30, 9)})

	momentumBefore := trendNames(m.momentum)

	m = sortBy(t, m, "DIP · name")
	if got, want := trendNames(m.dips), []string{"Alpha", "Bravo", "Charlie"}; !slices.Equal(got, want) {
		t.Errorf("dips by name = %v, want %v", got, want)
	}
	if got := trendNames(m.momentum); !slices.Equal(got, momentumBefore) {
		t.Errorf("momentum = %v, want %v left alone by the dip table's sort", got, momentumBefore)
	}

	m = key(m, "S")
	if got, want := trendNames(m.dips), []string{"Charlie", "Bravo", "Alpha"}; !slices.Equal(got, want) {
		t.Errorf("dips reversed = %v, want %v", got, want)
	}
}

func TestMomentumSortReordersOnlyTheMomentumTable(t *testing.T) {
	m := onDipRows(t,
		[]store.TrendRow{dipRow("Charlie", 100, 40, 40), dipRow("Alpha", 80, 70, 70)},
		[]store.TrendRow{
			momentumRow("Zulu", 10, 20, 5),
			momentumRow("Yankee", 5, 30, 9),
			momentumRow("X-Ray", 8, 16, 2),
		})

	dipsBefore := trendNames(m.dips)
	m.cursor[paneCards] = m.dipSectionsInfo()[secMomentum].curStart

	m = sortBy(t, m, "MOMENTUM · name")
	if got, want := trendNames(m.momentum), []string{"X-Ray", "Yankee", "Zulu"}; !slices.Equal(got, want) {
		t.Errorf("momentum by name = %v, want %v", got, want)
	}
	if got := trendNames(m.dips); !slices.Equal(got, dipsBefore) {
		t.Errorf("dips = %v, want %v left alone by the momentum table's sort", got, dipsBefore)
	}
}
