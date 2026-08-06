package tui

// The name step's command drawer, and the finish it made consistent.

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

// ':' is the palette key, but the field under it is what this view is mostly
// for — so it only opens the drawer when there is nothing typed yet.
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

// An empty query lists every applicable command, in registry order.
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

// Without a scanner there is nothing to scan or pair with, so those rows do
// not list at all — the palette answers "what can I do", and a row that only
// apologises is a worse answer than no row.
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

// Typing narrows, and enter runs whatever is highlighted.
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

// esc closes without running the highlighted command.
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

// :done and ctrl+d are the same thing, and with nothing pending both finish
// straight back to the collection.
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

// Queued cards no longer refuse the finish — they ask, through the same gate
// esc has always shown, and the prompt says what leaving would cost.
func TestDoneAsksRatherThanRefusingWhenCardsAreQueued(t *testing.T) {
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
			m = tc.act(m)

			if m.done {
				t.Fatal("queued cards must not be dropped without asking")
			}
			if m.state != stateLeaveConfirm {
				t.Fatalf("state = %v, want the leave gate", m.state)
			}
			v := m.viewContent()
			if !strings.Contains(v, "2 unsaved scans will be dropped") {
				t.Fatalf("the gate must say what leaving costs:\n%s", v)
			}
			// The old behaviour, which must not come back: a refusal that
			// sent you hunting for ctrl+s before you could leave.
			if strings.Contains(v, "finish or ctrl+s them first") {
				t.Fatalf("done refused instead of asking:\n%s", v)
			}

			// y leaves, anything else stays.
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
			if next.(model).done {
				t.Fatal("a stray key on the gate must stay")
			}
			next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
			if !next.(model).done {
				t.Fatal("y on the gate must finish")
			}
		})
	}
}

// The drawer replaces the hint line rather than stacking under it, and the
// hint line advertises the key when the drawer is closed.
func TestPaletteReplacesTheHintLine(t *testing.T) {
	m := paletteModel(t, &fakeScanner{})
	v := m.viewContent()
	if !strings.Contains(v, ": commands") {
		t.Fatalf("the closed view must advertise ':':\n%s", v)
	}
	// The palette leads the line, ahead of pairing — it is the one key that
	// means the same thing on every view in the program.
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
