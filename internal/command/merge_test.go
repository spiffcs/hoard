package command

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

func mergeSourceDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "other.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	c := scryfall.Card{
		ID: "merge-id", Set: "uma", CollectorNumber: "7", Name: "Ulamog",
		PriceUSD: f(10), ScryfallURL: "https://scryfall.com/card/uma/7",
		Raw: json.RawMessage(`{"rarity":"mythic","type_line":"Legendary Creature"}`),
	}
	if err := st.AddCardFinish(c, finish.Nonfoil, 3); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return path
}

func mergeTargetStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestCmdMerge(t *testing.T) {
	ctx := context.Background()
	st := mergeTargetStore(t)
	src := mergeSourceDB(t)

	out, err := execCmd(ctx, st, []string{"merge", src}, false)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	for _, want := range []string{"Merged 3 cards", "1 printing carried", "Not carried:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	rows, err := st.ListCollectionByFinish()
	if err != nil {
		t.Fatalf("ListCollectionByFinish: %v", err)
	}
	if len(rows) != 1 || rows[0].Quantity != 3 {
		t.Errorf("holdings = %+v, want 3 copies of one printing", rows)
	}
}

func TestCmdMergeDryRun(t *testing.T) {
	ctx := context.Background()
	st := mergeTargetStore(t)
	src := mergeSourceDB(t)

	out, err := execCmd(ctx, st, []string{"merge", src, "--dry-run"}, false)
	if err != nil {
		t.Fatalf("merge --dry-run: %v", err)
	}
	if !strings.Contains(out, "Would merge 3 cards") {
		t.Errorf("output missing the dry-run verb:\n%s", out)
	}
	if !strings.Contains(out, "Dry run: nothing was written.") {
		t.Errorf("output missing the dry-run hint:\n%s", out)
	}
	rows, err := st.ListCollectionByFinish()
	if err != nil {
		t.Fatalf("ListCollectionByFinish: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("a dry run wrote %+v", rows)
	}
}

func TestCmdMergeWritesDocument(t *testing.T) {
	ctx := context.Background()
	st := mergeTargetStore(t)
	src := mergeSourceDB(t)
	doc := filepath.Join(t.TempDir(), "merge.json")

	if _, err := execCmd(ctx, st, []string{"merge", src, "-o", doc}, false); err != nil {
		t.Fatalf("merge -o: %v", err)
	}
	fh, err := os.Open(doc)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer fh.Close()

	h, err := hoardjson.ReadHoard(fh)
	if err != nil {
		t.Fatalf("the document -o wrote does not read back: %v", err)
	}
	if len(h.Printings) != 1 || h.Printings[0].ScryfallID != "merge-id" {
		t.Fatalf("printings = %+v, want the one card", h.Printings)
	}
	if len(h.Printings[0].Raw) == 0 {
		t.Error("the printing carries no card document; a merged card would read as blank")
	}
	if len(h.Holdings.Rows) != 1 || h.Holdings.Rows[0].Count != 3 {
		t.Errorf("holdings = %+v, want 3 copies", h.Holdings.Rows)
	}
}

func TestCmdMergeRefusesToOverwriteADatabase(t *testing.T) {
	ctx := context.Background()
	st := mergeTargetStore(t)
	src := mergeSourceDB(t)

	victim := filepath.Join(t.TempDir(), "precious.db")
	if vst, err := store.Open(victim); err != nil {
		t.Fatalf("Open: %v", err)
	} else if err := vst.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	before, err := os.ReadFile(victim)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	if _, err := execCmd(ctx, st, []string{"merge", src, "-o", victim}, false); err == nil {
		t.Fatal("-o overwrote a SQLite database")
	}
	after, err := os.ReadFile(victim)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(before) != string(after) {
		t.Error("the database at -o was modified")
	}
}

func TestCmdMergeNeedsAPath(t *testing.T) {
	ctx := context.Background()
	st := mergeTargetStore(t)
	if _, err := execCmd(ctx, st, []string{"merge"}, false); err == nil {
		t.Fatal("merge with no argument was accepted")
	} else if !strings.Contains(err.Error(), "exactly one hoard database") {
		t.Errorf("error was %q, want the sentence the command answers with", err)
	}
}
