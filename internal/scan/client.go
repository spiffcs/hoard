package scan

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

const identityCommonName = "dev.spiffcs.hoard.scan.mac"

const browseWindow = 2500 * time.Millisecond

const resolveWindow = 4 * time.Second

const pairWindow = 15 * time.Second

type Client struct {
	StateDir string

	Finder link.Finder
}

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

var ErrNotPaired = link.ErrPeerNotPinned

func (c *Client) Devices(ctx context.Context) ([]Device, error) {

	services, err := c.finder().Browse(ctx, "", browseWindow)
	if err != nil {
		return nil, friendly(err)
	}
	out := make([]Device, 0, len(services))
	for _, s := range services {
		out = append(out, Device{ID: s.Name, Name: s.Name, Kind: KindRemote})
	}
	return out, nil
}

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

	session, err := link.Dial(ctx, svc, parsed, link.TrustPairing(id, pins))
	if err != nil {
		return friendly(err)
	}
	defer session.Close()

	if err := awaitReady(ctx, session); err != nil {
		return err
	}

	return pins.Pin(session.PeerFingerprint(), svc.Name)
}

func (c *Client) Open(ctx context.Context, opts OpenOptions) (*Session, error) {
	id, err := c.identity()
	if err != nil {
		return nil, err
	}
	pins := c.pins()
	if len(pins.All()) == 0 {

		return nil, &friendlyError{err: ErrNotPaired, msg: "" +
			"this machine has not paired with a phone yet. Open Hoardling's Pair tab and enter the six digits"}
	}

	svc, err := c.locate(ctx, opts.DeviceID)
	if err != nil {
		return nil, err
	}

	code, _ := link.ParseCode(opts.PairingCode)

	session, err := link.Dial(ctx, svc, code, link.TrustStore(id, pins))
	if err != nil {
		return nil, friendly(err)
	}

	_ = pins.Rename(session.PeerFingerprint(), svc.Name)

	s := &Session{link: session, events: make(chan Event, 8)}

	if path := os.Getenv("HOARD_SCAN_LOG"); path != "" {
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			s.log = f
		}
	}
	go s.pump(ctx)
	return s, nil
}

func (c *Client) locate(ctx context.Context, deviceID string) (link.Service, error) {

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

type friendlyError struct {
	msg string
	err error
}

func (e *friendlyError) Error() string { return e.msg }
func (e *friendlyError) Unwrap() error { return e.err }

func friendly(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, link.ErrNotFound):
		return &friendlyError{err: err, msg: "" +
			"no iPhone running Hoardling was found on this network. Open the app on your phone, and make sure both devices are on the same Wi-Fi (or the phone is plugged in)"}
	case errors.Is(err, link.ErrPeerNotPinned):
		return &friendlyError{err: err, msg: "" +
			"that phone is not paired with this machine. Run the pairing step and enter the six digits from Hoardling's Pair tab"}
	case errors.Is(err, link.ErrNoDNSSD):
		return fmt.Errorf("card scanning needs macOS's dns-sd to find the phone: %w", err)
	}
	return err
}

func marshalHUD(r HUDResult) ([]byte, error) { return json.Marshal(r) }

func parseInt(s string) (int, error) { return strconv.Atoi(s) }

func parseFloat(s string) (float64, error) { return strconv.ParseFloat(s, 64) }
