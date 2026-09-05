package link

import (
	"crypto/tls"
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

func deafPeer(t *testing.T) *conn {
	t.Helper()
	serverID, clientID := newTestIdentity(t, "phone"), newTestIdentity(t, "mac")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })

	accepted := make(chan struct{})
	go func() {
		raw, err := listener.Accept()
		if err != nil {
			close(accepted)
			return
		}
		server := tls.Server(raw, &tls.Config{
			Certificates: []tls.Certificate{serverID.Certificate},
			MinVersion:   tls.VersionTLS12,
			ClientAuth:   tls.RequireAnyClientCert,
		})
		_ = server.Handshake()
		close(accepted)
		<-t.Context().Done()
		_ = server.Close()
		_ = raw.Close()
	}()

	raw, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })

	var h Handshake
	trust := TrustPairing(clientID, NewPinStore(t.TempDir()+"/pins.json"))
	client := tls.Client(raw, trust.ClientConfig(&h))
	if err := client.Handshake(); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	<-accepted
	return &conn{role: RoleControl, tls: client}
}

func TestASendToAPhoneThatStoppedReadingGivesUp(t *testing.T) {
	prev := writeTimeout
	writeTimeout = 250 * time.Millisecond
	t.Cleanup(func() { writeTimeout = prev })

	c := deafPeer(t)
	big := make([]byte, 16*1024*1024)

	done := make(chan error, 1)
	go func() { done <- c.send(Frame{Kind: KindNDJSON, Payload: big}) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the send reported success to a peer that never read it")
		}
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Errorf("send error = %v, want a write deadline timeout", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("send blocked with no write deadline: a phone that leaves " +
			"Wi-Fi freezes the UI goroutine until the OS gives up")
	}
}
