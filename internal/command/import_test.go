package command

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/collsource"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// manaboxFixture is collsource's ManaBox sample, addressed relative to this
// package rather than to the module root. `go test` runs each package with its
// own directory as the working directory, so a module-root path only works
// while the package sits at the module root — which stopped being true when
// the commands moved under internal/.
const manaboxFixture = "../collsource/testdata/manabox.csv"

// The other two third-party exports, beside ManaBox's. collsource owns all
// three so that the parser tests and these read the same bytes: a fixture that
// drifted to suit one layer would quietly stop describing the other.
const (
	moxfieldFixture = "../collsource/testdata/moxfield.csv"
	delverFixture   = "../collsource/testdata/delver.csv"
)

// stubFetch swaps the resolver's Scryfall lookup for an in-memory one over
// the given cards, matching by the same keys the resolver indexes. Covers
// deck add and import alike — they share cardResolver. Returns a call counter
// so tests can see whether the name-fallback pass ran.
func stubFetch(t *testing.T, cards ...scryfall.Card) *int {
	t.Helper()
	index := make(map[string]scryfall.Card, len(cards)*3)
	for _, c := range cards {
		index[c.ID] = c
		index[strings.ToLower(c.Set)+"/"+c.CollectorNumber] = c
		index[strings.ToLower(c.Name)] = c
	}
	calls := new(int)
	old := cardResolver.Fetch
	t.Cleanup(func() { cardResolver.Fetch = old })
	cardResolver.Fetch = func(_ context.Context, ids []scryfall.Identifier) ([]scryfall.Card, []scryfall.Identifier, error) {
		*calls++
		var found []scryfall.Card
		var notFound []scryfall.Identifier
		seen := make(map[string]bool)
		for _, ident := range ids {
			c, ok := index[ident.Key()]
			if !ok {
				notFound = append(notFound, ident)
				continue
			}
			if !seen[c.ID] {
				seen[c.ID] = true
				found = append(found, c)
			}
		}
		return found, notFound, nil
	}
	return calls
}

// importCmd dispatches `hoard import` through the real tree, so these tests
// cover the flag parsing and the one-file argument check as well as the
// import itself.
func importCmd(st *store.Store, args ...string) error {
	_, err := execCmd(context.Background(), st, append([]string{"import"}, args...), false)
	return err
}

func importStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// Fully priced printings, so fillPriceGaps finds no gap and never talks to
// MTGJSON in tests.
func importFixtures() []scryfall.Card {
	return []scryfall.Card{
		{ID: "sol-id-1", Set: "c21", CollectorNumber: "125", Name: "Sol Ring",
			ScryfallURL: "http://sol", PriceUSD: f(2), PriceUSDFoil: f(12.5),
			Finishes: []string{"nonfoil", "foil"}},
		{ID: "bolt-id-1", Set: "2x2", CollectorNumber: "117", Name: "Lightning Bolt",
			ScryfallURL: "http://bolt", PriceUSD: f(1.5), PriceUSDFoil: f(4),
			Finishes: []string{"nonfoil", "foil"}},
		{ID: "remora-id-1", Set: "ice", CollectorNumber: "78", Name: "Mystic Remora",
			ScryfallURL: "http://rem", PriceUSD: f(5), PriceUSDFoil: f(50),
			Finishes: []string{"nonfoil"}},
	}
}

// The ManaBox fixture: 2 Sol Ring + 1 foil Bolt in "Trade Binder", 1 Remora
// with no binder name. Preserving binders must fan them out accordingly.
func TestCmdImportPreservesManaBoxBinders(t *testing.T) {
	st := importStore(t)
	stubFetch(t, importFixtures()...)
	err := importCmd(st, "--preserve-binders", manaboxFixture)
	if err != nil {
		t.Fatalf("hoard import: %v", err)
	}

	binders, err := st.ListBinders()
	if err != nil {
		t.Fatalf("ListBinders: %v", err)
	}
	if len(binders) != 2 || binders[1].Name != "Trade Binder" {
		t.Fatalf("binders = %+v, want the default plus Trade Binder", binders)
	}
	if got := binders[1].TotalCopies; got != 3 {
		t.Errorf("Trade Binder holds %d copies, want 3", got)
	}
	rows, err := st.BinderByFinish(binders[0].ID)
	if err != nil {
		t.Fatalf("BinderByFinish: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "Mystic Remora" || rows[0].Quantity != 1 {
		t.Errorf("default binder rows = %+v, want just the remora", rows)
	}
}

// A set+number Scryfall does not know still resolves through the second,
// name-keyed pass rather than being reported unresolved.
func TestCmdImportFallsBackToNames(t *testing.T) {
	st := importStore(t)
	calls := stubFetch(t, importFixtures()...)
	csv := filepath.Join(t.TempDir(), "mox.csv")
	os.WriteFile(csv, []byte(
		"Count,Tradelist Count,Name,Edition,Collector Number,Foil\n"+
			"1,0,Sol Ring,zzz,999,\n"), 0o644)

	if err := importCmd(st, csv); err != nil {
		t.Fatalf("hoard import: %v", err)
	}
	if *calls != 2 {
		t.Errorf("fetch called %d times, want 2 (bulk pass + name fallback)", *calls)
	}
	totals, err := st.CollectionTotals()
	if err != nil {
		t.Fatalf("CollectionTotals: %v", err)
	}
	if totals.TotalCopies != 1 {
		t.Errorf("copies = %d, want the fallback-resolved Sol Ring", totals.TotalCopies)
	}
}

func TestCmdImportDryRunWritesNothing(t *testing.T) {
	st := importStore(t)
	stubFetch(t, importFixtures()...)
	err := importCmd(st, "--dry-run", "--preserve-binders", manaboxFixture)
	if err != nil {
		t.Fatalf("hoard import: %v", err)
	}
	totals, _ := st.CollectionTotals()
	if totals.TotalCopies != 0 {
		t.Errorf("dry run wrote %d copies", totals.TotalCopies)
	}
	binders, _ := st.ListBinders()
	if len(binders) != 1 {
		t.Errorf("dry run created binders: %+v", binders)
	}
}

// The sprint checkpoint, now with a deck in the source: export → import into a
// fresh database → identical binder totals, with the deck's rows skipped
// rather than poured into a binder as loose cards.
func TestExportImportRoundTrip(t *testing.T) {
	src := importStore(t)
	cards := importFixtures()
	if err := src.AddCardFinish(cards[0], "nonfoil", 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	trade, err := src.CreateBinder("Trade")
	if err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	if err := src.AddCardFinishTo(trade, cards[0], "foil", 1); err != nil {
		t.Fatalf("AddCardFinishTo: %v", err)
	}
	if err := src.AddCardFinishTo(trade, cards[2], "nonfoil", 3); err != nil {
		t.Fatalf("AddCardFinishTo: %v", err)
	}
	// A deck too: before Container Kind existed, its cards came back as loose
	// copies and inflated the binder totals.
	if err := src.UpsertPrintings([]scryfall.Card{cards[1]}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	if _, err := src.UpsertDeck(store.DeckMeta{Name: "Fish", Source: "manual", SourceID: "deck:fish"},
		[]store.Entry{{ScryfallID: cards[1].ID, Finish: "nonfoil", Board: "main", Quantity: 4}}); err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}

	file := filepath.Join(t.TempDir(), "roundtrip.csv")
	if _, err := execCmd(context.Background(), src, []string{"export", "-o", file}, false); err != nil {
		t.Fatalf("hoard export: %v", err)
	}

	dst := importStore(t)
	stubFetch(t, cards...)
	// Partial, not clean: the deck's four Bolts are in the file and are not
	// coming back this way. The binder halves below still have to be exact.
	if err := importCmd(dst, "--preserve-binders", file); !errors.Is(err, errPartial) {
		t.Fatalf("hoard import: err = %v, want the partial sentinel for the skipped deck rows", err)
	}

	want, err := src.CollectionTotals()
	if err != nil {
		t.Fatalf("CollectionTotals: %v", err)
	}
	got, err := dst.CollectionTotals()
	if err != nil {
		t.Fatalf("CollectionTotals: %v", err)
	}
	if got != want {
		t.Errorf("round-trip totals = %+v, want %+v (deck rows must not inflate the binders)", got, want)
	}
	srcBinders, _ := src.ListBinders()
	dstBinders, _ := dst.ListBinders()
	if len(srcBinders) != len(dstBinders) {
		t.Fatalf("binders: %d vs %d", len(srcBinders), len(dstBinders))
	}
	for i := range srcBinders {
		if srcBinders[i].Name != dstBinders[i].Name || srcBinders[i].TotalCopies != dstBinders[i].TotalCopies {
			t.Errorf("binder %d: %s/%d vs %s/%d", i,
				srcBinders[i].Name, srcBinders[i].TotalCopies,
				dstBinders[i].Name, dstBinders[i].TotalCopies)
		}
	}
	// No "Fish" binder either: the deck's rows were skipped, not renamed.
	for _, b := range dstBinders {
		if b.Name == "Fish" {
			t.Error("the deck came back as a binder")
		}
	}
}

// A canonical export's deck rows are skipped, and the exit status has to say
// so: this exact command against the real collection restored 356 of 2,235
// copies and exited 0, so a backup script ran green while dropping 22 decks.
// errPartial is what Run maps to exit 2 — "done, mostly" — and the rows that
// could be imported are still imported.
func TestCmdImportIsPartialWhenItSkipsDeckRows(t *testing.T) {
	const header = "Count,Name,Set,Collector Number,Finish,Condition,Scryfall ID," +
		"Container,Container Kind,Board,Price USD\n"
	const binderRow = "2,Sol Ring,c21,125,nonfoil,,sol-id-1,Binder,binder,main,2.00\n"
	const deckRow = "4,Lightning Bolt,2x2,117,nonfoil,,bolt-id-1,Fish,deck,main,1.50\n"

	write := func(t *testing.T, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "export.csv")
		if err := os.WriteFile(path, []byte(header+body), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		return path
	}

	t.Run("deck rows present", func(t *testing.T) {
		st := importStore(t)
		stubFetch(t, importFixtures()...)
		err := importCmd(st, write(t, binderRow+deckRow))
		if !errors.Is(err, errPartial) {
			t.Errorf("err = %v, want the partial sentinel (exit 2)", err)
		}
		// The binder half still landed: partial means done-mostly, not undone.
		totals, _ := st.CollectionTotals()
		if totals.TotalCopies != 2 {
			t.Errorf("copies = %d, want the 2 binder copies to have been written anyway",
				totals.TotalCopies)
		}
	})

	// The rehearsal has to predict the real run, or it is not a rehearsal —
	// and --dry-run is the form the bug was reported in.
	t.Run("dry run", func(t *testing.T) {
		st := importStore(t)
		stubFetch(t, importFixtures()...)
		if err := importCmd(st, "--dry-run", write(t, binderRow+deckRow)); !errors.Is(err, errPartial) {
			t.Errorf("err = %v, want the partial sentinel (exit 2)", err)
		}
	})

	// And a file with nothing to skip still exits clean, so the status stays
	// worth reading.
	t.Run("no deck rows", func(t *testing.T) {
		st := importStore(t)
		stubFetch(t, importFixtures()...)
		if err := importCmd(st, write(t, binderRow)); err != nil {
			t.Errorf("err = %v, want nil for a file that imported whole", err)
		}
	})
}

// Importing the same content twice is refused via the ledger; --again is the
// explicit override, and a dry run neither records nor refuses.
func TestCmdImportRefusesRepeats(t *testing.T) {
	st := importStore(t)
	stubFetch(t, importFixtures()...)
	file := manaboxFixture

	if err := importCmd(st, "--dry-run", file); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if err := importCmd(st, file); err != nil {
		t.Fatalf("first import (after a dry run): %v", err)
	}
	totals, _ := st.CollectionTotals()
	first := totals.TotalCopies

	err := importCmd(st, file)
	if err == nil || !strings.Contains(err.Error(), "already imported") {
		t.Fatalf("second import: err = %v, want an already-imported refusal", err)
	}
	totals, _ = st.CollectionTotals()
	if totals.TotalCopies != first {
		t.Errorf("copies changed on a refused import: %d -> %d", first, totals.TotalCopies)
	}

	if err := importCmd(st, "--again", file); err != nil {
		t.Fatalf("--again: %v", err)
	}
	totals, _ = st.CollectionTotals()
	if totals.TotalCopies != 2*first {
		t.Errorf("copies after --again = %d, want %d (an explicit double)", totals.TotalCopies, 2*first)
	}
}

// --format names one of five CSV dialects. An unknown value used to be carried
// all the way to the CSV lexer, so `--format json` over a JSON file answered
// with a complaint about a bare quote on line 2 and sent the reader looking for
// a quoting problem that was not there.
func TestCmdImportRejectsAnUnknownFormat(t *testing.T) {
	st := importStore(t)
	path := filepath.Join(t.TempDir(), "holdings.json")
	if err := os.WriteFile(path, []byte(`{
  "schemaVersion": "1.0.1",
  "kind": "holdings",
  "holdings": {"rows": []}
}
`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	err := importCmd(st, "--format", "json", path)
	if err == nil {
		t.Fatal("hoard import --format json succeeded, want a usage error")
	}
	// The value it could not honour, then the set it can, in `schema --kind`'s
	// shape — not a lexing complaint about the file, which is well-formed.
	if !strings.Contains(err.Error(), `unknown format "json"`) {
		t.Errorf("err = %v, want it to name the value it refused", err)
	}
	for _, format := range []string{"auto", "manabox", "moxfield", "delver", "hoard"} {
		if !strings.Contains(err.Error(), format) {
			t.Errorf("err = %v, want it to list %q as an accepted format", err, format)
		}
	}
}

// The empty string is refused with the rest. It was a synonym for auto by
// accident — collsource sniffs on anything falsy — and nothing ever offered it,
// so the only way to send it is an unset variable in --format "$FMT", which is
// this bug's own shape: a value the caller did not mean, honoured as something
// plausible instead of questioned.
func TestCmdImportRejectsAnEmptyFormat(t *testing.T) {
	st := importStore(t)
	err := importCmd(st, "--format", "", manaboxFixture)
	if err == nil {
		t.Fatal("hoard import --format (empty) succeeded, want a usage error")
	}
	if !strings.Contains(err.Error(), "unknown format") {
		t.Errorf("err = %v, want the unknown-format sentence", err)
	}
}

// importFormats restates a table collsource keeps to itself, so the restatement
// is checked rather than trusted: every name the command accepts must be one
// the parser will dispatch on. A missing column is the parser having got past
// the name; an unknown-format complaint is this list having invented one.
func TestImportFormatsAreAllRealToTheParser(t *testing.T) {
	for _, format := range importFormats {
		if format == "auto" {
			continue // not a spec — the instruction to sniff for one
		}
		_, err := collsource.Parse(strings.NewReader("a,b\n"), format)
		if err != nil && strings.Contains(err.Error(), "unknown format") {
			t.Errorf("--format %s is offered but collsource has no such parser: %v", format, err)
		}
	}
}

// The clean case: a named format that is real still imports exactly as it did.
// The fixture is ManaBox's, named rather than sniffed, so the check that runs
// first cannot have swallowed a file the parser would have accepted.
func TestCmdImportNamedFormatIsUnchanged(t *testing.T) {
	st := importStore(t)
	stubFetch(t, importFixtures()...)
	if err := importCmd(st, "--format", "manabox", manaboxFixture); err != nil {
		t.Fatalf("hoard import --format manabox: %v", err)
	}
	totals, err := st.CollectionTotals()
	if err != nil {
		t.Fatalf("CollectionTotals: %v", err)
	}
	if totals.TotalCopies != 4 {
		t.Errorf("imported %d copies, want 4", totals.TotalCopies)
	}
}

// A hoard CSV round trip is card-exact and value-approximate, and only the
// first half was ever written down. 915 cards out, 915 cards in, $3,797.19 ->
// $3,797.53: the whole 34 cents was one card whose price had moved between the
// export and the import, because no importer reads a price from a file — every
// dialect's price column is dropped, and hoard's own is not even mapped. That
// is the right behaviour and it is indistinguishable, from the outside, from a
// rounding bug in a program that handles money. The help page is where someone
// checks before they decide which one they are looking at.
//
// Asserted on the page rather than left as prose in the source, because an
// undocumented behaviour and a documented one differ only in a paragraph that
// nothing keeps honest.
func TestCmdImportHelpSaysPricesAreReDerived(t *testing.T) {
	var b bytes.Buffer
	renderHelp(&b, ui.Env{Width: 100}, "import")
	help := b.String()
	for _, want := range []string{"re-derived", "Price", "catalog"} {
		if !strings.Contains(help, want) {
			t.Errorf("hoard import --help does not mention %q; a round trip that\n"+
				"comes back a few cents different has to say why somewhere", want)
		}
	}
}

// A destination that does not exist is knowable from the store alone, and the
// import used to find that out last: --binder with a typo resolved all 678 rows
// of a real collection over the network — 4.6 seconds, with a progress counter
// climbing to 75/678 — and only then asked whether the binder existed. The cost
// is the smaller half. The counter is the larger one: it reports work being
// done successfully, so the operator's read of the run is "it's working" right
// up until the refusal, and a typo becomes five seconds of misplaced confidence.
//
// The resolver's call count is the assertion, not the elapsed time. A timing
// test would measure the stub, which is instant either way; "the network was
// never consulted" is the property the fix actually establishes, and it holds on
// any machine.
func TestCmdImportChecksTheBinderBeforeResolvingAnything(t *testing.T) {
	st := importStore(t)
	calls := stubFetch(t, importFixtures()...)

	err := importCmd(st, "--binder", "NoSuchBinder", manaboxFixture)
	if err == nil {
		t.Fatal("hoard import --binder NoSuchBinder succeeded, want a refusal")
	}
	if !strings.Contains(err.Error(), `no binder matching "NoSuchBinder"`) {
		t.Errorf("err = %v, want it to name the binder it could not find", err)
	}
	if *calls != 0 {
		t.Errorf("the resolver ran %d time(s) for an import that had nowhere to go;\n"+
			"the destination check belongs above the resolve, not below it", *calls)
	}

	// The refusal names the way out. Both commands are real — `hoard binder new
	// NAME` and `hoard binder list` are in the binder command's own tree — and a
	// test is what keeps that true, since an error message is the one place a
	// command that does not exist can be advertised for a release without
	// anything failing.
	for _, want := range []string{"hoard binder new", "hoard binder list"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to name %q as the fix", err, want)
		}
	}

	totals, terr := st.CollectionTotals()
	if terr != nil {
		t.Fatalf("CollectionTotals: %v", terr)
	}
	if totals.TotalCopies != 0 {
		t.Errorf("a refused import wrote %d copies", totals.TotalCopies)
	}
}

// The create-it advice is attached to exactly one refusal. An ambiguous
// fragment is the other way BinderByRef says no, and there "create it with
// binder new" is advice to make a third binder named like the two that already
// matched — the reader's problem is that they have too many, not none.
func TestCmdImportDoesNotAdviseCreatingAnAmbiguousBinder(t *testing.T) {
	st := importStore(t)
	stubFetch(t, importFixtures()...)
	for _, name := range []string{"Trade Alpha", "Trade Beta"} {
		if _, err := st.CreateBinder(name); err != nil {
			t.Fatalf("CreateBinder(%q): %v", name, err)
		}
	}

	err := importCmd(st, "--binder", "Trade", manaboxFixture)
	if err == nil {
		t.Fatal("hoard import --binder Trade succeeded, want an ambiguity refusal")
	}
	if !strings.Contains(err.Error(), "matches 2 binders") {
		t.Fatalf("err = %v, want the store's ambiguity refusal", err)
	}
	if strings.Contains(err.Error(), "binder new") {
		t.Errorf("err = %v, want no create-it advice: the name matched two binders,\n"+
			"so creating a third is not the fix", err)
	}
}

// The guard's date is the one date in the CLI a user ever saw as RFC 3339 in
// UTC, which on a US clock reads as tomorrow — an otherwise excellent refusal
// undermined by looking like it had the wrong day.
//
// Checked through the command so the assertion covers the sentence a user
// actually reads; ledger_test.go pins the conversion itself against a fixed
// zone, which is the half that can be got wrong invisibly on a UTC machine.
func TestCmdImportRefusalDatesReadLikeEveryOtherDate(t *testing.T) {
	st := importStore(t)
	stubFetch(t, importFixtures()...)
	if err := importCmd(st, manaboxFixture); err != nil {
		t.Fatalf("first import: %v", err)
	}

	err := importCmd(st, manaboxFixture)
	if err == nil {
		t.Fatal("second import succeeded, want an already-imported refusal")
	}
	const marker = "already imported on "
	msg := err.Error()
	i := strings.Index(msg, marker)
	if i < 0 {
		t.Fatalf("err = %v, want the ledger's refusal", err)
	}
	stamp := msg[i+len(marker):]
	if j := strings.Index(stamp, " ("); j >= 0 {
		stamp = stamp[:j]
	}
	if strings.ContainsAny(stamp, "TZ") {
		t.Errorf("refusal stamp = %q, still raw RFC 3339 — every other date in the\n"+
			"CLI renders like '10 Aug 2026'", stamp)
	}
	if _, perr := time.Parse("2 Jan 2006 15:04", stamp); perr != nil {
		t.Errorf("refusal stamp = %q, want the CLI's own date format: %v", stamp, perr)
	}
}

func TestCmdImportRejectsBadFlagCombos(t *testing.T) {
	st := importStore(t)
	for _, args := range [][]string{
		{},
		{"a.csv", "b.csv"},
		{"--binder", "Trade", "--preserve-binders", "a.csv"},
	} {
		if err := importCmd(st, args...); err == nil {
			t.Errorf("hoard import %v succeeded, want an error", args)
		}
	}
}

// A pre-rename export whose Container column says "Binder" keeps landing in
// the default binder after it is renamed: the old name is a reserved alias,
// not a new binder to create.
func TestCmdImportAliasesOldDefaultNameAfterRename(t *testing.T) {
	st := importStore(t)
	stubFetch(t, importFixtures()...)
	def, err := st.BinderByRef(store.LooseName)
	if err != nil {
		t.Fatalf("BinderByRef: %v", err)
	}
	if err := st.RenameBinder(def.ID, "Shoebox"); err != nil {
		t.Fatalf("RenameBinder: %v", err)
	}

	csv := filepath.Join(t.TempDir(), "old-export.csv")
	os.WriteFile(csv, []byte(
		"Count,Name,Set,Collector Number,Finish,Scryfall ID,Container,Container Kind,Board,Price USD\n"+
			"2,Sol Ring,c21,125,nonfoil,sol-id-1,Binder,binder,main,2.00\n"), 0o644)
	if err := importCmd(st, "--preserve-binders", csv); err != nil {
		t.Fatalf("hoard import: %v", err)
	}

	binders, err := st.ListBinders()
	if err != nil {
		t.Fatalf("ListBinders: %v", err)
	}
	if len(binders) != 1 || binders[0].Name != "Shoebox" {
		t.Fatalf("binders = %+v, want only the renamed default", binders)
	}
	if got := binders[0].TotalCopies; got != 2 {
		t.Errorf("default binder holds %d copies, want 2", got)
	}
}

// A lone dash reads the CSV from stdin, the spelling `hoard add --file` has
// always accepted. ImportOptions has documented its Display as a path or
// stdin since it was written; only this command never delivered the second
// half, and the round trip import itself recommends —
// `hoard export --deck X --format text | hoard deck add --file -` — needed a
// temporary file without it.
func TestCmdImportStdin(t *testing.T) {
	st := importStore(t)
	stubFetch(t, importFixtures()...)
	csv, err := os.ReadFile(manaboxFixture)
	if err != nil {
		t.Fatal(err)
	}
	withStdin(t, string(csv))

	if err := importCmd(st, "--preserve-binders", "-"); err != nil {
		t.Fatalf("hoard import -: %v", err)
	}
	binders, err := st.ListBinders()
	if err != nil {
		t.Fatalf("ListBinders: %v", err)
	}
	if len(binders) != 2 || binders[1].Name != "Trade Binder" {
		t.Fatalf("binders = %+v, want the default plus Trade Binder", binders)
	}
	totals, err := st.CollectionTotals()
	if err != nil {
		t.Fatalf("CollectionTotals: %v", err)
	}
	if totals.TotalCopies != 4 {
		t.Errorf("imported %d copies, want the fixture's 4", totals.TotalCopies)
	}
}

// The three third-party exports, each imported end to end into a database of
// its own.
//
// collsource's tests prove the headers sniff and the rows parse; they stop at
// the parser. These carry each fixture the rest of the way — resolve, then
// store — because that is where a format's compatibility is actually decided,
// and until now only ManaBox was taken that far: Moxfield reached a database
// only through a synthetic one-row CSV written to exercise the name fallback,
// and Delver reached one never.
//
// Asserted as the complete set of holdings rather than as a total, because a
// total hides the two failures that actually happen — a row silently dropped
// and a row imported twice — by letting them cancel. Sorted by name and finish
// rather than taken in the store's own order, which is by value, so that a
// price change in the fixtures cannot reorder the expectation.
//
// Condition rides along deliberately. These three apps do not share a
// vocabulary — ManaBox speaks Cardmarket's seven ("excellent"), Moxfield and
// Delver TCGplayer's five ("Lightly Played", "Played") — and nothing else
// proves both scales survive as far as the database rather than folding to
// unknown somewhere past the parser.
func TestCmdImportThirdPartyFormatsLandInTheDatabase(t *testing.T) {
	type holding struct {
		Name      string
		Finish    string
		Quantity  int
		Condition string
	}
	for _, tc := range []struct {
		format  string
		fixture string
		want    []holding
	}{
		{
			format:  "manabox",
			fixture: manaboxFixture,
			want: []holding{
				// Cardmarket's "excellent" folds onto lp; "near_mint" is nm.
				{"Lightning Bolt", "foil", 1, "lp"},
				{"Mystic Remora", "nonfoil", 1, "nm"},
				{"Sol Ring", "nonfoil", 2, "nm"},
			},
		},
		{
			format:  "moxfield",
			fixture: moxfieldFixture,
			want: []holding{
				// The quoted comma in this name has to survive the CSV reader,
				// the resolver's name-keyed pass and the insert to land here.
				{"Borrowing 100,000 Arrows", "nonfoil", 1, "nm"},
				{"Lightning Bolt", "foil", 1, "lp"},
				{"Sol Ring", "nonfoil", 2, "nm"},
			},
		},
		{
			format:  "delver",
			fixture: delverFixture,
			want: []holding{
				// "Played" is Cardmarket's word but TCGplayer's position: mp.
				{"Lightning Bolt", "foil", 1, "mp"},
				// Written "2x" in the fixture.
				{"Sol Ring", "nonfoil", 2, "nm"},
			},
		},
	} {
		t.Run(tc.format, func(t *testing.T) {
			st := importStore(t)
			// The shared fixtures plus the one card only the Moxfield export
			// names. Added here rather than to importFixtures() because that
			// list is the resolver's whole universe for every other test in
			// this file, and widening it would weaken their unresolved paths.
			stubFetch(t, append(importFixtures(), scryfall.Card{
				ID: "arrows-id-1", Set: "ptk", CollectorNumber: "31",
				Name: "Borrowing 100,000 Arrows", ScryfallURL: "http://arrows",
				PriceUSD: f(0.5), Finishes: []string{"nonfoil"},
			})...)

			// Sniffed, not named. --format would prove the parser runs; it
			// would not prove hoard recognizes the file a user actually has.
			if err := importCmd(st, tc.fixture); err != nil {
				t.Fatalf("hoard import %s: %v", tc.fixture, err)
			}

			rows, err := st.AllByFinish()
			if err != nil {
				t.Fatalf("AllByFinish: %v", err)
			}
			got := make([]holding, 0, len(rows))
			for _, r := range rows {
				got = append(got, holding{r.Name, r.Finish, r.Quantity, r.Condition})
			}
			sort.Slice(got, func(i, j int) bool {
				if got[i].Name != got[j].Name {
					return got[i].Name < got[j].Name
				}
				return got[i].Finish < got[j].Finish
			})
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("holdings after importing the %s fixture:\n got %+v\nwant %+v",
					tc.format, got, tc.want)
			}
		})
	}
}

// A file that will not parse names its source, and for a pipe the source is
// the word stdin — a dash in that sentence would read like a flag. Matches
// what `watch import -` already says.
func TestCmdImportStdinNamesTheSource(t *testing.T) {
	st := importStore(t)
	withStdin(t, "not,a,collection,csv\n")
	err := importCmd(st, "-")
	if err == nil || !strings.Contains(err.Error(), "stdin") {
		t.Fatalf("err = %v, want a parse error naming stdin", err)
	}
}
