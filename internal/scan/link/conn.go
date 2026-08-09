package link

// Opening the link to the phone.
//
// A session is two TCP connections carrying framed messages under TLS, matched
// at the far end by a session id in their hellos. Counterparts:
// ScanLink/PeerEnds.swift (the connect half) and PeerLink.swift.
//
// Two connections rather than one because a 200 KB preview JPEG queued ahead of
// a scan event is head-of-line blocking on exactly the latency the design
// exists to protect. Control is small, ordered and never dropped.

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// dialTimeout bounds one connection's TCP and TLS handshake. Generous for a
// phone coming up on a slow network, short enough that a wrong address fails
// while the user is still looking at the terminal.
const dialTimeout = 10 * time.Second

// Session is a live link: two connections to one phone.
type Session struct {
	// ID is the session id both connections carried in their hellos, and what
	// the phone matched them by.
	ID string
	// Name is the phone's Bonjour instance name.
	Name string

	control *conn
	preview *conn

	// fingerprint is the phone's certificate fingerprint, observed during the
	// control handshake. This is what to pin once a pairing completes.
	fingerprint []byte

	frames chan Frame
	errsMu sync.Mutex
	errs   []error

	closeOnce sync.Once
	done      chan struct{}
}

// conn is one framed TLS connection.
type conn struct {
	role Role
	tls  *tls.Conn
	// writeMu serialises writers. A frame is a header and a payload written
	// back to back, and two goroutines interleaving those produces a stream
	// the far end reads as a protocol error — which it treats as fatal, and
	// correctly so.
	writeMu sync.Mutex
}

func (c *conn) send(f Frame) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return WriteFrame(c.tls, f)
}

// PeerFingerprint is the phone's certificate fingerprint for this session.
func (s *Session) PeerFingerprint() []byte { return s.fingerprint }

// Frames is every frame arriving on either connection.
//
// Both connections feed one channel deliberately. The Swift side shipped a bug
// where the preview callback dropped non-preview frames on the floor, so
// fixture stills sent there arrived and vanished with no error on either end
// (RemoteController.swift:127-136). Merging the streams makes that
// unrepresentable rather than merely fixed.
//
// Closed when the session ends, for any reason.
func (s *Session) Frames() <-chan Frame { return s.frames }

// Send writes a frame on the control connection, which is where every command
// belongs.
func (s *Session) Send(f Frame) error { return s.control.send(f) }

// SendJSON writes one NDJSON command frame.
func (s *Session) SendJSON(v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.Send(Frame{Kind: KindNDJSON, Payload: payload})
}

// SendLine writes one bare command line — `capture`, `auto-on`, `quit` — which
// is what ScanCommand parses on the phone.
func (s *Session) SendLine(cmd string) error {
	return s.Send(Frame{Kind: KindNDJSON, Payload: []byte(cmd)})
}

// Err returns the first error that ended the session, if any. A session that
// was closed deliberately reports nil.
func (s *Session) Err() error {
	s.errsMu.Lock()
	defer s.errsMu.Unlock()
	if len(s.errs) == 0 {
		return nil
	}
	return s.errs[0]
}

func (s *Session) fail(err error) {
	if err == nil {
		return
	}
	s.errsMu.Lock()
	s.errs = append(s.errs, err)
	s.errsMu.Unlock()
}

// Close ends the session. Safe to call more than once.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		close(s.done)
		if s.control != nil {
			_ = s.control.tls.Close()
		}
		if s.preview != nil {
			_ = s.preview.tls.Close()
		}
	})
	return nil
}

// newSessionID returns a fresh session id.
//
// Formatted as a UUID because that is what the Swift side sends and what a
// session log is easier to read with, but it is not parsed anywhere: the phone
// treats it as an opaque string to match two connections by, and it is the
// value the pairing proof is an HMAC over. So this is 128 random bits with
// dashes, not a dependency.
func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	// Version 4, variant 1 — cosmetic, but a malformed UUID in a log is a
	// distraction nobody needs.
	b[6] = (b[6] & 0x0F) | 0x40
	b[8] = (b[8] & 0x3F) | 0x80
	h := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:32]), nil
}

// Dial opens a session to a resolved phone.
//
// Both connections are opened concurrently and each sends its hello as soon as
// its own TLS handshake completes — never before. The proof commits to the
// peer's certificate fingerprint, which does not exist until TLS has settled;
// a hello sent early carries a proof bound to nothing, and the phone reports
// that as a wrong pairing code (PeerEnds.swift:369-385).
func Dial(ctx context.Context, svc Service, code Code, trust *Trust) (*Session, error) {
	if !svc.Resolved() {
		return nil, fmt.Errorf("link: %q has no address; resolve it first", svc.Name)
	}
	id, err := newSessionID()
	if err != nil {
		return nil, err
	}

	s := &Session{
		ID:     id,
		Name:   svc.Name,
		frames: make(chan Frame, 16),
		done:   make(chan struct{}),
	}

	type result struct {
		c   *conn
		fp  []byte
		err error
	}
	results := make(chan result, 2)
	for _, role := range []Role{RoleControl, RolePreview} {
		go func(role Role) {
			c, fp, err := dialRole(ctx, svc, role, id, code, trust)
			results <- result{c: c, fp: fp, err: err}
		}(role)
	}

	var failure error
	for range 2 {
		r := <-results
		switch {
		case r.err != nil:
			// Keep the first failure, but drain both so neither goroutine is
			// left holding a connection nobody closes.
			if failure == nil {
				failure = r.err
			}
		case r.c.role == RoleControl:
			s.control, s.fingerprint = r.c, r.fp
		default:
			s.preview = r.c
		}
	}
	if failure != nil {
		if s.control != nil {
			_ = s.control.tls.Close()
		}
		if s.preview != nil {
			_ = s.preview.tls.Close()
		}
		return nil, failure
	}

	// One reader per connection, one channel out. The wait group is what lets
	// the channel be closed exactly once, after both have stopped.
	var wg sync.WaitGroup
	wg.Add(2)
	go s.read(s.control, &wg)
	go s.read(s.preview, &wg)
	go func() {
		wg.Wait()
		close(s.frames)
	}()

	return s, nil
}

// dialRole opens one connection, completes TLS, and sends its hello.
func dialRole(
	ctx context.Context, svc Service, role Role, session string, code Code, trust *Trust,
) (*conn, []byte, error) {
	dialer := net.Dialer{Timeout: dialTimeout}
	raw, err := dialer.DialContext(ctx, "tcp", svc.Addr())
	if err != nil {
		return nil, nil, fmt.Errorf("link: dialling %s for %s: %w", svc.Addr(), role, err)
	}

	// Nagle off on control only, matching PeerLink.swift:326-329: a capture
	// verb is eight bytes, and waiting to coalesce it would add up to 40 ms to
	// a shutter press. Go disables Nagle by default, so the interesting half
	// is turning it back *on* for preview, where payloads are large and
	// coalescing is a gain.
	if tcp, ok := raw.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(role == RoleControl)
	}

	var h Handshake
	tconn := tls.Client(raw, trust.ClientConfig(&h))

	hsCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	if err := tconn.HandshakeContext(hsCtx); err != nil {
		_ = raw.Close()
		if errors.Is(err, ErrPeerNotPinned) {
			// Worth its own sentence: this is "you have not paired with this
			// phone", which is a thing to go and do, not a network fault.
			return nil, nil, fmt.Errorf("link: %s: %w", svc.Name, ErrPeerNotPinned)
		}
		return nil, nil, fmt.Errorf("link: TLS to %s for %s: %w", svc.Name, role, err)
	}

	fingerprint := h.PeerFingerprint()
	proof, err := Proof(session, code, fingerprint)
	if err != nil {
		_ = tconn.Close()
		return nil, nil, err
	}
	hello := Hello{Role: role, Session: session, Proof: proof}
	payload, err := json.Marshal(hello)
	if err != nil {
		_ = tconn.Close()
		return nil, nil, err
	}

	c := &conn{role: role, tls: tconn}
	if err := c.send(Frame{Kind: KindNDJSON, Payload: payload}); err != nil {
		_ = tconn.Close()
		return nil, nil, fmt.Errorf("link: sending hello for %s: %w", role, err)
	}
	return c, fingerprint, nil
}

// read pumps one connection's frames onto the session channel until it ends.
func (s *Session) read(c *conn, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		f, err := ReadFrame(c.tls, MaxPayload)
		if err != nil {
			select {
			case <-s.done:
				// Closed deliberately; the read error is the close, not news.
			default:
				if !errors.Is(err, io.EOF) {
					s.fail(fmt.Errorf("link: reading %s: %w", c.role, err))
				}
				// One leg dying takes the session with it. A control
				// connection that survives a dead preview keeps scanning while
				// every still vanishes into a closed socket, with no error on
				// either side (RemoteController.swift:137-144).
				_ = s.Close()
			}
			return
		}
		select {
		case s.frames <- f:
		case <-s.done:
			return
		}
	}
}
