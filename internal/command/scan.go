package command

// Camera glue for `hoard add --scan`: choosing a phone and remembering the
// pairing, so the second scan of a session asks nothing.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/spiffcs/hoard/internal/scan"
	"github.com/spiffcs/hoard/internal/tui"
)

// helperScanner drives the native scan helper for the add TUI's scan action
// (ctrl+o). On platforms without the helper its calls return errors that the TUI
// surfaces as a banner, so the session continues.
type helperScanner struct{}

func (helperScanner) Devices(ctx context.Context) ([]scan.Device, error) {
	devices, err := scan.ListDevices(ctx)
	if err != nil {
		return nil, err
	}
	// A phone needs a code the first time it is used, and never again. Marked
	// here rather than in the helper because whether a phone is already paired
	// is a fact about this machine.
	prefs := loadScanPrefs()
	for i, d := range devices {
		if prefs.Codes[d.ID] == "" {
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
	// The code for this phone, saved by Pair. An empty one reaches the helper
	// as a missing --code and fails there with a message about the Pair tab,
	// which is the right place to say it — a device that got this far was
	// listed without NeedsPairing, so an empty code here means the prefs file
	// lost it rather than that the user skipped a step.
	prefs := loadScanPrefs()
	s, err := scan.Open(ctx, scan.OpenOptions{
		DeviceID:    deviceID,
		PairingCode: prefs.Codes[deviceID],
	})
	if err != nil {
		return nil, err
	}
	return s, nil
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

// scanPrefs holds the small amount of scan state worth surviving between runs.
//
// It used to carry a preview rotation as well. That existed because Continuity
// Camera handed the Mac a landscape frame it had to turn upright; the phone
// reads its own already-upright frame, so there is nothing left to correct. A
// `rotation` still sitting in an older scan.json is simply ignored.
type scanPrefs struct {
	// Codes are pairing codes for phones, keyed by device id.
	//
	// Kept per device rather than globally: two phones are two pairings, and a
	// single code would silently stop working the moment a second one appeared.
	// Absent means "never paired with this one", which is what makes the prompt
	// a once-per-phone event rather than a once-per-session one.
	Codes map[string]string `json:"codes,omitempty"`
}

// scanPrefsPath is where scan preferences live — beside the database, so they
// follow the same per-user location.
func scanPrefsPath() (string, error) {
	dir, err := dataDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hoard", "scan.json"), nil
}

// loadScanPrefs reads saved scan preferences, falling back to an empty set if
// they're missing or unreadable — a preferences file is never worth failing a
// scan over. An unreadable file reads as "no pairings", which costs a re-pair
// and never silently uses the wrong code.
func loadScanPrefs() scanPrefs {
	var p scanPrefs
	path, err := scanPrefsPath()
	if err != nil {
		return p
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return p
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return scanPrefs{}
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
