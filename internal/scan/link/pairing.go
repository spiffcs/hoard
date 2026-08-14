package link

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

const ServiceType = "_hoardscan._tcp"

const pairingSalt = "dev.spiffcs.hoard.scan.pairing.v1"

const derivedKeyLen = 32

var ErrBadCode = errors.New("link: pairing code must be six digits")

type Code struct {
	digits string
}

func ParseCode(raw string) (Code, error) {
	cleaned := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] >= '0' && raw[i] <= '9' {
			cleaned = append(cleaned, raw[i])
		}
	}
	if len(cleaned) != 6 {
		return Code{}, fmt.Errorf("%w: got %d", ErrBadCode, len(cleaned))
	}
	return Code{digits: string(cleaned)}, nil
}

func (c Code) Digits() string { return c.digits }

func (c Code) Display() string {
	if len(c.digits) != 6 {
		return c.digits
	}
	return c.digits[:3] + " " + c.digits[3:]
}

func (c Code) IsZero() bool { return c.digits == "" }

func (c Code) key() ([]byte, error) {
	return hkdf.Key(sha256.New, []byte(c.digits), []byte(pairingSalt), "", derivedKeyLen)
}

func Proof(session string, c Code, peerFingerprint []byte) (string, error) {
	key, err := c.key()
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(session))
	if peerFingerprint != nil {

		mac.Write([]byte{0x00})
		mac.Write(peerFingerprint)
	}
	return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

func VerifyProof(claimed, session string, c Code, ownFingerprint []byte) bool {
	want, err := Proof(session, c, ownFingerprint)
	if err != nil {
		return false
	}

	return hmac.Equal([]byte(claimed), []byte(want))
}

type Role string

const (
	RoleControl Role = "control"
	RolePreview Role = "preview"
)

type Hello struct {
	Role    Role   `json:"role"`
	Session string `json:"session"`
	Proof   string `json:"proof"`
	Name    string `json:"name"`
}
