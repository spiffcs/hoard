package browse

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/store"
)

// moversFilterStore seeds three movers whose names, sets and finishes are
// pairwise distinguishable, so one query can be shown to select a subset
// rather than happening to select everything.
func moversFilterStore() *fakeStore {
	st := testStore()
	st.movers = []store.PriceChange{
		{ScryfallID: "riser-id", Name: "Riser", SetCode: "aaa", CollectorNumber: "1",
			Finish: "nonfoil", Copies: 2, Old: 1, New: 5},
		{ScryfallID: "sinker-id", Name: "Sinker", SetCode: "bbb", CollectorNumber: "2",
			Finish: "foil", Copies: 1, Old: 50, New: 10},
		{ScryfallID: "brainstorm-id", Name: "Brainstorm", SetCode: "aaa", CollectorNumber: "3",
			Finish: "nonfoil", Copies: 4, Old: 2, New: 3},
	}
	return st
}

// onMovers puts the model on the movers view with every seeded row visible.
func onMovers(t *testing.T, st *fakeStore) Model {
	t.Helper()
	m := atAllCards(t, newTestModel(t, st))
	m = key(m, "v")
	if m.view != viewMovers {
		t.Fatalf("view = %v, want movers", m.view)
	}
	if len(m.movers) != 3 {
		t.Fatalf("movers = %d rows, want the 3 seeded", len(m.movers))
	}
	return m
}

// THE BUG: `/` on the movers view opens the bar and takes the query, but
// nothing consumes it when the movers rows are built — so the list does not
// narrow the way All Cards does.
func TestMoversFilterNarrowsLive(t *testing.T) {
	m := onMovers(t, moversFilterStore())
	m = typeFilter(m, "ris")

	if m.mode() != modeFilter {
		t.Fatalf("mode = %v, want the filter bar open", m.mode())
	}
	if got, want := len(m.filteredMovers), 1; got != want {
		t.Errorf("filteredMovers = %d, want %d", got, want)
	}
	if len(m.movers) != 1 || m.movers[0].Name != "Riser" {
		t.Fatalf("movers = %+v, want only Riser", moverNames(m.movers))
	}

	// Rendered, not just held: the pane on screen is what the owner reads.
	out := m.View()
	if !strings.Contains(out, "Riser") {
		t.Errorf("the matching row must still render:\n%s", out)
	}
	for _, gone := range []string{"Sinker", "Brainstorm"} {
		if strings.Contains(out, gone) {
			t.Errorf("%q must not render under the filter %q:\n%s", gone, "ris", out)
		}
	}

	// The bar's own count speaks for the list it is filtering.
	if !strings.Contains(out, "1 match") {
		t.Errorf("filter bar must count the movers it selected:\n%s", out)
	}
}

// The header's count and net describe the filtered ranking, not the pristine
// one — a filtered view that still claims 3 moved is a lie about the hoard.
func TestMoversFilterCorrectsTheHeader(t *testing.T) {
	m := onMovers(t, moversFilterStore())
	m = typeFilter(m, "ris")

	_, totals := m.viewHeader()
	// Riser alone: 2 copies × +$4 = +$8.
	if !strings.Contains(totals, "1 moved") || !strings.Contains(totals, "$8.00") {
		t.Errorf("movers totals = %q, want the filtered count and net", totals)
	}
}

// esc clears the query and restores every row, as on All Cards.
func TestMoversFilterEscRestores(t *testing.T) {
	m := onMovers(t, moversFilterStore())
	m = typeFilter(m, "ris")
	if len(m.movers) != 1 {
		t.Fatalf("movers = %v, want the filter to have bitten first", moverNames(m.movers))
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.mode() == modeFilter {
		t.Error("esc must close the bar")
	}
	if len(m.movers) != 3 {
		t.Errorf("movers = %v, want all 3 back", moverNames(m.movers))
	}
}

// The query survives closing the bar with enter — the bar edits the query,
// it is not the query.
func TestMoversFilterSurvivesEnter(t *testing.T) {
	m := onMovers(t, moversFilterStore())
	m = typeFilter(m, "ris")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.mode() == modeFilter {
		t.Fatal("enter must close the bar")
	}
	if len(m.movers) != 1 || m.movers[0].Name != "Riser" {
		t.Errorf("movers = %v, want the query still applied", moverNames(m.movers))
	}
}

// The grammar the movers row can answer: set, finish and qty read off the
// row itself, and trait terms come from the id set the catalog matched.
func TestMoversFilterHonoursFieldTerms(t *testing.T) {
	tests := []struct {
		query string
		want  []string
	}{
		{"set:aaa", []string{"Riser", "Brainstorm"}},
		{"finish:foil", []string{"Sinker"}},
		{"qty>=2", []string{"Riser", "Brainstorm"}},
		{"price>4", []string{"Riser", "Sinker"}},
		{"name:sink", []string{"Sinker"}},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			m := onMovers(t, moversFilterStore())
			m = typeFilter(m, tt.query)
			if m.filterErr != "" {
				t.Fatalf("filter error: %s", m.filterErr)
			}
			got := moverNames(m.movers)
			if len(got) != len(tt.want) {
				t.Fatalf("movers = %v, want %v", got, tt.want)
			}
			for _, w := range tt.want {
				if !slices.Contains(got, w) {
					t.Errorf("movers = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// A trait term is answered by the catalog id set, which a mover row carries
// a scryfall id for — so the trait half of the grammar works unchanged.
func TestMoversFilterHonoursTraitTerms(t *testing.T) {
	st := moversFilterStore()
	st.traits = map[string][]string{
		"riser-id":      {"mythic", "creature"},
		"sinker-id":     {"rare", "instant"},
		"brainstorm-id": {"common", "instant"},
	}
	m := onMovers(t, st)
	m = typeFilter(m, "rarity:mythic")
	if m.filterErr != "" {
		t.Fatalf("filter error: %s", m.filterErr)
	}
	if got := moverNames(m.movers); len(got) != 1 || got[0] != "Riser" {
		t.Errorf("movers = %v, want only the mythic", got)
	}
}

// A movers list emptied by the query says so. "no price movement in this
// window" would send the reader to F to fetch prices they already have.
func TestMoversEmptiedByFilterReadsAsFiltered(t *testing.T) {
	m := onMovers(t, moversFilterStore())
	m = typeFilter(m, "zzz")
	if len(m.movers) != 0 {
		t.Fatalf("movers = %v, want nothing to match", moverNames(m.movers))
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // close the bar; the query stays
	m = next.(Model)
	out := m.View()
	if strings.Contains(out, "no price movement") {
		t.Errorf("an emptied-by-filter movers list must not blame the window:\n%s", out)
	}
	if !strings.Contains(out, "no movers match") {
		t.Errorf("an emptied-by-filter movers list must say it is filtered:\n%s", out)
	}
}

// board is the one term a mover row cannot answer — store.Movers sums copies
// across every holding of a card+finish, so there is no board to compare. The
// bar says so instead of quietly selecting nothing.
func TestMoversFilterSaysWhatItCannotAnswer(t *testing.T) {
	m := onMovers(t, moversFilterStore())
	m = typeFilter(m, "board:main")
	if m.filterErr != "" {
		t.Fatalf("board:main must parse — it is a real key: %s", m.filterErr)
	}
	out := m.View()
	if !strings.Contains(out, "does not apply on movers") {
		t.Errorf("the bar must name the term movers cannot answer:\n%s", out)
	}
}

// Every view the query narrows counts for itself, and each counts its own
// whole result — a number describing the holdings pane, quoted over a table
// the query is not touching, would be worse than no number. With market
// fixed there is no view left returning -1, and the -1 path stays for the
// next one added rather than because a view uses it today.
func TestFilterMatchCountOnlyWhereItApplies(t *testing.T) {
	st := moversFilterStore()
	m := atAllCards(t, newTestModel(t, st))
	if got := m.filterMatchCount(); got != len(m.filteredCards) {
		t.Errorf("holdings count = %d, want %d", got, len(m.filteredCards))
	}
	for _, v := range []viewMode{viewMovers, viewWatches, viewMarket} {
		m.view = v
		got := m.filterMatchCount()
		switch v {
		case viewMovers:
			if got != len(m.filteredMovers) {
				t.Errorf("movers count = %d, want %d", got, len(m.filteredMovers))
			}
		case viewWatches:
			// The watches screen consumes the query across all three of its
			// tables, so it does have a number to give — one for the screen.
			if got != m.watchTotalRows() {
				t.Errorf("watches count = %d, want %d", got, m.watchTotalRows())
			}
		default:
			// Market counts the same way, over the full rankings rather than
			// the three pages on screen.
			want := len(m.marketAllRows) + len(m.marketAllComps)
			if got != want {
				t.Errorf("market count = %d, want %d", got, want)
			}
		}
	}
}

// moverNames is names' twin for the movers rows; the two cannot share one
// generic without naming a field, which is the whole difference between them.
func moverNames(rows []store.PriceChange) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}
