package link

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

type Service struct {
	Name string

	Host string
	Port int
}

func (s Service) Addr() string {
	return fmt.Sprintf("%s:%d", strings.TrimSuffix(s.Host, "."), s.Port)
}

func (s Service) Resolved() bool { return s.Host != "" && s.Port != 0 }

type Finder interface {
	Browse(ctx context.Context, name string, window time.Duration) ([]Service, error)

	Resolve(ctx context.Context, name string, window time.Duration) (Service, error)
}

var ErrNoDNSSD = errors.New("link: /usr/bin/dns-sd not found")

var ErrNotFound = errors.New("link: no phone running Hoardling was found")

type DNSSD struct {
	Path string

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

type runOpts struct {
	stop func(line string) bool

	settle time.Duration
}

func (d DNSSD) run(
	parent context.Context, window time.Duration, opts runOpts, args ...string,
) ([]string, error) {
	ctx, cancel := context.WithTimeout(parent, window)
	defer cancel()

	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer pr.Close()

	cmd := exec.CommandContext(ctx, d.path(), args...)
	cmd.Stdout = pw
	if err := cmd.Start(); err != nil {
		pw.Close()

		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNoDNSSD, d.path())
		}
		return nil, fmt.Errorf("link: starting dns-sd: %w", err)
	}

	pw.Close()

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

			break reading
		}
	}

	cancel()

	pr.Close()

	for range scanned { //nolint:revive // draining; the values are already collected
	}

	_ = cmd.Wait()

	if err := parent.Err(); err != nil {
		return lines, err
	}
	return lines, nil
}

const browseSettle = 400 * time.Millisecond

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

	if !strings.HasPrefix(fields[5], "_") {
		return "", "", false, false
	}
	return unescapeDNSSD(strings.Join(fields[6:], " ")), fields[3], add, true
}

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

const reachedAt = " can be reached at "

func parseResolveLine(line string) (host string, port int, ok bool) {
	i := strings.Index(line, reachedAt)
	if i < 0 {
		return "", 0, false
	}
	rest := strings.TrimSpace(line[i+len(reachedAt):])

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

		if i+3 < len(s) && isDigit(s[i+1]) && isDigit(s[i+2]) && isDigit(s[i+3]) {
			if n, err := strconv.Atoi(s[i+1 : i+4]); err == nil && n < 256 {
				b.WriteByte(byte(n))
				i += 4
				continue
			}
		}

		b.WriteByte(s[i+1])
		i += 2
	}
	return b.String()
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
