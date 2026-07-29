// Package scan reads Magic card names from a camera via an external,
// platform-native helper (macOS: Continuity Camera + Vision OCR).
//
// A scan is a *session*, not a one-shot: Open leaves the camera window up so a
// run of cards can be captured one after another. The Go side stays
// cross-platform, with stubs on platforms that have no helper.
package scan

import (
	"encoding/json"
	"errors"
)

// Event kinds emitted by a live capture session.
const (
	EventReady    = "ready"    // window is up and previewing
	EventScan     = "scan"     // a capture was taken and read
	EventRotation = "rotation" // the user turned the preview
	EventError    = "error"    // capture failed; the session is still alive
	EventClosed   = "closed"   // the window closed; the session is over
)

// Event is one message from a capture session.
type Event struct {
	Kind string `json:"event"`
	// Name is the best guess at the card name; Candidates holds it plus the
	// other lines read, best guess first. Set on EventScan.
	Name       string   `json:"name"`
	Candidates []string `json:"candidates"`
	// Rotation is the manual preview correction currently in effect. Persist it
	// and pass it back to Open so the correction sticks between runs.
	Rotation int    `json:"rotation"`
	Message  string `json:"message"`
	Device   string `json:"device"`
}

// Lines returns the OCR'd text of a scan event, best guess first, falling back
// to Name when the helper reported no candidate list.
func (e Event) Lines() []string {
	if len(e.Candidates) > 0 {
		return e.Candidates
	}
	if e.Name != "" {
		return []string{e.Name}
	}
	return nil
}

// parseEvent decodes one newline-delimited JSON event from the helper.
func parseEvent(data []byte) (Event, error) {
	var e Event
	if err := json.Unmarshal(data, &e); err != nil {
		return Event{}, err
	}
	if e.Kind == "" {
		return Event{}, errors.New("event has no kind")
	}
	return e, nil
}

// Device is a camera the helper can capture from. Kind is a short human tag
// ("iPhone", "built-in", "external") for telling similar names apart.
type Device struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

var (
	// ErrUnsupported is returned on platforms without a scan helper.
	ErrUnsupported = errors.New("card scanning is only available on macOS builds")
	// ErrHelperMissing means the native helper binary was not found.
	ErrHelperMissing = errors.New("hoard-scan helper not found; build it with ./build-scan.sh")
)

// deviceList mirrors the JSON the helper prints for --list-devices.
type deviceList struct {
	Devices []Device `json:"devices"`
}

// parseDevices turns the helper's --list-devices JSON into a device slice.
func parseDevices(data []byte) ([]Device, error) {
	var dl deviceList
	if err := json.Unmarshal(data, &dl); err != nil {
		return nil, err
	}
	return dl.Devices, nil
}
