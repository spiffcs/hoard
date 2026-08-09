package scan

// A capture session, carried over the Go-native link rather than a helper
// process.
//
// What used to happen here: exec a Swift .app, write verbs to its stdin, scan
// JSON lines off its stdout. What happens now: two TLS connections straight to
// the phone, carrying the same NDJSON inside frames. The Event surface above
// this file is unchanged on purpose — the TUI, autoscan, review and auto-commit
// paths were never the problem and do not move. See
// docs/specs/scan-transport-port.md.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spiffcs/hoard/internal/scan/link"
)

// Session is a running capture session: one link to the phone, held open
// across many captures. Commands go in, Events come out, until Close (or the
// phone dropping off) ends it.
//
// The link is long-lived on purpose. Opening one per card costs a browse, a
// handshake and a camera warm-up every time, and makes the user re-trigger it
// from the terminal between cards.
type Session struct {
	link *link.Session

	events chan Event

	// log receives a timestamped copy of the session's traffic when
	// HOARD_SCAN_LOG names a file — the telemetry tap for tuning sessions
	// where the TUI, not a terminal, owns the output.
	//
	// logMu serializes the parties that touch the file: the pump goroutine,
	// the Bubble Tea goroutine (Note), and pump's close. Close nils the
	// handle under the lock so a late writer no-ops instead of writing into a
	// closed fd.
	logMu sync.Mutex
	log   *os.File

	mu     sync.Mutex
	closed bool
}

// logLine appends one timestamped line to the telemetry log, if one is open.
// The prefixes carry over from the subprocess era so old and new session logs
// read the same: "<" for events from the phone, "!" for its trace lines, "~"
// for this side's own notes.
func (s *Session) logLine(prefix string, line []byte) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	if s.log == nil {
		return
	}
	fmt.Fprintf(s.log, "%s %s %s\n", time.Now().Format("15:04:05.000"), prefix, line)
}

func (s *Session) closeLog() {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	if s.log != nil {
		_ = s.log.Close()
		s.log = nil
	}
}

// Events returns the stream of events from the phone. It is closed when the
// session ends, for any reason.
func (s *Session) Events() <-chan Event { return s.events }

// Capture asks for a photo. The result arrives later as an EventScan.
func (s *Session) Capture() error { return s.send("capture") }

// Auto turns the phone's hands-free capture trigger on or off. Callers gate on
// the ready event's feature list rather than sending blind.
func (s *Session) Auto(on bool) error {
	if on {
		return s.send("auto-on")
	}
	return s.send("auto-off")
}

// Rearm nudges a parked auto trigger back to searching. Sent when a card was
// probably placed that the trigger's geometry could not see — stacked squarely
// on the pile — and the re-read is discarded if it turns out to be the card
// already processed.
func (s *Session) Rearm() error { return s.send("rearm") }

// Chime plays the card-processed sound, fired when a scan resolves rather than
// when it is captured: either resolution means "place the next card", and this
// is its sound.
func (s *Session) Chime() error { return s.send("chime") }

// Result reports a resolved card's price outcome for the phone's HUD. Only
// sent to phones that advertised "hud" on their ready event; older ones get
// Chime instead.
func (s *Session) Result(r HUDResult) error {
	js, err := marshalHUD(r)
	if err != nil {
		return err
	}
	return s.send("result " + string(js))
}

// Torch turns the phone's flashlight on or off. Only sent to phones that
// advertised "torch"; the state that actually took arrives as an EventTorch.
func (s *Session) Torch(on bool) error {
	if on {
		return s.send("torch-on")
	}
	return s.send("torch-off")
}

// Note appends one line to the session's telemetry log, prefix "~" — this
// side's half of the per-card latency breakdown, landing beside the phone's own
// timing lines. A session without a log swallows it.
func (s *Session) Note(line string) { s.logLine("~", []byte(line)) }

// send writes one command line to the phone.
func (s *Session) send(cmd string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("scan session is closed")
	}
	if err := s.link.SendLine(cmd); err != nil {
		return fmt.Errorf("sending %q to the phone: %w", cmd, err)
	}
	return nil
}

// Shutdown asks the phone to end the session without touching the event
// channel, for a caller that is itself consuming Events and must see the final
// ones — Close's drain would race such a consumer and could swallow them. Safe
// to call more than once.
func (s *Session) Shutdown() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	// Best effort: a phone that has already gone away cannot be told to stop,
	// and that is not a failure worth reporting to someone mid-pile.
	_ = s.link.SendLine("quit")
	return s.link.Close()
}

// closeGrace is how long Close waits for the pump to drain after the link is
// told to stop. Generous against a slow teardown, short enough that the
// terminal never reads as frozen.
const closeGrace = 2 * time.Second

// Close ends the session and releases the phone's camera, consuming any
// remaining events so the pump can finish. Safe to call more than once.
//
// The deadline matters: Close runs on the TUI's update goroutine, so a wedged
// link used to freeze the whole terminal with ctrl-c dead — the key handler
// being exactly the code blocked here.
func (s *Session) Close() error {
	err := s.Shutdown()
	drained := make(chan struct{})
	go func() {
		for range s.events { //nolint:revive // draining
		}
		close(drained)
	}()
	select {
	case <-drained:
	case <-time.After(closeGrace):
	}
	return err
}

// pump turns the link's frames into Events, then reports how the session ended.
func (s *Session) pump(ctx context.Context) {
	defer close(s.events)
	defer s.closeLog()

	var sawEvent bool
	for frame := range s.link.Frames() {
		switch frame.Kind {
		case link.KindNDJSON:
			line := bytes.TrimSpace(frame.Payload)
			if len(line) == 0 {
				continue
			}
			s.logLine("<", line)
			ev, err := parseEvent(line)
			if err != nil {
				continue // ignore anything that isn't one of our events
			}
			if !sawEvent && ev.Kind == EventReady {
				// The phone declares readiness, so this is the first moment it
				// is known to be listening. Asking earlier would talk to a link
				// that has not finished coming up.
				s.onReady()
			}
			sawEvent = true
			select {
			case s.events <- ev:
			case <-ctx.Done():
				return
			}
		case link.KindTrace:
			// The phone's timing and trigger lines. These used to reach the
			// log by way of the helper re-emitting them on its stderr; now they
			// arrive directly, and the "!" prefix is kept so a session log from
			// before and after the port reads the same.
			s.logLine("!", bytes.TrimSpace(frame.Payload))
		case link.KindStill:
			s.saveStill(frame.Payload)
		case link.KindPreview:
			// Nothing sends these; the kind stays in the wire contract.
		}
	}

	// A link that died without ever producing an event failed at startup — the
	// phone's camera never came up, or the pairing was refused — and its error
	// is the useful message.
	if err := s.link.Err(); err != nil && !sawEvent {
		s.events <- Event{Kind: EventError, Message: err.Error()}
	}
}

// onReady sends the two things the old helper sent on the phone's ready event,
// so a tuning session behaves identically across the port.
func (s *Session) onReady() {
	// Raw captures, when the parent has somewhere to put them. Tied to
	// HOARD_SCAN_DEBUG_DIR rather than a flag of its own because that variable
	// already means "I am building a fixture set" everywhere else, and a second
	// way to say the same thing is a second way to forget. Off otherwise:
	// these are 4032x3024 JPEGs, one per card.
	if os.Getenv("HOARD_SCAN_DEBUG_DIR") != "" {
		_ = s.link.SendLine("stills-on")
	}
	// The trigger knobs the session was started with, so one command line
	// tunes the phone and a session's telemetry can be read against the values
	// that produced it.
	stable, interval, ok := tuningFromEnv()
	if ok {
		_ = s.link.SendLine(fmt.Sprintf("tune %d %g", stable, interval))
	}
}

// saveStill writes one full-resolution capture into the debug directory.
func (s *Session) saveStill(payload []byte) {
	dir := os.Getenv("HOARD_SCAN_DEBUG_DIR")
	if dir == "" || len(payload) == 0 {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	name := filepath.Join(dir, time.Now().Format("20060102-150405.000")+".jpg")
	// Best effort, like every other part of the telemetry path: a fixture that
	// fails to land must never disturb the session that produced it.
	_ = os.WriteFile(name, payload, 0o644)
}

// tuningFromEnv reads the two trigger knobs, if either is set.
func tuningFromEnv() (stable int, interval float64, ok bool) {
	stable, interval = 6, 0.1
	sRaw := strings.TrimSpace(os.Getenv("HOARD_SCAN_AUTO_STABLE"))
	iRaw := strings.TrimSpace(os.Getenv("HOARD_SCAN_AUTO_INTERVAL"))
	if sRaw == "" && iRaw == "" {
		return 0, 0, false
	}
	if sRaw != "" {
		if v, err := parseInt(sRaw); err == nil {
			stable = v
		}
	}
	if iRaw != "" {
		if v, err := parseFloat(iRaw); err == nil {
			interval = v
		}
	}
	return stable, interval, true
}
