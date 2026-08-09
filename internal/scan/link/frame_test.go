package link

// Behaviour the Swift vectors cannot express: what happens on malformed input,
// on a truncated peer, and at the limits. These are this side's contract with
// its own callers rather than agreement with the phone.

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// TestUnknownKindIsFatal is the rule that keeps a desynchronised stream from
// looking like a working one. Skipping an unknown frame means trusting a length
// field this build cannot interpret, and the next header then comes out of the
// middle of a payload.
func TestUnknownKindIsFatal(t *testing.T) {
	for _, kind := range []byte{4, 5, 200, 255} {
		raw := []byte{kind, 0, 0, 0, 0}
		_, err := ReadFrame(bytes.NewReader(raw), MaxPayload)
		if !errors.Is(err, ErrUnknownKind) {
			t.Errorf("kind %d: err = %v, want ErrUnknownKind", kind, err)
		}
	}
	// And it is refused on the way out too, so a bug here cannot put a frame
	// on the wire that the phone is obliged to treat as fatal.
	if _, err := Encode(Frame{Kind: Kind(9)}); !errors.Is(err, ErrUnknownKind) {
		t.Errorf("Encode(kind 9): err = %v, want ErrUnknownKind", err)
	}
	if err := WriteFrame(io.Discard, Frame{Kind: Kind(9)}); !errors.Is(err, ErrUnknownKind) {
		t.Errorf("WriteFrame(kind 9): err = %v, want ErrUnknownKind", err)
	}
}

// TestPayloadLimit covers the narrowed ceiling an unverified connection gets.
// Before the limit existed, a peer that had proved nothing could declare 64 MB
// and feed it a byte at a time; twenty such connections were a gigabyte of
// buffers, no pairing code required.
func TestPayloadLimit(t *testing.T) {
	// A header declaring 64 MiB, with no payload behind it. The point is that
	// the reader refuses before allocating, so the body never has to exist.
	header := []byte{byte(KindNDJSON), 0x04, 0x00, 0x00, 0x00}
	_, err := ReadFrame(bytes.NewReader(header), HelloPayloadLimit)
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Errorf("oversize under hello limit: err = %v, want ErrPayloadTooLarge", err)
	}

	// Exactly at the limit is allowed; one over is not.
	atLimit := make([]byte, HelloPayloadLimit)
	frame, err := Encode(Frame{Kind: KindNDJSON, Payload: atLimit})
	if err != nil {
		t.Fatalf("Encode at limit: %v", err)
	}
	if _, err := ReadFrame(bytes.NewReader(frame), HelloPayloadLimit); err != nil {
		t.Errorf("payload exactly at the limit was rejected: %v", err)
	}
	over, err := Encode(Frame{Kind: KindNDJSON, Payload: make([]byte, HelloPayloadLimit+1)})
	if err != nil {
		t.Fatalf("Encode over limit: %v", err)
	}
	if _, err := ReadFrame(bytes.NewReader(over), HelloPayloadLimit); !errors.Is(err, ErrPayloadTooLarge) {
		t.Errorf("one byte over the limit: err = %v, want ErrPayloadTooLarge", err)
	}
}

// TestEOFSemantics separates a peer that closed cleanly from one that vanished.
// Callers act differently: a clean close ends the session, a truncated frame is
// an error worth reporting, and collapsing the two loses that.
func TestEOFSemantics(t *testing.T) {
	t.Run("clean boundary", func(t *testing.T) {
		if _, err := ReadFrame(bytes.NewReader(nil), MaxPayload); !errors.Is(err, io.EOF) {
			t.Errorf("empty stream: err = %v, want io.EOF", err)
		}
	})
	t.Run("truncated header", func(t *testing.T) {
		_, err := ReadFrame(bytes.NewReader([]byte{0, 0}), MaxPayload)
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("partial header: err = %v, want io.ErrUnexpectedEOF", err)
		}
	})
	t.Run("truncated payload", func(t *testing.T) {
		full, err := Encode(Frame{Kind: KindStill, Payload: bytes.Repeat([]byte{7}, 100)})
		if err != nil {
			t.Fatal(err)
		}
		_, err = ReadFrame(bytes.NewReader(full[:len(full)-1]), MaxPayload)
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("payload one byte short: err = %v, want io.ErrUnexpectedEOF", err)
		}
	})
}

// TestZeroLengthPayload — an empty ndjson frame is legal and must not be
// mistaken for a closed stream.
func TestZeroLengthPayload(t *testing.T) {
	raw, err := Encode(Frame{Kind: KindNDJSON})
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) != HeaderSize {
		t.Fatalf("empty frame is %d bytes, want %d", len(raw), HeaderSize)
	}
	got, err := ReadFrame(bytes.NewReader(raw), MaxPayload)
	if err != nil {
		t.Fatalf("reading empty frame: %v", err)
	}
	if got.Kind != KindNDJSON || len(got.Payload) != 0 {
		t.Errorf("got %v/%d bytes, want ndjson/0", got.Kind, len(got.Payload))
	}
}

// TestRoundTrip is the property the vectors check at fixed sizes, generalised
// across the lengths where the four-byte header changes shape.
func TestRoundTrip(t *testing.T) {
	sizes := []int{0, 1, 127, 128, 255, 256, 65535, 65536, 100000}
	for _, kind := range []Kind{KindNDJSON, KindPreview, KindStill, KindTrace} {
		for _, n := range sizes {
			payload := make([]byte, n)
			for i := range payload {
				payload[i] = byte(i)
			}
			raw, err := Encode(Frame{Kind: kind, Payload: payload})
			if err != nil {
				t.Fatalf("%v/%d: Encode: %v", kind, n, err)
			}
			got, err := ReadFrame(bytes.NewReader(raw), MaxPayload)
			if err != nil {
				t.Fatalf("%v/%d: ReadFrame: %v", kind, n, err)
			}
			if got.Kind != kind || !bytes.Equal(got.Payload, payload) {
				t.Errorf("%v/%d: round trip changed the frame", kind, n)
			}
		}
	}
}

// TestTextIsNotACopyHazard — Text is used on the ndjson and trace paths, and
// those payloads are handed straight to a JSON decoder.
func TestText(t *testing.T) {
	f := Frame{Kind: KindNDJSON, Payload: []byte(`{"event":"ready"}`)}
	if got := f.Text(); got != `{"event":"ready"}` {
		t.Errorf("Text() = %q", got)
	}
	if got := (Frame{Kind: KindNDJSON}).Text(); got != "" {
		t.Errorf("empty payload Text() = %q, want empty", got)
	}
}
