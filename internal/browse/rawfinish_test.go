package browse

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/finish"
)

func rawOnlyCommand(t *testing.T, m *Model) *command {
	t.Helper()
	for i := range m.commands {
		if m.commands[i].id == "filter.raw" {
			return &m.commands[i]
		}
	}
	t.Fatal("no filter.raw command in the registry")
	return nil
}

func runRawOnly(t *testing.T, m Model) Model {
	t.Helper()
	c := rawOnlyCommand(t, &m)
	if !c.applies(&m) {
		t.Fatalf("filter.raw does not apply on view %v", m.view)
	}
	c.run(&m)
	return m
}

func cardNames(rows []card) []string {
	out := make([]string, 0, len(rows))
	for _, c := range rows {
		out = append(out, c.Name)
	}
	return out
}

func TestRawOnlyCommandHidesPremiumFinishes(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	if got := cardNames(m.filteredCards); len(got) != 6 {
		t.Fatalf("seeded holdings = %v, want six rows including two foils", got)
	}

	m = runRawOnly(t, m)

	if got := cardNames(m.filteredCards); len(got) != 4 {
		t.Errorf("rows = %v, want the four nonfoil rows alone", got)
	}
	for _, c := range m.filteredCards {
		if c.Finish != finish.Nonfoil {
			t.Errorf("%s is %q, want only finishes shown as '-'", c.Name, c.Finish)
		}
	}
	out := m.View()
	if !strings.Contains(out, "Bitterblossom") {
		t.Errorf("the raw rows must still render:\n%s", out)
	}
	for _, gone := range []string{"Ancient Tomb", "Force of Will"} {
		if strings.Contains(out, gone) {
			t.Errorf("the foil row %q must not render:\n%s", gone, out)
		}
	}

	if !strings.Contains(m.filter.raw, "finish:nonfoil") {
		t.Errorf("filter.raw = %q, want it to carry finish:nonfoil", m.filter.raw)
	}
	if m.filterText != m.filter.raw {
		t.Errorf("filterText = %q, want it to match the live filter %q", m.filterText, m.filter.raw)
	}

	m = runRawOnly(t, m)

	if got := cardNames(m.filteredCards); len(got) != 6 {
		t.Errorf("rows after toggling off = %v, want all six back", got)
	}
	if !m.filter.empty() || m.filterText != "" {
		t.Errorf("toggling off left filter %q / text %q, want both clear", m.filter.raw, m.filterText)
	}
}

func TestRawOnlyCommandComposesWithTypedFilter(t *testing.T) {
	m := atAllCards(t, newTestModel(t, testStore()))
	m = typeFilter(m, "set:uma")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if got := cardNames(m.filteredCards); len(got) != 2 {
		t.Fatalf("set:uma rows = %v, want the nonfoil and the foil from uma", got)
	}

	m = runRawOnly(t, m)

	if got := cardNames(m.filteredCards); len(got) != 1 || got[0] != "Bitterblossom" {
		t.Errorf("rows = %v, want uma's nonfoil row alone", got)
	}
	if m.filterText != "set:uma finish:nonfoil" {
		t.Errorf("filterText = %q, want the typed term kept alongside finish:nonfoil", m.filterText)
	}

	m = runRawOnly(t, m)

	if m.filterText != "set:uma" {
		t.Errorf("filterText = %q, want the typed term left standing alone", m.filterText)
	}
	if got := cardNames(m.filteredCards); len(got) != 2 {
		t.Errorf("rows = %v, want both set:uma rows back", got)
	}
}

func TestRawOnlyCommandIsInThePalette(t *testing.T) {
	m := openTestPalette(t)
	for _, r := range "rawonly" {
		m = key(m, string(r))
	}
	if len(m.palette.matches) == 0 {
		t.Fatal("query 'rawonly' matched no command")
	}
	if top := m.commands[m.palette.matches[0].index]; top.id != "filter.raw" {
		t.Errorf("top match = %s, want filter.raw", top.id)
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if !strings.Contains(m.filter.raw, "finish:nonfoil") {
		t.Errorf("running it from the palette left filter %q, want finish:nonfoil", m.filter.raw)
	}
}
