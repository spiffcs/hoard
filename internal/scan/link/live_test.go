package link

// The Stage D acceptance check: can hoard pair with Hoardling and hold a link
// open, with no helper process anywhere in the path?
//
// Off by default — it needs a phone, a network and a person to read six digits
// off a screen. See docs/specs/scan-transport-port.md §7.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// liveStateDir is where the live tests keep their identity and pin set.
//
// A stable path rather than t.TempDir(), deliberately: the second run is the
// interesting one. It proves a pinned phone reconnects with no pairing code at
// all, which is what makes the phone's rotating code possible and is the whole
// point of trust-on-first-use.
func liveStateDir(t *testing.T) string {
	t.Helper()
	if dir := os.Getenv("HOARD_LINK_STATE"); dir != "" {
		return dir
	}
	return filepath.Join(os.TempDir(), "hoard-link-livetest")
}

func liveTrustParts(t *testing.T) (*Identity, *PinStore) {
	t.Helper()
	dir := liveStateDir(t)
	id, err := LoadOrCreateIdentity(filepath.Join(dir, "identity.pem"), "dev.spiffcs.hoard.scan.mac")
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	return id, NewPinStore(filepath.Join(dir, "pins.json"))
}

// findPhone browses and resolves, which must happen fresh every time: iOS
// rotates its .local hostname and takes a new ephemeral port per launch, so a
// cached address is stale as soon as the app restarts.
func findPhone(t *testing.T) Service {
	t.Helper()
	d := DNSSD{}
	ctx := context.Background()
	found, err := d.Browse(ctx, "", 5*time.Second)
	if err != nil {
		t.Fatalf("Browse: %v\n\nIs Hoardling open, and both devices on the same Wi-Fi?", err)
	}
	svc, err := d.Resolve(ctx, found[0].Name, 5*time.Second)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", found[0].Name, err)
	}
	return svc
}

// awaitReady reads frames until the phone says it is ready, logging everything
// on the way. `ready` is the phone's own declaration that its camera is up, and
// it is only sent after the pairing check passes — so seeing it *is* the
// verification (RemoteController.swift:147-151).
func awaitReady(t *testing.T, s *Session, within time.Duration) bool {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case f, ok := <-s.Frames():
			if !ok {
				t.Errorf("the link closed before the phone said ready: %v", s.Err())
				return false
			}
			switch f.Kind {
			case KindNDJSON:
				t.Logf("  <- ndjson %s", f.Text())
				if strings.Contains(f.Text(), `"event":"ready"`) {
					return true
				}
			case KindTrace:
				t.Logf("  <- trace  %s", strings.TrimSpace(f.Text()))
			default:
				t.Logf("  <- %s (%d bytes)", f.Kind, len(f.Payload))
			}
		case <-deadline:
			t.Errorf("no ready event within %s", within)
			return false
		}
	}
}

// TestLivePairPhone pairs with the phone, using the six digits from its Pair
// tab. Run this first, once.
//
//	HOARD_LINK_LIVE=1 HOARD_LINK_CODE=123456 \
//	  go test ./internal/scan/link/ -run TestLivePairPhone -v
func TestLivePairPhone(t *testing.T) {
	if os.Getenv("HOARD_LINK_LIVE") == "" {
		t.Skip("set HOARD_LINK_LIVE=1 with Hoardling open")
	}
	raw := os.Getenv("HOARD_LINK_CODE")
	if raw == "" {
		t.Skip("set HOARD_LINK_CODE to the six digits on Hoardling's Pair tab")
	}
	code, err := ParseCode(raw)
	if err != nil {
		t.Fatalf("HOARD_LINK_CODE: %v", err)
	}

	id, pins := liveTrustParts(t)
	svc := findPhone(t)
	t.Logf("phone %q at %s", svc.Name, svc.Addr())
	t.Logf("this Mac's fingerprint: %x", id.Fingerprint())

	// The pairing window: an unknown certificate may complete TLS, because the
	// gate in this moment is the proof on the hello, not the pinning.
	s, err := Dial(context.Background(), svc, code, TrustPairing(id, pins))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer s.Close()
	t.Logf("linked; phone fingerprint %x", s.PeerFingerprint())

	if !awaitReady(t, s, 20*time.Second) {
		t.Fatal("pairing did not complete")
	}

	// Trust on first use, from this side. The phone pinned this Mac when it
	// accepted the proof; this is the matching half, and it is done only after
	// ready proves the pairing actually completed rather than merely that a
	// certificate was seen.
	if err := pins.Pin(s.PeerFingerprint(), svc.Name); err != nil {
		t.Fatalf("pinning: %v", err)
	}
	t.Logf("PAIRED with %q; pin set now has %d peer(s)", svc.Name, len(pins.All()))
}

// TestLiveReconnectPhone reconnects with no pairing code at all.
//
// This is the property that matters most: a peer whose certificate is already
// pinned does not present the code (PeerEnds.swift:196-220), which is what lets
// the phone rotate its code without breaking every existing pairing. Run it
// after TestLivePairPhone.
//
//	HOARD_LINK_LIVE=1 go test ./internal/scan/link/ -run TestLiveReconnectPhone -v
func TestLiveReconnectPhone(t *testing.T) {
	if os.Getenv("HOARD_LINK_LIVE") == "" {
		t.Skip("set HOARD_LINK_LIVE=1 with Hoardling open")
	}
	id, pins := liveTrustParts(t)
	if len(pins.All()) == 0 {
		t.Skip("no pinned phone yet; run TestLivePairPhone first")
	}

	svc := findPhone(t)
	t.Logf("phone %q at %s", svc.Name, svc.Addr())

	// No code, and the pairing window closed: an unpinned peer could not even
	// finish the handshake here.
	var noCode Code
	s, err := Dial(context.Background(), svc, noCode, TrustStore(id, pins))
	if err != nil {
		t.Fatalf("Dial without a code: %v", err)
	}
	defer s.Close()

	if !awaitReady(t, s, 20*time.Second) {
		t.Fatal("reconnect did not reach ready")
	}
	t.Logf("RECONNECTED to %q with no pairing code", svc.Name)

	// And the link carries commands in the other direction. A chime is the
	// safest thing to ask for: it is audible confirmation, and it changes
	// nothing in anyone's collection.
	if err := s.SendLine("chime"); err != nil {
		t.Errorf("sending chime: %v", err)
	} else {
		t.Log("sent `chime` — the phone should make a sound")
	}
	time.Sleep(2 * time.Second)
	if err := s.Err(); err != nil {
		t.Errorf("session error after sending: %v", err)
	}
}
