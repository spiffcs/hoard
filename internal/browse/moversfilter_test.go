package browse

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func moversFilterStore() *fakeStore {
	st := testStore()
	st.movers = []store.PriceChange{
		{ScryfallID: "riser-id", Name: "Riser", SetCode: "aaa", CollectorNumber: "1",
			Finish: finish.Nonfoil, Copies: 2, Old: 1, New: 5},
		{ScryfallID: "sinker-id", Name: "Sinker", SetCode: "bbb", CollectorNumber: "2",
			Finish: finish.Foil, Copies: 1, Old: 50, New: 10},
		{ScryfallID: "brainstorm-id", Name: "Brainstorm", SetCode: "aaa", CollectorNumber: "3",
			Finish: finish.Nonfoil, Copies: 4, Old: 2, New: 3},
	}
	return st
}

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

	out := m.View()
	if !strings.Contains(out, "Riser") {
		t.Errorf("the matching row must still render:\n%s", out)
	}
	for _, gone := range []string{"Sinker", "Brainstorm"} {
		if strings.Contains(out, gone) {
			t.Errorf("%q must not render under the filter %q:\n%s", gone, "ris", out)
		}
	}

	if !strings.Contains(out, "1 match") {
		t.Errorf("filter bar must count the movers it selected:\n%s", out)
	}
}

func TestMoversFilterCorrectsTheHeader(t *testing.T) {
	m := onMovers(t, moversFilterStore())
	m = typeFilter(m, "ris")

	_, totals := m.viewHeader()

	if !strings.Contains(totals, "1 moved") || !strings.Contains(totals, "$8.00") {
		t.Errorf("movers totals = %q, want the filtered count and net", totals)
	}
}

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

func TestMoversEmptiedByFilterReadsAsFiltered(t *testing.T) {
	m := onMovers(t, moversFilterStore())
	m = typeFilter(m, "zzz")
	if len(m.movers) != 0 {
		t.Fatalf("movers = %v, want nothing to match", moverNames(m.movers))
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	out := m.View()
	if strings.Contains(out, "no price movement") {
		t.Errorf("an emptied-by-filter movers list must not blame the window:\n%s", out)
	}
	if !strings.Contains(out, "no movers match") {
		t.Errorf("an emptied-by-filter movers list must say it is filtered:\n%s", out)
	}
}

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

			if got != m.watchTotalRows() {
				t.Errorf("watches count = %d, want %d", got, m.watchTotalRows())
			}
		default:

			want := len(m.marketAllRows) + len(m.marketAllComps)
			if got != want {
				t.Errorf("market count = %d, want %d", got, want)
			}
		}
	}
}

func moverNames(rows []store.PriceChange) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}

func TestMoversHeaderPutsTheNetLast(t *testing.T) {
	st := testStore()
	for i := range 80 {
		st.movers = append(st.movers, mover(fmt.Sprintf("M%02d-id", i), finish.Nonfoil, 1, 10, 12))
	}
	m := atAllCards(t, newTestModel(t, st))
	m = key(m, "v")

	_, totals := m.viewHeader()
	page := strings.Index(totals, " of 80")
	moved := strings.Index(totals, "80 moved")
	if page < 0 || moved < 0 {
		t.Fatalf("totals = %q, want both the page phrase and the summary", totals)
	}
	if page > moved {
		t.Errorf("totals = %q, want the page phrase before the summary", totals)
	}
	if strings.HasPrefix(totals, " · ") {
		t.Errorf("totals = %q, want no leading separator", totals)
	}
}

func TestMoversHeaderWithoutPaging(t *testing.T) {
	m := onMovers(t, moversFilterStore())
	_, totals := m.viewHeader()
	if strings.HasPrefix(totals, " · ") || strings.HasPrefix(totals, "·") {
		t.Errorf("totals = %q, want the summary to lead cleanly", totals)
	}
	if !strings.HasPrefix(totals, "3 moved") {
		t.Errorf("totals = %q, want it to open with the count", totals)
	}
}

func TestMoversHeaderColorsTheNet(t *testing.T) {

	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	paint := func(t *testing.T, rows []store.PriceChange) string {
		t.Helper()
		st := testStore()
		st.movers = rows
		m := atAllCards(t, newTestModel(t, st))
		m.env.Color = true
		m = key(m, "v")
		_, totals := m.viewHeader()
		return totals
	}
	gain := paint(t, []store.PriceChange{mover("up-id", finish.Nonfoil, 1, 10, 12)})
	loss := paint(t, []store.PriceChange{mover("dn-id", finish.Nonfoil, 1, 12, 10)})
	flat := paint(t, []store.PriceChange{
		mover("up-id", finish.Nonfoil, 1, 10, 12), mover("dn-id", finish.Nonfoil, 1, 12, 10)})

	env := ui.Env{Color: true}
	for _, tc := range []struct {
		name, totals, money string
		frac                float64
	}{
		{"a net gain", gain, "+$2.00", 1},
		{"a net loss", loss, "-$2.00", -1},
		{"a net of nothing", flat, "$0.00", 0},
	} {
		want := env.Diverge(tc.frac)(tc.money)
		if !strings.Contains(tc.totals, want) {
			t.Errorf("%s: totals = %q, want it to carry %q", tc.name, tc.totals, want)
		}
	}

	if env.Diverge(1)("x") == env.Diverge(-1)("x") {
		t.Fatal("the ramp paints gains and losses the same; this test proves nothing")
	}
}
