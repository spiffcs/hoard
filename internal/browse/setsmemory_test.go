package browse

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func setsModel(t *testing.T, f *fakeStore) Model {
	t.Helper()
	m, err := New(f, WithEnv(ui.Env{Color: true}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	return next.(Model)
}

func TestSetsModeRemembersTheSetYouWereOn(t *testing.T) {
	m := atSet(t, setsModel(t, testStore()), "mh3")

	m = key(m, "B")
	if m.setsMode {
		t.Fatal("B did not leave sets mode")
	}

	m = key(m, "B")
	if !m.setsMode {
		t.Fatal("B did not return to sets mode")
	}
	sel := m.selectedContainer()
	if sel == nil || sel.setCode != "mh3" {
		t.Fatalf("came back to %+v, want the mh3 set still selected", sel)
	}
	if title, _ := m.viewHeader(); !strings.Contains(title, "MH3") {
		t.Errorf("header = %q, want mh3's cards listed again", title)
	}
	for _, c := range m.cards {
		if c.SetCode != "mh3" {
			t.Errorf("card pane shows %s/%s, want only mh3", c.SetCode, c.CollectorNumber)
		}
	}
}

func TestSetsModeFallsBackWhenTheRememberedSetIsGone(t *testing.T) {
	f := &fakeStore{collection: []store.CollectionRow{
		row("Sol Ring", "c21", "1", finish.Nonfoil, 1, 10),
		row("Solitude", "mh3", "1", finish.Nonfoil, 1, 30),
	}}
	m := atSet(t, setsModel(t, f), "mh3")

	m = key(m, "B")
	f.collection = f.collection[:1]
	m = key(m, "B")

	sel := m.selectedContainer()
	if sel == nil || sel.Kind != kindAllCards {
		t.Fatalf("came back to %+v, want the top row — mh3 is no longer held", sel)
	}
}

func TestSetsModeStartsAtTheTopWithNoSetToRemember(t *testing.T) {
	m := setsModel(t, testStore())

	m = key(m, "B")
	m = key(m, "B")

	sel := m.selectedContainer()
	if sel == nil || sel.Kind != kindAllCards {
		t.Fatalf("came back to %+v, want the top row — no set was ever picked", sel)
	}
}
