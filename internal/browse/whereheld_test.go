package browse

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func whereStore() *fakeStore {
	f := testStore()
	f.deckCards = map[int64][]store.EntryView{
		201: {entry("Sol Ring", "main", finish.Nonfoil, 1, 10)},
		202: {entry("Solitude", "main", finish.Nonfoil, 1, 34)},
	}
	return f
}

func cardRow(t *testing.T, m Model, name string) string {
	t.Helper()
	body := strings.Join(m.cardLines(120), "\n")
	return strings.TrimRight(ansi.Strip(lineFor(t, body, name)), " ")
}

func TestAllCardsAddsAWhereColumnAfterValue(t *testing.T) {
	m := atAllCards(t, newTestModel(t, whereStore()))

	head := ansi.Strip(m.cardLines(120)[0])
	where := strings.Index(head, "WHERE")
	if where < 0 {
		t.Fatalf("header = %q, want a WHERE column naming the binder or deck", head)
	}
	if value := strings.Index(head, "VALUE"); where < value {
		t.Errorf("header = %q, want WHERE to the right of VALUE", head)
	}
}

func TestAllCardsRowsEndWithTheContainerHoldingThem(t *testing.T) {
	m := atAllCards(t, newTestModel(t, whereStore()))

	for _, tc := range []struct{ card, want string }{
		{"Bitterblossom", store.LooseName},
		{"Solitude", "Rich Deck"},
	} {
		if got := cardRow(t, m, tc.card); !strings.HasSuffix(got, tc.want) {
			t.Errorf("%s row = %q, want it to end in %q", tc.card, got, tc.want)
		}
	}
}

func TestAllCardsRowHeldInTwoPlacesCountsTheRest(t *testing.T) {
	m := atAllCards(t, newTestModel(t, whereStore()))

	want := store.LooseName + " +1"
	if got := cardRow(t, m, "Sol Ring"); !strings.HasSuffix(got, want) {
		t.Errorf("Sol Ring row = %q, want it to end in %q — one row merges %s and Cheap Deck",
			got, want, store.LooseName)
	}
}

func TestBinderViewHasNoWhereColumn(t *testing.T) {
	m := newTestModel(t, whereStore())

	if head := ansi.Strip(m.cardLines(120)[0]); strings.Contains(head, "WHERE") {
		t.Errorf("header = %q, want no WHERE column: every row in a binder is in that binder", head)
	}
}

const longContainerName = "Mono Blue Tempo Ponza Sideboard Testing Binder"

func longWhereStore() *fakeStore {
	f := whereStore()
	f.decks = append(f.decks, deck(203, longContainerName, 1, 5))
	f.deckCards[203] = []store.EntryView{entry("Griselbrand", "main", finish.Nonfoil, 1, 20)}
	return f
}

func headerTitles(t *testing.T, m Model, width int) []string {
	t.Helper()
	return strings.Fields(ansi.Strip(m.cardLines(width)[0]))
}

func TestAllCardsClipsALongWhereValue(t *testing.T) {
	m := atAllCards(t, newTestModel(t, longWhereStore()))

	got := cardRow(t, m, "Griselbrand")
	if strings.Contains(got, longContainerName) {
		t.Fatalf("Griselbrand row = %q, want the container name clipped, not printed in full", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("Griselbrand row = %q, want it to end in an ellipsis marking the clip", got)
	}
	head, _ := strings.CutSuffix(got, "…")
	if i := strings.LastIndex(head, longContainerName[:4]); i < 0 ||
		!strings.HasPrefix(longContainerName, strings.TrimSpace(head[i:])) {
		t.Errorf("Griselbrand row = %q, want the clipped text to be a leading run of %q",
			got, longContainerName)
	}
}

func TestALongWhereValueDoesNotCostAnotherColumn(t *testing.T) {
	const width = 80

	want := headerTitles(t, atAllCards(t, newTestModel(t, whereStore())), width)
	got := headerTitles(t, atAllCards(t, newTestModel(t, longWhereStore())), width)

	if !slices.Equal(got, want) {
		t.Errorf("header = %v, want %v: a long container name must not squeeze out other columns",
			got, want)
	}
}

func whereCell(t *testing.T, m Model, width int, rowKey string) string {
	t.Helper()
	lines := m.cardLines(width)
	start := strings.Index(ansi.Strip(lines[0]), "WHERE")
	if start < 0 {
		t.Fatalf("no WHERE column in header %q", ansi.Strip(lines[0]))
	}
	row := []rune(ansi.Strip(lineFor(t, strings.Join(lines, "\n"), rowKey)))
	return strings.TrimRight(string(row[min(start, len(row)):]), " ")
}

const outlierContainerName = "Tricky Terrain Collector's Edition"

func outlierWhereStore() *fakeStore {
	f := whereStore()
	f.decks = append(f.decks, deck(203, outlierContainerName, 1, 5))
	f.deckCards[203] = []store.EntryView{entry("Griselbrand", "main", finish.Nonfoil, 1, 20)}
	return f
}

func TestWhereClipsAContainerNameThatIsTooLong(t *testing.T) {
	m := atAllCards(t, newTestModel(t, outlierWhereStore()))

	const pane = 128
	if got := whereCell(t, m, pane, "Solitude"); got != "Rich Deck" {
		t.Errorf("Solitude WHERE = %q, want %q shown in full", got, "Rich Deck")
	}
	got := whereCell(t, m, pane, "$20.00")
	if ui.Width(got) > ui.Width("Rich Deck")+2 {
		t.Errorf("outlier WHERE = %q (width %d), want it clipped to the width the other rows "+
			"need (%d): one long name must not set the column width for every row",
			got, ui.Width(got), ui.Width("Rich Deck"))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("outlier WHERE = %q, want an ellipsis marking the clip", got)
	}
}

func cappedWhereStore() *fakeStore {
	f := whereStore()
	f.uncounted = map[int64]bool{defaultBinderID: true}
	f.binders = map[int64]string{300: "Sideboard AB"}
	f.decks = []store.DeckSummary{deck(201, "Cheap Deck", 100, 50), deck(202, "Sideboard A", 100, 500)}
	return f
}

func TestWhereShowsElevenCharactersAtMost(t *testing.T) {
	m := atAllCards(t, newTestModel(t, cappedWhereStore()))

	const pane = 128
	if got := whereCell(t, m, pane, "Solitude"); got != "Sideboard A" {
		t.Errorf("Solitude WHERE = %q, want %q: eleven characters is the widest the column "+
			"shows in full", got, "Sideboard A")
	}

	got := whereCell(t, m, pane, "Bitterblossom")
	if got == "Sideboard AB" {
		t.Fatalf("Bitterblossom WHERE = %q, want it clipped: twelve characters is past the cap",
			got)
	}
	if !strings.HasSuffix(got, "…") || ui.Width(got) > 11 {
		t.Errorf("Bitterblossom WHERE = %q (width %d), want at most 11 wide ending in an ellipsis",
			got, ui.Width(got))
	}
}
