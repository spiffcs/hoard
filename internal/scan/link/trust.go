package link

// Who this end is willing to talk to.
//
// Trust on first use: this device presents the certificate from Identity and
// accepts a peer whose certificate fingerprint it has pinned. The pairing code
// authenticates exactly one exchange — the first, where the two ends learn each
// other's fingerprints — and after that the pinned certificate does the work.
//
// Counterpart: ScanLink/PeerTrust.swift.

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// ErrPeerNotPinned is the handshake refusing a certificate this device has
// never paired with. It is deliberately distinguishable from a network error:
// "you have not paired yet" and "the phone went away" want different advice.
var ErrPeerNotPinned = errors.New("link: peer certificate is not pinned")

// PinStore is the set of peers this device trusts, on disk.
//
// A file rather than the keychain — see LoadOrCreateIdentity for the trade.
// Mode 0600 matters more here than it looks: a pinned fingerprint is not a
// secret, it is a public hash, but the *set* of them is the authorisation
// list, and an attacker who can add an entry to it needs none of the rest of
// this. PeerTrust.swift:33-36 makes the same argument for the keychain.
type PinStore struct {
	path string
	mu   sync.Mutex
}

// NewPinStore returns the pin set stored at path. The file need not exist.
func NewPinStore(path string) *PinStore { return &PinStore{path: path} }

// pinFile is the on-disk shape: base64 fingerprint to the name it was pinned
// under, which is only ever shown to a human.
type pinFile struct {
	Peers map[string]string `json:"peers"`
}

func (s *PinStore) load() (pinFile, error) {
	var pf pinFile
	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return pinFile{Peers: map[string]string{}}, nil
		}
		return pf, err
	}
	if err := json.Unmarshal(data, &pf); err != nil {
		// Unlike the identity, a corrupt pin set is not fatal: the worst it
		// costs is a re-pair, and refusing to start over it would be worse
		// than the problem. It is never *silently* widened, only emptied.
		return pinFile{Peers: map[string]string{}}, nil
	}
	if pf.Peers == nil {
		pf.Peers = map[string]string{}
	}
	return pf, nil
}

func (s *PinStore) save(pf pinFile) error {
	data, err := json.Marshal(pf)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".pins-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path)
}

// Pin records a peer as trusted. Idempotent.
func (s *PinStore) Pin(fingerprint []byte, name string) error {
	if len(fingerprint) != sha256.Size {
		return fmt.Errorf("link: fingerprint is %d bytes, want %d", len(fingerprint), sha256.Size)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pf, err := s.load()
	if err != nil {
		return err
	}
	pf.Peers[base64.StdEncoding.EncodeToString(fingerprint)] = name
	return s.save(pf)
}

// Rename updates the label a pinned peer is remembered under, and does nothing
// at all for a fingerprint that is not pinned.
//
// Deliberately not Pin with a different name. Pin creates an entry when one is
// missing, and creating an entry is the operation that grants trust — the whole
// authorisation list is these keys. A caller whose only business is refreshing
// a display label must not be able to widen trust by getting an argument wrong,
// so the two are separate methods rather than one with a flag. The missing-key
// case returning nil rather than an error is part of that: "that peer is not
// pinned" is the ordinary answer here, not a fault.
func (s *PinStore) Rename(fingerprint []byte, name string) error {
	if len(fingerprint) != sha256.Size || name == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pf, err := s.load()
	if err != nil {
		return err
	}
	key := base64.StdEncoding.EncodeToString(fingerprint)
	if cur, ok := pf.Peers[key]; !ok || cur == name {
		// Unchanged, or not ours to change. No write either way — this runs on
		// every session open, and the common case is a name that still matches.
		return nil
	}
	pf.Peers[key] = name
	return s.save(pf)
}

// Forget drops one peer.
func (s *PinStore) Forget(fingerprint []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pf, err := s.load()
	if err != nil {
		return err
	}
	delete(pf.Peers, base64.StdEncoding.EncodeToString(fingerprint))
	return s.save(pf)
}

// ForgetAll is the "unpair everything" action.
func (s *PinStore) ForgetAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save(pinFile{Peers: map[string]string{}})
}

// All returns every pinned fingerprint.
func (s *PinStore) All() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	pf, err := s.load()
	if err != nil {
		return nil
	}
	keys := make([]string, 0, len(pf.Peers))
	for k := range pf.Peers {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic, for tests and for logging
	out := make([][]byte, 0, len(keys))
	for _, k := range keys {
		if fp, err := base64.StdEncoding.DecodeString(k); err == nil {
			out = append(out, fp)
		}
	}
	return out
}

// Names returns pinned peers as name-by-fingerprint, for showing a human what
// this device is paired with.
func (s *PinStore) Names() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	pf, err := s.load()
	if err != nil {
		return nil
	}
	return pf.Peers
}

// Contains reports whether a fingerprint is pinned, in constant time.
func (s *PinStore) Contains(fingerprint []byte) bool {
	found := 0
	for _, pinned := range s.All() {
		// No early exit: the value being compared is attacker-supplied, and
		// short-circuiting leaks how much of it was right.
		found |= subtle.ConstantTimeCompare(pinned, fingerprint)
	}
	return found == 1
}

// Trust is what this end presents and what it will accept.
//
// Pinned and AcceptUnknown are functions, not values, because the policy is
// read *live* on every handshake. A snapshot is wrong in two ways that both
// fail silently (PeerTrust.swift:99-112): a peer pinned during this session is
// absent from a set captured before it, so the phone that just paired is a
// stranger again on its next connection; and the pairing window could not
// close without rebuilding the listener.
type Trust struct {
	Identity *Identity
	// Pinned returns the fingerprints trusted right now.
	Pinned func() [][]byte
	// AcceptUnknown reports whether a peer with an unknown fingerprint may
	// complete TLS right now. True only while pairing is open — the phone
	// showing a code, this end having just been given one. TLS is not the
	// gate in that window; the pairing proof on the hello is. False the rest
	// of the time, so an unpinned peer cannot even finish a handshake.
	AcceptUnknown func() bool
}

// TrustStore builds a Trust backed by a PinStore, with the pairing window
// closed. This is the scanning-session policy.
func TrustStore(id *Identity, pins *PinStore) *Trust {
	return &Trust{
		Identity:      id,
		Pinned:        pins.All,
		AcceptUnknown: func() bool { return false },
	}
}

// TrustPairing is the same thing with the pairing window open, for the one
// moment a person has just typed a code.
func TrustPairing(id *Identity, pins *PinStore) *Trust {
	t := TrustStore(id, pins)
	t.AcceptUnknown = func() bool { return true }
	return t
}

// Handshake records what one connection's TLS handshake saw.
//
// Per connection, deliberately, and this is not a stylistic choice. The Swift
// side learned it the hard way (PeerTrust.swift:205-213): an NWListener builds
// its parameters once and every accepted connection shares them, so a callback
// installed there fires per handshake with no way to say which connection it
// belongs to — wrong in the direction that attributes one peer's fingerprint
// to another's connection. A Go tls.Config has exactly the same hazard if it
// is reused, so the fingerprint hangs off this instead.
type Handshake struct {
	mu sync.Mutex
	fp []byte
}

// PeerFingerprint is the fingerprint of the certificate the peer presented,
// available once the handshake has completed. This is what the hello's proof
// is bound to, and what gets pinned when a pairing succeeds.
func (h *Handshake) PeerFingerprint() []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.fp == nil {
		return nil
	}
	out := make([]byte, len(h.fp))
	copy(out, h.fp)
	return out
}

func (h *Handshake) record(fp []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fp = fp
}

// ClientConfig builds the TLS configuration for one outbound connection,
// recording what it saw into h.
//
// InsecureSkipVerify with a hand-written VerifyPeerCertificate is the standard
// idiom for "no CA, pinned leaf", and the only way to express it: the
// certificates here are self-signed by design, so the system's own trust
// evaluation would reject every one of them, correctly and uselessly. Pinning
// is the whole policy.
func (t *Trust) ClientConfig(h *Handshake) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{t.Identity.Certificate},
		MinVersion:   tls.VersionTLS12,
		// Not a relaxation. The verification below is stricter than the
		// default: it accepts exactly one certificate rather than any
		// certificate a public CA vouches for.
		InsecureSkipVerify:    true,
		VerifyPeerCertificate: t.verify(h),
	}
}

// verify returns the pinning check. It runs on the handshake goroutine.
func (t *Trust) verify(h *Handshake) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return errors.New("link: peer presented no certificate")
		}
		// The leaf is first, and it is the only one that matters: the chain
		// is self-signed and one element long by construction.
		leaf := rawCerts[0]
		if _, err := x509.ParseCertificate(leaf); err != nil {
			return fmt.Errorf("link: peer certificate did not parse: %w", err)
		}
		sum := sha256.Sum256(leaf)
		seen := sum[:]

		// Recorded before the decision, not after: the hello step needs this
		// fingerprint even in the pairing window, where an unknown peer is
		// allowed through precisely so the proof can bind to it.
		h.record(seen)

		// Asked now, not when this closure was built. See Trust.
		for _, pinned := range t.Pinned() {
			if subtle.ConstantTimeCompare(pinned, seen) == 1 {
				return nil
			}
		}
		if t.AcceptUnknown() {
			return nil
		}
		return ErrPeerNotPinned
	}
}
