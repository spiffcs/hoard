package command

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/store"
)

func TestParseWindow(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want time.Duration
	}{
		{"30d", 30 * 24 * time.Hour},
		{"2w", 14 * 24 * time.Hour},
		{"48h", 48 * time.Hour},
		{"1.5d", 36 * time.Hour},
		{" 7d ", 7 * 24 * time.Hour},
	} {
		got, err := parseWindow(tc.in)
		if err != nil {
			t.Errorf("parseWindow(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseWindow(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseWindowRejectsNonsense(t *testing.T) {
	for _, in := range []string{"", "d", "-3d", "0d", "soon", "3 days", "0"} {
		if got, err := parseWindow(in); err == nil {
			t.Errorf("parseWindow(%q) = %v, want an error", in, got)
		}
	}
}

// movers now writes to the Env it is handed, so its output can be asserted.
// Before the port it wrote to os.Stdout, which is why this file only ever
// covered parseWindow — the command itself was unreachable from a test.
func TestMoversSaysWhenNoHistoryExists(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	out := mustExec(t, context.Background(), st, []string{"movers"})
	want := "No price history recorded yet. Run hoard update-prices to start."
	if !strings.Contains(out, want) {
		t.Errorf("movers output = %q, want it to contain %q", out, want)
	}
}

// The --json path is reachable the same way, and the tree is what decides
// that movers may honor --json at all.
func TestMoversJSONIsWellFormed(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	out, err := execCmd(context.Background(), st, []string{"movers"}, true)
	if err != nil {
		t.Fatalf("movers --json: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("movers --json emitted invalid JSON (%v): %q", err, out)
	}
}

// A bad --since fails with a message that says what to type, and does not drag
// the command table along with it — cobra's SilenceUsage on the root is what
// keeps a forty-row dump off a one-line mistake.
func TestMoversRejectsABadWindow(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	out, err := execCmd(context.Background(), st, []string{"movers", "--since", "soon"}, false)
	if err == nil {
		t.Fatal("expected an error for --since soon")
	}
	if !strings.Contains(err.Error(), "--since") {
		t.Errorf("message does not name the flag: %v", err)
	}
	if strings.Contains(out, "Collection commands:") {
		t.Error("a bad flag value printed the command table")
	}
}
