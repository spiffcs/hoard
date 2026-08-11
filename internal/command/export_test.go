package command

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

// exportStore builds a hoard with a card in the default binder, a foil of the
// same printing in a second binder, and an unpriced card in a deck — enough to
// exercise container labels, finish pricing, and the nil-price cell.
func exportStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	sol := scryfall.Card{ID: "sol", Set: "c21", CollectorNumber: "125", Name: "Sol Ring",
		ScryfallURL: "http://x", PriceUSD: f(2), PriceUSDFoil: f(12.5)}
	if err := st.AddCardFinish(sol, "nonfoil", 2); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	trade, err := st.CreateBinder("Trade")
	if err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	if err := st.AddCardFinishTo(trade, sol, "foil", 1); err != nil {
		t.Fatalf("AddCardFinishTo: %v", err)
	}

	remora := scryfall.Card{ID: "rem", Set: "ice", CollectorNumber: "78",
		Name: "Mystic Remora", ScryfallURL: "http://y"}
	if err := st.UpsertPrintings([]scryfall.Card{remora}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	if _, err := st.UpsertDeck(store.DeckMeta{Name: "Fish", Source: "manual", SourceID: "deck:fish"},
		[]store.Entry{{ScryfallID: "rem", Finish: "nonfoil", Board: "main", Quantity: 1}}); err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}
	return st
}

func TestCmdExportWritesEverythingByDefault(t *testing.T) {
	st := exportStore(t)
	out := filepath.Join(t.TempDir(), "out.csv")
	if _, err := execCmd(context.Background(), st, []string{"export", "-o", out}, false); err != nil {
		t.Fatalf("hoard export: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := strings.Join([]string{
		"Count,Name,Set,Collector Number,Finish,Condition,Scryfall ID,Container,Container Kind,Board,Price USD",
		"2,Sol Ring,c21,125,nonfoil,,sol,Binder,binder,main,2.00",
		"1,Mystic Remora,ice,78,nonfoil,,rem,Fish,deck,main,",
		"1,Sol Ring,c21,125,foil,,sol,Trade,binder,main,12.50",
		"",
	}, "\n")
	if string(got) != want {
		t.Errorf("export:\n%s\nwant:\n%s", got, want)
	}
}

func TestCmdExportScopesToOneBinder(t *testing.T) {
	st := exportStore(t)
	out := filepath.Join(t.TempDir(), "out.csv")
	if _, err := execCmd(context.Background(), st, []string{"export", "--binder", "Trade", "-o", out}, false); err != nil {
		t.Fatalf("hoard export: %v", err)
	}
	got, _ := os.ReadFile(out)
	if strings.Contains(string(got), "Fish") || strings.Contains(string(got), "Binder,binder,main") {
		t.Errorf("--binder Trade leaked other containers:\n%s", got)
	}
	if !strings.Contains(string(got), "1,Sol Ring,c21,125,foil,,sol,Trade,binder,main,12.50") {
		t.Errorf("--binder Trade is missing its own card:\n%s", got)
	}
}

func TestCmdExportRejectsBadFlagCombos(t *testing.T) {
	st := exportStore(t)
	for _, args := range [][]string{
		{"--binder", "Trade", "--deck", "Fish"},
		{"--all", "--binder", "Trade"},
		{"--format", "mtgo"},
	} {
		if _, err := execCmd(context.Background(), st, append([]string{"export"}, args...), false); err == nil {
			t.Errorf("hoard export %v succeeded, want an error", args)
		}
	}
}

// TestCmdExportJSON walks the whole wiring — store, row assembly, canonical
// sort, document envelope — and pins the exact bytes a script consumes.
func TestCmdExportJSON(t *testing.T) {
	st := exportStore(t)
	out := filepath.Join(t.TempDir(), "out.json")
	if _, err := execCmd(context.Background(), st, []string{"export", "-o", out}, true); err != nil {
		t.Fatalf("hoard export: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := `{
  "schemaVersion": "1.1.0",
  "kind": "holdings",
  "holdings": {
    "rows": [
      {
        "card": {
          "name": "Sol Ring",
          "scryfallId": "sol",
          "setCode": "c21",
          "number": "125",
          "finish": "nonfoil"
        },
        "count": 2,
        "container": "Binder",
        "containerKind": "binder",
        "board": "main",
        "priceUsd": 2
      },
      {
        "card": {
          "name": "Mystic Remora",
          "scryfallId": "rem",
          "setCode": "ice",
          "number": "78",
          "finish": "nonfoil"
        },
        "count": 1,
        "container": "Fish",
        "containerKind": "deck",
        "board": "main"
      },
      {
        "card": {
          "name": "Sol Ring",
          "scryfallId": "sol",
          "setCode": "c21",
          "number": "125",
          "finish": "foil"
        },
        "count": 1,
        "container": "Trade",
        "containerKind": "binder",
        "board": "main",
        "priceUsd": 12.5
      }
    ]
  }
}
`
	if string(got) != want {
		t.Errorf("export --json:\n%s\nwant:\n%s", got, want)
	}
}

// The bug this format exists for: a deck could be exported and never restored.
// `import` skips deck rows on purpose and `deck add --file` reads text
// decklists only, which nothing emitted — so this is the whole loop, both real
// commands, asserting that the cards, their counts, their printings, their
// finishes and their boards all come home.
func TestCmdExportTextRoundTripsThroughDeckAdd(t *testing.T) {
	src := importStore(t)
	cards := importFixtures()
	stubFetch(t, cards...)
	if err := src.UpsertPrintings(cards); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	if _, err := src.UpsertDeck(store.DeckMeta{Name: "Fish", Source: "manual", SourceID: "deck:fish"},
		[]store.Entry{
			{ScryfallID: "sol-id-1", Finish: "foil", Board: "main", Quantity: 1},
			{ScryfallID: "bolt-id-1", Finish: "nonfoil", Board: "main", Quantity: 4},
			{ScryfallID: "remora-id-1", Finish: "nonfoil", Board: "commander", Quantity: 1},
		}); err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}

	file := filepath.Join(t.TempDir(), "fish.txt")
	if _, err := execCmd(context.Background(), src,
		[]string{"export", "--deck", "Fish", "--format", "text", "-o", file}, false); err != nil {
		t.Fatalf("hoard export --format text: %v", err)
	}
	written, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// Read back into a *different* database, because that is what a restore
	// is: the source hoard is gone and only the file is left.
	dst := importStore(t)
	if _, err := execCmd(context.Background(), dst,
		[]string{"deck", "add", "--file", file, "--name", "RoundTrip"}, false); err != nil {
		t.Fatalf("hoard deck add --file (over our own export):\n%s\n%v", written, err)
	}

	deck, err := dst.DeckByRef("RoundTrip")
	if err != nil {
		t.Fatalf("DeckByRef: %v", err)
	}
	entries, err := dst.DeckEntries(deck.ID)
	if err != nil {
		t.Fatalf("DeckEntries: %v", err)
	}
	type held struct {
		finish, board string
		qty           int
	}
	got := make(map[string]held, len(entries))
	for _, e := range entries {
		got[e.Card.Name] = held{e.Finish, e.Board, e.Quantity}
	}
	want := map[string]held{
		"Sol Ring":       {"foil", "main", 1},
		"Lightning Bolt": {"nonfoil", "main", 4},
		"Mystic Remora":  {"nonfoil", "commander", 1},
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s came back as %+v, want %+v\nexported file:\n%s", name, got[name], w, written)
		}
	}
	if len(entries) != len(want) {
		t.Errorf("restored deck holds %d entries, want %d:\n%s", len(entries), len(want), written)
	}
}

// --format text writes a file `deck add --file` turns into exactly one deck,
// so an unscoped export would restore as one deck holding every deck's cards.
// It has to be a usage error rather than a merge.
func TestCmdExportTextRefusesMoreThanOneContainer(t *testing.T) {
	st := exportStore(t)
	if _, err := execCmd(context.Background(), st, []string{"export", "--format", "text"}, false); err == nil {
		t.Error("hoard export --format text over the whole collection succeeded, want a usage error")
	}
	// Scoped to one container it is exactly the file the reader wants.
	out, err := execCmd(context.Background(), st, []string{"export", "--deck", "Fish", "--format", "text"}, false)
	if err != nil {
		t.Fatalf("hoard export --deck Fish --format text: %v", err)
	}
	if out != "1 Mystic Remora (ice) 78\n" {
		t.Errorf("deck text export:\n%s", out)
	}
}

// A scope flag given twice used to take the last one without a word, so
// `--deck A --deck B` answered with B's container alone: a smaller document
// that is a plausible answer to a question nobody asked. Both names exist and
// both hold cards here, because a repeat whose second name is a typo errors on
// its own and hides the defect.
func TestCmdExportRefusesARepeatedScopeFlag(t *testing.T) {
	st := exportStore(t)
	if _, err := st.UpsertDeck(store.DeckMeta{Name: "Goblins", Source: "manual", SourceID: "deck:goblins"},
		[]store.Entry{{ScryfallID: "sol", Finish: "nonfoil", Board: "main", Quantity: 1}}); err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}
	for _, args := range [][]string{
		{"--deck", "Fish", "--deck", "Goblins"},
		{"--binder", "Binder", "--binder", "Trade"},
	} {
		out, err := execCmd(context.Background(), st, append([]string{"export"}, args...), false)
		if err == nil {
			t.Errorf("hoard export %v succeeded, want a usage error; wrote:\n%s", args, out)
			continue
		}
		if !strings.Contains(err.Error(), args[0]) {
			t.Errorf("hoard export %v: err = %v, want it to name the flag given twice", args, err)
		}
	}
}

// The clean case the refusal must not touch: one --deck writes exactly the
// bytes it always wrote. Asserted whole rather than by substring, because the
// risk a validation change carries is refusing something that used to work.
func TestCmdExportOneScopeFlagIsUnchanged(t *testing.T) {
	st := exportStore(t)
	got := mustExec(t, context.Background(), st, []string{"export", "--deck", "Fish"})
	want := strings.Join([]string{
		"Count,Name,Set,Collector Number,Finish,Condition,Scryfall ID,Container,Container Kind,Board,Price USD",
		"1,Mystic Remora,ice,78,nonfoil,,rem,Fish,deck,main,",
		"",
	}, "\n")
	if got != want {
		t.Errorf("export --deck Fish:\n%s\nwant:\n%s", got, want)
	}
}

// --json with a foreign --format is a contradiction, not a precedence puzzle.
func TestCmdExportJSONConflictsWithForeignFormat(t *testing.T) {
	st := exportStore(t)
	if _, err := execCmd(context.Background(), st, []string{"export", "--format", "moxfield"}, true); err == nil {
		t.Error("hoard export --format moxfield --json succeeded, want an error")
	}
}

// An explicit --format csv alongside --json is the same contradiction as
// --format moxfield: the user named two output shapes. It used to be
// indistinguishable from the default because the reconciliation tested the
// flag's value rather than whether it was set, so a script that says
// --format csv and inherits --json from a wrapper got JSON without a word.
func TestCmdExportJSONConflictsWithExplicitCSV(t *testing.T) {
	st := exportStore(t)
	out, err := execCmd(context.Background(), st, []string{"export", "--format", "csv"}, true)
	if err == nil {
		t.Fatalf("hoard export --format csv --json succeeded, want an error; wrote:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--format csv") {
		t.Errorf("err = %v, want it to name the format it conflicts with", err)
	}
}

// The two non-contradictions still stand: --json alone is the JSON document,
// and --format json --json says one thing twice.
func TestCmdExportJSONAgreesWithItself(t *testing.T) {
	st := exportStore(t)
	bare, err := execCmd(context.Background(), st, []string{"export"}, true)
	if err != nil {
		t.Fatalf("hoard export --json: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(bare), "{") {
		t.Errorf("hoard export --json wrote:\n%s", bare)
	}
	spelled, err := execCmd(context.Background(), st, []string{"export", "--format", "json"}, true)
	if err != nil {
		t.Fatalf("hoard export --format json --json: %v", err)
	}
	if spelled != bare {
		t.Errorf("--format json --json differs from --json:\n%s\n%s", spelled, bare)
	}
}
