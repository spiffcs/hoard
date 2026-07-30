package browse

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/arbitrage"
	"github.com/spiffcs/hoard/internal/store"
)

// fakeStore drives the model without a database, the way internal/tui's tests
// drive the add cascade with a fake Searcher.
type fakeStore struct {
	totals     store.CollectionTotals
	decks      []store.DeckSummary
	collection []store.CollectionRow
	deckCards  map[int64][]store.EntryView

	// traits maps a scryfall id to the trait terms it satisfies, standing in
	// for the generated columns without a database.
	traits   map[string][]string
	enriched int

	err error // when set, every read fails

	// matchCalls counts catalog queries, so a test can assert that a plain name
	// search never reaches the database.
	matchCalls int

	movers   []store.PriceChange
	unpriced []store.UnpricedRow

	// holdings tracks SetHoldingQuantity so an edit and its undo can be
	// observed without a database.
	holdings    map[string]int
	removedCard map[string][]store.Holding
	removedDeck int64
}

func (f *fakeStore) MatchingCardIDs(tf store.TraitFilter) (map[string]bool, error) {
	f.matchCalls++
	if f.err != nil {
		return nil, f.err
	}
	want := append(append([]string{}, tf.Rarities...), tf.Types...)
	want = append(want, tf.Colors...)
	out := map[string]bool{}
	for id, have := range f.traits {
		ok := true
		for _, w := range want {
			if !slices.Contains(have, strings.ToLower(w)) {
				ok = false
				break
			}
		}
		if ok {
			out[id] = true
		}
	}
	return out, nil
}

func (f *fakeStore) Movers(since string) ([]store.PriceChange, error) {
	return f.movers, f.err
}
func (f *fakeStore) Unpriced() ([]store.UnpricedRow, error) { return f.unpriced, f.err }

func (f *fakeStore) EnrichedCount() (int, int, error) {
	return f.enriched, len(f.collection), f.err
}

func (f *fakeStore) CollectionTotals() (store.CollectionTotals, error) {
	return f.totals, f.err
}
func (f *fakeStore) ListDecks() ([]store.DeckSummary, error) { return f.decks, f.err }
func (f *fakeStore) ListCollectionByFinish() ([]store.CollectionRow, error) {
	return f.collection, f.err
}
func (f *fakeStore) DeckEntries(id int64) ([]store.EntryView, error) {
	return f.deckCards[id], f.err
}
func (f *fakeStore) CardDetail(string) (store.CardDetail, error) {
	return store.CardDetail{}, f.err
}
func (f *fakeStore) HoldingsOf(string) ([]store.Holding, error)             { return nil, f.err }
func (f *fakeStore) PriceSeries(string, string) ([]store.PricePoint, error) { return nil, f.err }

// SetHoldingQuantity mutates the fixture the way the store mutates the
// database, so an edit followed by an undo is observable end to end.
func (f *fakeStore) SetHoldingQuantity(id, finish string, qty int) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	var previous int
	var found bool
	out := f.collection[:0]
	for _, r := range f.collection {
		if r.ScryfallID == id && r.Finish == finish {
			previous, found = r.Quantity, true
			if qty == 0 {
				continue // removed, not stored as zero
			}
			unit := r.Value / float64(max(r.Quantity, 1))
			r.Quantity = qty
			r.Value = unit * float64(qty)
		}
		out = append(out, r)
	}
	f.collection = out

	// The real store upserts, so setting a quantity on a printing with no row
	// creates one — which is exactly what undoing a zeroed holding depends on.
	if !found && qty > 0 {
		f.collection = append(f.collection,
			row(strings.TrimSuffix(id, "-id"), "uma", "1", finish, qty, float64(qty)))
	}
	return previous, nil
}

func (f *fakeStore) RemoveFromCollection(id string) ([]store.Holding, error) {
	if f.err != nil {
		return nil, f.err
	}
	var removed []store.Holding
	var kept []store.CollectionRow
	for _, r := range f.collection {
		if r.ScryfallID == id {
			removed = append(removed, store.Holding{
				ContainerKind: store.KindCollection, Finish: r.Finish,
				Board: "main", Quantity: r.Quantity,
			})
			continue
		}
		kept = append(kept, r)
	}
	f.collection = kept
	if f.removedCard == nil {
		f.removedCard = map[string][]store.Holding{}
	}
	f.removedCard[id] = removed
	return removed, nil
}

func (f *fakeStore) RestoreHoldings(id string, holdings []store.Holding) error {
	if f.err != nil {
		return f.err
	}
	for _, h := range holdings {
		f.collection = append(f.collection,
			row(strings.TrimSuffix(id, "-id"), "uma", "1", h.Finish, h.Quantity, float64(h.Quantity)))
	}
	return nil
}
func (f *fakeStore) RemoveContainer(id int64) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.removedDeck = id
	delete(f.deckCards, id)
	for i, d := range f.decks {
		if d.ID == id {
			f.decks = append(f.decks[:i], f.decks[i+1:]...)
			break
		}
	}
	return 1, nil
}

func (f *fakeStore) UpsertDeck(meta store.DeckMeta, entries []store.Entry) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	id := f.removedDeck
	views := make([]store.EntryView, 0, len(entries))
	var copies int
	for _, e := range entries {
		views = append(views, entry(e.ScryfallID[:len(e.ScryfallID)-3], e.Board, e.Finish, e.Quantity, 1))
		copies += e.Quantity
	}
	f.deckCards[id] = views
	f.decks = append(f.decks, deck(id, meta.Name, copies, 0))
	return id, nil
}

func price(v float64) *float64 { return &v }

func deck(id int64, name string, copies int, value float64) store.DeckSummary {
	d := store.DeckSummary{DistinctCards: copies, TotalCopies: copies, Value: value}
	d.ID = id
	d.Name = name
	d.Kind = store.KindDeck
	return d
}

func row(name, set, num, finish string, qty int, value float64) store.CollectionRow {
	r := store.CollectionRow{Finish: finish, Quantity: qty, Value: value}
	r.ScryfallID = name + "-id"
	r.Name = name
	r.SetCode = set
	r.CollectorNumber = num
	r.PriceUSD = price(value / float64(max(qty, 1)))
	return r
}

// entry prices the finish it is actually held in. Putting the figure in
// PriceUSD for a foil holding would leave EntryView.Price() reading the nil
// foil column, and the card would be silently worth nothing.
func entry(name, board, finish string, qty int, usd float64) store.EntryView {
	e := store.EntryView{Finish: finish, Board: board, Quantity: qty}
	e.Card.ScryfallID = name + "-id"
	e.Card.Name = name
	e.Card.SetCode = "mh3"
	e.Card.CollectorNumber = "1"
	if finish == "normal" {
		e.Card.PriceUSD = price(usd)
	} else {
		e.Card.PriceUSDFoil = price(usd)
	}
	return e
}

func testStore() *fakeStore {
	return &fakeStore{
		totals: store.CollectionTotals{DistinctCards: 3, TotalCopies: 8, Value: 300},
		decks: []store.DeckSummary{
			deck(1, "Cheap Deck", 100, 50),
			deck(2, "Rich Deck", 100, 500),
		},
		collection: []store.CollectionRow{
			row("Bitterblossom", "uma", "85", "normal", 4, 136),
			row("Ancient Tomb", "uma", "236", "foil", 1, 134),
			row("Sol Ring", "c21", "1", "normal", 3, 30),
		},
		traits: map[string][]string{
			"Bitterblossom-id": {"mythic", "enchantment", "B"},
			"Ancient Tomb-id":  {"rare", "land"},
			"Sol Ring-id":      {"uncommon", "artifact"},
		},
		enriched: 3,
		deckCards: map[int64][]store.EntryView{
			2: {entry("Solitude", "main", "normal", 1, 34), entry("Force of Will", "side", "foil", 2, 90)},
			1: {entry("Llanowar Elves", "main", "normal", 1, 1)},
		},
	}
}

// newTestModel returns a model sized as if the terminal had reported itself.
func newTestModel(t *testing.T, st Store) Model {
	t.Helper()
	m, err := New(st)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	return next.(Model)
}

func key(m Model, k string) Model {
	var msg tea.KeyMsg
	switch k {
	case "up", "down", "tab", "left", "right", "home", "end", "pgup", "pgdown":
		msg = tea.KeyMsg{Type: keyType(k)}
	default:
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
	}
	next, _ := m.Update(msg)
	return next.(Model)
}

func keyType(k string) tea.KeyType {
	switch k {
	case "up":
		return tea.KeyUp
	case "down":
		return tea.KeyDown
	case "tab":
		return tea.KeyTab
	case "left":
		return tea.KeyLeft
	case "right":
		return tea.KeyRight
	case "home":
		return tea.KeyHome
	case "end":
		return tea.KeyEnd
	case "pgup":
		return tea.KeyPgUp
	case "pgdown":
		return tea.KeyPgDown
	}
	return tea.KeyNull
}

// The left pane is the summary: the loose collection first, then decks ranked by
// value. That ordering is the whole reason the pane replaces `summary`.
func TestContainersAreCollectionThenDecksByValue(t *testing.T) {
	m := newTestModel(t, testStore())

	var got []string
	for _, c := range m.containers {
		got = append(got, c.Name)
	}
	want := []string{store.LooseName, "Rich Deck", "Cheap Deck"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("containers = %v, want %v", got, want)
	}
	if m.containers[0].Value != 300 || m.containers[0].Copies != 8 {
		t.Errorf("collection row = %+v, want the totals", m.containers[0])
	}
}

// Selecting a container is `deck show`: the card pane follows the left cursor.
func TestMovingContainerCursorLoadsThatContainersCards(t *testing.T) {
	m := newTestModel(t, testStore())

	if len(m.cards) != 3 || m.cards[0].Name != "Bitterblossom" {
		t.Fatalf("initial cards = %+v, want the collection by value", names(m.cards))
	}
	m = key(m, "down") // → Rich Deck
	if len(m.cards) != 2 {
		t.Fatalf("cards = %v, want Rich Deck's two", names(m.cards))
	}
	if m.cards[0].Name != "Force of Will" {
		t.Errorf("cards = %v, want the 2x$90 foil first by value", names(m.cards))
	}
	m = key(m, "down") // → Cheap Deck
	if len(m.cards) != 1 || m.cards[0].Name != "Llanowar Elves" {
		t.Errorf("cards = %v, want Cheap Deck's one", names(m.cards))
	}
}

// Moving the card cursor must not re-read the container's cards; only the left
// pane drives loading.
func TestCardCursorDoesNotReloadCards(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	m = key(m, "tab")
	if m.focus != paneCards {
		t.Fatal("tab did not move focus to the card pane")
	}
	m = key(m, "down")
	if m.cursor[paneCards] != 1 {
		t.Errorf("card cursor = %d, want 1", m.cursor[paneCards])
	}
	if m.cursor[paneContainers] != 0 {
		t.Errorf("container cursor moved to %d", m.cursor[paneContainers])
	}
}

func TestTabTogglesFocusBothWays(t *testing.T) {
	m := newTestModel(t, testStore())
	if m.focus != paneContainers {
		t.Fatal("should start on the container pane")
	}
	if m = key(m, "tab"); m.focus != paneCards {
		t.Fatal("tab did not reach the card pane")
	}
	if m = key(m, "tab"); m.focus != paneContainers {
		t.Fatal("tab did not toggle back")
	}
	// Left and right are unambiguous in a two-pane layout, so they are absolute.
	if m = key(m, "right"); m.focus != paneCards {
		t.Error("right did not focus the card pane")
	}
	if m = key(m, "right"); m.focus != paneCards {
		t.Error("right should stay on the card pane, not toggle")
	}
	if m = key(m, "left"); m.focus != paneContainers {
		t.Error("left did not focus the container pane")
	}
}

// Cursors clamp rather than wrap: a list that jumps from the last row back to
// the first loses your place on a long collection.
func TestCursorClampsAtBothEnds(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "up")
	if m.cursor[paneContainers] != 0 {
		t.Errorf("up from the top = %d, want 0", m.cursor[paneContainers])
	}
	for range 10 {
		m = key(m, "down")
	}
	if got, want := m.cursor[paneContainers], len(m.containers)-1; got != want {
		t.Errorf("down past the end = %d, want %d", got, want)
	}
}

func TestSortCycles(t *testing.T) {
	m := newTestModel(t, testStore())
	if m.cards[0].Name != "Bitterblossom" {
		t.Fatalf("default sort = %v, want value order", names(m.cards))
	}

	m = key(m, "s") // → name
	if m.sort != sortName {
		t.Fatalf("sort = %v, want name", m.sort)
	}
	if m.cards[0].Name != "Ancient Tomb" {
		t.Errorf("by name = %v, want Ancient Tomb first", names(m.cards))
	}

	m = key(m, "s") // → qty
	if m.cards[0].Name != "Bitterblossom" {
		t.Errorf("by qty = %v, want the 4-copy card first", names(m.cards))
	}

	m = key(m, "s") // back to value
	if m.sort != sortValue || m.cards[0].Name != "Bitterblossom" {
		t.Errorf("cycle did not return to value: %v %v", m.sort, names(m.cards))
	}
}

// The sort mode has to survive changing container, or the pane silently reverts
// to value ordering every time the left cursor moves.
func TestSortPersistsAcrossContainers(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "s") // name
	m = key(m, "down")
	if m.sort != sortName {
		t.Fatalf("sort reset to %v", m.sort)
	}
	if m.cards[0].Name != "Force of Will" {
		t.Errorf("deck cards = %v, want name order", names(m.cards))
	}
}

// A read that fails mid-session becomes a status line: the screen already holds
// content worth keeping, and quitting would throw it away.
func TestReadFailureBecomesAStatusNotAQuit(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	st.err = errFake{}

	m = key(m, "r")
	if !m.statusErr || m.status == "" {
		t.Errorf("want an error status, got %q (err=%v)", m.status, m.statusErr)
	}
	if m.err != nil {
		t.Errorf("session ended with %v, want it to stay open", m.err)
	}
	// The previously loaded rows are still there.
	if len(m.containers) == 0 {
		t.Error("containers were cleared on a failed reload")
	}
}

type errFake struct{}

func (errFake) Error() string { return "database is locked" }

// An empty hoard must render rather than panic on the missing selection.
func TestEmptyHoard(t *testing.T) {
	m := newTestModel(t, &fakeStore{})
	if len(m.containers) != 1 {
		t.Fatalf("containers = %+v, want just the empty collection", m.containers)
	}
	if len(m.cards) != 0 {
		t.Errorf("cards = %v, want none", names(m.cards))
	}
	if out := m.View(); out == "" {
		t.Error("View rendered nothing for an empty hoard")
	}
	// Moving around an empty pane must not move the cursor off the end.
	m = key(m, "tab")
	m = key(m, "down")
	if m.cursor[paneCards] != 0 {
		t.Errorf("card cursor = %d on an empty pane", m.cursor[paneCards])
	}
}

func names(cards []card) []string {
	out := make([]string, len(cards))
	for i, c := range cards {
		out[i] = c.Name
	}
	return out
}

// The panes must degrade the way the CLI's tables do — dropping columns rather
// than wrapping — because a wrapped row breaks the one-line-per-card contract
// the cursor depends on.
func TestViewFitsEveryWidth(t *testing.T) {
	for _, w := range []int{40, 60, 80, 100, 140} {
		t.Run(strconv.Itoa(w), func(t *testing.T) {
			m, err := New(testStore())
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			next, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: 20})
			out := next.(Model).View()

			for i, line := range strings.Split(out, "\n") {
				if got := ansi.StringWidth(line); got > w {
					t.Errorf("line %d is %d cells wide at width %d: %q", i, got, w, line)
				}
			}
		})
	}
}

// A pane taller than the list must not pad the cursor off the end, and a list
// longer than the pane must scroll rather than overflow.
func TestScrollingKeepsTheCursorVisible(t *testing.T) {
	st := testStore()
	// More rows than a short terminal can show.
	for i := range 40 {
		st.collection = append(st.collection,
			row("Filler "+strconv.Itoa(i), "set", strconv.Itoa(i), "normal", 1, float64(i)))
	}
	m, err := New(st)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	m = next.(Model)
	m = key(m, "tab")

	rows := m.visibleRows()
	for range len(m.cards) + 5 {
		m = key(m, "down")
		if m.cursor[paneCards] < m.offset[paneCards] ||
			m.cursor[paneCards] >= m.offset[paneCards]+rows {
			t.Fatalf("cursor %d outside window [%d,%d)",
				m.cursor[paneCards], m.offset[paneCards], m.offset[paneCards]+rows)
		}
		// And the rendered pane never exceeds the terminal height.
		if n := len(strings.Split(m.View(), "\n")); n > 12 {
			t.Fatalf("rendered %d lines at height 12", n)
		}
	}
	if m.cursor[paneCards] != len(m.cards)-1 {
		t.Errorf("cursor = %d, want the last of %d", m.cursor[paneCards], len(m.cards))
	}
}

// BOARD is meaningful in a deck and a column of blanks against loose holdings.
func TestBoardColumnOnlyAppearsForDecks(t *testing.T) {
	m := newTestModel(t, testStore())
	if strings.Contains(m.View(), "BOARD") {
		t.Error("BOARD shown for the loose collection")
	}
	m = key(m, "down") // → Rich Deck
	if !strings.Contains(m.View(), "BOARD") {
		t.Error("BOARD missing for a deck")
	}
}

// typeFilter opens the bar and types a query, one keystroke at a time, the way
// a person does — so incremental parsing is exercised, not just the final text.
func typeFilter(m Model, q string) Model {
	m = key(m, "/")
	for _, r := range q {
		if r == ' ' {
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
			m = next.(Model)
			continue
		}
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	return m
}

func TestFilterNarrowsAsYouType(t *testing.T) {
	m := newTestModel(t, testStore())
	if len(m.cards) != 3 {
		t.Fatalf("want 3 cards to start, got %d", len(m.cards))
	}
	m = typeFilter(m, "sol")
	if len(m.cards) != 1 || m.cards[0].Name != "Sol Ring" {
		t.Errorf("cards = %v, want just Sol Ring", names(m.cards))
	}
	// Backspacing widens it again from the rows already in memory. "so" also
	// matches Bitterblo(sso)m, which is the point of a substring search.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = next.(Model)
	if len(m.cards) != 2 {
		t.Errorf("cards = %v after backspace, want Sol Ring and Bitterblossom", names(m.cards))
	}
}

// While the bar is open every printable key is text. Without this, typing a
// card name containing "q" quits the browser.
func TestFilterBarSwallowsCommandKeys(t *testing.T) {
	m := newTestModel(t, testStore())
	m = typeFilter(m, "q")
	if !m.filtering || m.filterText != "q" {
		t.Errorf("filtering=%v text=%q — q was treated as quit", m.filtering, m.filterText)
	}
	m = typeFilter(m, "s")
	if m.sort != sortValue {
		t.Error("s changed the sort while the filter bar was open")
	}
}

// Enter keeps the query and closes the bar; escape abandons it entirely.
func TestFilterEnterKeepsEscapeClears(t *testing.T) {
	m := newTestModel(t, testStore())
	m = typeFilter(m, "sol")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.filtering {
		t.Error("enter left the bar open")
	}
	if len(m.cards) != 1 {
		t.Errorf("enter dropped the filter: %v", names(m.cards))
	}

	// And escape from the closed state clears it.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if len(m.cards) != 3 {
		t.Errorf("esc did not clear the filter: %v", names(m.cards))
	}
}

func TestFilterEscapeFromTheBarAbandonsTheQuery(t *testing.T) {
	m := newTestModel(t, testStore())
	m = typeFilter(m, "sol")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.filtering || len(m.cards) != 3 {
		t.Errorf("esc left filtering=%v cards=%v", m.filtering, names(m.cards))
	}
}

// A plain name search must not touch the database — that is the whole reason
// the filter is split into a trait half and a holding half.
func TestNameFilterNeverQueriesTheCatalog(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	m = typeFilter(m, "sol ring qty>1")
	if st.matchCalls != 0 {
		t.Errorf("made %d catalog queries for a name/qty filter, want 0", st.matchCalls)
	}
}

func TestTraitFilterQueriesTheCatalog(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	m = typeFilter(m, "rarity:mythic")
	if st.matchCalls == 0 {
		t.Fatal("a rarity filter did not query the catalog")
	}
	if len(m.cards) != 1 || m.cards[0].Name != "Bitterblossom" {
		t.Errorf("cards = %v, want the mythic", names(m.cards))
	}
}

// The two halves have to compose, or `rarity:mythic qty>3` silently ignores one.
func TestTraitAndHoldingTermsCompose(t *testing.T) {
	m := newTestModel(t, testStore())
	m = typeFilter(m, "rarity:mythic qty>3")
	if len(m.cards) != 1 {
		t.Errorf("cards = %v, want the 4-copy mythic", names(m.cards))
	}
	m = newTestModel(t, testStore())
	m = typeFilter(m, "rarity:mythic qty>10")
	if len(m.cards) != 0 {
		t.Errorf("cards = %v, want none", names(m.cards))
	}
}

// A half-typed comparison must not empty the pane; the last valid query stands
// until the new one parses.
func TestPartialQueryKeepsTheLastGoodResult(t *testing.T) {
	m := newTestModel(t, testStore())
	m = typeFilter(m, "qty>1") // valid: everything held in more than one copy
	if m.filterErr != "" || len(m.cards) != 2 {
		t.Fatalf("setup: err=%q cards=%v", m.filterErr, names(m.cards))
	}

	// A trailing operator cannot parse. The rows must stay as they were rather
	// than emptying between two keystrokes of a comparison.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'>'}})
	m = next.(Model)
	if m.filterErr == "" {
		t.Error("want an error shown for a dangling comparison")
	}
	if len(m.cards) != 2 {
		t.Errorf("cards = %v, want the last good result held", names(m.cards))
	}

	// Taking the stray character back off resolves it.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = next.(Model)
	if m.filterErr != "" {
		t.Errorf("error %q persisted after the query became valid again", m.filterErr)
	}
	if len(m.cards) != 2 {
		t.Errorf("cards = %v, want the query working again", names(m.cards))
	}
}

// An empty result from a trait filter is ambiguous on a hoard that was never
// refreshed: the columns are NULL, so every trait query correctly returns
// nothing and looks identical to owning none of that card.
func TestEmptyTraitResultExplainsAnUnrefreshedCatalog(t *testing.T) {
	st := testStore()
	st.traits = map[string][]string{} // nothing matches
	st.enriched = 0                   // ...because nothing has been refreshed
	m := newTestModel(t, st)
	m = typeFilter(m, "rarity:mythic")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if got := m.statusLine(); !strings.Contains(got, "update-prices") {
		t.Errorf("status = %q, want it to point at update-prices", got)
	}
}

// The filter survives switching container: it is a lens on the hoard, not on
// one deck, and re-typing it for every container would make it useless.
func TestFilterPersistsAcrossContainers(t *testing.T) {
	st := testStore()
	st.deckCards[2] = append(st.deckCards[2], entry("Sol Ring", "main", "normal", 1, 2))
	m := newTestModel(t, st)
	m = typeFilter(m, "ring")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)

	m = key(m, "left")
	m = key(m, "down") // → Rich Deck
	if len(m.cards) != 1 || m.cards[0].Name != "Sol Ring" {
		t.Errorf("deck cards = %v, want the filter still applied", names(m.cards))
	}
}

// findCard is where a named card sits in the pane, or -1.
func findCard(m Model, name string) int {
	for i, c := range m.cards {
		if c.Name == name {
			return i
		}
	}
	return -1
}

func TestAdjustQuantity(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "tab") // to the cards
	i := findCard(m, "Bitterblossom")
	m.cursor[paneCards] = i

	m = key(m, "+")
	if got := m.cards[findCard(m, "Bitterblossom")].Quantity; got != 5 {
		t.Errorf("after +: qty = %d, want 5", got)
	}
	m = key(m, "-")
	m = key(m, "-")
	if got := m.cards[findCard(m, "Bitterblossom")].Quantity; got != 3 {
		t.Errorf("after two -: qty = %d, want 3", got)
	}
}

// Zeroing a holding removes it: "held in no copies" and "not held" are one
// state, and a zero row would show up in every listing that counts holdings.
func TestAdjustQuantityToZeroRemovesTheRow(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "tab")
	m.cursor[paneCards] = findCard(m, "Ancient Tomb") // held ×1

	m = key(m, "-")
	if findCard(m, "Ancient Tomb") != -1 {
		t.Errorf("row survived being zeroed: %v", names(m.cards))
	}
	// And it is undoable.
	m = key(m, "u")
	if findCard(m, "Ancient Tomb") == -1 {
		t.Errorf("undo did not restore it: %v", names(m.cards))
	}
}

func TestUndoRestoresAQuantity(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "tab")
	m.cursor[paneCards] = findCard(m, "Sol Ring") // held ×3

	m = key(m, "+")
	m = key(m, "+")
	if got := m.cards[findCard(m, "Sol Ring")].Quantity; got != 5 {
		t.Fatalf("qty = %d, want 5", got)
	}
	m = key(m, "u")
	// One level of undo, so this restores the quantity before the *last* edit.
	if got := m.cards[findCard(m, "Sol Ring")].Quantity; got != 4 {
		t.Errorf("after undo: qty = %d, want 4 (one level)", got)
	}
	m = key(m, "u")
	if !strings.Contains(m.status, "nothing to undo") {
		t.Errorf("status = %q, want the second undo to report nothing left", m.status)
	}
}

// Removals ask first. A single keystroke that deletes a hundred-card deck,
// through the same key that moves the cursor, is a trap.
func TestRemoveAsksBeforeActing(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	m = key(m, "tab")
	m.cursor[paneCards] = findCard(m, "Sol Ring")

	m = key(m, "d")
	if m.confirm == nil {
		t.Fatal("d did not ask for confirmation")
	}
	if !strings.Contains(m.confirm.prompt, "Sol Ring") {
		t.Errorf("prompt = %q, want it to name the card", m.confirm.prompt)
	}
	if findCard(m, "Sol Ring") == -1 {
		t.Error("the card was removed before confirming")
	}

	// Anything but y cancels — the safe reading of a stray keystroke is no.
	m = key(m, "n")
	if m.confirm != nil || findCard(m, "Sol Ring") == -1 {
		t.Error("n did not cancel the removal")
	}
}

func TestRemoveCardAndUndo(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "tab")
	m.cursor[paneCards] = findCard(m, "Sol Ring")

	m = key(m, "d")
	m = key(m, "y")
	if findCard(m, "Sol Ring") != -1 {
		t.Fatalf("card not removed: %v", names(m.cards))
	}
	m = key(m, "u")
	if findCard(m, "Sol Ring") == -1 {
		t.Errorf("undo did not restore the card: %v", names(m.cards))
	}
}

func TestRemoveDeckAndUndo(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	m = key(m, "down") // → Rich Deck, focus on the container pane

	m = key(m, "d")
	if m.confirm == nil || !m.confirm.deck {
		t.Fatalf("d on a deck did not stage a deck removal: %+v", m.confirm)
	}
	m = key(m, "y")
	for _, c := range m.containers {
		if c.Name == "Rich Deck" {
			t.Fatal("deck was not removed")
		}
	}

	m = key(m, "u")
	var found bool
	for _, c := range m.containers {
		if c.Name == "Rich Deck" {
			found = true
		}
	}
	if !found {
		t.Error("undo did not bring the deck back")
	}
}

// The loose collection is not a deck and has nothing to be removed into.
func TestCollectionCannotBeRemoved(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "d") // cursor is on Collection
	if m.confirm != nil {
		t.Error("staged a removal of the loose collection")
	}
	if !m.statusErr {
		t.Errorf("status = %q, want a refusal", m.status)
	}
}

// A deck is owned by the list it was imported from. Editing its cards here
// would diverge from that source until the next import silently overwrote it.
func TestDeckCardsAreReadOnly(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "down") // → Rich Deck
	m = key(m, "tab")  // → its cards
	before := m.cards[0].Quantity

	m = key(m, "+")
	if m.cards[0].Quantity != before {
		t.Error("a deck card's quantity was changed")
	}
	if !m.statusErr || !strings.Contains(m.status, "owned by the imported list") {
		t.Errorf("status = %q, want it to explain why", m.status)
	}

	m = key(m, "d")
	if m.confirm != nil {
		t.Error("staged a removal of a deck card")
	}
}

// An edit changes the collection's total, which lives on the left pane, so both
// panes have to be re-read or the totals go stale while the row shows the new
// number.
func TestEditRefreshesContainerTotals(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	m = key(m, "tab")
	m.cursor[paneCards] = findCard(m, "Sol Ring")

	st.totals.TotalCopies = 99 // what the next read will report
	m = key(m, "+")
	if m.containers[0].Copies != 99 {
		t.Errorf("collection row = %d copies, want the re-read total", m.containers[0].Copies)
	}
}

// The cursor must stay on the row that was edited; jumping to the top after
// every keystroke makes adjusting a quantity by three impossible.
func TestEditKeepsTheCursorInPlace(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "tab")
	m.cursor[paneCards] = findCard(m, "Sol Ring")
	at := m.cursor[paneCards]

	m = key(m, "+")
	if m.cursor[paneCards] != at {
		t.Errorf("cursor moved from %d to %d after an edit", at, m.cursor[paneCards])
	}
	if m.cards[m.cursor[paneCards]].Name != "Sol Ring" {
		t.Errorf("cursor is on %q, want it still on Sol Ring", m.cards[m.cursor[paneCards]].Name)
	}
}

func TestResample(t *testing.T) {
	pt := func(day int, price float64) store.PricePoint {
		return store.PricePoint{
			AsOf:  time.Date(2026, 5, day, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
			Price: price,
		}
	}
	// A price that moved on day 1 then held for a month, then moved again. By
	// index this is three equal thirds; by time the flat month dominates, which
	// is the truth a sparkline should show.
	series := []store.PricePoint{pt(1, 10), pt(2, 20), pt(31, 30)}
	got := resample(series, 10)
	if len(got) != 10 {
		t.Fatalf("got %d buckets, want 10", len(got))
	}
	if got[len(got)-1] != 30 {
		t.Errorf("last bucket = %v, want the latest price", got[len(got)-1])
	}
	var at20 int
	for _, v := range got {
		if v == 20 {
			at20++
		}
	}
	if at20 < 6 {
		t.Errorf("only %d of 10 buckets sit at the month-long price: %v", at20, got)
	}
}

func TestResampleEdgeCases(t *testing.T) {
	if got := resample(nil, 8); got != nil {
		t.Errorf("nil series = %v, want nil", got)
	}
	one := []store.PricePoint{{AsOf: "2026-05-01T00:00:00Z", Price: 7}}
	if got := resample(one, 8); len(got) != 1 || got[0] != 7 {
		t.Errorf("single point = %v, want [7]", got)
	}
	// Two observations at the same instant have no span to spread across.
	same := []store.PricePoint{
		{AsOf: "2026-05-01T00:00:00Z", Price: 7},
		{AsOf: "2026-05-01T00:00:00Z", Price: 9},
	}
	if got := resample(same, 8); len(got) != 1 || got[0] != 9 {
		t.Errorf("zero-span series = %v, want the latest price", got)
	}
}

func TestDetailOpensAndCloses(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	m = key(m, "tab")

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail == nil {
		t.Fatal("enter did not open the detail")
	}
	if out := m.View(); !strings.Contains(out, "HELD") || !strings.Contains(out, "PRICE") {
		t.Errorf("detail view missing its sections:\n%s", out)
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if next.(Model).detail != nil {
		t.Error("esc did not close the detail")
	}
}

// The overlay covers the panes, so it must swallow the keys that would move a
// cursor the reader cannot see.
func TestDetailSwallowsNavigationKeys(t *testing.T) {
	m := newTestModel(t, testStore())
	m = key(m, "tab")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	at := m.cursor[paneCards]

	m = key(m, "down")
	m = key(m, "s")
	if m.cursor[paneCards] != at {
		t.Error("the cursor moved behind the detail overlay")
	}
	if m.sort != sortValue {
		t.Error("s changed the sort behind the detail overlay")
	}
}

func TestViewCyclesAndLoads(t *testing.T) {
	st := testStore()
	st.movers = []store.PriceChange{
		{Name: "Riser", SetCode: "a", CollectorNumber: "1", Finish: "normal", Copies: 2, Old: 1, New: 5},
		{Name: "Sinker", SetCode: "b", CollectorNumber: "2", Finish: "foil", Copies: 1, Old: 50, New: 10},
	}
	st.unpriced = []store.UnpricedRow{
		{Name: "No Price", SetCode: "c", CollectorNumber: "3", Finish: "foil", Copies: 1, HeldIn: "Collection"},
	}
	m := newTestModel(t, st)

	m = key(m, "v")
	if m.view != viewMovers {
		t.Fatalf("view = %v, want movers", m.view)
	}
	// Ordered by impact regardless of direction: the sinker moved $40, the
	// riser $8.
	if len(m.movers) != 2 || m.movers[0].Name != "Sinker" {
		t.Errorf("movers = %+v, want the biggest impact first", m.movers)
	}
	if out := m.View(); !strings.Contains(out, "MOVERS") || !strings.Contains(out, "IMPACT") {
		t.Errorf("movers view not rendered:\n%s", out)
	}

	m = key(m, "v")
	if m.view != viewUnpriced || len(m.unpriced) != 1 {
		t.Fatalf("view = %v with %d rows", m.view, len(m.unpriced))
	}
	if out := m.View(); !strings.Contains(out, "UNPRICED") || !strings.Contains(out, "HELD IN") {
		t.Errorf("unpriced view not rendered:\n%s", out)
	}

	m = key(m, "v")
	if m.view != viewArbitrage {
		t.Errorf("view = %v, want arbitrage", m.view)
	}
	m = key(m, "v")
	if m.view != viewHoldings {
		t.Errorf("view = %v, want back to holdings", m.view)
	}
}

// Movers and unpriced describe the whole hoard, so the cursor must count their
// rows rather than the selected container's.
func TestViewRowCountFollowsTheMode(t *testing.T) {
	st := testStore()
	st.movers = []store.PriceChange{{Name: "One"}, {Name: "Two"}}
	m := newTestModel(t, st)
	if got := m.rowCount(paneCards); got != 3 {
		t.Errorf("holdings rowCount = %d, want 3", got)
	}
	m = key(m, "v")
	if got := m.rowCount(paneCards); got != 2 {
		t.Errorf("movers rowCount = %d, want 2", got)
	}
}

// The analytical panes list different rows than the holdings pane, so the
// cursor there indexes a different slice. An edit or a detail lookup must
// refuse rather than act on whichever holding sits at the same offset.
func TestAnalyticalViewsRefuseHoldingActions(t *testing.T) {
	st := testStore()
	st.movers = []store.PriceChange{
		{Name: "Riser", Finish: "normal", Copies: 2, Old: 1, New: 5},
	}
	m := newTestModel(t, st)
	before := len(st.collection)
	qty := m.cards[0].Quantity

	m = key(m, "v") // → movers
	m = key(m, "+")
	if !m.statusErr || !strings.Contains(m.status, "press v") {
		t.Errorf("status = %q, want a refusal that says how to get back", m.status)
	}

	m = key(m, "d")
	if m.confirm != nil {
		t.Error("staged a removal from the movers view")
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.detail != nil {
		t.Error("opened card detail from the movers view")
	}

	// Nothing was touched.
	m = key(m, "v")
	m = key(m, "v") // back to holdings
	if len(st.collection) != before || m.cards[0].Quantity != qty {
		t.Errorf("the collection changed: %d rows, top qty %d (want %d, %d)",
			len(st.collection), m.cards[0].Quantity, before, qty)
	}
}

func arbModel(t *testing.T, fn ArbitrageFunc) Model {
	t.Helper()
	m, err := New(testStore(), WithArbitrage(fn))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 20})
	return next.(Model)
}

func opp(name string, buy, sell float64) arbitrage.Opportunity {
	return arbitrage.Opportunity{
		Card:      store.OwnedFinish{Name: name, SetCode: "mh3", CollectorNumber: "1", Finish: "normal"},
		BuyAt:     buy,
		DearAt:    buy * 2,
		SellAt:    sell,
		HasRetail: true,
		HasBuy:    sell > 0,
	}
}

// Arbitrage is the only view that needs the network, so it must not fetch just
// because the user cycled past it.
func TestArbitrageDoesNotFetchOnArrival(t *testing.T) {
	var calls int
	m := arbModel(t, func(context.Context) (arbitrage.Result, error) {
		calls++
		return arbitrage.Result{}, nil
	})
	m = key(m, "v")
	m = key(m, "v")
	m = key(m, "v") // → arbitrage
	if m.view != viewArbitrage {
		t.Fatalf("view = %v", m.view)
	}
	if calls != 0 {
		t.Errorf("fetched %d times on arrival, want 0", calls)
	}
	if out := m.View(); !strings.Contains(out, "press enter") {
		t.Errorf("view does not invite the fetch:\n%s", out)
	}
}

func TestArbitrageFetchesOnEnterAndRenders(t *testing.T) {
	res := arbitrage.Result{
		Opportunities: []arbitrage.Opportunity{opp("Profitable", 2, 20), opp("Liquid", 10, 9)},
		Compared:      2,
	}
	m := arbModel(t, func(context.Context) (arbitrage.Result, error) { return res, nil })
	for range 3 {
		m = key(m, "v")
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if !m.arbLoading {
		t.Fatal("enter did not start a fetch")
	}
	if cmd == nil {
		t.Fatal("no command returned to run the fetch")
	}
	if out := m.View(); !strings.Contains(out, "asking every vendor") {
		t.Errorf("no progress shown:\n%s", out)
	}

	// Deliver the reply the command produces, as the runtime would.
	next, _ = m.Update(arbitrageMsg{gen: m.arbGen, res: res})
	m = next.(Model)

	if m.arbLoading || !m.arbLoaded {
		t.Fatalf("loading=%v loaded=%v", m.arbLoading, m.arbLoaded)
	}
	if len(m.arbRows) == 0 {
		t.Fatal("no rows after a successful fetch")
	}
	out := m.View()
	for _, want := range []string{"ARBITRAGE", "Profitable", "arbitrage", "GAIN"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
}

// A reply to a request the user has already left must not overwrite the pane.
func TestStaleArbitrageReplyIsDiscarded(t *testing.T) {
	m := arbModel(t, func(context.Context) (arbitrage.Result, error) {
		return arbitrage.Result{}, nil
	})
	for range 3 {
		m = key(m, "v")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	stale := m.arbGen - 1

	next, _ = m.Update(arbitrageMsg{
		gen: stale,
		res: arbitrage.Result{Opportunities: []arbitrage.Opportunity{opp("Ghost", 1, 5)}},
	})
	m = next.(Model)
	if m.arbLoaded || len(m.arbRows) != 0 {
		t.Errorf("a stale reply landed: loaded=%v rows=%d", m.arbLoaded, len(m.arbRows))
	}
}

// Without an injected fetch the view says so rather than looking broken.
func TestArbitrageUnavailableWithoutAFetcher(t *testing.T) {
	m := newTestModel(t, testStore()) // no WithArbitrage
	for range 3 {
		m = key(m, "v")
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd != nil {
		t.Error("returned a command with no fetcher configured")
	}
	if !m.statusErr || !strings.Contains(m.status, "unavailable") {
		t.Errorf("status = %q, want it to say arbitrage is unavailable", m.status)
	}
}

// A genuine failure is shown, unlike a cancellation.
func TestArbitrageErrorIsShown(t *testing.T) {
	m := arbModel(t, func(context.Context) (arbitrage.Result, error) {
		return arbitrage.Result{}, errFake{}
	})
	for range 3 {
		m = key(m, "v")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	next, _ = m.Update(arbitrageMsg{gen: m.arbGen, err: errFake{}})
	m = next.(Model)
	if !m.statusErr {
		t.Errorf("status = %q, want the failure surfaced", m.status)
	}
}

// Editing keys have no meaning against vendor quotes, and the cursor indexes a
// different slice here.
func TestArbitrageRefusesHoldingActions(t *testing.T) {
	m := arbModel(t, func(context.Context) (arbitrage.Result, error) {
		return arbitrage.Result{}, nil
	})
	for range 3 {
		m = key(m, "v")
	}
	m = key(m, "+")
	if !m.statusErr {
		t.Errorf("status = %q, want a refusal", m.status)
	}
}

// capturingArb records the context it was handed, so a test can assert the
// browser cancelled it without any goroutine choreography.
//
// tea.Batch does not run its commands — it returns a BatchMsg for the runtime to
// expand — so driving the fetch by calling the returned Cmd would never start
// it. Watching the context is both simpler and closer to what matters.
type capturingArb struct{ ctx context.Context }

func (c *capturingArb) fetch(ctx context.Context) (arbitrage.Result, error) {
	c.ctx = ctx
	<-ctx.Done() // stand in for a slow download
	return arbitrage.Result{}, ctx.Err()
}

// startFetch puts the model into the arbitrage view with a fetch in flight,
// returning the capture so the test can inspect its context.
func startFetch(t *testing.T) (Model, *capturingArb) {
	t.Helper()
	cap := &capturingArb{}
	m := arbModel(t, cap.fetch)
	for range 3 {
		m = key(m, "v")
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil || !m.arbLoading {
		t.Fatal("enter did not start a fetch")
	}
	// Run the fetch's own goroutine the way the runtime would, so the context is
	// captured; it blocks until cancelled.
	go func() {
		if batch, ok := cmd().(tea.BatchMsg); ok {
			for _, c := range batch {
				if c != nil {
					go c()
				}
			}
		}
	}()
	for range 200 {
		if cap.ctx != nil {
			return m, cap
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("the fetch never received a context")
	return m, cap
}

// Escape during a fetch cancels it rather than leaving a download running behind
// a pane the user has stopped looking at.
func TestArbitrageEscapeCancels(t *testing.T) {
	m, cap := startFetch(t)

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.arbLoading {
		t.Error("still loading after esc")
	}
	if cap.ctx.Err() == nil {
		t.Error("esc did not cancel the fetch's context")
	}

	// And the cancellation must not be reported as a failure.
	next, _ = m.Update(arbitrageMsg{gen: m.arbGen, err: context.Canceled})
	if got := next.(Model); got.statusErr {
		t.Errorf("cancellation reported as an error: %q", got.status)
	}
}

// Leaving the view with v also abandons the fetch.
func TestArbitrageViewChangeCancels(t *testing.T) {
	m, cap := startFetch(t)

	m = key(m, "v") // → back to holdings
	if m.arbLoading {
		t.Error("still loading after leaving the view")
	}
	if m.view != viewHoldings {
		t.Errorf("view = %v, want holdings", m.view)
	}
	if cap.ctx.Err() == nil {
		t.Error("leaving the view did not cancel the fetch")
	}
}

// Adding is a handoff, not a nested program: two bubbletea programs cannot share
// a terminal, so the browser quits with a flag and its caller runs the cascade
// before re-entering.
func TestAddKeyRequestsTheCascade(t *testing.T) {
	m := newTestModel(t, testStore())
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	got := next.(Model)

	if !got.wantAdd {
		t.Error("a did not request the add cascade")
	}
	if cmd == nil {
		t.Fatal("a did not return a command; the program would stay open")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("a returned %T, want a quit so the terminal is released", cmd())
	}
}

// The key has to be advertised or nobody finds it.
func TestAddKeyIsInTheHelp(t *testing.T) {
	m := newTestModel(t, testStore())
	for _, focus := range []pane{paneContainers, paneCards} {
		m.focus = focus
		if !strings.Contains(m.helpLine(), "a add") {
			t.Errorf("focus %v help = %q, want it to mention adding", focus, m.helpLine())
		}
	}
}

// While the filter bar is open, "a" is text.
func TestAddKeyIsTextWhileFiltering(t *testing.T) {
	m := newTestModel(t, testStore())
	m = typeFilter(m, "a")
	if m.wantAdd {
		t.Error("typing a into the filter bar requested the add cascade")
	}
	if m.filterText != "a" {
		t.Errorf("filterText = %q", m.filterText)
	}
}
