package scan

import (
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/scan/link"
)

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

func newTestClient(t *testing.T, names ...string) (*Client, *link.PinStore) {
	t.Helper()
	dir := t.TempDir()
	c := NewClient(dir)
	c.Finder = fakeFinder{names: names}
	return c, link.NewPinStore(filepath.Join(dir, "link-pins.json"))
}

func TestDevicesListsEveryPhoneOnTheNetwork(t *testing.T) {
	c, _ := newTestClient(t, "Chris's iPhone", "Spare iPhone")

	devices, err := c.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 2 {
		t.Fatalf("Devices returned %d phones, want 2", len(devices))
	}
	for _, d := range devices {
		if d.Kind != KindRemote {
			t.Errorf("%q Kind = %q, want %q", d.Name, d.Kind, KindRemote)
		}
		if d.ID != d.Name {
			t.Errorf("%q ID = %q; a browse has only the instance name to offer", d.Name, d.ID)
		}
	}
}

func TestDevicesDoesNotConsultThePinStore(t *testing.T) {
	names := []string{"Chris's iPhone", "Spare iPhone"}

	unpinned, _ := newTestClient(t, names...)
	before, err := unpinned.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	pinned, pins := newTestClient(t, names...)
	fp := sha256.Sum256([]byte("chris"))
	if err := pins.Pin(fp[:], "An Older Name"); err != nil {
		t.Fatal(err)
	}
	after, err := pinned.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(before, after) {
		t.Errorf("pinning changed what a browse reports:\n before %+v\n after  %+v", before, after)
	}
}

func TestOpenWithNothingPinnedReportsNotPaired(t *testing.T) {
	c, _ := newTestClient(t, "Chris's iPhone")

	_, err := c.Open(context.Background(), OpenOptions{DeviceID: "Chris's iPhone"})
	if err == nil {
		t.Fatal("Open succeeded with an empty pin store")
	}
	if !errors.Is(err, ErrNotPaired) {
		t.Errorf("Open with no pins: %v, want ErrNotPaired", err)
	}
	if !strings.Contains(err.Error(), "Pair tab") {
		t.Errorf("the sentence a person reads was lost: %q", err.Error())
	}
}

func TestFriendlyKeepsBothTheSentenceAndTheSentinel(t *testing.T) {

	if !errors.Is(friendly(link.ErrPeerNotPinned), ErrNotPaired) {
		t.Error("ErrNotPaired is not the sentinel the handshake actually returns")
	}
	for _, tc := range []struct {
		name     string
		sentinel error
		contains string

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

			if tc.replaced && strings.Contains(got.Error(), "link:") {
				t.Errorf("the raw error leaked into the message: %q", got.Error())
			}
		})
	}
	if friendly(nil) != nil {
		t.Error("friendly(nil) is not nil")
	}

	other := errors.New("something else")
	if friendly(other) != other {
		t.Error("friendly rewrote an error it has no sentence for")
	}
}
