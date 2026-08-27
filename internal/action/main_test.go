package action

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/spiffcs/hoard/internal/catalog"
)

var offlineTCGCSV string

func TestMain(m *testing.M) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "this test reached for the network; give it a local stub instead",
			http.StatusNotImplemented)
	}))
	defer srv.Close()

	catalog.ListingURL = srv.URL + "/bulk-data"
	offlineTCGCSV = srv.URL

	os.Exit(m.Run())
}
