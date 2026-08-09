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
  "schemaVersion": "1.1.4",
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

// --json with a foreign --format is a contradiction, not a precedence puzzle.
func TestCmdExportJSONConflictsWithForeignFormat(t *testing.T) {
	st := exportStore(t)
	if _, err := execCmd(context.Background(), st, []string{"export", "--format", "moxfield"}, true); err == nil {
		t.Error("hoard export --format moxfield --json succeeded, want an error")
	}
}
