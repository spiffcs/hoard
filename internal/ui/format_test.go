package ui

import "testing"

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
		{[]string{"Z", "U"}, "U"},
		{[]string{""}, ""},
	} {
		if got := IdentityKey(tc.colors); got != tc.want {
			t.Errorf("IdentityKey(%v) = %q, want %q", tc.colors, got, tc.want)
		}
	}
}

func TestPips(t *testing.T) {
	for _, tc := range []struct {
		colors []string
		want   string
	}{
		{[]string{"U", "W"}, "WU"},
		{[]string{"B"}, "B"},
		{[]string{}, "C"},
		{nil, "—"},
		{[]string{"Z"}, "C"},
	} {
		if got := Pips(tc.colors); got != tc.want {
			t.Errorf("Pips(%v) = %q, want %q", tc.colors, got, tc.want)
		}
	}
}
