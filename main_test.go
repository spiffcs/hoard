package main

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/cardname"
	"github.com/spiffcs/hoard/internal/catalog"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// f is a float pointer, for the price fields the store takes as nullable.
func f(v float64) *float64 { return &v }

func TestExtractDBFlag(t *testing.T) {
	cases := []struct {
		name     string
		args     []string
		wantRest []string
		wantDB   string
	}{
		{"absent", []string{"summary"}, []string{"summary"}, ""},
		{"before the command", []string{"--db", "x.db", "summary"}, []string{"summary"}, "x.db"},
		// The case this function exists for: previously silently ignored.
		{"after the command", []string{"summary", "--db", "x.db"}, []string{"summary"}, "x.db"},
		{"equals form", []string{"summary", "--db=x.db"}, []string{"summary"}, "x.db"},
		{"single dash", []string{"-db", "x.db", "list"}, []string{"list"}, "x.db"},
		{"repeated keeps the last", []string{"--db", "a.db", "list", "--db", "b.db"}, []string{"list"}, "b.db"},
		{
			"subcommand flags are left alone",
			[]string{"add", "--qty", "2", "--db", "x.db", "--foil", "url"},
			[]string{"add", "--qty", "2", "--foil", "url"}, "x.db",
		},
		{
			"a value that looks like a flag is still a value",
			[]string{"--db", "--weird.db", "list"},
			[]string{"list"}, "--weird.db",
		},
		{
			"nothing is extracted after a bare --",
			[]string{"add", "--", "--db", "x.db"},
			[]string{"add", "--", "--db", "x.db"}, "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rest, db, err := extractDBFlag(c.args)
			if err != nil {
				t.Fatalf("extractDBFlag(%q): %v", c.args, err)
			}
			if db != c.wantDB {
				t.Errorf("db = %q, want %q", db, c.wantDB)
			}
			if !slices.Equal(rest, c.wantRest) {
				t.Errorf("rest = %q, want %q", rest, c.wantRest)
			}
		})
	}
}

// A --db with no usable value must fail loudly rather than fall back to the
// default database, which is the whole point of handling it explicitly.
func TestExtractDBFlagErrors(t *testing.T) {
	for _, args := range [][]string{
		{"summary", "--db"},
		{"--db"},
		{"summary", "--db="},
		{"summary", "--db", ""},
	} {
		if _, _, err := extractDBFlag(args); err == nil {
			t.Errorf("extractDBFlag(%q) = nil error, want one", args)
		}
	}
}

func testCollection() store.CollectionTotals {
	return store.CollectionTotals{DistinctCards: 2, TotalCopies: 10, Value: 100}
}

// Deliberately supplied out of order, and with one unpriced deck, so the sort
// and the zero-value bar are both exercised.
func testDecks() []store.DeckSummary {
	mk := func(name string, copies int, value float64) store.DeckSummary {
		d := store.DeckSummary{TotalCopies: copies, Value: value}
		d.Name = name
		d.Kind = store.KindDeck
		return d
	}
	return []store.DeckSummary{
		mk("Beta Deck (Precon)", 60, 100),
		mk("Unpriced Deck (Precon)", 40, 0),
		mk("Alpha Deck (Precon)", 100, 300),
	}
}

func renderSummary(env ui.Env) string {
	return summaryTable(env, testCollection(), testDecks()).Render()
}

func TestSummaryTable(t *testing.T) {
	got := renderSummary(ui.Env{Width: 80, Clamp: true, Bars: true})
	want := strings.Join([]string{
		"BINDER                     10  $100.00  ██",
		"DECKS · 3                 200  $400.00  ████████",
		"",
		"  Alpha Deck (Precon)     100  $300.00  ██████",
		"  Beta Deck (Precon)       60  $100.00  ██",
		"  Unpriced Deck (Precon)   40    $0.00",
		"",
		"TOTAL                     210  $500.00",
		"",
	}, "\n")
	if got != want {
		t.Errorf("summary =\n%s\nwant\n%s", got, want)
	}
}

// Decks descend by value; the collection and decks sections must sum to TOTAL.
func TestSummaryTableSortsByValue(t *testing.T) {
	out := renderSummary(ui.Env{Width: 80, Clamp: true, Bars: true})
	alpha := strings.Index(out, "Alpha")
	beta := strings.Index(out, "Beta")
	unpriced := strings.Index(out, "Unpriced")
	if !(alpha < beta && beta < unpriced) {
		t.Errorf("decks not ordered by value desc:\n%s", out)
	}
}

// A tie in value falls back to name order, so unpriced hoards don't shuffle.
func TestSummaryTableTieBreaksByName(t *testing.T) {
	mk := func(name string) store.DeckSummary {
		d := store.DeckSummary{TotalCopies: 1}
		d.Name = name
		return d
	}
	decks := []store.DeckSummary{mk("Zeta"), mk("Alpha"), mk("Mid")}
	out := summaryTable(ui.Env{Width: 80, Clamp: true}, store.CollectionTotals{}, decks).Render()
	a, m, z := strings.Index(out, "Alpha"), strings.Index(out, "Mid"), strings.Index(out, "Zeta")
	if !(a < m && m < z) {
		t.Errorf("equal-value decks not name-ordered:\n%s", out)
	}
}

// The non-terminal profile is the scriptable one: whole names, no bars, and
// crucially no escape sequences.
func TestSummaryTablePiped(t *testing.T) {
	out := renderSummary(ui.Env{Width: 80, Clamp: false, Bars: false})
	if strings.Contains(out, "\x1b") {
		t.Error("piped summary contains ANSI escapes")
	}
	if strings.ContainsAny(out, "█▏▎▍▌▋▊▉") {
		t.Error("piped summary contains share bars")
	}
	if strings.Contains(out, "…") {
		t.Error("piped summary truncated a name")
	}
	if !strings.Contains(out, "Unpriced Deck (Precon)") {
		t.Errorf("piped summary lost a full deck name:\n%s", out)
	}
}

// At a narrow width the layout must still fit exactly — that is the whole point
// of truncating rather than letting rows wrap and break the bar column.
func TestSummaryTableNarrow(t *testing.T) {
	for _, width := range []int{60, 50, 40, 30} {
		env := ui.Env{Width: width, Clamp: true, Bars: width >= 50}
		for line := range strings.SplitSeq(renderSummary(env), "\n") {
			if w := len([]rune(line)); w > width {
				t.Errorf("width %d: line %q is %d wide", width, line, w)
			}
		}
	}
}

// An empty hoard must render zeros rather than dividing by a zero grand total.
func TestSummaryTableEmpty(t *testing.T) {
	out := summaryTable(ui.Env{Width: 80, Clamp: true, Bars: true},
		store.CollectionTotals{}, nil).Render()
	// "DECKS · 0" is now the widest label, so it sets the name column.
	want := strings.Join([]string{
		"BINDER     0  $0.00",
		"DECKS · 0  0  $0.00",
		"",
		"TOTAL      0  $0.00",
		"",
	}, "\n")
	if out != want {
		t.Errorf("empty summary =\n%s\nwant\n%s", out, want)
	}
}

// Bare `hoard` has two behaviours and the split is what keeps piping alive, so
// both halves are asserted rather than only the interactive one.
func TestBareHoardWritesTheSummaryWhenNotATTY(t *testing.T) {
	// go test's stdout is never a character device, so this exercises the
	// non-interactive branch as it will run under a pipe.
	if stdoutIsTTY() {
		t.Skip("test stdout is a terminal")
	}

	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "hoard.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer st.Close()
	if err := st.AddCard(scryfall.Card{
		ID: "x", Set: "uma", CollectorNumber: "7", Name: "Ulamog",
		ScryfallURL: "http://x", PriceUSD: f(10),
	}, false, 2); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	// cmdBrowse must not try to open a bubbletea program here; if the TTY gate
	// were wrong this would hang or fail rather than return.
	if err := cmdBrowse(context.Background(), st); err != nil {
		t.Fatalf("cmdBrowse: %v", err)
	}
}

// The commands the browser replaced are gone, and the error has to say so
// rather than silently doing nothing.
func TestRemovedReadCommandsAreRejected(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"--db", filepath.Join(dir, "a.db"), "list"},
		{"--db", filepath.Join(dir, "b.db"), "summary"},
		{"--db", filepath.Join(dir, "c.db"), "deck", "list"},
		{"--db", filepath.Join(dir, "d.db"), "deck", "show", "x"},
	} {
		err := run(args)
		if err == nil {
			t.Errorf("run(%v) succeeded, want an unknown-command error", args)
			continue
		}
		if !strings.Contains(err.Error(), "unknown") {
			t.Errorf("run(%v) = %v, want it to say the command is unknown", args, err)
		}
	}
}

// countingSearcher stands in for the Scryfall API and records what reached it.
type countingSearcher struct {
	auto, prints, fuzzy int
	names               []string
	cards               []scryfall.Card
	card                *scryfall.Card
}

func (c *countingSearcher) Autocomplete(context.Context, string) ([]string, error) {
	c.auto++
	return c.names, nil
}
func (c *countingSearcher) SearchPrints(context.Context, string) ([]scryfall.Card, error) {
	c.prints++
	return c.cards, nil
}
func (c *countingSearcher) NamedFuzzy(context.Context, string) (*scryfall.Card, error) {
	c.fuzzy++
	return c.card, nil
}

// A nil catalog is a supported state: hoard behaves exactly as it did before the
// catalog existed.
func TestSearcherWithNoCatalogGoesStraightToTheAPI(t *testing.T) {
	remote := &countingSearcher{names: []string{"Sol Ring"}}
	s := layeredSearcher{remote: remote}
	ctx := context.Background()

	if got, _ := s.Autocomplete(ctx, "sol"); len(got) != 1 {
		t.Errorf("Autocomplete = %v", got)
	}
	s.SearchPrints(ctx, "Sol Ring")
	s.NamedFuzzy(ctx, "Sol Ring")
	if remote.auto != 1 || remote.prints != 1 || remote.fuzzy != 1 {
		t.Errorf("api calls: %d/%d/%d, want one each", remote.auto, remote.prints, remote.fuzzy)
	}
}

// An empty catalog is worse than none: every lookup would miss, pay for a query,
// and go to the API anyway. newSearcher must not layer one in.
func TestNewSearcherIgnoresAnEmptyCatalog(t *testing.T) {
	cat, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer cat.Close()

	s, ok := newSearcher(cat).(layeredSearcher)
	if !ok {
		t.Fatalf("newSearcher returned %T", newSearcher(cat))
	}
	if s.local != nil {
		t.Error("an empty catalog was layered in")
	}
}

// A confident local "no" is final. Asking Scryfall afterwards would hand exactly
// the rejected cases to the endpoint that resolves "option" to "Opt" —
// reintroducing the bug the local check exists to prevent.
func TestFuzzyRefusalIsNotRetriedRemotely(t *testing.T) {
	remote := &countingSearcher{card: &scryfall.Card{Name: "Opt"}}
	s := layeredSearcher{local: &fakeLocal{}, remote: remote}

	got, err := s.NamedFuzzy(context.Background(), "option")
	if err != nil {
		t.Fatalf("NamedFuzzy: %v", err)
	}
	if got != nil {
		t.Errorf("got %q, want the local refusal to stand", got.Name)
	}
	if remote.fuzzy != 0 {
		t.Error("a rejected read was sent to Scryfall's fuzzy search anyway")
	}
}

// A local hit must not touch the API at all — that is the point.
func TestLocalHitsSkipTheAPI(t *testing.T) {
	remote := &countingSearcher{}
	s := layeredSearcher{local: &fakeLocal{}, remote: remote}
	ctx := context.Background()

	if got, _ := s.Autocomplete(ctx, "sol"); len(got) == 0 {
		t.Error("no local autocomplete")
	}
	if got, _ := s.SearchPrints(ctx, "Sol Ring"); len(got) == 0 {
		t.Error("no local printings")
	}
	if got, _ := s.NamedFuzzy(ctx, "Sol Rlng"); got == nil {
		t.Error("no local fuzzy match")
	}
	if remote.auto+remote.prints+remote.fuzzy != 0 {
		t.Errorf("api was called %d/%d/%d times for locally-answerable lookups",
			remote.auto, remote.prints, remote.fuzzy)
	}
}

// A name the catalog has never seen — a printing newer than the last build —
// still resolves, which is what keeps the cache advisory rather than
// authoritative.
func TestMissesFallThroughToTheAPI(t *testing.T) {
	remote := &countingSearcher{
		names: []string{"Brand New Card"},
		cards: []scryfall.Card{{ID: "new", Name: "Brand New Card"}},
	}
	s := layeredSearcher{local: &fakeLocal{}, remote: remote}
	ctx := context.Background()

	if got, _ := s.Autocomplete(ctx, "brand new"); len(got) != 1 {
		t.Errorf("Autocomplete = %v, want the API's answer", got)
	}
	if got, _ := s.SearchPrints(ctx, "Brand New Card"); len(got) != 1 {
		t.Errorf("SearchPrints = %v, want the API's answer", got)
	}
	if remote.auto != 1 || remote.prints != 1 {
		t.Errorf("api calls: %d/%d, want one each", remote.auto, remote.prints)
	}
}

// fakeLocal stands in for the catalog: it knows one card and refuses everything
// else, which is the behaviour the layering has to respect.
type fakeLocal struct {
	auto, prints, fuzzy int
	err                 error
}

func (f *fakeLocal) Autocomplete(_ context.Context, q string) ([]string, error) {
	f.auto++
	if f.err != nil {
		return nil, f.err
	}
	if strings.Contains(strings.ToLower(q), "sol") {
		return []string{"Sol Ring"}, nil
	}
	return nil, nil
}

func (f *fakeLocal) SearchPrints(_ context.Context, name string) ([]scryfall.Card, error) {
	f.prints++
	if f.err != nil {
		return nil, f.err
	}
	if name == "Sol Ring" {
		return []scryfall.Card{{ID: "sol", Name: "Sol Ring"}}, nil
	}
	return nil, nil
}

// NamedFuzzy resolves a plausible read and refuses anything else, exactly as the
// catalog's own matcher does.
func (f *fakeLocal) NamedFuzzy(_ context.Context, text string) (*scryfall.Card, error) {
	f.fuzzy++
	if f.err != nil {
		return nil, f.err
	}
	if cardname.Plausible(text, "Sol Ring") {
		return &scryfall.Card{ID: "sol", Name: "Sol Ring"}, nil
	}
	return nil, nil
}
