package ui

import "testing"

// Bytes is what the download prompt shows before spending somebody's
// bandwidth, so it should read the way a person would say it.
func TestBytes(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1 << 20, "1 MB"},
		{77 << 20, "77 MB"},
		{3 << 30, "3.0 GB"},
	} {
		if got := Bytes(tc.n); got != tc.want {
			t.Errorf("Bytes(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// Identity letters always render in wheel order — "UW" and "WU" are the
// same identity and must sort and display identically.
func TestIdentityKey(t *testing.T) {
	for _, tc := range []struct {
		colors []string
		want   string
	}{
		{nil, ""},
		{[]string{}, ""},
		{[]string{"W"}, "W"},
		{[]string{"U", "W"}, "WU"},
		{[]string{"G", "R", "B", "U", "W"}, "WUBRG"},
		{[]string{"G", "W"}, "WG"},
		{[]string{"Z", "U"}, "U"}, // junk letters drop
		{[]string{""}, ""},
	} {
		if got := IdentityKey(tc.colors); got != tc.want {
			t.Errorf("IdentityKey(%v) = %q, want %q", tc.colors, got, tc.want)
		}
	}
}

// Pips distinguishes the three states a reader needs told apart: colored,
// colorless (a known-empty identity), and unknown (no document stored).
func TestPips(t *testing.T) {
	for _, tc := range []struct {
		colors []string
		want   string
	}{
		{[]string{"U", "W"}, "WU"},
		{[]string{"B"}, "B"},
		{[]string{}, "C"},    // colorless: known and empty
		{nil, "—"},           // unknown: never enriched
		{[]string{"Z"}, "C"}, // junk-only reduces to colorless, not unknown
	} {
		if got := Pips(tc.colors); got != tc.want {
			t.Errorf("Pips(%v) = %q, want %q", tc.colors, got, tc.want)
		}
	}
}
