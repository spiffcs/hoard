package action

import (
	"context"
	"testing"

	"github.com/spiffcs/hoard/internal/catalog"
	"github.com/spiffcs/hoard/internal/progress"
)

// A stale or missing catalog must not price a refresh.
//
// Deps.Confirm's nil default declines — which is every cron job and every
// piped run — so a stale catalog would otherwise be used forever, and
// update-prices would report success over numbers that had not moved since
// the catalog was built.
func TestEnsureCatalogDeclinedIsNotUsable(t *testing.T) {
	cat, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer cat.Close()

	var notes []string
	p := func(ev progress.Event) {
		if ev.Note != "" {
			notes = append(notes, ev.Note)
		}
	}
	// An empty catalog is the clearest case of "cannot price from this": it
	// has no prices at all. Confirm declines explicitly.
	d := Deps{Catalog: cat, Confirm: func(string) bool { return false }}
	if EnsureCatalog(context.Background(), d, p) {
		t.Error("an empty catalog was reported as usable for prices")
	}
	if len(notes) != 1 {
		t.Errorf("notes = %v, want the fall-through-to-API narration", notes)
	}

	// The nil-Confirm default must behave identically.
	if EnsureCatalog(context.Background(), Deps{Catalog: cat}, nil) {
		t.Error("nil Confirm accepted a download")
	}
}

// No catalog at all is a supported state, not an error.
func TestEnsureCatalogNilCatalog(t *testing.T) {
	if EnsureCatalog(context.Background(), Deps{}, nil) {
		t.Error("a nil catalog was reported as usable")
	}
}

func TestCatalogStatusNilCatalog(t *testing.T) {
	if _, err := CatalogStatus(context.Background(), Deps{}); err == nil {
		t.Error("CatalogStatus with no catalog succeeded")
	}
	if _, err := CatalogUpdate(context.Background(), Deps{}, nil); err == nil {
		t.Error("CatalogUpdate with no catalog succeeded")
	}
}
