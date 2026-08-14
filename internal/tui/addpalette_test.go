package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func paletteModel(t *testing.T, sc Scanner) model {
	t.Helper()
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, sc, "", nil)
	m.width, m.height = 100, 30
	return m
}

func typeRunes(m model, s string) model {
	for _, r := range s {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(model)
	}
	return m
}

func TestColonOpensPaletteOnlyOnAnEmptyField(t *testing.T) {
	m := paletteModel(t, &fakeScanner{})
	m = typeRunes(m, ":")
	if m.addPalette == nil {
		t.Fatal("':' on an empty field must open the palette")
	}

	m = paletteModel(t, &fakeScanner{})
	m = typeRunes(m, "sol:")
	if m.addPalette != nil {
		t.Fatal("':' mid-name must be a colon, not the palette")
	}
	if got := m.nameInput.Value(); got != "sol:" {
		t.Fatalf("name field = %q, want %q", got, "sol:")
	}
}

func TestPaletteListsScanPairDone(t *testing.T) {
	m := paletteModel(t, &fakeScanner{})
	m = typeRunes(m, ":")
	var got []string
	for _, item := range m.addPalette.items {
		got = append(got, item.Title)
	}
	want := []string{"Scan", "Pair", "Done"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("commands = %v, want %v", got, want)
	}
	if v := m.viewContent(); !strings.Contains(v, "enter run · esc close · ↑/↓ choose · type to narrow") {
		t.Fatalf("the shared palette help line is missing:\n%s", v)
	}
}

func TestPaletteHidesScanningWithoutAScanner(t *testing.T) {
	m := paletteModel(t, nil)
	m = typeRunes(m, ":")
	for _, item := range m.addPalette.items {
		if item.Title == "Scan" || item.Title == "Pair" {
			t.Fatalf("%q listed with no scanner available", item.Title)
		}
	}
	if len(m.addPalette.items) != 1 || m.addPalette.items[0].Title != "Done" {
		t.Fatalf("want Done alone, got %+v", m.addPalette.items)
	}
}

func TestPaletteNarrowsAndRunsPair(t *testing.T) {
	m := paletteModel(t, &fakeScanner{})
	m = typeRunes(m, ":pair")
	if n := len(m.addPalette.Matches()); n != 1 {
		t.Fatalf("matches = %d, want 1", n)
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if m.addPalette != nil {
		t.Fatal("running a command must close the drawer")
	}
	if m.state != statePairIntro {
		t.Fatalf("state = %v, want statePairIntro", m.state)
	}
}

func TestPaletteEscClosesWithoutRunning(t *testing.T) {
	m := paletteModel(t, &fakeScanner{})
	m = typeRunes(m, ":")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	if m.addPalette != nil {
		t.Fatal("esc must close the drawer")
	}
	if m.state != stateName {
		t.Fatalf("esc from the drawer left state %v, want stateName", m.state)
	}
}

func TestDoneFinishesWhenNothingIsPending(t *testing.T) {
	for _, tc := range []struct {
		name string
		act  func(model) model
	}{
		{"palette", func(m model) model {
			m = typeRunes(m, ":done")
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			return next.(model)
		}},
		{"ctrl+d", func(m model) model {
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
			return next.(model)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := tc.act(paletteModel(t, &fakeScanner{}))
			if !m.done {
				t.Fatal("finishing must set done")
			}
		})
	}
}

func TestDoneDropsQueuedCardsWithoutAskingStandalone(t *testing.T) {
	for _, tc := range []struct {
		name string
		act  func(model) model
	}{
		{"palette", func(m model) model {
			m = typeRunes(m, ":done")
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			return next.(model)
		}},
		{"ctrl+d", func(m model) model {
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
			return next.(model)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := paletteModel(t, &fakeScanner{})
			m.review = []queueItem{{ocrLine: "x"}, {ocrLine: "y"}}
			m.resolving = 1
			m = tc.act(m)

			if !m.done {
				t.Fatalf("queued cards must not hold the session open: state = %v", m.state)
			}

			var got string
			for _, e := range m.summary.Entries {
				if e.Kind == "discarded" {
					got = e.Line
				}
			}
			if got != "3 scanned cards discarded unprocessed" {
				t.Fatalf("summary discard line = %q, want all three accounted for", got)
			}

			if len(m.review) != 0 || m.resolving != 0 {
				t.Fatalf("queue survived the finish: %d queued, %d resolving",
					len(m.review), m.resolving)
			}
		})
	}
}

func TestPaletteReplacesTheHintLine(t *testing.T) {
	m := paletteModel(t, &fakeScanner{})
	v := m.viewContent()
	if !strings.Contains(v, ": commands") {
		t.Fatalf("the closed view must advertise ':':\n%s", v)
	}

	want := ": commands · ctrl+p pair · ctrl+o scan · enter search · ctrl+d done"
	if !strings.Contains(v, want) {
		t.Fatalf("help line is not %q:\n%s", want, v)
	}
	m = typeRunes(m, ":")
	v = m.viewContent()
	if strings.Contains(v, "enter search") {
		t.Fatalf("the hint line is still under the open drawer:\n%s", v)
	}
	if !strings.Contains(v, "▸ Scan") {
		t.Fatalf("no cursor on the first row:\n%s", v)
	}
}
