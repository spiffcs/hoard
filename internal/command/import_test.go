package command

import (
	"bytes"
	"context"
	"errors"
	"github.com/spiffcs/hoard/internal/finish"
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

const manaboxFixture = "../collsource/testdata/manabox.csv"

const (
	moxfieldFixture = "../collsource/testdata/moxfield.csv"
	delverFixture   = "../collsource/testdata/delver.csv"
)

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

func TestExportImportRoundTrip(t *testing.T) {
	src := importStore(t)
	cards := importFixtures()
	if err := src.AddCardFinish(cards[0], finish.Nonfoil, 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	trade, err := src.CreateBinder("Trade")
	if err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	if err := src.AddCardFinishTo(trade, cards[0], finish.Foil, 1); err != nil {
		t.Fatalf("AddCardFinishTo: %v", err)
	}
	if err := src.AddCardFinishTo(trade, cards[2], finish.Nonfoil, 3); err != nil {
		t.Fatalf("AddCardFinishTo: %v", err)
	}

	if err := src.UpsertPrintings([]scryfall.Card{cards[1]}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	if _, err := src.UpsertDeck(store.DeckMeta{Name: "Fish", Source: "manual", SourceID: "deck:fish"},
		[]store.Entry{{ScryfallID: cards[1].ID, Finish: finish.Nonfoil, Board: "main", Quantity: 4}}); err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}

	file := filepath.Join(t.TempDir(), "roundtrip.csv")
	if _, err := execCmd(context.Background(), src, []string{"export", "-o", file}, false); err != nil {
		t.Fatalf("hoard export: %v", err)
	}

	dst := importStore(t)
	stubFetch(t, cards...)

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

	for _, b := range dstBinders {
		if b.Name == "Fish" {
			t.Error("the deck came back as a binder")
		}
	}
}

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

		totals, _ := st.CollectionTotals()
		if totals.TotalCopies != 2 {
			t.Errorf("copies = %d, want the 2 binder copies to have been written anyway",
				totals.TotalCopies)
		}
	})

	t.Run("dry run", func(t *testing.T) {
		st := importStore(t)
		stubFetch(t, importFixtures()...)
		if err := importCmd(st, "--dry-run", write(t, binderRow+deckRow)); !errors.Is(err, errPartial) {
			t.Errorf("err = %v, want the partial sentinel (exit 2)", err)
		}
	})

	t.Run("no deck rows", func(t *testing.T) {
		st := importStore(t)
		stubFetch(t, importFixtures()...)
		if err := importCmd(st, write(t, binderRow)); err != nil {
			t.Errorf("err = %v, want nil for a file that imported whole", err)
		}
	})
}

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

	if !strings.Contains(err.Error(), `unknown format "json"`) {
		t.Errorf("err = %v, want it to name the value it refused", err)
	}
	for _, format := range []string{"auto", "manabox", "moxfield", "delver", "hoard"} {
		if !strings.Contains(err.Error(), format) {
			t.Errorf("err = %v, want it to list %q as an accepted format", err, format)
		}
	}
}

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

func TestImportFormatsAreAllRealToTheParser(t *testing.T) {
	for _, format := range importFormats {
		if format == "auto" {
			continue
		}
		_, err := collsource.Parse(strings.NewReader("a,b\n"), format)
		if err != nil && strings.Contains(err.Error(), "unknown format") {
			t.Errorf("--format %s is offered but collsource has no such parser: %v", format, err)
		}
	}
}

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

				{"Lightning Bolt", "foil", 1, "lp"},
				{"Mystic Remora", "nonfoil", 1, "nm"},
				{"Sol Ring", "nonfoil", 2, "nm"},
			},
		},
		{
			format:  "moxfield",
			fixture: moxfieldFixture,
			want: []holding{

				{"Borrowing 100,000 Arrows", "nonfoil", 1, "nm"},
				{"Lightning Bolt", "foil", 1, "lp"},
				{"Sol Ring", "nonfoil", 2, "nm"},
			},
		},
		{
			format:  "delver",
			fixture: delverFixture,
			want: []holding{

				{"Lightning Bolt", "foil", 1, "mp"},

				{"Sol Ring", "nonfoil", 2, "nm"},
			},
		},
	} {
		t.Run(tc.format, func(t *testing.T) {
			st := importStore(t)

			stubFetch(t, append(importFixtures(), scryfall.Card{
				ID: "arrows-id-1", Set: "ptk", CollectorNumber: "31",
				Name: "Borrowing 100,000 Arrows", ScryfallURL: "http://arrows",
				PriceUSD: f(0.5), Finishes: []string{"nonfoil"},
			})...)

			if err := importCmd(st, tc.fixture); err != nil {
				t.Fatalf("hoard import %s: %v", tc.fixture, err)
			}

			rows, err := st.AllByFinish()
			if err != nil {
				t.Fatalf("AllByFinish: %v", err)
			}
			got := make([]holding, 0, len(rows))
			for _, r := range rows {
				got = append(got, holding{r.Name, r.Finish.String(), r.Quantity, r.Condition})
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

func TestCmdImportStdinNamesTheSource(t *testing.T) {
	st := importStore(t)
	withStdin(t, "not,a,collection,csv\n")
	err := importCmd(st, "-")
	if err == nil || !strings.Contains(err.Error(), "stdin") {
		t.Fatalf("err = %v, want a parse error naming stdin", err)
	}
}
