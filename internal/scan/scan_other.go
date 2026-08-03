//go:build !darwin

package scan

import "context"

// Session exists off macOS only so callers compile; it is never constructed.
type Session struct{}

func (*Session) Events() <-chan Event   { return nil }
func (*Session) Capture() error         { return ErrUnsupported }
func (*Session) Rotate(bool) error      { return ErrUnsupported }
func (*Session) Auto(bool) error        { return ErrUnsupported }
func (*Session) AutoFraming(bool) error { return ErrUnsupported }
func (*Session) Torch(bool) error       { return ErrUnsupported }
func (*Session) VideoEffects() error    { return ErrUnsupported }
func (*Session) Note(string)            {}
func (*Session) Rearm() error           { return ErrUnsupported }
func (*Session) Chime() error           { return ErrUnsupported }
func (*Session) Result(HUDResult) error { return ErrUnsupported }
func (*Session) Shutdown() error        { return nil }
func (*Session) Close() error           { return nil }

// Open is unsupported off macOS; the camera+OCR helper is macOS-only.
func Open(context.Context, string, int) (*Session, error) {
	return nil, ErrUnsupported
}

// ListDevices is unsupported off macOS, for the same reason as Open.
func ListDevices(context.Context) ([]Device, error) {
	return nil, ErrUnsupported
}
