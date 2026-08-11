package action

import (
	"strings"
	"testing"
	"time"
)

// westOfGreenwich pins the clock this process formats against, so the
// assertions below can name the string they expect instead of computing it the
// same way the code does.
//
// The distinction matters more than it looks. A test that builds its want with
// .Local() passes whether the code calls .Local() or not — the two agree
// exactly when the machine runs UTC, which is what CI does, so the one
// environment that never sees the bug is the one that would vet the fix. Fixing
// the zone here makes the expectation a literal, and a literal cannot agree
// with the bug by accident.
//
// Mutating the package-level time.Local is safe in this package: nothing in
// internal/action calls t.Parallel(), so no other test is reading the clock
// while this one holds it.
func westOfGreenwich(t *testing.T) {
	t.Helper()
	saved := time.Local
	time.Local = time.FixedZone("PDT", -7*60*60)
	t.Cleanup(func() { time.Local = saved })
}

// The import guard's stamp was the only date in the CLI a user saw raw: RFC
// 3339, in UTC, while every other date renders local and human. This is the
// exact stamp that made it a problem — 03:11 UTC on the 11th is 20:11 on the
// 10th for most of the US, so a refusal for an import run that evening came
// back dated tomorrow, and the first thing a reader had reason to doubt was
// whether the guard knew what it was talking about.
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

// The time survives, not just the date. A second import attempt usually follows
// the first by minutes, and a refusal that can only say "10 Aug 2026" cannot
// tell this morning's import from the one being attempted now — which is the
// one question the reader brought to it.
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

// A stamp that will not parse is shown as it was stored. A ledger row written
// by some other version is still evidence that the content was imported, and
// the refusal is worth more than its date is — dropping the date, or failing
// the guard over it, would trade a real protection for a cosmetic one.
func TestHumanWhenPassesThroughAnUnparseableStamp(t *testing.T) {
	westOfGreenwich(t)

	for _, stamp := range []string{"", "yesterday", "2026-08-10"} {
		if got := humanWhen(stamp); got != stamp {
			t.Errorf("humanWhen(%q) = %q, want it returned unchanged", stamp, got)
		}
	}
}
