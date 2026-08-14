package scan

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

type Session struct {
	link *link.Session

	events chan Event

	logMu sync.Mutex
	log   *os.File

	mu     sync.Mutex
	closed bool
}

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

func (s *Session) Events() <-chan Event { return s.events }

func (s *Session) Capture() error { return s.send("capture") }

func (s *Session) Auto(on bool) error {
	if on {
		return s.send("auto-on")
	}
	return s.send("auto-off")
}

func (s *Session) Rearm() error { return s.send("rearm") }

func (s *Session) Chime() error { return s.send("chime") }

func (s *Session) Result(r HUDResult) error {
	js, err := marshalHUD(r)
	if err != nil {
		return err
	}
	return s.send("result " + string(js))
}

func (s *Session) Torch(on bool) error {
	if on {
		return s.send("torch-on")
	}
	return s.send("torch-off")
}

func (s *Session) Note(line string) { s.logLine("~", []byte(line)) }

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

func (s *Session) Shutdown() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()

	_ = s.link.SendLine("quit")
	return s.link.Close()
}

const closeGrace = 2 * time.Second

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
				continue
			}
			if !sawEvent && ev.Kind == EventReady {

				s.onReady()
			}
			sawEvent = true
			select {
			case s.events <- ev:
			case <-ctx.Done():
				return
			}
		case link.KindTrace:

			s.logLine("!", bytes.TrimSpace(frame.Payload))
		case link.KindStill:
			s.saveStill(frame.Payload)
		case link.KindPreview:

		}
	}

	if err := s.link.Err(); err != nil && !sawEvent {
		s.events <- Event{Kind: EventError, Message: err.Error()}
	}
}

func (s *Session) onReady() {

	if os.Getenv("HOARD_SCAN_DEBUG_DIR") != "" {
		_ = s.link.SendLine("stills-on")
	}

	stable, interval, ok := tuningFromEnv()
	if ok {
		_ = s.link.SendLine(fmt.Sprintf("tune %d %g", stable, interval))
	}
}

func (s *Session) saveStill(payload []byte) {
	dir := os.Getenv("HOARD_SCAN_DEBUG_DIR")
	if dir == "" || len(payload) == 0 {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	name := filepath.Join(dir, time.Now().Format("20060102-150405.000")+".jpg")

	_ = os.WriteFile(name, payload, 0o644)
}

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
