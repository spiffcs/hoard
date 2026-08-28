package release

import (
	"strconv"
	"strings"
)

type version struct{ major, minor, patch int }

// Newer reports whether latest is a strictly greater release than current.
// Anything it cannot parse compares to nothing, so a dev build is never told
// to upgrade and a lower tag is never offered as one.
func Newer(current, latest string) bool {
	c, ok := parseVersion(current)
	if !ok {
		return false
	}
	l, ok := parseVersion(latest)
	if !ok {
		return false
	}
	switch {
	case l.major != c.major:
		return l.major > c.major
	case l.minor != c.minor:
		return l.minor > c.minor
	default:
		return l.patch > c.patch
	}
}

func parseVersion(s string) (version, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}

	majorText, rest, ok := strings.Cut(s, ".")
	if !ok {
		return version{}, false
	}
	minorText, patchText, ok := strings.Cut(rest, ".")
	if !ok || strings.Contains(patchText, ".") {
		return version{}, false
	}

	major, ok := atoi(majorText)
	if !ok {
		return version{}, false
	}
	minor, ok := atoi(minorText)
	if !ok {
		return version{}, false
	}
	patch, ok := atoi(patchText)
	if !ok {
		return version{}, false
	}
	return version{major: major, minor: minor, patch: patch}, true
}

func atoi(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= 0
}
