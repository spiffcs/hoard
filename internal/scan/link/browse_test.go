package link

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const realBrowseOutput = `Browsing for _companion-link._tcp
DATE: ---Sun 09 Aug 2026---
18:06:05.925  ...STARTING...
Timestamp     A/R    Flags  if Domain               Service Type         Instance Name
18:06:05.926  Add        3  26 local.               _companion-link._tcp. optimus
18:06:05.926  Add        3   1 local.               _companion-link._tcp. optimus
18:06:05.926  Add        3  25 local.               _companion-link._tcp. optimus
18:06:05.926  Add        2  15 local.               _companion-link._tcp. optimus
`

func TestParseBrowseLine(t *testing.T) {
	var got []string
	for _, line := range strings.Split(realBrowseOutput, "\n") {
		if name, iface, add, ok := parseBrowseLine(line); ok {
			if !add {
				t.Errorf("captured output has no Rmv, but %q parsed as one", line)
			}
			got = append(got, name+"@"+iface)
		}
	}
	want := []string{"optimus@26", "optimus@1", "optimus@25", "optimus@15"}
	if len(got) != len(want) {
		t.Fatalf("parsed %d rows (%v), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("row %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseBrowseLineRejectsNoise(t *testing.T) {
	noise := []string{
		"",
		"Browsing for _hoardscan._tcp",
		"DATE: ---Sun 09 Aug 2026---",
		"18:06:05.925  ...STARTING...",
		"Timestamp     A/R    Flags  if Domain               Service Type         Instance Name",
		"18:06:05.926  Add        3  26 local.               notaservicetype iPhone",
	}
	for _, line := range noise {
		if _, _, _, ok := parseBrowseLine(line); ok {
			t.Errorf("parsed a non-row: %q", line)
		}
	}
}

func TestParseBrowseLineNameWithSpaces(t *testing.T) {
	line := `18:06:05.926  Add        3   1 local.               _hoardscan._tcp.     Chris's iPhone 14 Pro`
	name, iface, add, ok := parseBrowseLine(line)
	if !ok || !add {
		t.Fatalf("did not parse: ok=%v add=%v", ok, add)
	}
	if name != "Chris's iPhone 14 Pro" {
		t.Errorf("name = %q", name)
	}
	if iface != "1" {
		t.Errorf("iface = %q", iface)
	}
}

const realResolveOutput = `Lookup optimus._companion-link._tcp.local.
DATE: ---Sun 09 Aug 2026---
18:06:11.953  ...STARTING...
18:06:11.954  optimus._companion-link._tcp.local. can be reached at optimus.local.:49722 (interface 26) Flags: 1
 rpMac=0 rpHN=d796d15bdfe2 rpFl=0x20000 rpVr=680.1.1
18:06:11.954  optimus._companion-link._tcp.local. can be reached at optimus.local.:49722 (interface 1) Flags: 1
`

func TestParseResolveLine(t *testing.T) {
	var hits int
	for _, line := range strings.Split(realResolveOutput, "\n") {
		host, port, ok := parseResolveLine(line)
		if !ok {
			continue
		}
		hits++
		if host != "optimus.local." {
			t.Errorf("host = %q, want optimus.local.", host)
		}
		if port != 49722 {
			t.Errorf("port = %d, want 49722", port)
		}
	}
	if hits != 2 {
		t.Errorf("matched %d lines, want 2", hits)
	}
}

func TestParseResolveLineRejectsNoise(t *testing.T) {
	for _, line := range []string{
		"",
		"Lookup optimus._companion-link._tcp.local.",
		" rpMac=0 rpHN=d796d15bdfe2",
		"18:06:11.954  x can be reached at nohostorport",
		"18:06:11.954  x can be reached at host.local.:notanumber (interface 1)",
		"18:06:11.954  x can be reached at host.local.:99999 (interface 1)",
		"18:06:11.954  x can be reached at :49722 (interface 1)",
	} {
		if _, _, ok := parseResolveLine(line); ok {
			t.Errorf("parsed a bad line: %q", line)
		}
	}
}

func TestUnescapeDNSSD(t *testing.T) {
	cases := map[string]string{
		"optimus":            "optimus",
		`Chris's\032iPhone`:  "Chris's iPhone",
		`a\.b`:               "a.b",
		`back\\slash`:        `back\slash`,
		`\032\032`:           "  ",
		`trailing\`:          `trailing\`,
		"plain text with sp": "plain text with sp",

		`\256overflow`: "256overflow",
	}
	for in, want := range cases {
		if got := unescapeDNSSD(in); got != want {
			t.Errorf("unescapeDNSSD(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestServiceAddr(t *testing.T) {
	s := Service{Name: "phone", Host: "phone.local.", Port: 49722}
	if !s.Resolved() {
		t.Error("a service with host and port is not Resolved")
	}

	if got := s.Addr(); got != "phone.local:49722" {
		t.Errorf("Addr() = %q", got)
	}
	if (Service{Name: "x"}).Resolved() {
		t.Error("an unresolved service claims to be resolved")
	}
}

func TestBrowseMissingTool(t *testing.T) {
	d := DNSSD{Path: "/nonexistent/dns-sd"}
	_, err := d.Browse(context.Background(), "", time.Second)
	if !errors.Is(err, ErrNoDNSSD) {
		t.Errorf("missing tool: err = %v, want ErrNoDNSSD", err)
	}
}

func TestBrowseNoResults(t *testing.T) {

	if _, err := os.Stat("/usr/bin/dns-sd"); err != nil {
		t.Skip("no dns-sd on this platform")
	}
	d := DNSSD{Type: "_hoardnothingadvertisesthis._tcp"}
	_, err := d.Browse(context.Background(), "", 2*time.Second)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("browse for a nonexistent service: err = %v, want ErrNotFound", err)
	}
}

func fakeDNSSD(t *testing.T, lines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-dns-sd")
	var b strings.Builder
	b.WriteString("#!/bin/sh\nprintf '%s\\n'")
	for _, line := range lines {

		fmt.Fprintf(&b, " '%s'", strings.ReplaceAll(line, "'", `'\''`))
	}
	b.WriteString("\nwhile :; do sleep 30; done\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

const fakeBrowseHeader = "Timestamp     A/R    Flags  if Domain               Service Type         Instance Name"

func browseRow(iface, name string) string {
	return "18:06:05.926  Add        3  " + iface + " local.               _hoardscan._tcp.     " + name
}

func TestBrowseForANamedPhoneStopsWhenItAppears(t *testing.T) {
	d := DNSSD{Path: fakeDNSSD(t,
		"Browsing for _hoardscan._tcp",
		fakeBrowseHeader,
		browseRow("26", `Chris's iPhone`),
	)}

	start := time.Now()
	got, err := d.Browse(context.Background(), `Chris's iPhone`, 10*time.Second)
	took := time.Since(start)
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if len(got) != 1 || got[0].Name != `Chris's iPhone` {
		t.Fatalf("Browse found %v, want the one named phone", got)
	}

	if took > 2*time.Second {
		t.Errorf("named browse took %v of a 10s window; it should end at the phone", took)
	}
}

func TestBrowseForAnAbsentPhoneServesTheWindow(t *testing.T) {
	d := DNSSD{Path: fakeDNSSD(t,
		"Browsing for _hoardscan._tcp",
		fakeBrowseHeader,
		browseRow("26", "Someone Else's iPhone"),
	)}

	start := time.Now()
	got, err := d.Browse(context.Background(), `Chris's iPhone`, 600*time.Millisecond)
	took := time.Since(start)
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if took < 500*time.Millisecond {
		t.Errorf("browse for an absent phone returned after %v; it cannot know it is not coming", took)
	}
	if len(got) != 1 || got[0].Name != "Someone Else's iPhone" {
		t.Errorf("Browse returned %v; the phones it did see are what names the error", got)
	}
}

func TestBrowseEnumeratingSettlesAfterTheLastAnswer(t *testing.T) {
	d := DNSSD{Path: fakeDNSSD(t,
		"Browsing for _hoardscan._tcp",
		fakeBrowseHeader,
		browseRow("26", "Spare iPhone"),
		browseRow("26", `Chris's iPhone`),
	)}

	start := time.Now()
	got, err := d.Browse(context.Background(), "", 10*time.Second)
	took := time.Since(start)
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Browse found %v, want both phones", got)
	}
	if took > 2*time.Second {
		t.Errorf("enumerating browse took %v of a 10s window; it should settle", took)
	}
	if took < browseSettle {
		t.Errorf("enumerating browse returned in %v, before the settle could have elapsed", took)
	}
}

func TestResolveStopsAtTheFirstAddress(t *testing.T) {
	d := DNSSD{Path: fakeDNSSD(t,
		"Lookup Chris's iPhone._hoardscan._tcp.local.",
		`17:00:00.000  Chris's\032iPhone._hoardscan._tcp.local. can be reached at Chriss-iPhone.local.:49722 (interface 1)`,
	)}

	start := time.Now()
	svc, err := d.Resolve(context.Background(), `Chris's iPhone`, 10*time.Second)
	took := time.Since(start)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if svc.Addr() != "Chriss-iPhone.local:49722" {
		t.Errorf("Resolve gave %q", svc.Addr())
	}
	if took > 2*time.Second {
		t.Errorf("resolve took %v of a 10s window; the address was in the first line", took)
	}
}

func TestBrowseReportsCallerCancellation(t *testing.T) {
	d := DNSSD{Path: fakeDNSSD(t, "Browsing for _hoardscan._tcp")}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	_, err := d.Browse(ctx, "", 10*time.Second)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled browse reported %v, want context.Canceled", err)
	}
}

func TestLiveBrowseAnyService(t *testing.T) {
	if os.Getenv("HOARD_LINK_LIVE") == "" {
		t.Skip("set HOARD_LINK_LIVE=1 to browse the real network")
	}
	svcType := os.Getenv("HOARD_LINK_LIVE_TYPE")
	if svcType == "" {
		svcType = "_companion-link._tcp"
	}
	d := DNSSD{Type: svcType}
	ctx := context.Background()

	found, err := d.Browse(ctx, "", 4*time.Second)
	if err != nil {
		t.Fatalf("Browse(%s): %v", svcType, err)
	}
	t.Logf("browse found %d instance(s) of %s", len(found), svcType)
	for _, s := range found {
		t.Logf("  %q", s.Name)
	}

	resolved, err := d.Resolve(ctx, found[0].Name, 4*time.Second)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", found[0].Name, err)
	}
	t.Logf("resolved %q -> %s", resolved.Name, resolved.Addr())
	if !resolved.Resolved() {
		t.Error("Resolve returned a service that is not dialable")
	}
}

func TestLiveFindPhone(t *testing.T) {
	if os.Getenv("HOARD_LINK_LIVE") == "" {
		t.Skip("set HOARD_LINK_LIVE=1, with Hoardling open on a phone on this network")
	}
	d := DNSSD{}
	ctx := context.Background()

	found, err := d.Browse(ctx, "", 5*time.Second)
	if err != nil {
		t.Fatalf("Browse(%s): %v\n\nIs Hoardling open, and are both devices on the same Wi-Fi?",
			ServiceType, err)
	}
	for _, s := range found {
		resolved, err := d.Resolve(ctx, s.Name, 5*time.Second)
		if err != nil {
			t.Errorf("found %q but could not resolve it: %v", s.Name, err)
			continue
		}
		t.Logf("FOUND %q at %s", resolved.Name, resolved.Addr())
	}
}

func TestLiveDialPhone(t *testing.T) {
	if os.Getenv("HOARD_LINK_LIVE") == "" {
		t.Skip("set HOARD_LINK_LIVE=1, with Hoardling open on a phone on this network")
	}
	d := DNSSD{}
	ctx := context.Background()

	found, err := d.Browse(ctx, "", 5*time.Second)
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	svc, err := d.Resolve(ctx, found[0].Name, 5*time.Second)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", found[0].Name, err)
	}
	t.Logf("resolved %q -> %s", svc.Name, svc.Addr())

	addrs, err := net.DefaultResolver.LookupHost(ctx, strings.TrimSuffix(svc.Host, "."))
	if err != nil {
		t.Errorf("the advertised hostname does not resolve: %v", err)
	} else {
		t.Logf("addresses: %v", addrs)
	}

	conn, err := net.DialTimeout("tcp", svc.Addr(), 5*time.Second)
	if err != nil {
		t.Fatalf("dialling %s: %v", svc.Addr(), err)
	}
	defer conn.Close()
	t.Logf("TCP CONNECTED %s -> %s", conn.LocalAddr(), conn.RemoteAddr())
}
