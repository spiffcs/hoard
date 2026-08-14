package link

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewIdentityShape(t *testing.T) {
	id, err := NewIdentity("dev.spiffcs.hoard.scan.mac")
	if err != nil {
		t.Fatal(err)
	}
	leaf := id.Certificate.Leaf
	if leaf == nil {
		t.Fatal("Leaf is nil; crypto/tls would have to re-parse on every handshake")
	}
	if leaf.Subject.CommonName != "dev.spiffcs.hoard.scan.mac" {
		t.Errorf("CN = %q", leaf.Subject.CommonName)
	}
	if leaf.Subject.CommonName != leaf.Issuer.CommonName {
		t.Error("not self-signed")
	}
	key, ok := id.Certificate.PrivateKey.(*ecdsa.PrivateKey)
	if !ok {
		t.Fatalf("private key is %T, want *ecdsa.PrivateKey", id.Certificate.PrivateKey)
	}
	if key.Curve != elliptic.P256() {
		t.Errorf("curve = %v, want P-256", key.Curve.Params().Name)
	}

	if !leaf.NotBefore.Before(time.Now()) {
		t.Error("NotBefore is not in the past")
	}
	if leaf.NotAfter.Before(time.Now().Add(9 * 365 * 24 * time.Hour)) {
		t.Error("certificate expires sooner than the ten years peers assume")
	}

	if err := leaf.CheckSignature(
		leaf.SignatureAlgorithm, leaf.RawTBSCertificate, leaf.Signature); err != nil {
		t.Errorf("self-signature does not verify: %v", err)
	}
}

func TestFingerprintIsSHA256OfDER(t *testing.T) {
	id, err := NewIdentity("x")
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(id.DER)
	if !bytes.Equal(id.Fingerprint(), want[:]) {
		t.Error("Fingerprint() is not SHA-256 over the certificate DER")
	}

	if !bytes.Equal(id.DER, id.Certificate.Certificate[0]) {
		t.Error("DER differs from the bytes crypto/tls will send")
	}
}

func TestIdentitiesAreDistinct(t *testing.T) {

	a, err := NewIdentity("same-name")
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewIdentity("same-name")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a.Fingerprint(), b.Fingerprint()) {
		t.Fatal("two freshly minted identities share a fingerprint")
	}
	if a.Certificate.Leaf.SerialNumber.Cmp(b.Certificate.Leaf.SerialNumber) == 0 {
		t.Error("two freshly minted identities share a serial number")
	}
}

func TestLoadOrCreateIsStable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "identity.pem")

	first, err := LoadOrCreateIdentity(path, "dev.spiffcs.hoard.scan.mac")
	if err != nil {
		t.Fatal(err)
	}
	second, err := LoadOrCreateIdentity(path, "dev.spiffcs.hoard.scan.mac")
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(first.Fingerprint(), second.Fingerprint()) {
		t.Fatal("loading an existing identity produced a different fingerprint")
	}
	if !bytes.Equal(first.DER, second.DER) {
		t.Error("reloaded DER differs from what was written")
	}

	if _, ok := second.Certificate.PrivateKey.(*ecdsa.PrivateKey); !ok {
		t.Error("reloaded identity has no usable private key")
	}
}

func TestIdentityFilePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "identity.pem")
	if _, err := LoadOrCreateIdentity(path, "x"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("identity file mode = %o, want 600", perm)
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("identity directory mode = %o, want 700", perm)
	}
}

func TestCorruptIdentityIsNotSilentlyReplaced(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "identity.pem")

	good, err := LoadOrCreateIdentity(path, "x")
	if err != nil {
		t.Fatal(err)
	}

	for name, content := range map[string]string{
		"empty":            "",
		"garbage":          "not a pem file at all",
		"cert only":        string(mustCertPEM(t, good)),
		"truncated base64": "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n",
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadOrCreateIdentity(path, "x"); err == nil {
				t.Error("a corrupt identity was silently replaced instead of refused")
			}

			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != content {
				t.Error("a corrupt identity file was overwritten")
			}
		})
	}
}

func mustCertPEM(t *testing.T, id *Identity) []byte {
	t.Helper()
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: id.DER})
}

func TestLoadMissingIdentityCreates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "identity.pem")
	if _, err := loadIdentity(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("loadIdentity on a missing file: %v, want os.ErrNotExist", err)
	}
	if _, err := LoadOrCreateIdentity(path, "x"); err != nil {
		t.Fatalf("LoadOrCreateIdentity did not create: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("identity was not written: %v", err)
	}
}
