package link

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

const dialTimeout = 10 * time.Second

type Session struct {
	ID string

	Name string

	control *conn
	preview *conn

	fingerprint []byte

	frames chan Frame
	errsMu sync.Mutex
	errs   []error

	closeOnce sync.Once
	done      chan struct{}
}

type conn struct {
	role Role
	tls  *tls.Conn

	writeMu sync.Mutex
}

func (c *conn) send(f Frame) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return WriteFrame(c.tls, f)
}

func (s *Session) PeerFingerprint() []byte { return s.fingerprint }

func (s *Session) Frames() <-chan Frame { return s.frames }

func (s *Session) Send(f Frame) error { return s.control.send(f) }

func (s *Session) SendJSON(v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.Send(Frame{Kind: KindNDJSON, Payload: payload})
}

func (s *Session) SendLine(cmd string) error {
	return s.Send(Frame{Kind: KindNDJSON, Payload: []byte(cmd)})
}

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

func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}

	b[6] = (b[6] & 0x0F) | 0x40
	b[8] = (b[8] & 0x3F) | 0x80
	h := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:32]), nil
}

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

func dialRole(
	ctx context.Context, svc Service, role Role, session string, code Code, trust *Trust,
) (*conn, []byte, error) {
	dialer := net.Dialer{Timeout: dialTimeout}
	raw, err := dialer.DialContext(ctx, "tcp", svc.Addr())
	if err != nil {
		return nil, nil, fmt.Errorf("link: dialling %s for %s: %w", svc.Addr(), role, err)
	}

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

func (s *Session) read(c *conn, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		f, err := ReadFrame(c.tls, MaxPayload)
		if err != nil {
			select {
			case <-s.done:

			default:
				if !errors.Is(err, io.EOF) {
					s.fail(fmt.Errorf("link: reading %s: %w", c.role, err))
				}

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
