package action

import (
	"strings"
	"testing"
	"time"
)

func westOfGreenwich(t *testing.T) {
	t.Helper()
	saved := time.Local
	time.Local = time.FixedZone("PDT", -7*60*60)
	t.Cleanup(func() { time.Local = saved })
}

func TestHumanWhenRendersTheLedgerStampLocally(t *testing.T) {
	westOfGreenwich(t)

	got := humanWhen("2026-08-11T03:11:07Z")
	const want = "10 Aug 2026 20:11"
	if got != want {
		t.Errorf("humanWhen(03:11:07Z on the 11th) = %q, want %q — the stamp is stored\n"+
			"in UTC and must be shown on the reader's own clock", got, want)
	}
	if strings.ContainsAny(got, "TZ") {
		t.Errorf("humanWhen = %q, still carrying RFC 3339 punctuation", got)
	}
}

func TestHumanWhenKeepsTheTimeOfDay(t *testing.T) {
	westOfGreenwich(t)

	morning := humanWhen("2026-08-10T15:04:00Z")
	evening := humanWhen("2026-08-10T22:47:00Z")
	if morning == evening {
		t.Fatalf("two imports seven hours apart both render %q", morning)
	}
	if want := "10 Aug 2026 08:04"; morning != want {
		t.Errorf("morning = %q, want %q", morning, want)
	}
	if want := "10 Aug 2026 15:47"; evening != want {
		t.Errorf("evening = %q, want %q", evening, want)
	}
}

func TestHumanWhenPassesThroughAnUnparseableStamp(t *testing.T) {
	westOfGreenwich(t)

	for _, stamp := range []string{"", "yesterday", "2026-08-10"} {
		if got := humanWhen(stamp); got != stamp {
			t.Errorf("humanWhen(%q) = %q, want it returned unchanged", stamp, got)
		}
	}
}
