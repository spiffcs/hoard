package link

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

var ErrPeerNotPinned = errors.New("link: peer certificate is not pinned")

type PinStore struct {
	path string
	mu   sync.Mutex
}

func NewPinStore(path string) *PinStore { return &PinStore{path: path} }

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

		return nil
	}
	pf.Peers[key] = name
	return s.save(pf)
}

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

func (s *PinStore) ForgetAll() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save(pinFile{Peers: map[string]string{}})
}

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
	sort.Strings(keys)
	out := make([][]byte, 0, len(keys))
	for _, k := range keys {
		if fp, err := base64.StdEncoding.DecodeString(k); err == nil {
			out = append(out, fp)
		}
	}
	return out
}

func (s *PinStore) Names() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	pf, err := s.load()
	if err != nil {
		return nil
	}
	return pf.Peers
}

func (s *PinStore) Contains(fingerprint []byte) bool {
	found := 0
	for _, pinned := range s.All() {

		found |= subtle.ConstantTimeCompare(pinned, fingerprint)
	}
	return found == 1
}

type Trust struct {
	Identity *Identity

	Pinned func() [][]byte

	AcceptUnknown func() bool
}

func TrustStore(id *Identity, pins *PinStore) *Trust {
	return &Trust{
		Identity:      id,
		Pinned:        pins.All,
		AcceptUnknown: func() bool { return false },
	}
}

func TrustPairing(id *Identity, pins *PinStore) *Trust {
	t := TrustStore(id, pins)
	t.AcceptUnknown = func() bool { return true }
	return t
}

type Handshake struct {
	mu sync.Mutex
	fp []byte
}

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

func (t *Trust) ClientConfig(h *Handshake) *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{t.Identity.Certificate},
		MinVersion:   tls.VersionTLS12,

		InsecureSkipVerify:    true,
		VerifyPeerCertificate: t.verify(h),
	}
}

func (t *Trust) verify(h *Handshake) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return errors.New("link: peer presented no certificate")
		}

		leaf := rawCerts[0]
		if _, err := x509.ParseCertificate(leaf); err != nil {
			return fmt.Errorf("link: peer certificate did not parse: %w", err)
		}
		sum := sha256.Sum256(leaf)
		seen := sum[:]

		h.record(seen)

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
