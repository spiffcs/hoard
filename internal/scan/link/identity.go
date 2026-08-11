package link

// This end's TLS identity: a P-256 key pair and the self-signed certificate
// carrying its public half.
//
// There is no certificate authority in a house with one phone and one Mac, and
// inventing one would be worse than not having one. So each end mints its own
// certificate once, keeps it forever, and the two learn each other's
// fingerprints during pairing — trust on first use, the shape KDE Connect and
// Syncthing use for exactly this problem.
//
// Counterpart: ScanLink/PeerIdentity.swift, which is 340 lines because iOS has
// no public API that generates a self-signed X.509 certificate and has to
// assemble one byte by byte in DER. crypto/x509 does it directly, so almost
// all of that file has no counterpart here. What must still match exactly is
// the *fingerprint*: SHA-256 over the certificate DER, which is what one end
// pins about the other.

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// identityLifetime matches PeerIdentity.swift's default: ten years. Long
// because rotating invalidates every peer that pinned the old certificate,
// which is why neither side has a refresh path.
const identityLifetime = 10 * 365 * 24 * time.Hour

// backdate is how far before now the certificate becomes valid, covering
// clock skew between a Mac and a phone. PeerIdentity.swift:193 uses the same
// 300 seconds.
const backdate = 5 * time.Minute

// Identity is this device's certificate and private key.
type Identity struct {
	// DER is the certificate exactly as it goes on the wire. The fingerprint
	// is taken over these bytes, so it is kept rather than re-encoded — a
	// re-encode that differed by a byte would change the fingerprint and
	// silently unpair every peer.
	DER []byte
	// Certificate is the same thing in the shape crypto/tls wants.
	Certificate tls.Certificate
}

// Fingerprint is SHA-256 over the certificate DER — the pinned value, and what
// the pairing proof is bound to.
//
// Over the whole certificate rather than the public key alone: both are
// defensible, and the whole-certificate hash is what Syncthing's device ID and
// KDE Connect's pinning use. PeerTrust.swift:146-149 computes exactly this, so
// the two ends agree by construction.
func (i *Identity) Fingerprint() []byte {
	sum := sha256.Sum256(i.DER)
	return sum[:]
}

// NewIdentity mints a fresh identity. commonName lands in the certificate's
// subject, which travels in the clear on every handshake — so it is a fixed
// service string, never a device name.
func NewIdentity(commonName string) (*Identity, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("link: generating key: %w", err)
	}

	// X.509 wants a positive, unique serial. Unique matters because a peer
	// that pins by fingerprint may still index by serial.
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, fmt.Errorf("link: generating serial: %w", err)
	}

	now := time.Now()
	// No extensions, deliberately. This certificate makes no claim that needs
	// one: it is not a CA, it is not matched against a hostname, and the peer
	// authorises it by fingerprint rather than by anything it asserts about
	// itself. basicConstraints or a SAN would be decoration the verify block
	// on either end ignores.
	template := &x509.Certificate{
		SerialNumber:       serial,
		Subject:            pkix.Name{CommonName: commonName},
		Issuer:             pkix.Name{CommonName: commonName},
		NotBefore:          now.Add(-backdate),
		NotAfter:           now.Add(identityLifetime),
		SignatureAlgorithm: x509.ECDSAWithSHA256,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("link: creating certificate: %w", err)
	}

	// Parse what was just emitted, so a malformed certificate fails here
	// rather than as an unexplained handshake failure days later. This is the
	// same check PeerIdentity.swift:271-276 makes for the same reason.
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("link: generated certificate did not parse: %w", err)
	}

	return &Identity{
		DER: der,
		Certificate: tls.Certificate{
			Certificate: [][]byte{der},
			PrivateKey:  key,
			Leaf:        leaf,
		},
	}, nil
}

// LoadOrCreateIdentity returns the identity stored at path, generating and
// saving one on first use.
//
// A file rather than the macOS keychain, which is what the Swift helper uses
// (RemoteController.swift:49-54). The trade is deliberate: a file is readable
// by anything running as the user, where the keychain at least gates on
// unlock — but hoard already writes pairing codes in cleartext to this same
// directory, so the private key is the only genuinely new secret, and a file
// keeps the identity path off the Security framework and portable off macOS.
func LoadOrCreateIdentity(path, commonName string) (*Identity, error) {
	id, err := loadIdentity(path)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		// A corrupt or unreadable identity is not silently replaced.
		// Regenerating would unpair every phone that pinned the old
		// certificate, and doing that by accident — because a file was
		// truncated — is worse than refusing to start.
		return nil, err
	}

	id, err = NewIdentity(commonName)
	if err != nil {
		return nil, err
	}
	if err := saveIdentity(path, id); err != nil {
		return nil, err
	}
	return id, nil
}

// loadIdentity reads a PEM file holding the certificate and its key.
func loadIdentity(path string) (*Identity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var certDER, keyDER []byte
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		switch block.Type {
		case "CERTIFICATE":
			certDER = block.Bytes
		case "EC PRIVATE KEY", "PRIVATE KEY":
			keyDER = block.Bytes
		}
	}
	if certDER == nil || keyDER == nil {
		return nil, fmt.Errorf("link: %s is not a complete identity", path)
	}
	leaf, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, fmt.Errorf("link: parsing stored certificate: %w", err)
	}
	key, err := x509.ParseECPrivateKey(keyDER)
	if err != nil {
		return nil, fmt.Errorf("link: parsing stored key: %w", err)
	}
	return &Identity{
		DER: certDER,
		Certificate: tls.Certificate{
			Certificate: [][]byte{certDER},
			PrivateKey:  key,
			Leaf:        leaf,
		},
	}, nil
}

// saveIdentity writes the identity atomically, 0600.
//
// Atomically because a half-written identity is indistinguishable from a
// corrupt one, and loadIdentity deliberately refuses rather than regenerating.
// An interrupted first run would otherwise brick pairing until someone deleted
// the file by hand.
func saveIdentity(path string, id *Identity) error {
	key, ok := id.Certificate.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		return errors.New("link: identity has no EC private key to save")
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return fmt.Errorf("link: encoding key: %w", err)
	}

	var buf []byte
	buf = append(buf, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: id.DER})...)
	buf = append(buf, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})...)

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".identity-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once the rename succeeds
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(buf); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
