package release

import "testing"

func TestNewerOnlyReportsAStrictlyGreaterRelease(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
		why             string
	}{
		{"v0.4.0", "v0.4.1", true, "a newer patch is an upgrade"},
		{"v0.4.0", "v0.5.0", true, "a newer minor is an upgrade"},
		{"v0.4.0", "v1.0.0", true, "a newer major is an upgrade"},
		{"v0.4.1", "v0.4.1", false, "the same release is not an upgrade"},
		{"v0.5.0", "v0.4.1", false, "an older release must never be offered"},
		{"v1.0.0", "v0.9.9", false, "a lower major must never be offered"},
		{"0.4.0", "v0.4.1", true, "a missing v prefix still compares"},
		{"v0.4.10", "v0.4.9", false, "versions compare numerically, not as strings"},
		{"v0.4.9", "v0.4.10", true, "versions compare numerically, not as strings"},
		{"dev-7d48d2bc4a69", "v0.4.1", false, "a dev build has no version to compare"},
		{"", "v0.4.1", false, "an unknown current version compares to nothing"},
		{"v0.4.0", "", false, "an unknown latest version compares to nothing"},
		{"v0.4.0", "not-a-version", false, "an unparseable tag is not an upgrade"},
	}
	for _, c := range cases {
		if got := Newer(c.current, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v: %s", c.current, c.latest, got, c.want, c.why)
		}
	}
}
