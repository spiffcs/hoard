package link

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

const identityLifetime = 10 * 365 * 24 * time.Hour

const backdate = 5 * time.Minute

type Identity struct {
	DER []byte

	Certificate tls.Certificate
}

func (i *Identity) Fingerprint() []byte {
	sum := sha256.Sum256(i.DER)
	return sum[:]
}

func NewIdentity(commonName string) (*Identity, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("link: generating key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, fmt.Errorf("link: generating serial: %w", err)
	}

	now := time.Now()

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

func LoadOrCreateIdentity(path, commonName string) (*Identity, error) {
	id, err := loadIdentity(path)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, os.ErrNotExist) {

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
	defer os.Remove(tmp.Name())
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
