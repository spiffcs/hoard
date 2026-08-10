package scan

import (
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/scan/link"
)

// fakeFinder stands in for dns-sd. Browse is the only half Devices uses; the
// interface's other method is here to satisfy it.
type fakeFinder struct {
	names []string
	err   error
}

func (f fakeFinder) Browse(_ context.Context, name string, _ time.Duration) ([]link.Service, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]link.Service, 0, len(f.names))
	for _, n := range f.names {
		if name != "" && n != name {
			continue
		}
		out = append(out, link.Service{Name: n})
	}
	if len(out) == 0 {
		return nil, link.ErrNotFound
	}
	return out, nil
}

func (f fakeFinder) Resolve(_ context.Context, name string, _ time.Duration) (link.Service, error) {
	return link.Service{Name: name, Host: "phone.local.", Port: 49722}, nil
}

// newTestClient returns a client over a scratch state dir, plus its pin store.
func newTestClient(t *testing.T, names ...string) (*Client, *link.PinStore) {
	t.Helper()
	dir := t.TempDir()
	c := NewClient(dir)
	c.Finder = fakeFinder{names: names}
	return c, link.NewPinStore(filepath.Join(dir, "link-pins.json"))
}

func TestDevicesFlagsAnUnpairedPhone(t *testing.T) {
	c, pins := newTestClient(t, "Chris's iPhone", "Spare iPhone")
	fp := sha256.Sum256([]byte("chris"))
	if err := pins.Pin(fp[:], "Chris's iPhone"); err != nil {
		t.Fatal(err)
	}

	devices, err := c.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 {
		t.Fatalf("Devices returned %d phones, want 2", len(devices))
	}
	want := map[string]bool{"Chris's iPhone": false, "Spare iPhone": true}
	for _, d := range devices {
		w, ok := want[d.Name]
		if !ok {
			t.Errorf("unexpected phone %q", d.Name)
			continue
		}
		if d.NeedsPairing != w {
			t.Errorf("%q NeedsPairing = %v, want %v", d.Name, d.NeedsPairing, w)
		}
	}
}

// The known limitation, recorded rather than left to be rediscovered: Devices
// matches the pinned *label*, and a browse carries no fingerprint to match
// instead (dns-sd -B prints instance names only, and the phone advertises no
// TXT record). So renaming a paired phone reads as unpaired, and a phone that
// was reset — new certificate, same name — reads as paired.
//
// Both are display-only. The gate is Trust.verify, which never consults a name,
// so this test asserts a cosmetic wrong answer and not an authorisation one. If
// a later change makes NeedsPairing exact, this test should be deleted, not
// bent to fit. See docs/specs/followups-2026-08-09.md B2.
func TestDevicesMisreadsPairingWhenTheNameMoves(t *testing.T) {
	c, pins := newTestClient(t, "Chris's iPhone 17")
	fp := sha256.Sum256([]byte("chris"))
	// Paired under the old name.
	if err := pins.Pin(fp[:], "Chris's iPhone"); err != nil {
		t.Fatal(err)
	}

	devices, err := c.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !devices[0].NeedsPairing {
		t.Skip("NeedsPairing is now exact; delete this characterization test")
	}
	// The fingerprint that actually governs the session is still pinned, which
	// is what makes the wrong flag cosmetic.
	if !pins.Contains(fp[:]) {
		t.Fatal("the rename cost the pairing itself, not just the flag")
	}
}

// Open refreshes the stored label from the fingerprint the handshake proved, so
// the misread above lasts until the next successful session rather than
// forever. Open itself needs a phone; this is the store operation it performs.
func TestRenameHealsTheLabelAfterASession(t *testing.T) {
	c, pins := newTestClient(t, "Chris's iPhone 17")
	fp := sha256.Sum256([]byte("chris"))
	if err := pins.Pin(fp[:], "Chris's iPhone"); err != nil {
		t.Fatal(err)
	}

	// What Open does once Dial returns: the peer's fingerprint, and the name it
	// is advertising under now.
	if err := pins.Rename(fp[:], "Chris's iPhone 17"); err != nil {
		t.Fatal(err)
	}

	devices, err := c.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if devices[0].NeedsPairing {
		t.Error("a session did not heal the stale label")
	}
}

// friendly replaces the sentence a person reads without throwing away the error
// it replaced. Both halves matter: the sentence is the reason friendly exists,
// and the sentinel is what lets a caller tell "not paired" from "phone gone".
func TestFriendlyKeepsBothTheSentenceAndTheSentinel(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sentinel error
		contains string
		// replaced is whether friendly substitutes the whole sentence. The
		// dns-sd case does not: it appends, because the path it names is the
		// actionable part and no rewording improves on it.
		replaced bool
	}{
		{"not pinned", link.ErrPeerNotPinned, "not paired with this machine", true},
		{"not found", link.ErrNotFound, "no iPhone running Hoardling was found", true},
		{"no dns-sd", link.ErrNoDNSSD, "card scanning needs macOS's dns-sd", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := friendly(tc.sentinel)
			if !errors.Is(got, tc.sentinel) {
				t.Errorf("friendly severed the sentinel: %v", got)
			}
			if msg := got.Error(); !strings.Contains(msg, tc.contains) {
				t.Errorf("friendly lost the sentence: %q", msg)
			}
			// The raw text is what friendly exists to hide, so where it does
			// rewrite it must not also append it the way %w wrapping would.
			if tc.replaced && strings.Contains(got.Error(), "link:") {
				t.Errorf("the raw error leaked into the message: %q", got.Error())
			}
		})
	}
	if friendly(nil) != nil {
		t.Error("friendly(nil) is not nil")
	}
	// Anything without a sentinel passes through untouched.
	other := errors.New("something else")
	if friendly(other) != other {
		t.Error("friendly rewrote an error it has no sentence for")
	}
}
