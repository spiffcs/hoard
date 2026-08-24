package pricing

import (
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/finish"
)

func TestFillGapsDoesNotStampGapsWhenResolutionFails(t *testing.T) {
	s := newStore(t)
	if err := s.AddCardFinish(unpricedFoil(), finish.Foil, 1); err != nil {
		t.Fatalf("AddCardFinish: %v", err)
	}

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
