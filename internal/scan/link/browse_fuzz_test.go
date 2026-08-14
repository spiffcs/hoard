package link

import (
	"strings"
	"testing"
)

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

			if name != "" || iface != "" {
				t.Fatalf("rejected line returned name=%q iface=%q", name, iface)
			}
			return
		}

		if strings.ContainsAny(iface, " \t") {
			t.Fatalf("iface %q contains whitespace, from %q", iface, line)
		}
		if iface == "" {
			t.Fatalf("accepted line gave an empty interface, from %q", line)
		}

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

		if port <= 0 || port > 65535 {
			t.Fatalf("accepted port %d, from %q", port, line)
		}
		if host == "" {
			t.Fatalf("accepted an empty host, from %q", line)
		}
	})
}

func FuzzUnescapeDNSSD(f *testing.F) {
	f.Add("Chris\\032iPhone")
	f.Add("\\255\\000")
	f.Add("trailing\\")
	f.Add("\\99")
	f.Add("\\256")
	f.Add("plain")

	f.Fuzz(func(t *testing.T, s string) {
		got := unescapeDNSSD(s)

		if len(got) > len(s) {
			t.Fatalf("unescape grew %q (%d) to %q (%d)", s, len(s), got, len(got))
		}
	})
}
