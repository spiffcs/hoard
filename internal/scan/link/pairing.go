package link

// Pairing, and why it is not optional.
//
// The scanner auto-commits: a card it is confident about is written to the
// collection without asking. An unauthenticated peer on the local network is
// therefore a way for anyone on that network to inject cards into someone's
// database. Six digits is not a lot of entropy and is not meant to be — under
// trust-on-first-use the code authenticates exactly one exchange, the first,
// where the two ends learn each other's certificate fingerprints. After that
// the pinned certificate does the work and the code is never sent again.
//
// Counterpart: ScanLink/Pairing.swift. Values here are fixed by that file.

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

// ServiceType is the Bonjour service the phone advertises.
const ServiceType = "_hoardscan._tcp"

// pairingSalt is the HKDF salt. It is a protocol constant, not a secret, and
// changing it silently breaks every existing pairing.
const pairingSalt = "dev.spiffcs.hoard.scan.pairing.v1"

// derivedKeyLen is the HKDF output length in bytes.
const derivedKeyLen = 32

// ErrBadCode means a pairing code was not six digits.
var ErrBadCode = errors.New("link: pairing code must be six digits")

// Code is a pairing code: six digits, shown on the phone, typed on the Mac.
type Code struct {
	digits string
}

// ParseCode accepts anything with six digits in it, ignoring separators — the
// code is read aloud off a phone screen and typed, so "045 208" is the same
// code as "045208".
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

// Digits is the bare six characters.
func (c Code) Digits() string { return c.digits }

// Display groups the code for reading aloud, which is how it actually travels
// between a phone on a stand and a person at a keyboard.
func (c Code) Display() string {
	if len(c.digits) != 6 {
		return c.digits
	}
	return c.digits[:3] + " " + c.digits[3:]
}

// IsZero reports whether this is the empty Code, which no session can use.
func (c Code) IsZero() bool { return c.digits == "" }

// key derives the pre-shared key from the digits.
//
// Stretched rather than used raw: six digits is a million possibilities, and a
// key derived by one hash invocation is one an attacker who captures a
// handshake can grind offline in seconds. HKDF with a fixed salt does not fix
// that — nothing fixes six digits against an offline attack — but it costs
// nothing and means the key is not simply the code.
func (c Code) key() ([]byte, error) {
	return hkdf.Key(sha256.New, []byte(c.digits), []byte(pairingSalt), "", derivedKeyLen)
}

// Proof is what a peer sends to show it knows the code, without sending it:
// an HMAC of the session id, bound to the certificate it is looking at.
//
// The session id is fresh per connection attempt, so a captured proof is worth
// nothing against the next one.
//
// peerFingerprint is what binds the proof to a particular TLS channel, and it
// is load-bearing. Without it a proof says only "I know the code", which a
// relay can forward verbatim: it completes TLS with each honest end
// separately, passes their proofs through, and both ends pin *it*. With it a
// proof says "I know the code and I am looking at the certificate whose
// fingerprint is this" — which the relay cannot produce, because the
// certificate each end sees is the relay's own.
//
// So the sender binds the fingerprint it *observed*, and the verifier checks
// against its own certificate, which it knows independently.
//
// A nil fingerprint is the unbound form. That is tests and history; it is not
// a supported way to run the link.
func Proof(session string, c Code, peerFingerprint []byte) (string, error) {
	key, err := c.key()
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(session))
	if peerFingerprint != nil {
		// A separator, so a session id ending in bytes that happen to look
		// like a fingerprint cannot be re-split to authenticate a different
		// pair of values.
		mac.Write([]byte{0x00})
		mac.Write(peerFingerprint)
	}
	return base64.StdEncoding.EncodeToString(mac.Sum(nil)), nil
}

// VerifyProof checks a claimed proof in constant time.
//
// hmac.Equal rather than ==: comparing MACs with a short-circuiting equality
// leaks how much of the value was right through timing, which is the standard
// way this check is quietly not a check.
//
// ownFingerprint is this end's *own* certificate fingerprint — not the peer's.
// See Proof for why the two sides differ.
func VerifyProof(claimed, session string, c Code, ownFingerprint []byte) bool {
	want, err := Proof(session, c, ownFingerprint)
	if err != nil {
		return false
	}
	// Both are base64 of a 32-byte MAC, so comparing the encodings is
	// equivalent to comparing the MACs, and hmac.Equal keeps it constant time.
	// A malformed claimed value simply differs in length.
	return hmac.Equal([]byte(claimed), []byte(want))
}

// Role says what a connection carries.
//
// Two connections rather than one, and this is the reason: a 200 KB preview
// JPEG queued ahead of a scan event is head-of-line blocking on exactly the
// latency the design exists to protect. Control is small, ordered and never
// dropped; preview is large, lossy and dropped the moment it would queue.
type Role string

const (
	RoleControl Role = "control"
	RolePreview Role = "preview"
)

// Hello is the first frame on every connection: which connection it is, which
// session it belongs to, and proof that the sender knows the pairing code.
//
// It is sent once TLS is *ready* and never before. The proof commits to the
// peer's certificate fingerprint, which does not exist until the handshake has
// settled — a hello sent early carries a proof bound to nothing, and the far
// side reports that as a wrong pairing code.
//
// Name is what the far end is, for the phone's benefit. Empty from this side,
// and tagged without omitempty deliberately: the Swift encoder always emits
// the key, and a decoder on a build that requires it would reject a hello that
// silently dropped it.
type Hello struct {
	Role    Role   `json:"role"`
	Session string `json:"session"`
	Proof   string `json:"proof"`
	Name    string `json:"name"`
}
