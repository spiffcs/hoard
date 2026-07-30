package pricing

import (
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// An outage during resolution must not be recorded as "MTGJSON had no price":
// that stamp silences re-asks for a week, and the only recovery is editing the
// database by hand. The failure here is a set file that is valid gzip but not
// JSON — reachable without the network, and exactly as unresolvable as a dead
// host.
func TestFillGapsDoesNotStampGapsWhenResolutionFails(t *testing.T) {
	s := newStore(t)
	if err := s.AddCardFinish(unpricedFoil(), "foil", 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}

	// The card's set file, cached for today, gzip-valid and JSON-broken — so
	// SetIdentifiers fails with a real error rather than ErrNoSuchSet, before
	// any network I/O.
	cacheDir := t.TempDir()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte("this is not json"))
	zw.Close()
	name := time.Now().Format("2006-01-02") + "-M3C.json.gz"
	if err := os.WriteFile(filepath.Join(cacheDir, name), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := New(s, cacheDir).FillGaps(context.Background()); err == nil {
		t.Fatal("FillGaps succeeded over a failed resolution, want an error")
	}

	gaps, err := s.UnpricedByOwnedFinish()
	if err != nil || len(gaps) != 1 {
		t.Fatalf("gaps = %+v, %v", gaps, err)
	}
	if gaps[0].CheckedAt != nil {
		t.Errorf("gap was stamped checked at %s during a failed resolution — it would now be silenced for a week",
			*gaps[0].CheckedAt)
	}
}
