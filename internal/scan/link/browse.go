package link

// Finding the phone.
//
// Discovery goes through mDNSResponder, and on macOS 15 that is not a
// preference — a raw multicast socket is refused outright unless the binary
// carries com.apple.developer.networking.multicast, a managed entitlement
// Apple grants only on application. Measured: sends fail EHOSTUNREACH and the
// join succeeds while delivering nothing, with no prompt ever offered. The
// evidence is in docs/specs/scan-transport-port.md §8.
//
// So this drives /usr/bin/dns-sd, which is an OS component: nothing hoard
// ships, builds, signs, or asks anyone to install. That keeps the release
// pipeline pure Go (.goreleaser.yaml builds CGO_ENABLED=0) where the
// alternative — DNSServiceBrowse through cgo — would break cross-compiling the
// darwin artifacts. The Finder interface exists so that swap stays a
// one-file change if parsing ever proves too fragile.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Service is a phone advertising itself.
type Service struct {
	// Name is the Bonjour instance name, which is the name the user gave the
	// device. It is also the id hoard remembers a pairing under, so it has to
	// survive the round trip through dns-sd unchanged.
	Name string
	// Host and Port are set by Resolve, not by Browse. Host is a .local name;
	// Go resolves those through the system resolver on darwin even in a
	// CGO_ENABLED=0 build, which is measured and is why no address lookup is
	// needed here.
	Host string
	Port int
}

// Addr is the dial target, once resolved.
func (s Service) Addr() string {
	return fmt.Sprintf("%s:%d", strings.TrimSuffix(s.Host, "."), s.Port)
}

// Resolved reports whether this service can be dialled.
func (s Service) Resolved() bool { return s.Host != "" && s.Port != 0 }

// Finder is discovery, narrow enough that the dns-sd implementation can be
// swapped for a cgo DNSServiceBrowse one without anything above it moving.
type Finder interface {
	// Browse returns the phones visible within the window, which is a ceiling
	// rather than a wait: mDNS answers arrive in milliseconds on a local
	// network, and a device a beat slow to appear is not an absent one.
	//
	// name, when non-empty, is the one phone the caller is after, and the
	// browse ends the moment it appears — there is nothing left to learn.
	// Empty means enumerate for a picker, which cannot stop at the first
	// answer without hiding the second phone.
	Browse(ctx context.Context, name string, window time.Duration) ([]Service, error)
	// Resolve turns an instance name into something dialable.
	Resolve(ctx context.Context, name string, window time.Duration) (Service, error)
}

// ErrNoDNSSD means the system tool is missing, which on macOS means something
// is badly wrong rather than that hoard is misconfigured.
var ErrNoDNSSD = errors.New("link: /usr/bin/dns-sd not found")

// ErrNotFound means the browse or resolve completed and saw nothing.
var ErrNotFound = errors.New("link: no phone running Hoardling was found")

// DNSSD finds phones by driving /usr/bin/dns-sd.
type DNSSD struct {
	// Path is the tool to run. Empty means /usr/bin/dns-sd — an absolute path
	// by default rather than a PATH lookup, so what runs cannot be changed by
	// the environment hoard happens to be started from.
	Path string
	// Type is the service to look for. Empty means ServiceType, which is the
	// only value production uses. It is settable so the parser and the whole
	// browse/resolve path can be exercised against a service that already
	// exists on a network — proving the plumbing without a phone in the room.
	Type string
}

func (d DNSSD) path() string {
	if d.Path != "" {
		return d.Path
	}
	return "/usr/bin/dns-sd"
}

func (d DNSSD) serviceType() string {
	if d.Type != "" {
		return d.Type
	}
	return ServiceType
}

// runOpts says when a run has heard enough and may stop reading.
type runOpts struct {
	// stop ends the run at the line it accepts. For a query with exactly one
	// right answer — a resolve, or a browse for a named phone — that line is
	// the whole result.
	stop func(line string) bool
	// settle ends the run once this long passes with no new line, after at
	// least one has arrived. For enumeration, where "enough" cannot be
	// recognised from any single line and the end of the answer is the
	// network going quiet.
	settle time.Duration
}

// run executes dns-sd for a bounded window and returns the lines it printed.
//
// dns-sd never exits on its own — every mode is a standing query — so the
// window is enforced by cancelling it, and the kill that follows is the normal
// path rather than an error.
//
// The window is a ceiling, not a duration to serve out. dns-sd calls
// fflush(stdout) inside each reply callback, so an answer is readable within
// milliseconds of arriving, and opts says when to stop reading rather than
// waiting for the deadline to do it: measured on a real phone, a browse that
// used to take its full 2.5s answers in 6ms and a resolve in 4ms.
//
// Only replies are flushed — the banner alone sits in the pipe's buffer — so
// a query nothing answers still costs the whole window and yields no lines at
// all. That is the one case where the waiting is the point, and it is why the
// ceiling stays.
func (d DNSSD) run(
	parent context.Context, window time.Duration, opts runOpts, args ...string,
) ([]string, error) {
	ctx, cancel := context.WithTimeout(parent, window)
	defer cancel()

	// An explicit pipe rather than cmd.StdoutPipe, so this end owns the read
	// side and can close it. See the close below for what that buys.
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer pr.Close()

	cmd := exec.CommandContext(ctx, d.path(), args...)
	cmd.Stdout = pw
	if err := cmd.Start(); err != nil {
		pw.Close()
		// Both forms, because which one arrives depends on how the tool was
		// named: exec.ErrNotFound comes from a PATH lookup, but the default
		// here is an absolute path, and that fails with ENOENT inside a
		// *PathError instead. Checking only the first left the default
		// configuration reporting the one error this package cannot act on.
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNoDNSSD, d.path())
		}
		return nil, fmt.Errorf("link: starting dns-sd: %w", err)
	}
	// The child holds the only write end from here, so a tool that does exit
	// on its own still produces an EOF.
	pw.Close()

	// Read on its own goroutine so the settle timer can fire while the scan
	// is blocked on a pipe that may never produce another line — which is
	// most of the time, and exactly when the timer needs to win.
	scanned := make(chan string)
	go func() {
		defer close(scanned)
		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			select {
			case scanned <- sc.Text():
			case <-ctx.Done():
				return
			}
		}
	}()

	var (
		lines  []string
		timer  *time.Timer
		settle <-chan time.Time
	)
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()

reading:
	for {
		select {
		case line, ok := <-scanned:
			if !ok {
				break reading
			}
			lines = append(lines, line)
			if opts.stop != nil && opts.stop(line) {
				break reading
			}
			if opts.settle > 0 {
				// Restarted by every line, so it measures quiet rather than
				// elapsed time: a phone answering a beat after the first one
				// extends the read instead of being cut off by it.
				if timer == nil {
					timer = time.NewTimer(opts.settle)
					settle = timer.C
				} else {
					timer.Reset(opts.settle)
				}
			}
		case <-settle:
			break reading
		case <-ctx.Done():
			// The window, and the only thing that ends a query nothing
			// answers. Waiting for the pipe to close instead is not
			// equivalent: the kill closes dns-sd's own descriptor, but a
			// killed process can leave one open in a child, and then this
			// loop outlives its deadline waiting on an EOF that is not
			// coming. Measured at 30s against a fake that forks a sleep.
			break reading
		}
	}

	// Killed here rather than left to the deadline: stopping the read early
	// and letting the process live out the window would hand the whole saving
	// straight back.
	cancel()
	// Closed from this side rather than waited on for EOF. The reader is
	// parked in a syscall on a pipe whose write end belongs to a process that
	// was just killed — but a killed process can leave the descriptor open in
	// a child of its own, and then the EOF never comes and this drain never
	// returns. Closing the read end ends it regardless of who still holds the
	// other one.
	pr.Close()
	// Drained before Wait, so the reader is finished with the pipe before
	// anything else touches it.
	for range scanned { //nolint:revive // draining; the values are already collected
	}
	// Wait always reports the kill; only a failure to start is interesting,
	// and that was handled above.
	_ = cmd.Wait()

	// A caller's own cancellation is worth distinguishing from the window
	// simply elapsing, which is the ordinary end of a browse. Asked of the
	// parent rather than the derived context, because this function now
	// cancels its own child on every early exit — the successful path — and
	// asking the child would report all of them as aborted.
	if err := parent.Err(); err != nil {
		return lines, err
	}
	return lines, nil
}

// browseSettle ends an enumerating browse once the network has been quiet for
// this long. Long enough that a second phone answering just behind the first
// is still in the list, short enough that a picker opens in a fraction of the
// window it used to serve out in full.
const browseSettle = 400 * time.Millisecond

// Browse lists the phones advertising _hoardscan._tcp.
//
// A named browse stops at its phone; an enumerating one settles. The split
// matters because the two callers want different things from the same query:
// opening a session already knows which phone and only needs to confirm it is
// here, while a picker is asking the network to say everything it has.
func (d DNSSD) Browse(ctx context.Context, name string, window time.Duration) ([]Service, error) {
	opts := runOpts{settle: browseSettle}
	if name != "" {
		opts = runOpts{stop: func(line string) bool {
			found, _, add, ok := parseBrowseLine(line)
			return ok && add && found == name
		}}
	}
	lines, err := d.run(ctx, window, opts, "-B", d.serviceType())
	if err != nil {
		return nil, err
	}
	// Keyed by name *and* interface, which is not a refinement: dns-sd reports
	// per interface, so one phone on a Mac with Wi-Fi, Thunderbolt bridge and
	// a couple of virtual adapters arrives four times. Tracking presence by
	// name alone means a single Rmv on any one of them retracts a phone that
	// is still plainly reachable on the others.
	type key struct {
		name  string
		iface string
	}
	seen := map[key]bool{}
	for _, line := range lines {
		name, iface, add, ok := parseBrowseLine(line)
		if !ok {
			continue
		}
		// Rmv retracts an earlier Add — a phone that appeared and left inside
		// the window is not a phone that is there now.
		if add {
			seen[key{name, iface}] = true
		} else {
			delete(seen, key{name, iface})
		}
	}
	names := map[string]bool{}
	for k := range seen {
		names[k.name] = true
	}
	out := make([]Service, 0, len(names))
	for name := range names {
		out = append(out, Service{Name: name})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(out) == 0 {
		return nil, ErrNotFound
	}
	return out, nil
}

// parseBrowseLine reads one row of `dns-sd -B` output.
//
//	Timestamp     A/R    Flags  if Domain    Service Type      Instance Name
//	18:06:05.926  Add        3  26 local.    _hoardscan._tcp.  Chris's iPhone
//
// The instance name is last and may contain spaces, so it is everything from
// the seventh column on. The banner, the DATE line, ...STARTING... and the
// header row all fail the A/R check and fall out on their own — note the
// header splits into nine fields, not seven, because "Service Type" and
// "Instance Name" are two words each.
func parseBrowseLine(line string) (name, iface string, add, ok bool) {
	fields := strings.Fields(line)
	if len(fields) < 7 {
		return "", "", false, false
	}
	switch fields[1] {
	case "Add":
		add = true
	case "Rmv":
		add = false
	default:
		return "", "", false, false
	}
	// The service type column, which every real row has and no banner does.
	if !strings.HasPrefix(fields[5], "_") {
		return "", "", false, false
	}
	return unescapeDNSSD(strings.Join(fields[6:], " ")), fields[3], add, true
}

// Resolve turns an instance name into a host and port.
//
// The first answer ends it, with nothing traded away: a resolve has exactly
// one right answer and the loop below already took the first one — it simply
// used to wait out the rest of the window before looking.
func (d DNSSD) Resolve(ctx context.Context, name string, window time.Duration) (Service, error) {
	lines, err := d.run(ctx, window, runOpts{stop: func(line string) bool {
		_, _, ok := parseResolveLine(line)
		return ok
	}}, "-L", name, d.serviceType(), "local.")
	if err != nil {
		return Service{}, err
	}
	for _, line := range lines {
		if host, port, ok := parseResolveLine(line); ok {
			return Service{Name: name, Host: host, Port: port}, nil
		}
	}
	return Service{}, fmt.Errorf("%w: %q did not resolve", ErrNotFound, name)
}

// reachedAt is the marker dns-sd -L puts before the host and port.
const reachedAt = " can be reached at "

// parseResolveLine reads the one line of `dns-sd -L` output that matters:
//
//	17:00:00.000  Chris's\032iPhone._hoardscan._tcp.local. can be reached at Chriss-iPhone.local.:49722 (interface 1)
//
// Everything else it prints is a banner or the TXT record.
func parseResolveLine(line string) (host string, port int, ok bool) {
	i := strings.Index(line, reachedAt)
	if i < 0 {
		return "", 0, false
	}
	rest := strings.TrimSpace(line[i+len(reachedAt):])
	// Trailing "(interface 1)".
	if j := strings.Index(rest, " ("); j >= 0 {
		rest = rest[:j]
	}
	colon := strings.LastIndex(rest, ":")
	if colon < 0 {
		return "", 0, false
	}
	p, err := strconv.Atoi(rest[colon+1:])
	if err != nil || p <= 0 || p > 65535 {
		return "", 0, false
	}
	host = strings.TrimSpace(rest[:colon])
	if host == "" {
		return "", 0, false
	}
	return host, p, true
}

// unescapeDNSSD undoes the escaping dns-sd applies to names.
//
// Bonjour instance names are the names people give their phones, so they
// routinely contain spaces and occasionally worse. dns-sd renders those as
// decimal escapes — a space is \032 — and a name handed back with the escape
// still in it will not resolve, and will not match a pairing hoard already
// remembers.
func unescapeDNSSD(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			i++
			continue
		}
		// \DDD, three decimal digits.
		if i+3 < len(s) && isDigit(s[i+1]) && isDigit(s[i+2]) && isDigit(s[i+3]) {
			if n, err := strconv.Atoi(s[i+1 : i+4]); err == nil && n < 256 {
				b.WriteByte(byte(n))
				i += 4
				continue
			}
		}
		// \x for any other x — the escape for a literal backslash or dot.
		b.WriteByte(s[i+1])
		i += 2
	}
	return b.String()
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
