package command

// Camera glue for `hoard add --scan`: choosing a phone and remembering the
// pairing, so the second scan of a session asks nothing.

import (
	"context"
	"path/filepath"
	"time"

	"github.com/spiffcs/hoard/internal/scan"
	"github.com/spiffcs/hoard/internal/tui"
)

// linkScanner drives the link to the phone for the add TUI's scan action
// (ctrl+o). Its calls return errors that the TUI surfaces as a banner, so a
// session continues when the phone is missing rather than ending.
type linkScanner struct{}

// client builds the scan client, rooted where the rest of hoard's per-user
// data lives — beside scan.json rather than in the keychain. The trade is
// argued in docs/specs/scan-transport-port.md §6; the short version is that
// this directory already holds hoard's other per-user state, and a file keeps
// the identity portable and off the Security framework.
//
// An error here means there is no data directory at all, which is a condition
// every other command already fails on, so it is folded into the call rather
// than surfaced separately.
func client() (*scan.Client, error) {
	dir, err := dataDir()
	if err != nil {
		return nil, err
	}
	return scan.NewClient(filepath.Join(dir, "hoard")), nil
}

func (linkScanner) Devices(ctx context.Context) ([]scan.Device, error) {
	c, err := client()
	if err != nil {
		return nil, err
	}
	return c.Devices(ctx)
}

// Pair checks a code against the phone, then records the phone as trusted.
//
// Verified before saving, deliberately: a pairing that has never been tested is
// a pairing that might be a typo, and the typo would otherwise surface at the
// start of the next scanning session as a link that never comes up. Better to
// fail here, while the user is still looking at the six digits.
//
// What gets saved is the phone's certificate fingerprint, not the code. Under
// trust on first use the code authorises exactly one exchange — the first — and
// the phone burns it immediately after, so storing it would be keeping a secret
// that is already spent. This is why there is no longer a codes map in
// scan.json: the pin set is the pairing record.
func (linkScanner) Pair(deviceID, code string) error {
	c, err := client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), pairTimeout)
	defer cancel()
	return c.Pair(ctx, deviceID, code)
}

// pairTimeout bounds the check from this side. The link has its own shorter
// deadline; this is the backstop for one that hangs rather than answers.
const pairTimeout = 30 * time.Second

func (linkScanner) Open(ctx context.Context, deviceID string) (tui.ScanSession, error) {
	c, err := client()
	if err != nil {
		return nil, err
	}
	// No pairing code: a phone that reached this point is already pinned, and
	// a pinned peer skips the proof entirely.
	s, err := c.Open(ctx, scan.OpenOptions{DeviceID: deviceID})
	if err != nil {
		return nil, err
	}
	return s, nil
}
