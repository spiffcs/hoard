package report

import (
	"os"
	"testing"
	"time"
)

func TestAsOfDateNamesTheStampsOwnDay(t *testing.T) {
	prev := os.Getenv("TZ")
	t.Setenv("TZ", "America/New_York")
	time.Local = time.FixedZone("EDT", -4*3600)
	t.Cleanup(func() {
		os.Setenv("TZ", prev)
		time.Local = time.UTC
	})

	if got := asOfDate("2026-08-23T00:00:00Z"); got != "23 Aug 2026" {
		t.Errorf("a stamp dated the 23rd must read as the 23rd west of UTC, got %q", got)
	}
	if got := asOfDate("not a stamp"); got != "not a stamp" {
		t.Errorf("an unparseable stamp passes through, got %q", got)
	}
}
