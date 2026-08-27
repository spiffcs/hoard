package command

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/catalog"
)

func gz(body string) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte(body))
	zw.Close()
	return buf.Bytes()
}

func offlineMTGJSON() http.HandlerFunc {
	empty := gz(`{"data":{"cards":[]}}`)
	priced := gz(fmt.Sprintf(`{"meta":{"date":%q,"version":"5.2.2"},"data":{}}`,
		time.Now().UTC().Format("2006-01-02")))
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/AllPricesToday.json.gz"),
			strings.HasSuffix(r.URL.Path, "/AllPrices.json.gz"),
			strings.HasSuffix(r.URL.Path, "/AllPrintings.json.gz"):
			w.Write(priced)
		case strings.HasSuffix(r.URL.Path, ".json.gz"):
			w.Write(empty)
		default:
			http.Error(w, "this test reached for the network; give it a local stub instead",
				http.StatusNotImplemented)
		}
	}
}

func TestMain(m *testing.M) {
	srv := httptest.NewServer(offlineMTGJSON())
	defer srv.Close()

	blackhole := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "this test reached for the network; give it a local stub instead",
			http.StatusNotImplemented)
	}))
	defer blackhole.Close()

	priceBaseURL = srv.URL
	tcgcsvBaseURL = blackhole.URL
	catalog.ListingURL = blackhole.URL + "/bulk-data"

	os.Exit(m.Run())
}
