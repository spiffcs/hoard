package link

import (
	"strings"
	"testing"
)

// These two parsers read the stdout of /usr/bin/dns-sd, which reports whatever
// other devices put on the network. The instance name in particular is
// attacker-chosen: it is a Bonjour name any host on the LAN can advertise, and
// it arrives DNS-SD-escaped. Table tests cover the shapes we captured from a
// real macOS 15.6 run; these cover the shapes nobody thought to capture.
//
//	go test ./internal/scan/link/ -run Fuzz -fuzz FuzzParseBrowseLine -fuzztime 30s

func FuzzParseBrowseLine(f *testing.F) {
	f.Add("18:06:05.926  Add        3  26 local.    _hoardscan._tcp.  Chris's iPhone")
	f.Add("18:06:05.926  Rmv        0  26 local.    _hoardscan._tcp.  Chris\\032iPhone")
	f.Add("DATE: ---Tue 11 Aug 2026---")
	f.Add("Timestamp     A/R    Flags  if Domain    Service Type      Instance Name")
	f.Add("")
	f.Add("x Add x x x _ ")
	f.Add("18:06:05.926  Add  3  26 local. _hoardscan._tcp.  \\000\\255")

	f.Fuzz(func(t *testing.T, line string) {
		name, iface, _, ok := parseBrowseLine(line)
		if !ok {
			// A rejected line must yield nothing a caller could mistake for
			// a device: returning ok=false alongside a plausible name is how
			// a banner becomes a peer.
			if name != "" || iface != "" {
				t.Fatalf("rejected line returned name=%q iface=%q", name, iface)
			}
			return
		}
		// The interface column is one whitespace-delimited field by
		// construction, so it can never contain a space. If it does, the
		// column indexing has drifted.
		if strings.ContainsAny(iface, " \t") {
			t.Fatalf("iface %q contains whitespace, from %q", iface, line)
		}
		if iface == "" {
			t.Fatalf("accepted line gave an empty interface, from %q", line)
		}
		// The name is everything from column seven on, so an accepted line
		// always has at least one character of it before unescaping.
		if len(strings.Fields(line)) < 7 {
			t.Fatalf("accepted a line with %d fields: %q", len(strings.Fields(line)), line)
		}
	})
}

func FuzzParseResolveLine(f *testing.F) {
	f.Add("17:00:00.000  Chris's\\032iPhone._hoardscan._tcp.local. can be reached at Chriss-iPhone.local.:49722 (interface 1)")
	f.Add("17:00:00.000  x can be reached at host.local.:0 (interface 1)")
	f.Add(" can be reached at :1")
	f.Add(" can be reached at host:99999")
	f.Add(" can be reached at host:-1")
	f.Add("")

	f.Fuzz(func(t *testing.T, line string) {
		host, port, ok := parseResolveLine(line)
		if !ok {
			if host != "" || port != 0 {
				t.Fatalf("rejected line returned host=%q port=%d", host, port)
			}
			return
		}
		// A port outside the range reaches net.Dial as a value that cannot
		// connect; the parser is the only place that can refuse it.
		if port <= 0 || port > 65535 {
			t.Fatalf("accepted port %d, from %q", port, line)
		}
		if host == "" {
			t.Fatalf("accepted an empty host, from %q", line)
		}
	})
}

// unescapeDNSSD decodes the \DDD escapes dns-sd emits. It is fed names chosen
// by other hosts, so it must terminate and return something for every input,
// including truncated and out-of-range escapes.
func FuzzUnescapeDNSSD(f *testing.F) {
	f.Add("Chris\\032iPhone")
	f.Add("\\255\\000")
	f.Add("trailing\\")
	f.Add("\\99")
	f.Add("\\256")
	f.Add("plain")

	f.Fuzz(func(t *testing.T, s string) {
		got := unescapeDNSSD(s)
		// Decoding only ever collapses escapes, so the result cannot grow.
		if len(got) > len(s) {
			t.Fatalf("unescape grew %q (%d) to %q (%d)", s, len(s), got, len(got))
		}
	})
}
