package link

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newTestIdentity(t *testing.T, name string) *Identity {
	t.Helper()
	id, err := NewIdentity(name)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestPinStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pins.json")
	s := NewPinStore(path)

	if got := s.All(); len(got) != 0 {
		t.Fatalf("fresh store has %d pins", len(got))
	}
	fp := sha256.Sum256([]byte("phone"))
	if s.Contains(fp[:]) {
		t.Error("empty store claims to contain a fingerprint")
	}

	if err := s.Pin(fp[:], "Chris's iPhone"); err != nil {
		t.Fatal(err)
	}
	if !s.Contains(fp[:]) {
		t.Error("pinned fingerprint is not contained")
	}
	if got := s.All(); len(got) != 1 || !bytes.Equal(got[0], fp[:]) {
		t.Errorf("All() = %d entries, want the one pinned", len(got))
	}
	if got := s.Names()[b64(fp[:])]; got != "Chris's iPhone" {
		t.Errorf("Names() lost the label: %q", got)
	}

	if err := s.Pin(fp[:], "Chris's iPhone"); err != nil {
		t.Fatal(err)
	}
	if got := s.All(); len(got) != 1 {
		t.Errorf("re-pinning made %d entries", len(got))
	}

	if !NewPinStore(path).Contains(fp[:]) {
		t.Error("pins did not survive reopening the store")
	}

	if err := s.Forget(fp[:]); err != nil {
		t.Fatal(err)
	}
	if s.Contains(fp[:]) {
		t.Error("forgotten fingerprint is still contained")
	}
}

func TestPinStoreRenameIsUpdateOnly(t *testing.T) {
	s := NewPinStore(filepath.Join(t.TempDir(), "pins.json"))
	paired := sha256.Sum256([]byte("paired phone"))
	if err := s.Pin(paired[:], "Chris's iPhone"); err != nil {
		t.Fatal(err)
	}

	if err := s.Rename(paired[:], "Chris's iPhone 17"); err != nil {
		t.Fatal(err)
	}
	if got := s.Names()[b64(paired[:])]; got != "Chris's iPhone 17" {
		t.Errorf("Rename left the label as %q", got)
	}

	if len(s.All()) != 1 || !s.Contains(paired[:]) {
		t.Errorf("Rename disturbed the pin set: %d entries", len(s.All()))
	}

	stranger := sha256.Sum256([]byte("some other phone"))
	if err := s.Rename(stranger[:], "Someone Else's iPhone"); err != nil {
		t.Fatal(err)
	}
	if s.Contains(stranger[:]) {
		t.Fatal("Rename granted trust to an unpinned fingerprint")
	}
	if len(s.All()) != 1 {
		t.Errorf("store grew to %d entries", len(s.All()))
	}

	if err := s.Rename(nil, "nobody"); err != nil {
		t.Fatal(err)
	}
	if len(s.All()) != 1 {
		t.Errorf("a nil fingerprint reached the file: %d entries", len(s.All()))
	}
}

func TestPinStoreForgetAll(t *testing.T) {
	s := NewPinStore(filepath.Join(t.TempDir(), "pins.json"))
	for _, n := range []string{"a", "b", "c"} {
		fp := sha256.Sum256([]byte(n))
		if err := s.Pin(fp[:], n); err != nil {
			t.Fatal(err)
		}
	}
	if len(s.All()) != 3 {
		t.Fatalf("expected 3 pins, got %d", len(s.All()))
	}
	if err := s.ForgetAll(); err != nil {
		t.Fatal(err)
	}
	if len(s.All()) != 0 {
		t.Error("ForgetAll left pins behind")
	}
}

func TestPinStorePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "pins.json")
	s := NewPinStore(path)
	fp := sha256.Sum256([]byte("phone"))
	if err := s.Pin(fp[:], "phone"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("pin file mode = %o, want 600", perm)
	}
}

func TestPinStoreRejectsWrongSizedFingerprint(t *testing.T) {
	s := NewPinStore(filepath.Join(t.TempDir(), "pins.json"))
	for _, bad := range [][]byte{nil, {}, make([]byte, 16), make([]byte, 31), make([]byte, 33)} {
		if err := s.Pin(bad, "x"); err == nil {
			t.Errorf("Pin accepted a %d-byte fingerprint", len(bad))
		}
	}
}

func TestCorruptPinStoreEmptiesRatherThanWidens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pins.json")
	if err := os.WriteFile(path, []byte("{{{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewPinStore(path)
	if got := s.All(); len(got) != 0 {
		t.Errorf("corrupt store returned %d pins", len(got))
	}
	fp := sha256.Sum256([]byte("anyone"))
	if s.Contains(fp[:]) {
		t.Error("corrupt store failed open")
	}
}

func b64(b []byte) string {
	data, _ := json.Marshal(b)
	var s string
	_ = json.Unmarshal(data, &s)
	return s
}

func handshake(t *testing.T, trust *Trust, h *Handshake, serverID *Identity) (clientErr, serverErr error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		conn, err := listener.Accept()
		if err != nil {
			serverErr = err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(handshakeTestTimeout))

		server := tls.Server(conn, &tls.Config{
			Certificates: []tls.Certificate{serverID.Certificate},
			MinVersion:   tls.VersionTLS12,
			ClientAuth:   tls.RequireAnyClientCert,
		})
		serverErr = server.Handshake()
		if serverErr == nil && len(server.ConnectionState().PeerCertificates) == 0 {
			serverErr = errors.New("client presented no certificate")
		}
		_ = server.Close()
	}()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(handshakeTestTimeout))

	client := tls.Client(conn, trust.ClientConfig(h))
	clientErr = client.Handshake()
	_ = client.Close()
	wg.Wait()
	return clientErr, serverErr
}

const handshakeTestTimeout = 5 * time.Second

func TestHandshakeRejectsUnpinnedPeer(t *testing.T) {
	pins := NewPinStore(filepath.Join(t.TempDir(), "pins.json"))
	trust := TrustStore(newTestIdentity(t, "mac"), pins)
	var h Handshake

	clientErr, _ := handshake(t, trust, &h, newTestIdentity(t, "phone"))
	if !errors.Is(clientErr, ErrPeerNotPinned) {
		t.Fatalf("handshake with an unpinned peer: %v, want ErrPeerNotPinned", clientErr)
	}

	if h.PeerFingerprint() == nil {
		t.Error("no fingerprint recorded for a refused peer")
	}
}

func TestHandshakeAcceptsPinnedPeer(t *testing.T) {
	pins := NewPinStore(filepath.Join(t.TempDir(), "pins.json"))
	phone := newTestIdentity(t, "phone")
	if err := pins.Pin(phone.Fingerprint(), "phone"); err != nil {
		t.Fatal(err)
	}
	trust := TrustStore(newTestIdentity(t, "mac"), pins)
	var h Handshake

	clientErr, serverErr := handshake(t, trust, &h, phone)
	if clientErr != nil {
		t.Fatalf("handshake with a pinned peer failed: %v", clientErr)
	}

	if serverErr != nil {
		t.Fatalf("server rejected the client: %v", serverErr)
	}
	if !bytes.Equal(h.PeerFingerprint(), phone.Fingerprint()) {
		t.Error("recorded fingerprint is not the peer's")
	}
}

func TestPairingWindowAcceptsUnknown(t *testing.T) {
	pins := NewPinStore(filepath.Join(t.TempDir(), "pins.json"))
	phone := newTestIdentity(t, "phone")
	trust := TrustPairing(newTestIdentity(t, "mac"), pins)
	var h Handshake

	if clientErr, _ := handshake(t, trust, &h, phone); clientErr != nil {
		t.Fatalf("pairing window refused an unknown peer: %v", clientErr)
	}
	if !bytes.Equal(h.PeerFingerprint(), phone.Fingerprint()) {
		t.Fatal("pairing did not record the peer's fingerprint to bind the proof to")
	}
}

func TestPolicyIsReadLive(t *testing.T) {
	pins := NewPinStore(filepath.Join(t.TempDir(), "pins.json"))
	phone := newTestIdentity(t, "phone")
	trust := TrustStore(newTestIdentity(t, "mac"), pins)

	var first Handshake
	if clientErr, _ := handshake(t, trust, &first, phone); !errors.Is(clientErr, ErrPeerNotPinned) {
		t.Fatalf("expected refusal before pinning, got %v", clientErr)
	}

	if err := pins.Pin(phone.Fingerprint(), "phone"); err != nil {
		t.Fatal(err)
	}

	var second Handshake
	if clientErr, _ := handshake(t, trust, &second, phone); clientErr != nil {
		t.Fatalf("a peer pinned during the session was refused: %v", clientErr)
	}
}

func TestClosingThePairingWindowTakesEffect(t *testing.T) {
	pins := NewPinStore(filepath.Join(t.TempDir(), "pins.json"))
	open := true
	trust := &Trust{
		Identity:      newTestIdentity(t, "mac"),
		Pinned:        pins.All,
		AcceptUnknown: func() bool { return open },
	}

	var a Handshake
	if clientErr, _ := handshake(t, trust, &a, newTestIdentity(t, "phone-a")); clientErr != nil {
		t.Fatalf("open window refused: %v", clientErr)
	}
	open = false
	var b Handshake
	if clientErr, _ := handshake(t, trust, &b, newTestIdentity(t, "phone-b")); !errors.Is(clientErr, ErrPeerNotPinned) {
		t.Fatalf("closed window accepted: %v", clientErr)
	}
}

func TestWrongPinIsRefused(t *testing.T) {
	pins := NewPinStore(filepath.Join(t.TempDir(), "pins.json"))
	wanted := newTestIdentity(t, "phone")
	impostor := newTestIdentity(t, "phone")
	if err := pins.Pin(wanted.Fingerprint(), "phone"); err != nil {
		t.Fatal(err)
	}
	trust := TrustStore(newTestIdentity(t, "mac"), pins)

	var h Handshake
	if clientErr, _ := handshake(t, trust, &h, impostor); !errors.Is(clientErr, ErrPeerNotPinned) {
		t.Fatalf("an impostor with a matching common name was accepted: %v", clientErr)
	}
}

func TestVerifyRejectsEmptyChain(t *testing.T) {
	pins := NewPinStore(filepath.Join(t.TempDir(), "pins.json"))
	trust := TrustPairing(newTestIdentity(t, "mac"), pins)
	var h Handshake
	verify := trust.verify(&h)

	if err := verify(nil, nil); err == nil {
		t.Error("accepted a handshake with no peer certificate")
	}
	if err := verify([][]byte{[]byte("not a certificate")}, nil); err == nil {
		t.Error("accepted an unparseable peer certificate")
	}

	if h.PeerFingerprint() != nil {
		t.Error("recorded a fingerprint from a certificate that never parsed")
	}
}

func TestHandshakeFingerprintMatchesTheWire(t *testing.T) {
	pins := NewPinStore(filepath.Join(t.TempDir(), "pins.json"))
	phone := newTestIdentity(t, "phone")
	trust := TrustPairing(newTestIdentity(t, "mac"), pins)
	var h Handshake
	if clientErr, _ := handshake(t, trust, &h, phone); clientErr != nil {
		t.Fatal(clientErr)
	}
	leaf, err := x509.ParseCertificate(phone.DER)
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(leaf.Raw)
	if !bytes.Equal(h.PeerFingerprint(), want[:]) {
		t.Error("recorded fingerprint is not SHA-256 over the certificate the peer sent")
	}
}

func TestPeerFingerprintIsACopy(t *testing.T) {
	var h Handshake
	fp := sha256.Sum256([]byte("x"))
	h.record(fp[:])
	got := h.PeerFingerprint()
	got[0] ^= 0xFF
	if !bytes.Equal(h.PeerFingerprint(), fp[:]) {
		t.Error("mutating the returned fingerprint changed the recorded one")
	}
}
