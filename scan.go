package main

// Camera glue for `hoard add --scan`: choosing a device and remembering the
// choice, so the second scan of a session asks nothing.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/spiffcs/hoard/internal/scan"
	"github.com/spiffcs/hoard/internal/tui"
)

// helperScanner drives the native camera helper for the add TUI's scan action
// (ctrl+o). On platforms without the helper its calls return errors that the TUI
// surfaces as a banner, so the session continues.
type helperScanner struct{}

func (helperScanner) Devices(ctx context.Context) ([]scan.Device, error) {
	return scan.ListDevices(ctx)
}

func (helperScanner) Open(ctx context.Context, deviceID string) (tui.ScanSession, error) {
	// The preview rotation the user last settled on is replayed into the helper,
	// so a phone that previews sideways is corrected once rather than every run.
	prefs := loadScanPrefs()
	s, err := scan.Open(ctx, deviceID, prefs.Rotation)
	if err != nil {
		return nil, err
	}
	return &persistingSession{Session: s, rotation: prefs.Rotation}, nil
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
						saveScanPrefs(scanPrefs{Rotation: ev.Rotation})
					}
				}
				p.events <- ev
			}
		}()
	})
	return p.events
}

// scanPrefs holds the small amount of scan state worth surviving between runs.
type scanPrefs struct {
	// Rotation is extra clockwise preview rotation in degrees (0/90/180/270).
	Rotation int `json:"rotation"`
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
