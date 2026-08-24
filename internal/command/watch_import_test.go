package command

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func etchedOnlyCard() scryfall.Card {
	return scryfall.Card{ID: "etched-1", Set: "brc", CollectorNumber: "178", Name: "Wake the Past",
		ScryfallURL: "http://etched", PriceUSDFoil: f(3),
		Finishes: []string{"etched"}}
}

func TestCmdWatchImportEndToEnd(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard(), etchedOnlyCard())
	ctx := context.Background()
	dir := t.TempDir()

	csvPath := filepath.Join(dir, "watches.csv")
	csv := "Name,Direction,Threshold,Finish\n" +
		"Sol Ring,under,5,\n" +
		"Wake the Past,over,10,etched\n" +
		"Blrgh Nonsense,under,1,\n"
	if err := os.WriteFile(csvPath, []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := execWatch(ctx, st, []string{"import", csvPath}, false); !errors.Is(err, errPartial) {
		t.Fatalf("import with a typo row = %v, want the partial sentinel", err)
	}

	watches, err := st.ListWatches()
	if err != nil || len(watches) != 2 {
		t.Fatalf("watches = %+v, %v, want 2", watches, err)
	}
	if w := watches[0]; w.ScryfallID != "sol" || w.Finish != finish.Nonfoil || w.Op != "under" || w.Threshold != 5 {
		t.Errorf("Sol Ring watch = %+v", w)
	}
	if w := watches[1]; w.ScryfallID != "etched-1" || w.Finish != finish.Foil || w.Op != "over" {
		t.Errorf("etched watch = %+v, want the foil price finish", w)
	}

	jsonPath := filepath.Join(dir, "watches.json")
	if err := os.WriteFile(jsonPath,
		[]byte(`[{"name":"Sol Ring","direction":"under","thresholdUsd":4}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := execWatch(ctx, st, []string{"import", jsonPath}, false); err != nil {
		t.Fatalf("json re-import: %v", err)
	}
	watches, err = st.ListWatches()
	if err != nil || len(watches) != 2 {
		t.Fatalf("after re-import: watches = %+v, %v, want still 2", watches, err)
	}
	if w := watches[0]; w.Threshold != 4 {
		t.Errorf("threshold = %v, want the re-import's 4", w.Threshold)
	}
}

func withStdin(t *testing.T, content string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = saved; f.Close() })
}

func TestCmdWatchImportStdin(t *testing.T) {
	st := exportStore(t)
	stubFetch(t, watchCard())
	withStdin(t, "Name,Direction,Threshold,Finish\nSol Ring,under,5,\n")

	if err := execWatch(context.Background(), st, []string{"import", "-"}, false); err != nil {
		t.Fatalf("watch import -: %v", err)
	}
	watches, err := st.ListWatches()
	if err != nil || len(watches) != 1 {
		t.Fatalf("watches = %+v, %v, want 1", watches, err)
	}
	if w := watches[0]; w.ScryfallID != "sol" || w.Op != "under" || w.Threshold != 5 {
		t.Errorf("watch = %+v", w)
	}
}

func TestCmdWatchImportStdinNamesTheSource(t *testing.T) {
	st := exportStore(t)
	withStdin(t, "")
	err := execWatch(context.Background(), st, []string{"import", "-"}, false)
	if err == nil || !strings.Contains(err.Error(), "stdin") {
		t.Fatalf("err = %v, want a parse error naming stdin", err)
	}
}

func TestCmdWatchImportArgErrors(t *testing.T) {
	st := exportStore(t)
	ctx := context.Background()
	if err := execWatch(ctx, st, []string{"import"}, false); err == nil {
		t.Error("import without a file must error")
	}
	if err := execWatch(ctx, st, []string{"import", "a.csv", "b.csv"}, false); err == nil {
		t.Error("import with two files must error")
	}
	if err := execWatch(ctx, st, []string{"import", filepath.Join(t.TempDir(), "absent.csv")}, false); err == nil {
		t.Error("import of a missing file must error")
	}
}

func TestCmdWatchImportPrefersHeldPrintings(t *testing.T) {
	st := exportStore(t)
	held, other := bitterblossom()
	if err := st.AddCardFinish(held, finish.Nonfoil, 4); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	stubFetch(t, watchCard(), held, other)
	dir := t.TempDir()
	path := filepath.Join(dir, "watches.csv")
	csv := "Name,Direction,Threshold,Set,Collector Number\n" +
		"Bitterblossom,over,1,,\n" +
		"Sol Ring,under,5,,\n"
	if err := os.WriteFile(path, []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := execCmd(context.Background(), st, []string{"watch", "import", path}, false)
	if err != nil {
		t.Fatalf("watch import: %v", err)
	}
	byID := map[string]bool{}
	watches, _ := st.ListWatches()
	for _, w := range watches {
		byID[w.ScryfallID] = true
	}
	if !byID["bb-uma"] || byID["bb-2x2"] {
		t.Errorf("watches = %+v, want the held uma printing and not 2x2", watches)
	}

	if !strings.Contains(out, "2 named only a card and follow a printing you hold") {
		t.Errorf("receipt = %q, want the held-printing count", out)
	}
}

func TestCmdWatchImportKeepsAnExplicitPrinting(t *testing.T) {
	st := exportStore(t)
	held, other := bitterblossom()
	if err := st.AddCardFinish(held, finish.Nonfoil, 4); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}
	stubFetch(t, held, other)
	path := filepath.Join(t.TempDir(), "watches.csv")
	csv := "Name,Direction,Threshold,Set,Collector Number\n" +
		"Bitterblossom,over,1,2x2,69\n"
	if err := os.WriteFile(path, []byte(csv), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := execCmd(context.Background(), st, []string{"watch", "import", path}, false)
	if err != nil {
		t.Fatalf("watch import: %v", err)
	}
	w, _ := st.ListWatches()
	if len(w) != 1 || w[0].ScryfallID != "bb-2x2" {
		t.Errorf("watches = %+v, want the printing the file named", w)
	}
	if strings.Contains(out, "named only a card") {
		t.Errorf("receipt = %q, want no held-printing note for an explicit printing", out)
	}
}
