package command

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/buildinfo"
	"github.com/spiffcs/hoard/internal/cardname"
	"github.com/spiffcs/hoard/internal/catalog"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

func TestVersionCommand(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}, {"-v"}} {
		oldStdout := os.Stdout
		r, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("os.Pipe: %v", err)
		}
		os.Stdout = w
		runErr := execute(args)
		w.Close()
		os.Stdout = oldStdout
		out, readErr := io.ReadAll(r)
		if readErr != nil {
			t.Fatalf("reading captured stdout: %v", readErr)
		}
		if runErr != nil {
			t.Errorf("execute(%v) = %v, want nil", args, runErr)
			continue
		}
		got := string(out)
		if !strings.Contains(got, "hoard "+buildinfo.Resolve()) {
			t.Errorf("execute(%v) output %q does not contain the resolved version %q", args, got, buildinfo.Resolve())
		}
		if !strings.Contains(got, "Fan Content") {
			t.Errorf("execute(%v) output %q does not carry the Fan Content notice", args, got)
		}
	}
}

func f(v float64) *float64 { return &v }

func TestGlobalFlagsParseAnywhere(t *testing.T) {
	for _, c := range []struct {
		name   string
		args   []string
		wantDB string
	}{
		{"absent", []string{"movers"}, ""},
		{"before the command", []string{"--db", "x.db", "movers"}, "x.db"},
		{"after the command", []string{"movers", "--db", "x.db"}, "x.db"},
		{"equals form", []string{"movers", "--db=x.db"}, "x.db"},
		{"repeated keeps the last", []string{"--db", "a.db", "movers", "--db", "b.db"}, "b.db"},
		{"a value that looks like a flag is still a value", []string{"--db", "--weird.db", "movers"}, "--weird.db"},
		{"subcommand flags are left alone", []string{"movers", "--since", "7d", "--db", "x.db"}, "x.db"},
	} {
		t.Run(c.name, func(t *testing.T) {
			root, g := buildRoot(&app{env: &cli.Env{}}, pipeEnv)
			sub, rest, err := root.Find(c.args)
			if err != nil {
				t.Fatalf("find: %v", err)
			}
			if err := sub.ParseFlags(rest); err != nil {
				t.Fatalf("parse %q: %v", c.args, err)
			}
			if g.db != c.wantDB {
				t.Errorf("db = %q, want %q", g.db, c.wantDB)
			}
		})
	}
}

func TestGlobalFlagsStopAtTheTerminator(t *testing.T) {
	root, g := buildRoot(&app{env: &cli.Env{}}, pipeEnv)
	sub, rest, err := root.Find([]string{"watch", "add", "--", "--db", "x.db"})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if err := sub.ParseFlags(rest); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if g.db != "" {
		t.Errorf("db = %q, want it untouched after --", g.db)
	}
	if want := []string{"--db", "x.db"}; !slices.Equal(sub.Flags().Args(), want) {
		t.Errorf("positionals = %q, want %q", sub.Flags().Args(), want)
	}
}

func TestGlobalDBFlagNeedsAValue(t *testing.T) {
	root, _ := buildRoot(&app{env: &cli.Env{}}, pipeEnv)
	sub, rest, err := root.Find([]string{"movers", "--db"})
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if err := sub.ParseFlags(rest); err == nil {
		t.Error("--db with no value parsed cleanly, want an error")
	}
}

func TestBareHoardWritesTheSummaryWhenNotATTY(t *testing.T) {

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
	}, finish.Nonfoil, 2); err != nil {
		t.Fatalf("AddCard: %v", err)
	}

	if err := cmdBrowse(context.Background(), st, false); err != nil {
		t.Fatalf("cmdBrowse: %v", err)
	}
}

func TestRemovedReadCommandsAreRejected(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"--db", filepath.Join(dir, "a.db"), "list"},
		{"--db", filepath.Join(dir, "b.db"), "summary"},
		{"--db", filepath.Join(dir, "c.db"), "deck", "list"},
		{"--db", filepath.Join(dir, "d.db"), "deck", "show", "x"},
	} {
		err := execute(args)
		if err == nil {
			t.Errorf("execute(%v) succeeded, want an unknown-command error", args)
			continue
		}
		if !strings.Contains(err.Error(), "unknown") {
			t.Errorf("execute(%v) = %v, want it to say the command is unknown", args, err)
		}
	}
}

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

func TestRemoteFuzzyRejectsImplausibleAnswer(t *testing.T) {

	if cardname.Plausible("option", "Opt") {
		t.Fatal("the option→Opt case must be implausible, or the adapter gate is vacuous")
	}
}

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

func TestGlobalJSONFlagParsesAnywhere(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want bool
	}{
		{[]string{"--json", "movers"}, true},
		{[]string{"movers", "--json"}, true},
		{[]string{"movers"}, false},

		{[]string{"watch", "add", "--", "--json"}, false},
	} {
		root, g := buildRoot(&app{env: &cli.Env{}}, pipeEnv)
		sub, rest, err := root.Find(tt.args)
		if err != nil {
			t.Fatalf("find %q: %v", tt.args, err)
		}
		if err := sub.ParseFlags(rest); err != nil {
			t.Fatalf("parse %q: %v", tt.args, err)
		}
		if g.json != tt.want {
			t.Errorf("json for %q = %v, want %v", tt.args, g.json, tt.want)
		}
	}
}

func TestUnknownCommandSuggestsTheNearMiss(t *testing.T) {
	root, _ := buildRoot(&app{env: &cli.Env{}}, pipeEnv)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs([]string{"mover"})

	err := root.Execute()
	if err == nil {
		t.Fatal("expected an error for an unknown command")
	}
	if !strings.Contains(err.Error(), "movers") {
		t.Errorf("no suggestion for %q in: %v", "mover", err)
	}
}

func TestRemovedReadCommandsStillRejected(t *testing.T) {
	for _, name := range []string{"list", "summary"} {
		root, _ := buildRoot(&app{env: &cli.Env{}}, pipeEnv)
		root.SetOut(io.Discard)
		root.SetErr(io.Discard)
		root.SetArgs([]string{name})
		if err := root.Execute(); err == nil {
			t.Errorf("hoard %s succeeded, want an unknown-command error", name)
		}
	}
}
