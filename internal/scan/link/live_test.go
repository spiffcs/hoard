package link

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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

	s, err := Dial(context.Background(), svc, code, TrustPairing(id, pins))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer s.Close()
	t.Logf("linked; phone fingerprint %x", s.PeerFingerprint())

	if !awaitReady(t, s, 20*time.Second) {
		t.Fatal("pairing did not complete")
	}

	if err := pins.Pin(s.PeerFingerprint(), svc.Name); err != nil {
		t.Fatalf("pinning: %v", err)
	}
	t.Logf("PAIRED with %q; pin set now has %d peer(s)", svc.Name, len(pins.All()))
}

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
