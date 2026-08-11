package link

// Certificate interop with the phone, in both directions.
//
// The fingerprint is the whole authorisation model: one end pins SHA-256 over
// the other's certificate DER, and from then on that hash *is* the peer's
// identity. If the two ends disagree about it by a single byte, pairing appears
// to succeed and every subsequent connection is refused — so this is checked
// against Security.framework's own answers rather than against Go's alone.
//
// testdata/certvectors.json holds both directions, recorded when the vectors
// were generated:
//
//   - goCert:    a certificate this package minted, handed to
//                SecCertificateCreateWithData, with whether Security parsed it
//                and what fingerprint it computed. This is the direction that
//                decides whether the phone can pin what hoard presents.
//   - swiftCert: a certificate PeerIdentity.swift's own selfSignedCertificate
//                produced, with its Swift-computed fingerprint. This is whether
//                hoard can pin what the phone presents.
//
// Both are verified here with no Swift toolchain present, so the test runs on
// Linux CI unchanged.

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

type certVectors struct {
	GoCert struct {
		DERHex         string `json:"derHex"`
		SwiftParsed    bool   `json:"swiftParsed"`
		SwiftFPHex     string `json:"swiftFingerprintHex"`
		SwiftSubjectCN string `json:"swiftSubjectCN"`
	} `json:"goCert"`
	SwiftCert struct {
		DERHex     string `json:"derHex"`
		SwiftFPHex string `json:"swiftFingerprintHex"`
		CommonName string `json:"commonName"`
	} `json:"swiftCert"`
}

func loadCertVectors(t *testing.T) certVectors {
	t.Helper()
	data, err := os.ReadFile("testdata/certvectors.json")
	if err != nil {
		t.Fatalf("reading cert vectors: %v", err)
	}
	var v certVectors
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("parsing cert vectors: %v", err)
	}
	return v
}

// TestSwiftAcceptsGoCertificate is the direction that gates the whole port. A
// certificate Security.framework will not parse is a phone that cannot
// complete a handshake, and the failure would present as a hang.
func TestSwiftAcceptsGoCertificate(t *testing.T) {
	v := loadCertVectors(t)
	der := unhex(t, v.GoCert.DERHex)

	if !v.GoCert.SwiftParsed {
		t.Fatal("Security.framework could not parse a certificate this package minted")
	}

	// Go's own fingerprint over the same bytes.
	sum := sha256.Sum256(der)
	if got := hex.EncodeToString(sum[:]); got != v.GoCert.SwiftFPHex {
		t.Errorf("fingerprint disagreement on a Go-minted certificate:\n  Go    %s\n  Swift %s",
			got, v.GoCert.SwiftFPHex)
	}

	// And the Identity accessor agrees with the raw computation, so a future
	// refactor of Fingerprint cannot drift from what the phone pins.
	id := &Identity{DER: der}
	if got := hex.EncodeToString(id.Fingerprint()); got != v.GoCert.SwiftFPHex {
		t.Errorf("Identity.Fingerprint() = %s, Swift computed %s", got, v.GoCert.SwiftFPHex)
	}

	// Security read the subject back. It is the only field either end looks
	// at, and only for diagnostics — but a certificate whose CN did not
	// survive encoding is a sign the DER is malformed in a way parsing alone
	// did not catch.
	if v.GoCert.SwiftSubjectCN == "" {
		t.Error("Security.framework read an empty subject from the Go certificate")
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("Go cannot parse its own certificate: %v", err)
	}
	if leaf.Subject.CommonName != v.GoCert.SwiftSubjectCN {
		t.Errorf("subject CN: Go says %q, Security says %q",
			leaf.Subject.CommonName, v.GoCert.SwiftSubjectCN)
	}
}

// TestGoAcceptsSwiftCertificate is the reverse: hoard must parse and fingerprint
// what the phone presents. PeerIdentity.swift hand-assembles its DER byte by
// byte, so this is checking a hand-rolled ASN.1 encoder against a real parser.
func TestGoAcceptsSwiftCertificate(t *testing.T) {
	v := loadCertVectors(t)
	der := unhex(t, v.SwiftCert.DERHex)

	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("Go cannot parse the phone's certificate shape: %v", err)
	}

	sum := sha256.Sum256(der)
	if got := hex.EncodeToString(sum[:]); got != v.SwiftCert.SwiftFPHex {
		t.Errorf("fingerprint disagreement on a Swift-minted certificate:\n  Go    %s\n  Swift %s",
			got, v.SwiftCert.SwiftFPHex)
	}

	if leaf.Subject.CommonName != v.SwiftCert.CommonName {
		t.Errorf("subject CN = %q, want %q", leaf.Subject.CommonName, v.SwiftCert.CommonName)
	}
	// The properties both ends assume of the other's certificate.
	if leaf.PublicKeyAlgorithm != x509.ECDSA {
		t.Errorf("public key algorithm = %v, want ECDSA", leaf.PublicKeyAlgorithm)
	}
	if leaf.SignatureAlgorithm != x509.ECDSAWithSHA256 {
		t.Errorf("signature algorithm = %v, want ECDSAWithSHA256", leaf.SignatureAlgorithm)
	}
}

// TestGoAndSwiftCertificatesAreShapeCompatible compares a freshly minted Go
// certificate against the recorded Swift one on the fields that have to line
// up. Neither end validates the other's certificate contents — pinning is the
// whole policy — but a structural divergence here is the sort of thing that
// works until some future OS starts caring.
func TestGoAndSwiftCertificatesAreShapeCompatible(t *testing.T) {
	v := loadCertVectors(t)
	swiftLeaf, err := x509.ParseCertificate(unhex(t, v.SwiftCert.DERHex))
	if err != nil {
		t.Fatalf("parsing Swift certificate: %v", err)
	}

	id, err := NewIdentity("dev.spiffcs.hoard.scan.mac")
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	goLeaf := id.Certificate.Leaf

	if goLeaf.PublicKeyAlgorithm != swiftLeaf.PublicKeyAlgorithm {
		t.Errorf("public key algorithm: Go %v, Swift %v",
			goLeaf.PublicKeyAlgorithm, swiftLeaf.PublicKeyAlgorithm)
	}
	if goLeaf.SignatureAlgorithm != swiftLeaf.SignatureAlgorithm {
		t.Errorf("signature algorithm: Go %v, Swift %v",
			goLeaf.SignatureAlgorithm, swiftLeaf.SignatureAlgorithm)
	}
	if goLeaf.Version != swiftLeaf.Version {
		t.Errorf("X.509 version: Go %d, Swift %d", goLeaf.Version, swiftLeaf.Version)
	}
	// Both are self-signed: issuer equals subject.
	if goLeaf.Issuer.CommonName != goLeaf.Subject.CommonName {
		t.Error("Go certificate is not self-signed")
	}
	if swiftLeaf.Issuer.CommonName != swiftLeaf.Subject.CommonName {
		t.Error("Swift certificate is not self-signed")
	}
	// Neither carries extensions. PeerIdentity.swift:181-187 says why, and a
	// Go template that quietly grew one would be a difference on the wire.
	if n := len(goLeaf.Extensions); n != 0 {
		t.Errorf("Go certificate has %d extensions, Swift has %d",
			n, len(swiftLeaf.Extensions))
	}
	// A serial that is not positive is rejected by some parsers and silently
	// re-read by others.
	if goLeaf.SerialNumber.Sign() <= 0 {
		t.Error("Go serial number is not positive")
	}
	if swiftLeaf.SerialNumber.Sign() <= 0 {
		t.Error("Swift serial number is not positive")
	}
}

// TestDumpIdentityForInterop prints a freshly minted certificate as hex, for
// feeding to the Swift side when regenerating testdata/certvectors.json.
// Skipped in normal runs.
//
//	HOARD_LINK_DUMP_CERT=1 go test ./internal/scan/link/ -run TestDumpIdentityForInterop -v
func TestDumpIdentityForInterop(t *testing.T) {
	if os.Getenv("HOARD_LINK_DUMP_CERT") == "" {
		t.Skip("set HOARD_LINK_DUMP_CERT=1 to emit a certificate for the Swift interop check")
	}
	id, err := NewIdentity("dev.spiffcs.hoard.scan.mac")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("DER_HEX=%s", hex.EncodeToString(id.DER))
	t.Logf("FINGERPRINT_HEX=%s", hex.EncodeToString(id.Fingerprint()))
}
