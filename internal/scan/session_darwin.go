//go:build darwin

package scan

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Session is a running capture helper: one camera window, held open across many
// captures. Commands go in, Events come out, until Close (or the user closing
// the window) ends it.
//
// The window is long-lived on purpose. Launching the helper per card costs a
// camera warm-up every time and makes the user re-trigger it from the terminal
// between cards.
type Session struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	events chan Event
	// log receives a timestamped copy of every helper line when
	// HOARD_SCAN_LOG names a file — the telemetry tap for tuning sessions
	// where the TUI, not a terminal, owns the helper's pipes.
	log *os.File

	mu     sync.Mutex
	closed bool
}

// logLine appends one timestamped line to the telemetry log, if one is open.
// The prefix separates the streams: "<" for helper events, "!" for its stderr.
func (s *Session) logLine(prefix string, line []byte) {
	if s.log == nil {
		return
	}
	fmt.Fprintf(s.log, "%s %s %s\n", time.Now().Format("15:04:05.000"), prefix, line)
}

// logWriter tees a helper stream into the telemetry log chunk-wise; stderr
// arrives line-buffered from the helper, so per-Write stamping reads fine.
type logWriter struct {
	s      *Session
	prefix string
}

func (w logWriter) Write(p []byte) (int, error) {
	for _, line := range bytes.Split(bytes.TrimRight(p, "\n"), []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			w.s.logLine(w.prefix, line)
		}
	}
	return len(p), nil
}

// Open starts a capture session on the given camera. deviceID may be empty to
// let the helper choose; rotation is the previously-saved preview correction.
// The returned Session's Events channel is closed when the helper exits.
func Open(ctx context.Context, deviceID string, rotation int) (*Session, error) {
	path, err := helperPath()
	if err != nil {
		return nil, err
	}

	args := []string{"--rotate", strconv.Itoa(rotation)}
	if deviceID != "" {
		args = append(args, "--device", deviceID)
	}

	cmd := exec.CommandContext(ctx, path, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	s := &Session{cmd: cmd, stdin: stdin, events: make(chan Event, 8)}
	// HOARD_SCAN_LOG tees the helper's streams, timestamped, to a file — the
	// only way to watch the live feed while the TUI owns the pipes. Append
	// mode, so one file collects a whole tuning session across camera reopens.
	if path := os.Getenv("HOARD_SCAN_LOG"); path != "" {
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			s.log = f
		}
	}
	var stderr bytes.Buffer
	if s.log != nil {
		cmd.Stderr = io.MultiWriter(&stderr, logWriter{s: s, prefix: "!"})
	} else {
		cmd.Stderr = &stderr
	}

	if err := cmd.Start(); err != nil {
		if s.log != nil {
			_ = s.log.Close()
		}
		return nil, err
	}

	go s.pump(stdout, &stderr)
	return s, nil
}

// pump forwards the helper's JSON lines onto the events channel, then reports
// how it ended. A helper that dies without ever emitting an event failed at
// startup — no camera, denied permission — and its stderr is the useful message.
func (s *Session) pump(stdout io.Reader, stderr *bytes.Buffer) {
	defer close(s.events)
	defer func() {
		if s.log != nil {
			_ = s.log.Close()
		}
	}()

	var sawEvent bool
	sc := bufio.NewScanner(stdout)
	// Candidate lists are small, but a generous cap beats a truncated line.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		s.logLine("<", line)
		ev, err := parseEvent(line)
		if err != nil {
			continue // ignore anything that isn't one of our events
		}
		sawEvent = true
		s.events <- ev
	}
	// A scanner error (an over-long line, a read failure) would otherwise look
	// exactly like the helper closing cleanly — say what actually happened.
	if err := sc.Err(); err != nil {
		s.events <- Event{Kind: EventError, Message: "reading helper output: " + err.Error()}
	}

	waitErr := s.cmd.Wait()
	if sawEvent {
		return
	}
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		s.events <- Event{Kind: EventError, Message: msg}
		return
	}
	if waitErr != nil {
		s.events <- Event{Kind: EventError, Message: waitErr.Error()}
	}
}

// Events returns the stream of events from the helper. It is closed when the
// session ends, for any reason.
func (s *Session) Events() <-chan Event { return s.events }

// Capture asks for a photo. The result arrives later as an EventScan.
func (s *Session) Capture() error { return s.send("capture") }

// Auto turns the helper's hands-free capture trigger on or off. Helpers that
// predate the feature answer with a live error event and stay up; callers
// gate on the ready event's feature list to avoid even that.
func (s *Session) Auto(on bool) error {
	if on {
		return s.send("auto-on")
	}
	return s.send("auto-off")
}

// Rearm nudges a parked auto trigger back to searching. The caller sends it
// when it suspects a card was placed that the trigger's geometry couldn't see
// (stacked squarely on the pile), and discards the re-read if it turns out to
// be the card it already processed.
func (s *Session) Rearm() error { return s.send("rearm") }

// Rotate turns the preview a quarter-turn; the new angle arrives as an
// EventRotation so it can be persisted.
func (s *Session) Rotate(left bool) error {
	if left {
		return s.send("rotate-left")
	}
	return s.send("rotate-right")
}

// send writes one command line to the helper.
func (s *Session) send(cmd string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("scan session is closed")
	}
	if _, err := io.WriteString(s.stdin, cmd+"\n"); err != nil {
		return fmt.Errorf("sending %q to scan helper: %w", cmd, err)
	}
	return nil
}

// Shutdown asks the helper to exit without touching the event channel, for a
// caller that is itself consuming Events and must see the final ones — Close's
// drain would race such a consumer and could swallow them. It is safe to call
// more than once. Closing stdin is itself a shutdown signal, so a helper that
// ignores the quit command still exits.
func (s *Session) Shutdown() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	_, _ = io.WriteString(s.stdin, "quit\n")
	return s.stdin.Close()
}

// Close ends the session and closes the camera window, consuming any remaining
// events so pump can finish and Wait can be reached. It is safe to call more
// than once.
func (s *Session) Close() error {
	err := s.Shutdown()
	for range s.events { //nolint:revive // draining
	}
	return err
}
