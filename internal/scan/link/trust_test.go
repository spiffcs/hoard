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

	// A missing file is an empty set, not an error: the first run has no pins.
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

	// Idempotent — pairing the same phone twice is ordinary.
	if err := s.Pin(fp[:], "Chris's iPhone"); err != nil {
		t.Fatal(err)
	}
	if got := s.All(); len(got) != 1 {
		t.Errorf("re-pinning made %d entries", len(got))
	}

	// A second store on the same path sees it: pins survive a restart, which
	// is the entire point of writing them down.
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
	// The set of pins is the authorisation list; an attacker who can add an
	// entry needs nothing else.
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

// TestCorruptPinStoreEmptiesRatherThanWidens — unlike the identity, a corrupt
// pin set is recoverable by re-pairing, so it must not be fatal. What it must
// never do is fail open.
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
	data, _ := json.Marshal(b) // base64 via json's []byte encoding
	var s string
	_ = json.Unmarshal(data, &s)
	return s
}

// handshake runs a real TLS exchange between this package's client config and
// a server presenting serverID, and reports what each side concluded.
//
// A real handshake rather than calling verify() directly: the pinning logic is
// installed through crypto/tls, and a test that bypasses it proves the function
// works without proving it is wired up.
//
// Loopback TCP rather than net.Pipe, and the difference is not cosmetic.
// net.Pipe is synchronous and unbuffered, so a write blocks until the peer
// reads — and when both TLS ends close, each tries to write close_notify with
// nobody reading. That deadlocks until a deadline fires, which turned this
// suite into 65 seconds of tests that all passed. TCP has kernel buffers, so
// the alert lands and both sides return at once.
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
		// The phone requires a client certificate (PeerTrust.swift:174-178),
		// so the server half insists on one here too — otherwise this would
		// pass with a Go config that never presents one.
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

// handshakeTestTimeout is a backstop, not a wait. A handshake over loopback is
// sub-millisecond; anything approaching this is a deadlock worth failing on
// rather than sitting through.
const handshakeTestTimeout = 5 * time.Second

func TestHandshakeRejectsUnpinnedPeer(t *testing.T) {
	pins := NewPinStore(filepath.Join(t.TempDir(), "pins.json"))
	trust := TrustStore(newTestIdentity(t, "mac"), pins)
	var h Handshake

	clientErr, _ := handshake(t, trust, &h, newTestIdentity(t, "phone"))
	if !errors.Is(clientErr, ErrPeerNotPinned) {
		t.Fatalf("handshake with an unpinned peer: %v, want ErrPeerNotPinned", clientErr)
	}
	// Even on refusal the fingerprint is recorded, so a caller can tell the
	// user which certificate it saw rather than only that something failed.
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
	// The server insisted on a client certificate, so this also proves hoard
	// presents one — without it the phone's peer_authentication_required
	// would refuse every connection.
	if serverErr != nil {
		t.Fatalf("server rejected the client: %v", serverErr)
	}
	if !bytes.Equal(h.PeerFingerprint(), phone.Fingerprint()) {
		t.Error("recorded fingerprint is not the peer's")
	}
}

// TestPairingWindowAcceptsUnknown — during pairing, TLS is not the gate; the
// proof on the hello is. An unknown peer must be able to complete a handshake
// so that the proof has a certificate to bind to.
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

// TestPolicyIsReadLive is the bug PeerTrust.swift:99-112 documents: a pin set
// captured when the config was built means the phone that just paired is a
// stranger again on its next connection, and the failure looks like a network
// fault.
func TestPolicyIsReadLive(t *testing.T) {
	pins := NewPinStore(filepath.Join(t.TempDir(), "pins.json"))
	phone := newTestIdentity(t, "phone")
	trust := TrustStore(newTestIdentity(t, "mac"), pins)

	// Built before the pin exists.
	var first Handshake
	if clientErr, _ := handshake(t, trust, &first, phone); !errors.Is(clientErr, ErrPeerNotPinned) {
		t.Fatalf("expected refusal before pinning, got %v", clientErr)
	}

	// Pin mid-life, exactly as a completed pairing does.
	if err := pins.Pin(phone.Fingerprint(), "phone"); err != nil {
		t.Fatal(err)
	}

	// The same Trust must now accept, with nothing rebuilt.
	var second Handshake
	if clientErr, _ := handshake(t, trust, &second, phone); clientErr != nil {
		t.Fatalf("a peer pinned during the session was refused: %v", clientErr)
	}
}

// TestClosingThePairingWindowTakesEffect is the other half: the window has to
// be closable without rebuilding the listener.
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

// TestWrongPinIsRefused — pinning one phone must not admit another. The two
// certificates differ only in their bytes, which is the whole security model.
func TestWrongPinIsRefused(t *testing.T) {
	pins := NewPinStore(filepath.Join(t.TempDir(), "pins.json"))
	wanted := newTestIdentity(t, "phone")
	impostor := newTestIdentity(t, "phone") // same CN, different key
	if err := pins.Pin(wanted.Fingerprint(), "phone"); err != nil {
		t.Fatal(err)
	}
	trust := TrustStore(newTestIdentity(t, "mac"), pins)

	var h Handshake
	if clientErr, _ := handshake(t, trust, &h, impostor); !errors.Is(clientErr, ErrPeerNotPinned) {
		t.Fatalf("an impostor with a matching common name was accepted: %v", clientErr)
	}
}

// TestVerifyRejectsEmptyChain — a peer that presents nothing must not be read
// as a peer that presented something unrecognised.
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
	// Neither case may leave a fingerprint behind for the hello to bind to.
	if h.PeerFingerprint() != nil {
		t.Error("recorded a fingerprint from a certificate that never parsed")
	}
}

// TestHandshakeFingerprintMatchesTheWire ties the recorded value back to the
// certificate crypto/tls actually saw, so a future refactor cannot start
// hashing something adjacent — the parsed form, say, rather than the raw DER.
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

// TestPeerFingerprintIsACopy — the caller binds this into a MAC, and handing
// out the internal slice would let a caller mutate what the session trusts.
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
