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
	// CollectorNumber and SetCode come from the card's bottom border, when it has
	// one. Cards printed before Exodus (1998) carry no collector number at all,
	// and only the M15 frame (2014) reliably prints the set code beside it, so
	// both are routinely empty. Treat an empty read as ordinary, not as failure.
	CollectorNumber string `json:"collectorNumber"`
	SetCode         string `json:"setCode"`
	// BottomLines is the raw text of the bottom band, for debugging a bad read.
	BottomLines []string `json:"bottomLines"`
	// Cards is every card the capture found, in reading order — a fanned
	// spread yields one entry per card. Empty from helpers that predate
	// multi-card scanning; use CardList, which falls back to the flat fields.
	Cards []Card `json:"cards"`
}

// Card is one card of a capture. Name and Candidates mirror the event's flat
// fields; CollectorNumber and SetCode are set only when the helper could read
// them off this card specifically — keeping the pairing per card is the point,
// since pooling a frame's reads once matched one card's name with another
// card's printing.
type Card struct {
	Name            string   `json:"name"`
	Candidates      []string `json:"candidates"`
	CollectorNumber string   `json:"collectorNumber"`
	SetCode         string   `json:"setCode"`
}

// Lines returns the card's OCR'd text, best guess first, falling back to Name
// when there is no candidate list.
func (c Card) Lines() []string {
	if len(c.Candidates) > 0 {
		return c.Candidates
	}
	if c.Name != "" {
		return []string{c.Name}
	}
	return nil
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

// CardList returns the capture's cards. A helper too old to report a card
// list is not an error: its flat fields describe exactly one card, so they
// become a list of one — the compatibility seam in a single place.
func (e Event) CardList() []Card {
	if len(e.Cards) > 0 {
		return e.Cards
	}
	if len(e.Lines()) == 0 {
		return nil
	}
	return []Card{{
		Name:            e.Name,
		Candidates:      e.Candidates,
		CollectorNumber: e.CollectorNumber,
		SetCode:         e.SetCode,
	}}
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
