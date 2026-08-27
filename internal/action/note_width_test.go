package action

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/ui"
)

func TestTheDownloadNoteFitsAStandardTerminal(t *testing.T) {
	st, cacheDir := backfillFixture(t)
	writeCachedArchive(t, cacheDir, time.Now().Format("2006-01-02"), archiveForSol)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	var sb strings.Builder
	p := ui.NewPrinterSize(&sb, true, 80, 40)
	if _, err := BackfillPrices(context.Background(),
		Deps{Store: st, CacheDir: cacheDir, PriceBaseURL: srv.URL}, p.Fn(), 30); err != nil {
		t.Fatalf("BackfillPrices: %v", err)
	}
	p.Close()

	out := sb.String()
	if !strings.Contains(out, "downloading price history · fetching") {
		t.Errorf("the opening note never made it onto the step line at 80 columns:\n%q", out)
	}
	if strings.Contains(out, "…") {
		t.Errorf("the opening note is too long for an 80-column terminal and had to "+
			"be cut short:\n%q", out)
	}
}
