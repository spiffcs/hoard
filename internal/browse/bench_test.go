package browse

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/cardfilter"
	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

const (
	benchPrintings = 108122
	benchSets      = 987
	benchWidth     = 120
	benchHeight    = 45
)

var benchFinishes = []finish.Finish{finish.Nonfoil, finish.Foil}

func benchSetCode(i int) string { return fmt.Sprintf("s%03d", i%benchSets) }

func benchStore(printings int) *fakeStore {
	rows := make([]store.CollectionRow, printings)
	seen := map[string]*store.SetSummary{}
	order := make([]string, 0, benchSets)

	for i := range printings {
		code := benchSetCode(i)
		fin := benchFinishes[i%len(benchFinishes)]
		price := float64(i%400) / 4
		r := store.CollectionRow{Finish: fin, Quantity: 1 + i%3, Value: price}
		r.ScryfallID = fmt.Sprintf("id-%06d", i)
		r.Name = fmt.Sprintf("Benchmark Card %06d", i)
		r.SetCode = code
		r.CollectorNumber = fmt.Sprintf("%d", i%400)
		mana := "{2}{U}"
		r.ManaCost = &mana
		r.ColorIdentity = []string{"U", "B"}
		if fin == finish.Nonfoil {
			r.PriceUSD = &price
		} else {
			r.PriceUSDFoil = &price
		}
		rows[i] = r

		s, ok := seen[code]
		if !ok {
			seen[code] = &store.SetSummary{
				Code: code, Name: "Benchmark Set " + code, ReleasedAt: "2021-06-18",
			}
			order = append(order, code)
			s = seen[code]
		}
		s.Copies += r.Quantity
		s.Value += price
	}

	sets := make([]store.SetSummary, len(order))
	for i, code := range order {
		sets[i] = *seen[code]
	}

	return &fakeStore{
		collection: rows,
		sets:       sets,
		settings:   map[string]string{},
		enriched:   printings,
		totals: store.CollectionTotals{
			DistinctCards: printings, TotalCopies: printings, Value: 1000,
		},
	}
}

func benchModel(b *testing.B, printings int) Model {
	b.Helper()
	m, err := New(benchStore(printings), WithEnv(ui.Env{Color: true, Width: benchWidth}))
	if err != nil {
		b.Fatalf("New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: benchWidth, Height: benchHeight})
	return next.(Model)
}

func BenchmarkNew(b *testing.B) {
	st := benchStore(benchPrintings)
	b.ResetTimer()
	for b.Loop() {
		if _, err := New(st, WithEnv(ui.Env{Color: true, Width: benchWidth})); err != nil {
			b.Fatalf("New: %v", err)
		}
	}
}

func BenchmarkView(b *testing.B) {
	m := benchModel(b, benchPrintings)
	b.ResetTimer()
	for b.Loop() {
		_ = m.View()
	}
}

func BenchmarkContainerLines(b *testing.B) {
	m := benchModel(b, benchPrintings)
	if len(m.containers) < benchSets {
		b.Fatalf("%d containers, want the full set list (%d)", len(m.containers), benchSets)
	}
	b.ResetTimer()
	for b.Loop() {
		_ = m.containerLines(40)
	}
}

func BenchmarkVisibleRows(b *testing.B) {
	m := benchModel(b, benchPrintings)
	b.ResetTimer()
	for b.Loop() {
		_ = m.visibleRows()
	}
}

func BenchmarkLoadCards(b *testing.B) {
	m := benchModel(b, benchPrintings)
	m.cursor[paneContainers] = 0
	b.ResetTimer()
	for b.Loop() {
		if err := m.loadCards(); err != nil {
			b.Fatalf("loadCards: %v", err)
		}
	}
}

func BenchmarkApplyFilter(b *testing.B) {
	m := benchModel(b, benchPrintings)
	f, err := cardfilter.Parse("benchmark")
	if err != nil {
		b.Fatalf("Parse: %v", err)
	}
	m.filter = f
	b.ResetTimer()
	for b.Loop() {
		m.applyFilter()
	}
}

func BenchmarkMeasureCardCols(b *testing.B) {
	m := benchModel(b, benchPrintings)
	if len(m.filteredCards) < benchPrintings {
		b.Fatalf("%d filtered cards, want %d", len(m.filteredCards), benchPrintings)
	}
	b.ResetTimer()
	for b.Loop() {
		_ = measureCardCols(m.filteredCards)
	}
}

func BenchmarkSortHoldings(b *testing.B) {
	m := benchModel(b, benchPrintings)
	b.ResetTimer()
	for b.Loop() {
		m.sortHoldings()
	}
}

func BenchmarkCursorDownSets(b *testing.B) {
	m := benchModel(b, benchPrintings)
	m.focus = paneContainers
	start := m.cursor[paneContainers]
	moves := 0
	b.ResetTimer()
	for b.Loop() {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(Model)
		moves++
	}
	b.StopTimer()
	if moves > 0 && m.cursor[paneContainers] == start {
		b.Fatalf("cursor never moved off %d after %d key presses, so this "+
			"benchmark measures nothing", start, moves)
	}
}
