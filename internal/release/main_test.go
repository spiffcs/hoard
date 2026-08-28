package release

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	blackhole := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "this test reached for the network; give it a local stub instead",
			http.StatusNotImplemented)
	}))
	defer blackhole.Close()

	releasesBaseURL = blackhole.URL

	os.Exit(m.Run())
}
