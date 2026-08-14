package link

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

type Kind uint8

const (
	KindNDJSON Kind = 0

	KindPreview Kind = 1

	KindStill Kind = 2

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

func (k Kind) valid() bool { return k <= KindTrace }

const HeaderSize = 5

const MaxPayload = 64 * 1024 * 1024

const HelloPayloadLimit = 4096

var (
	ErrUnknownKind = errors.New("link: unknown frame kind")

	ErrPayloadTooLarge = errors.New("link: frame payload too large")
)

type Frame struct {
	Kind    Kind
	Payload []byte
}

func (f Frame) Text() string { return string(f.Payload) }

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

	if len(f.Payload) > 0 {
		if _, err := w.Write(f.Payload); err != nil {
			return err
		}
	}
	return nil
}

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

		if errors.Is(err, io.EOF) {
			return Frame{}, io.ErrUnexpectedEOF
		}
		return Frame{}, err
	}
	return Frame{Kind: kind, Payload: payload}, nil
}
