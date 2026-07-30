package main

import (
	"testing"
	"time"
)

func TestParseWindow(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want time.Duration
	}{
		{"30d", 30 * 24 * time.Hour},
		{"2w", 14 * 24 * time.Hour},
		{"48h", 48 * time.Hour},
		{"1.5d", 36 * time.Hour},
		{" 7d ", 7 * 24 * time.Hour},
	} {
		got, err := parseWindow(tc.in)
		if err != nil {
			t.Errorf("parseWindow(%q): %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseWindow(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseWindowRejectsNonsense(t *testing.T) {
	for _, in := range []string{"", "d", "-3d", "0d", "soon", "3 days", "0"} {
		if got, err := parseWindow(in); err == nil {
			t.Errorf("parseWindow(%q) = %v, want an error", in, got)
		}
	}
}
