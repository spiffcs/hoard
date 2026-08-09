package command

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// An etched-only printing: the watch must land on the foil price, because
// that is the price Scryfall quotes for etched.
func etchedOnlyCard() scryfall.Card {
	return scryfall.Card{ID: "etched-1", Set: "brc", CollectorNumber: "178", Name: "Wake the Past",
		ScryfallURL: "http://etched", PriceUSDFoil: f(3),
		Finishes: []string{"etched"}}
}

// The whole CLI loop: a CSV with a resolvable card, an etched-only card and
// a typo imports the two real watches, reports the skip via exit-code-2
// semantics, and a JSON re-import adjusts a threshold instead of stacking.
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
	if w := watches[0]; w.ScryfallID != "sol" || w.Finish != "nonfoil" || w.Op != "under" || w.Threshold != 5 {
		t.Errorf("Sol Ring watch = %+v", w)
	}
	if w := watches[1]; w.ScryfallID != "etched-1" || w.Finish != "foil" || w.Op != "over" {
		t.Errorf("etched watch = %+v, want the foil price finish", w)
	}

	// The JSON dialect re-imports cleanly and moves the line: same watch,
	// new threshold, no third row.
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
