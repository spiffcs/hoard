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
