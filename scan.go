package main

// Camera glue for `hoard add --scan`: choosing a device and remembering the
// choice, so the second scan of a session asks nothing.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/spiffcs/hoard/internal/scan"
	"github.com/spiffcs/hoard/internal/tui"
)

// helperScanner drives the native camera helper for the add TUI's scan action
// (ctrl+o). On platforms without the helper its calls return errors that the TUI
// surfaces as a banner, so the session continues.
type helperScanner struct{}

func (helperScanner) Devices(ctx context.Context) ([]scan.Device, error) {
	devices, err := scan.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	// An iPhone-app source needs a code the first time it is used, and never
	// again. Marked here rather than in the helper because whether a phone is
	// already paired is a fact about this machine.
	prefs := loadScanPrefs()
	for i, d := range devices {
		if d.Kind == scan.KindRemote && prefs.Codes[d.ID] == "" {
			devices[i].NeedsPairing = true
		}
	}
	return devices, nil
}

// Pair checks a code against the phone, then remembers it.
//
// Verified before saving, deliberately: a pairing that has never been tested is
// a pairing that might be a typo, and the typo would otherwise surface at the
// start of the next scanning session as a link that never comes up. Better to
// fail here, while the user is still looking at the six digits.
func (helperScanner) Pair(deviceID, code string) error {
	ctx, cancel := context.WithTimeout(context.Background(), pairTimeout)
	defer cancel()
	if err := scan.VerifyPairing(ctx, deviceID, code); err != nil {
		return err
	}
	rememberPairingCode(deviceID, code)
	return nil
}

// pairTimeout bounds the check. The helper has its own shorter deadline; this
// is the backstop for a helper that hangs rather than answers.
const pairTimeout = 20 * time.Second

func (helperScanner) Open(ctx context.Context, deviceID string) (tui.ScanSession, error) {
	// The preview rotation the user last settled on is replayed into the helper,
	// so a phone that previews sideways is corrected once rather than every run.
	prefs := loadScanPrefs()
	s, err := scan.Open(ctx, scan.OpenOptions{
		DeviceID: deviceID,
		Rotation: prefs.Rotation,
		// Empty for a Continuity Camera, which is what makes this one call
		// serve both backends: the helper opens a local camera when there is no
		// code and translates for a phone when there is.
		PairingCode: prefs.Codes[deviceID],
	})
	if err != nil {
		return nil, err
	}
	return &persistingSession{Session: s, rotation: prefs.Rotation}, nil
}

// rememberPairingCode saves a code for a device, so the prompt is a
// once-per-phone event rather than a once-per-session one.
func rememberPairingCode(deviceID, code string) {
	prefs := loadScanPrefs()
	if prefs.Codes == nil {
		prefs.Codes = map[string]string{}
	}
	prefs.Codes[deviceID] = code
	saveScanPrefs(prefs)
}

// persistingSession watches a live session's events for rotation changes and
// saves the last one, so a correction made mid-session survives into the next
// run. It sits here rather than in the TUI because writing preference files
// isn't the TUI's job.
type persistingSession struct {
	*scan.Session
	events   chan scan.Event
	once     sync.Once
	rotation int
}

func (p *persistingSession) Events() <-chan scan.Event {
	p.once.Do(func() {
		p.events = make(chan scan.Event, 8)
		go func() {
			defer close(p.events)
			for ev := range p.Session.Events() {
				if ev.Kind == scan.EventRotation || ev.Kind == scan.EventClosed {
					if ev.Rotation != p.rotation {
						p.rotation = ev.Rotation
						// Read-modify-write, not a fresh struct: prefs now
						// carries pairing codes too, and replacing the whole
						// file to record a rotation would silently unpair
						// every phone the moment the preview was turned.
						prefs := loadScanPrefs()
						prefs.Rotation = ev.Rotation
						saveScanPrefs(prefs)
					}
				}
				p.events <- ev
			}
		}()
	})
	return p.events
}

// Close shuts the session down through the forwarding goroutine rather than
// through Session.Close, whose drain would race that goroutine for the same
// channel — and a final rotation correction swallowed by the drain would never
// reach the preferences file.
func (p *persistingSession) Close() error {
	if p.events == nil {
		// Events was never called, so there is no forwarder to protect.
		return p.Session.Close()
	}
	err := p.Session.Shutdown()
	// Consume what the forwarder relays until it sees the session's channel
	// close and exits. This both keeps it from blocking on a full buffer and
	// guarantees every last event passed through the rotation check above.
	for range p.events { //nolint:revive // draining
	}
	return err
}

// scanPrefs holds the small amount of scan state worth surviving between runs.
type scanPrefs struct {
	// Rotation is extra clockwise preview rotation in degrees (0/90/180/270).
	Rotation int `json:"rotation"`
	// Codes are pairing codes for iPhone-app sources, keyed by device id.
	//
	// Kept per device rather than globally: two phones are two pairings, and a
	// single code would silently stop working the moment a second one appeared.
	// Absent means "never paired with this one", which is what makes the prompt
	// a once-per-phone event rather than a once-per-session one.
	Codes map[string]string `json:"codes,omitempty"`
}

// defaultScanRotation corrects the sideways preview a portrait-held iPhone
// produces out of the box. Continuity Camera hands over a landscape frame and
// the rotation coordinator often can't tell how the phone is being held, so the
// image arrives turned a quarter-turn counter-clockwise; this turns it back.
// Overridden the moment the user adjusts it with ←/→ in the capture window.
const defaultScanRotation = 90

// scanPrefsPath is where scan preferences live — beside the database, so they
// follow the same per-user location.
func scanPrefsPath() (string, error) {
	dir, err := dataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hoard", "scan.json"), nil
}

// loadScanPrefs reads saved scan preferences, falling back to the defaults if
// they're missing or unreadable — a preferences file is never worth failing a
// scan over. A saved rotation of 0 is honoured; only an absent file gets the
// default.
func loadScanPrefs() scanPrefs {
	p := scanPrefs{Rotation: defaultScanRotation}
	path, err := scanPrefsPath()
	if err != nil {
		return p
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return p
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return scanPrefs{Rotation: defaultScanRotation}
	}
	return p
}

// saveScanPrefs persists scan preferences, ignoring failures for the same reason
// loadScanPrefs ignores them.
func saveScanPrefs(p scanPrefs) {
	path, err := scanPrefsPath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(p)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}
