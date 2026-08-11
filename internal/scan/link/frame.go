// Package link is hoard's own end of the wire to Hoardling, the iPhone app.
//
// The phone advertises _hoardscan._tcp and listens; this side browses and
// connects. Everything here has a counterpart in the phone's Swift sources
// under scan/hoard-scan/Sources/ScanLink and ScanWire, and the two must agree
// exactly: a mismatch in framing or in the pairing proof fails as a hang, with
// both ends sitting in "connecting" and nothing on either's stderr. That is why
// the tests here are driven by vectors generated from the Swift implementation
// rather than by values written out a second time by hand — see
// testdata/vectors.json.
package link

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Kind says what a frame's payload is. The values are on the wire; see
// ScanWire/FrameCodec.swift.
type Kind uint8

const (
	// KindNDJSON is one NDJSON line — an Event from the phone, or a command
	// to it. Ordered, never dropped, never delayed behind a preview frame.
	KindNDJSON Kind = 0
	// KindPreview is a downscaled preview JPEG: lossy and droppable by
	// design, because a stale preview frame is worse than a missing one.
	KindPreview Kind = 1
	// KindStill is a full-resolution still, sent only when asked for — the
	// fixture faucet, not part of the session loop.
	KindStill Kind = 2
	// KindTrace is a phone-side telemetry line. The helper used to re-emit
	// these on its stderr so HOARD_SCAN_LOG stayed whole across the process
	// hop; with no helper they arrive here directly.
	KindTrace Kind = 3
)

func (k Kind) String() string {
	switch k {
	case KindNDJSON:
		return "ndjson"
	case KindPreview:
		return "preview"
	case KindStill:
		return "still"
	case KindTrace:
		return "trace"
	}
	return fmt.Sprintf("Kind(%d)", uint8(k))
}

// valid reports whether k is a kind this build knows. Deliberately a closed
// set: see ErrUnknownKind.
func (k Kind) valid() bool { return k <= KindTrace }

// HeaderSize is one kind byte plus a four-byte big-endian length.
const HeaderSize = 5

// MaxPayload bounds a single frame, so a corrupt or hostile length cannot make
// the reader allocate arbitrarily while it waits for bytes that never arrive.
// A 48 MP still is comfortably inside it; nothing legitimate is not.
const MaxPayload = 64 * 1024 * 1024

// HelloPayloadLimit is the ceiling for a peer that has not proved the pairing
// code yet. A hello is a few hundred bytes, so nothing legitimate is larger,
// and without a narrowed limit an unverified connection can declare 64 MB and
// feed it a byte at a time.
const HelloPayloadLimit = 4096

var (
	// ErrUnknownKind means the peer sent a frame type this build does not
	// know. It is fatal to the connection, never skipped: the length that
	// follows cannot be trusted either, so reading on would parse the next
	// header out of the middle of a payload. The stream is already lost.
	ErrUnknownKind = errors.New("link: unknown frame kind")
	// ErrPayloadTooLarge means a frame declared more than the reader allows.
	ErrPayloadTooLarge = errors.New("link: frame payload too large")
)

// Frame is one framed message.
type Frame struct {
	Kind    Kind
	Payload []byte
}

// Text returns the payload as a string, for the line-shaped kinds.
func (f Frame) Text() string { return string(f.Payload) }

// Encode renders a frame as bytes.
func Encode(f Frame) ([]byte, error) {
	if len(f.Payload) > MaxPayload {
		return nil, fmt.Errorf("%w: %d bytes", ErrPayloadTooLarge, len(f.Payload))
	}
	if !f.Kind.valid() {
		return nil, fmt.Errorf("%w: %d", ErrUnknownKind, uint8(f.Kind))
	}
	out := make([]byte, HeaderSize+len(f.Payload))
	out[0] = byte(f.Kind)
	binary.BigEndian.PutUint32(out[1:], uint32(len(f.Payload)))
	copy(out[HeaderSize:], f.Payload)
	return out, nil
}

// WriteTo encodes a frame straight onto w, without the intermediate copy
// Encode makes. Worth having because stills are multi-megabyte.
func WriteFrame(w io.Writer, f Frame) error {
	if len(f.Payload) > MaxPayload {
		return fmt.Errorf("%w: %d bytes", ErrPayloadTooLarge, len(f.Payload))
	}
	if !f.Kind.valid() {
		return fmt.Errorf("%w: %d", ErrUnknownKind, uint8(f.Kind))
	}
	var header [HeaderSize]byte
	header[0] = byte(f.Kind)
	binary.BigEndian.PutUint32(header[1:], uint32(len(f.Payload)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	// Two writes rather than one buffer: the caller is a TCP connection with
	// Nagle disabled on the control leg, and the header is five bytes.
	if len(f.Payload) > 0 {
		if _, err := w.Write(f.Payload); err != nil {
			return err
		}
	}
	return nil
}

// ReadFrame reads exactly one frame from r.
//
// The Swift side needs a byte-accumulator here (FrameCodec.FrameReader),
// because Network.framework pushes bytes at it: a read can deliver half a
// header, three frames, or a header now and its payload in four pieces over
// the next second. Go pulls instead, and io.ReadFull collapses all of that into
// two calls — so the accumulator has no counterpart and is not missing.
//
// limit caps the payload; pass MaxPayload for a verified connection and
// HelloPayloadLimit for one that has not proved the code yet.
//
// A short read at a frame boundary returns io.EOF, which is a peer that closed
// cleanly. A short read mid-frame returns io.ErrUnexpectedEOF, which is a peer
// that vanished. Callers care about the difference.
func ReadFrame(r io.Reader, limit int) (Frame, error) {
	var header [HeaderSize]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return Frame{}, err
	}
	kind := Kind(header[0])
	if !kind.valid() {
		return Frame{}, fmt.Errorf("%w: %d", ErrUnknownKind, header[0])
	}
	length := int(binary.BigEndian.Uint32(header[1:]))
	if length > limit {
		return Frame{}, fmt.Errorf("%w: %d bytes, limit %d", ErrPayloadTooLarge, length, limit)
	}
	if length == 0 {
		return Frame{Kind: kind}, nil
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		// An EOF partway through a payload is not a clean close, and
		// reporting it as one would make a peer that vanished mid-still look
		// like a peer that said goodbye.
		if errors.Is(err, io.EOF) {
			return Frame{}, io.ErrUnexpectedEOF
		}
		return Frame{}, err
	}
	return Frame{Kind: kind, Payload: payload}, nil
}
