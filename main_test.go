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
	if err := st.AddCardFinish(scryfall.Card{
		ID: "x", Set: "uma", CollectorNumber: "7", Name: "Ulamog",
		ScryfallURL: "http://x", PriceUSD: f(10),
	}, "nonfoil", 2); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	// cmdBrowse must not try to open a bubbletea program here; if the TTY gate
	// were wrong this would hang or fail rather than return.
	if err := cmdBrowse(context.Background(), st, false); err != nil {
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

func (c *countingSearcher) NamedFuzzy(context.Context, string) (*scryfall.Card, cardname.Match, error) {
	c.fuzzy++
	return c.card, cardname.Match{}, nil
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

// A local miss — including a refusal — falls through to the API: a card from
// a set newer than the catalog build looks exactly like junk to the catalog,
// and a final local "no" made every newly-released card unscannable.
func TestLocalMissFallsThroughToRemoteFuzzy(t *testing.T) {
	remote := &countingSearcher{card: &scryfall.Card{ID: "new", Name: "Brand New Card"}}
	s := layeredSearcher{local: &fakeLocal{}, remote: remote}

	got, _, err := s.NamedFuzzy(context.Background(), "Brand New Card")
	if err != nil {
		t.Fatalf("NamedFuzzy: %v", err)
	}
	if got == nil || got.Name != "Brand New Card" {
		t.Errorf("got %v, want the API's answer for a post-catalog card", got)
	}
	if remote.fuzzy != 1 {
		t.Errorf("api fuzzy called %d times, want 1", remote.fuzzy)
	}
}

// What keeps that fallthrough safe: the Scryfall adapter is an identity check,
// not a search. The API resolves "option" to the card "Opt" because the query
// contains the name; the adapter must refuse an answer that doesn't plausibly
// explain what was read.
func TestRemoteFuzzyRejectsImplausibleAnswer(t *testing.T) {
	// scryfallSearcher itself would call the network; exercise the same gate
	// it applies, against the case that motivated it.
	if cardname.Plausible("option", "Opt") {
		t.Fatal("the option→Opt case must be implausible, or the adapter gate is vacuous")
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
	if got, _, _ := s.NamedFuzzy(ctx, "Sol Rlng"); got == nil {
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
func (f *fakeLocal) NamedFuzzy(_ context.Context, text string) (*scryfall.Card, cardname.Match, error) {
	f.fuzzy++
	if f.err != nil {
		return nil, cardname.Match{}, f.err
	}
	if cardname.Plausible(text, "Sol Ring") {
		n := cardname.Normalize(text)
		c := cardname.Normalize("Sol Ring")
		return &scryfall.Card{ID: "sol", Name: "Sol Ring"},
			cardname.Match{Exact: n == c, Similarity: cardname.Similarity(n, c)}, nil
	}
	return nil, cardname.Match{}, nil
}

func TestExtractJSONFlag(t *testing.T) {
	for _, tt := range []struct {
		args     []string
		wantRest []string
		wantJSON bool
	}{
		{[]string{"--json", "movers"}, []string{"movers"}, true},
		{[]string{"export", "--json"}, []string{"export"}, true},
		{[]string{"-json"}, []string{}, true},
		{[]string{"movers"}, []string{"movers"}, false},
		// After a bare --, --json is a positional for the subcommand, not ours.
		{[]string{"deck", "--", "--json"}, []string{"deck", "--", "--json"}, false},
	} {
		rest, jsonOut := extractJSONFlag(tt.args)
		if jsonOut != tt.wantJSON || strings.Join(rest, " ") != strings.Join(tt.wantRest, " ") {
			t.Errorf("extractJSONFlag(%v) = (%v, %v), want (%v, %v)",
				tt.args, rest, jsonOut, tt.wantRest, tt.wantJSON)
		}
	}
}
