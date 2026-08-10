package scan

// Finding and pairing with the phone.
//
// This replaces the one-shot helper invocations (`--list-devices`, `--verify`)
// that used to answer these questions by running a subprocess and reading its
// stdout.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spiffcs/hoard/internal/scan/link"
)

// identityCommonName is what this Mac's certificate calls itself. It travels
// in the clear on every handshake, which is why it is a fixed service string
// and never the machine's name.
const identityCommonName = "dev.spiffcs.hoard.scan.mac"

// browseWindow is how long to look for phones before giving up. Bonjour on a
// local network answers in milliseconds, and discovery returns as soon as it
// has its answer, so this is the ceiling a phone that never advertises costs
// — not what a browse takes.
const browseWindow = 2500 * time.Millisecond

// resolveWindow is how long to turn a chosen phone into an address. Same
// shape as browseWindow: the answer arrives in single-digit milliseconds, and
// this bounds the case where it never arrives at all.
const resolveWindow = 4 * time.Second

// pairWindow bounds a pairing attempt: connect, wait for the phone to accept
// the code, pin it. Long enough for a handshake on a slow network, short
// enough that a wrong code fails while the user is still looking at the screen.
const pairWindow = 15 * time.Second

// Client is this machine's end of the link.
//
// StateDir holds the two files that make this machine a peer: its certificate
// and the set of phones it trusts. Both are supplied by the caller rather than
// discovered here, so this package never has to know where a hoard install
// keeps its data.
type Client struct {
	StateDir string
	// Finder is discovery. Nil means dns-sd, which is the only implementation
	// and the one docs/specs/scan-transport-port.md §8.2 selected.
	Finder link.Finder
}

// NewClient returns a client keeping its identity and pins under stateDir.
func NewClient(stateDir string) *Client { return &Client{StateDir: stateDir} }

func (c *Client) finder() link.Finder {
	if c.Finder != nil {
		return c.Finder
	}
	return link.DNSSD{}
}

func (c *Client) identityPath() string { return filepath.Join(c.StateDir, "link-identity.pem") }
func (c *Client) pinsPath() string     { return filepath.Join(c.StateDir, "link-pins.json") }

func (c *Client) pins() *link.PinStore { return link.NewPinStore(c.pinsPath()) }

func (c *Client) identity() (*link.Identity, error) {
	return link.LoadOrCreateIdentity(c.identityPath(), identityCommonName)
}

// Devices lists the phones running Hoardling on this network.
//
// NeedsPairing is decided here rather than by the phone, because whether a
// pairing exists is a fact about this machine. It is read from the pin set:
// under trust on first use, "paired" means "this machine holds that phone's
// certificate fingerprint", which is the thing that actually governs whether a
// session can open.
func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	// No name: this is the enumerating browse, and it has to hear from every
	// phone rather than the first one.
	services, err := c.finder().Browse(ctx, "", browseWindow)
	if err != nil {
		return nil, friendly(err)
	}
	paired := map[string]bool{}
	for _, name := range c.pins().Names() {
		paired[name] = true
	}
	out := make([]Device, 0, len(services))
	for _, s := range services {
		out = append(out, Device{
			ID:           s.Name,
			Name:         s.Name,
			Kind:         KindRemote,
			NeedsPairing: !paired[s.Name],
		})
	}
	return out, nil
}

// Pair checks a code against the phone and, if it holds, records the phone as
// trusted.
//
// Verified before saving, deliberately: a pairing that has never been tested is
// a pairing that might be a typo, and the typo would otherwise surface at the
// start of the next scanning session as a link that never comes up.
//
// The check is the phone's ready event. The phone only sends it after the
// pairing proof passes, so seeing it *is* the verification.
func (c *Client) Pair(ctx context.Context, deviceID, code string) error {
	parsed, err := link.ParseCode(code)
	if err != nil {
		return errors.New("a pairing code is the six digits shown on Hoardling's Pair tab")
	}
	id, err := c.identity()
	if err != nil {
		return err
	}
	pins := c.pins()

	svc, err := c.locate(ctx, deviceID)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, pairWindow)
	defer cancel()

	// The pairing window: a certificate this machine has never seen may
	// complete TLS, because the gate in this moment is the proof on the hello,
	// not the pinning.
	session, err := link.Dial(ctx, svc, parsed, link.TrustPairing(id, pins))
	if err != nil {
		return friendly(err)
	}
	defer session.Close()

	if err := awaitReady(ctx, session); err != nil {
		return err
	}

	// Trust on first use, from this side. The phone pinned this machine when
	// it accepted the proof; this is the matching half, and it is done only
	// after ready proves the pairing completed rather than merely that a
	// certificate was seen.
	return pins.Pin(session.PeerFingerprint(), svc.Name)
}

// Open starts a capture session against a paired phone.
func (c *Client) Open(ctx context.Context, opts OpenOptions) (*Session, error) {
	id, err := c.identity()
	if err != nil {
		return nil, err
	}
	pins := c.pins()
	if len(pins.All()) == 0 {
		return nil, errors.New(
			"this machine has not paired with a phone yet. Open Hoardling's Pair tab and enter the six digits")
	}

	svc, err := c.locate(ctx, opts.DeviceID)
	if err != nil {
		return nil, err
	}

	// A code is not required once a phone is pinned — the phone skips the
	// proof for a peer it already knows, which is what lets it rotate the code
	// without breaking existing pairings. An empty one is therefore normal
	// here, not a missing argument.
	code, _ := link.ParseCode(opts.PairingCode)

	session, err := link.Dial(ctx, svc, code, link.TrustStore(id, pins))
	if err != nil {
		return nil, friendly(err)
	}

	s := &Session{link: session, events: make(chan Event, 8)}
	// HOARD_SCAN_LOG tees the session's traffic, timestamped, to a file — the
	// only way to watch the live feed while the TUI owns the output. Append
	// mode, so one file collects a whole tuning session across camera reopens.
	if path := os.Getenv("HOARD_SCAN_LOG"); path != "" {
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			s.log = f
		}
	}
	go s.pump(ctx)
	return s, nil
}

// locate finds a phone by id, or the only one on the network when id is empty,
// and resolves it to an address.
//
// Always a fresh resolve, never a cached address: iOS rotates its private
// .local hostname and takes a new ephemeral port on every launch, so an address
// from an earlier session is stale as soon as the app restarts. Measured — see
// docs/specs/scan-transport-port.md §9.
func (c *Client) locate(ctx context.Context, deviceID string) (link.Service, error) {
	// Named when the caller knows which phone, which is the common case and
	// the fast one: the browse ends the moment that phone answers instead of
	// serving out a window whose only remaining purpose would be to hear
	// about phones this call is going to discard.
	services, err := c.finder().Browse(ctx, deviceID, browseWindow)
	if err != nil {
		return link.Service{}, friendly(err)
	}
	chosen := services[0]
	if deviceID != "" {
		found := false
		for _, s := range services {
			if s.Name == deviceID {
				chosen, found = s, true
				break
			}
		}
		if !found {
			return link.Service{}, fmt.Errorf(
				"%q is not on this network right now. Open Hoardling and check both devices share a Wi-Fi network, or a cable",
				deviceID)
		}
	}
	svc, err := c.finder().Resolve(ctx, chosen.Name, resolveWindow)
	if err != nil {
		return link.Service{}, friendly(err)
	}
	return svc, nil
}

// awaitReady waits for the phone to declare its camera up.
func awaitReady(ctx context.Context, session *link.Session) error {
	for {
		select {
		case frame, ok := <-session.Frames():
			if !ok {
				if err := session.Err(); err != nil {
					return friendly(err)
				}
				return errors.New("the phone closed the link before it was ready")
			}
			if frame.Kind != link.KindNDJSON {
				continue
			}
			ev, err := parseEvent(frame.Payload)
			if err != nil {
				continue
			}
			switch ev.Kind {
			case EventReady:
				return nil
			case EventError:
				return errors.New(ev.Message)
			}
		case <-ctx.Done():
			return errors.New(
				"the phone did not accept that code. Check the digits on its Pair tab")
		}
	}
}

// friendly turns a link error into something a person at a terminal can act on.
//
// The raw text is genuinely unhelpful in the place it gets shown: a phone that
// backgrounded produces an errno and the word "software", and names neither the
// iPhone, the app, nor the one thing that fixes it.
func friendly(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, link.ErrNotFound):
		return errors.New(
			"no iPhone running Hoardling was found on this network. Open the app on your phone, and make sure both devices are on the same Wi-Fi (or the phone is plugged in)")
	case errors.Is(err, link.ErrPeerNotPinned):
		return errors.New(
			"that phone is not paired with this machine. Run the pairing step and enter the six digits from Hoardling's Pair tab")
	case errors.Is(err, link.ErrNoDNSSD):
		return fmt.Errorf("card scanning needs macOS's dns-sd to find the phone: %w", err)
	}
	return err
}

// marshalHUD encodes a HUD result compactly; the command protocol is
// line-based, so the JSON must not contain newlines.
func marshalHUD(r HUDResult) ([]byte, error) { return json.Marshal(r) }

func parseInt(s string) (int, error) { return strconv.Atoi(s) }

func parseFloat(s string) (float64, error) { return strconv.ParseFloat(s, 64) }
