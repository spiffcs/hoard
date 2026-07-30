package main

import (
	"context"
	"testing"

	"github.com/spiffcs/hoard/internal/catalog"
)

// A stale catalog must not price a refresh.
//
// confirm() declines automatically without a terminal — which is every cron job
// and every piped run — so a stale catalog would otherwise be used forever, and
// the command would report "Updated prices for N cards" over numbers that had
// not moved since the catalog was built.
func TestStaleCatalogIsNotUsedForPrices(t *testing.T) {
	// Decline deterministically, the way a non-interactive run does.
	old := confirmFn
	confirmFn = func(string) bool { return false }
	defer func() { confirmFn = old }()

	cat, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer cat.Close()

	// An empty catalog is the clearest case of "cannot price from this": it has
	// no prices at all.
	if ensureCatalog(context.Background(), cat) {
		t.Error("an empty catalog was reported as usable for prices")
	}
}

// No catalog at all is a supported state, not an error.
func TestNilCatalogIsNotUsableButIsNotFatal(t *testing.T) {
	if ensureCatalog(context.Background(), nil) {
		t.Error("a nil catalog was reported as usable")
	}
}

// humanBytes is what the download prompt shows before spending somebody's
// bandwidth, so it should read the way a person would say it.
func TestHumanBytes(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1 << 20, "1 MB"},
		{77 << 20, "77 MB"},
		{3 << 30, "3.0 GB"},
	} {
		if got := humanBytes(tc.n); got != tc.want {
			t.Errorf("humanBytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
