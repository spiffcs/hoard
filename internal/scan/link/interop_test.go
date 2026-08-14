package link

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

func TestSwiftAcceptsGoCertificate(t *testing.T) {
	v := loadCertVectors(t)
	der := unhex(t, v.GoCert.DERHex)

	if !v.GoCert.SwiftParsed {
		t.Fatal("Security.framework could not parse a certificate this package minted")
	}

	sum := sha256.Sum256(der)
	if got := hex.EncodeToString(sum[:]); got != v.GoCert.SwiftFPHex {
		t.Errorf("fingerprint disagreement on a Go-minted certificate:\n  Go    %s\n  Swift %s",
			got, v.GoCert.SwiftFPHex)
	}

	id := &Identity{DER: der}
	if got := hex.EncodeToString(id.Fingerprint()); got != v.GoCert.SwiftFPHex {
		t.Errorf("Identity.Fingerprint() = %s, Swift computed %s", got, v.GoCert.SwiftFPHex)
	}

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

	if leaf.PublicKeyAlgorithm != x509.ECDSA {
		t.Errorf("public key algorithm = %v, want ECDSA", leaf.PublicKeyAlgorithm)
	}
	if leaf.SignatureAlgorithm != x509.ECDSAWithSHA256 {
		t.Errorf("signature algorithm = %v, want ECDSAWithSHA256", leaf.SignatureAlgorithm)
	}
}

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

	if goLeaf.Issuer.CommonName != goLeaf.Subject.CommonName {
		t.Error("Go certificate is not self-signed")
	}
	if swiftLeaf.Issuer.CommonName != swiftLeaf.Subject.CommonName {
		t.Error("Swift certificate is not self-signed")
	}

	if n := len(goLeaf.Extensions); n != 0 {
		t.Errorf("Go certificate has %d extensions, Swift has %d",
			n, len(swiftLeaf.Extensions))
	}

	if goLeaf.SerialNumber.Sign() <= 0 {
		t.Error("Go serial number is not positive")
	}
	if swiftLeaf.SerialNumber.Sign() <= 0 {
		t.Error("Swift serial number is not positive")
	}
}

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
