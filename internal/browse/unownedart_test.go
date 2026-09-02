package browse

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/ui"
)

func catalogOnlyPrint(name, num, image string) scryfall.Card {
	c := catalogPrint(name, num, 3.25)
	c.ImageURI = image
	return c
}

func unownedArtModel(t *testing.T, prints ...scryfall.Card) (Model, *artRecorder) {
	t.Helper()

	f := eoeStore()
	f.unowned["eoe"] = nil
	f.absent = map[string]bool{}
	for _, p := range prints {
		f.absent[p.ID] = true
	}

	rec := &artRecorder{}
	m, err := New(f, WithEnv(ui.Env{Color: true}), WithSetPrints(eoePrints(prints...)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.imgTier = ui.ImageHalfblock
	m.imageFetch = rec.fetch

	next, cmd := m.Update(tea.WindowSizeMsg{Width: 100, Height: 70})
	m = pump(t, next.(Model), cmd)
	m = atSet(t, m, "eoe")
	if m.previewRows() <= 0 {
		t.Fatalf("this test needs a terminal tall enough for the art pane")
	}
	return m, rec
}

func artRows(t *testing.T, m Model) int {
	t.Helper()
	n := 0
	for _, l := range leftColumn(t, m) {
		if strings.Contains(l, artMark) {
			n++
		}
	}
	return n
}

func TestArtShowsForAPrintingOnlyTheCatalogKnows(t *testing.T) {
	nexus := catalogOnlyPrint("Cosmic Nexus", "4",
		"https://cards.scryfall.io/normal/front/n/e/Cosmic Nexus-id.jpg")
	m, rec := unownedArtModel(t, nexus)

	m = pumpKey(t, m, "tab")
	if got := artRows(t, m); got == 0 {
		t.Fatalf("no art for the owned card this test compares against:\n%s", m.View())
	}

	m = pumpKey(t, m, "b")
	got := cardNames(m.filteredCards)
	if len(got) != 1 || got[0] != "Cosmic Nexus" {
		t.Fatalf("unowned cards = %v, want just the catalog-only Cosmic Nexus", got)
	}

	if n := artRows(t, m); n == 0 {
		t.Errorf("a printing only the catalog knows draws no art at all:\n%s", m.View())
	}
	if !strings.Contains(strings.Join(rec.ids(), " "), nexus.ID) {
		t.Errorf("art was fetched for %v, never for the highlighted %q", rec.ids(), nexus.ID)
	}
}
