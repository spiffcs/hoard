// Tests for the auto-scan timing constants.
//
// They live apart from model_test.go because they pin *measurements* rather
// than behaviour: each number here was fitted to a recorded session, and the
// comment saying which session is the point of the test.

package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/cardname"
	"github.com/spiffcs/hoard/internal/scan"
	"github.com/spiffcs/hoard/internal/scryfall"
)

// Both timing constants were fitted to the 61-capture session recorded in
// docs/scanner-tuning.md rather than guessed, so the measurements that produced
// them are what this pins.
//
// They used to be chosen per source, the phone's values sitting beside an
// older, shorter pair fitted to the Continuity pipe. The Continuity path is
// gone and so is the choice; what survives is the requirement that these
// numbers stay inside the session that justified them.
func TestNudgeDelayClearsTheObservedSwap(t *testing.T) {
	// Across 60 result-to-capture gaps on the phone, the fastest was 3856ms and
	// the median 4896ms. A delay under the fastest observed swap fires while
	// the operator's hand is still moving, which buys an extra capture per card
	// instead of catching a parked one.
	if nudgeDelay < 3856*time.Millisecond {
		t.Errorf("nudge delay %v is below the fastest observed swap (3856ms), "+
			"so it will fire mid-swap", nudgeDelay)
	}
	// And above p75 it stops being a recheck at all — a card genuinely parked
	// waits longer than the operator does.
	if nudgeDelay > 7047*time.Millisecond {
		t.Errorf("nudge delay %v exceeds the p75 swap (7047ms), so a parked "+
			"card waits on the timer rather than the timer catching it", nudgeDelay)
	}
}

// The recheck loop widens on repeat echoes and stops widening.
//
// The loop used to end instead of widening: the echo branch returned no
// command, so a card the rules suppressed got exactly one recheck and then
// waited on the phone's own re-arm — 73.5s in one observed session, for a
// suppression the ten-second window should have released. Rescheduling
// unconditionally is the fix; this is what keeps it from becoming a permanent
// 5.5s poll of a card sitting still on the mat.
func TestNudgeBacksOffAndCaps(t *testing.T) {
	if got := nudgeBackoff(0); got != nudgeDelay {
		t.Errorf("nudgeBackoff(0) = %v, want the base delay %v", got, nudgeDelay)
	}
	// Each consecutive echo doubles, so a card being shown repeatedly is asked
	// about less and less often.
	if got, want := nudgeBackoff(1), 2*nudgeDelay; got != want {
		t.Errorf("nudgeBackoff(1) = %v, want %v", got, want)
	}
	// And the widening stops, because a card that arrives while the timer is
	// parked does not wait for it — the phone fires on disruption and voids
	// the pending generation. Only the case geometry cannot see is affected,
	// and that one should not be left for minutes.
	capped := nudgeDelay << nudgeBackoffSteps
	if got := nudgeBackoff(nudgeBackoffSteps + 5); got != capped {
		t.Errorf("nudgeBackoff past the cap = %v, want %v", got, capped)
	}
	if capped > time.Minute {
		t.Errorf("the capped recheck is %v, longer than anyone waits before "+
			"deciding the scanner has stopped working", capped)
	}
}

// The escalation has to fit in what is left of the shutter-to-result budget.
func TestNameTimeoutFitsTheShutterToResultBudget(t *testing.T) {
	// The budget is 700ms and the phone's own half measured 447ms median, so
	// the Scryfall escalation gets the ~250ms that remains and no more — past
	// that the rhythm the sounds depend on starts to slip.
	//
	// Fitted against the median rather than the p90 of 472ms, deliberately: at
	// p90 the budget is already spent before the escalation starts, so pinning
	// to it would forbid any network call at all. The escalation is a bonus
	// that catches a card printed since the last catalog build, and the right
	// trade is that it fits the typical card and is cut short on a slow one.
	const budget = 700 * time.Millisecond
	const phoneReadMedian = 447 * time.Millisecond
	if phoneReadMedian+nameTimeout > budget {
		t.Errorf("median phone read (%v) plus name timeout %v exceeds the %v budget",
			phoneReadMedian, nameTimeout, budget)
	}
}

// The border breaks a year tie, and only a year tie.
//
// Every case here is a card from the live session of 2026-08-04 that queued as
// "printing unverified" while holding a perfectly-read copyright year. The year
// narrowed each to printings that share a set, a collector number and a release
// date, leaving the printed border as the only difference on the card.
func TestYearAndBorderPicksBetweenSameNumberPrintings(t *testing.T) {
	for _, tc := range []struct {
		name, border, wantSet string
		wantRank              scanMatch
	}{
		{"white picks the white-bordered printing", "white", "4ed", scanMatchYearAndMarks},
		{"black picks the black-bordered printing", "black", "4bb", scanMatchYearAndMarks},
		// No border read is the old behaviour exactly: 1995 alone cannot
		// separate 4ED from 4BB and must not pretend to.
		{"no border settles nothing", "", "", scanMatchNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ranked, rank := rankByScanStrength(controlMagicPrints(), "", "", 1995, tc.border, "", "")
			if rank != tc.wantRank {
				t.Fatalf("rank = %v, want %v", rank, tc.wantRank)
			}
			if tc.wantSet != "" && ranked[0].Set != tc.wantSet {
				t.Errorf("led with %s, want %s", ranked[0].Set, tc.wantSet)
			}
		})
	}
}

// A collector number that matches nothing silences the number, not the card.
//
// Live 2026-08-06: Lion Umbra read 420 off its copyright row, a 2024 copyright
// year and a foil sparkle, against two printings. 420 matched neither, and the
// ranker returned scanMatchNone on the spot — so the year and the sparkle, both
// read cleanly off the same card, never got weighed at all and the card queued
// as "printing unverified: 2 printings".
func TestAnUnmatchedNumberFallsBackToTheYear(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "retro", Name: "Lion Umbra", Set: "mh3", CollectorNumber: "426",
			ReleasedAt: "2024-06-14", BorderColor: "black", Finishes: []string{"nonfoil", "foil"}},
		{ID: "old", Name: "Lion Umbra", Set: "arb", CollectorNumber: "12",
			ReleasedAt: "2009-04-30", BorderColor: "black", Finishes: []string{"nonfoil", "foil"}},
	}
	ranked, rank := rankByScanStrength(prints, "", "420", 2024, "black", "foil", "")
	if rank != scanMatchYearOnly {
		t.Fatalf("rank = %v, want year-only: 420 matches nothing but 2024 names one printing", rank)
	}
	if ranked[0].Set != "mh3" {
		t.Errorf("led with %s, want the 2024 printing", ranked[0].Set)
	}

	// The sole-printing floor stays out of reach, though. "Only one candidate
	// and no number was read" is the absence of evidence; a number that agrees
	// with nothing is a positive reason to doubt the name this list came from,
	// and it must not be answered by shrugging.
	lone := prints[:1]
	if _, rank := rankByScanStrength(lone, "", "999", 0, "", "", ""); rank != scanMatchNone {
		t.Errorf("rank = %v, want none: a lone printing does not rescue a number that matched nothing", rank)
	}
	// And with no number read at all, that same lone printing still commits —
	// the difference between the two is the whole point.
	if _, rank := rankByScanStrength(lone, "", "", 0, "", "", ""); rank != scanMatchSinglePrint {
		t.Errorf("rank = %v, want single-print", rank)
	}
}

// A black read is dark enough to exclude gold; a white read is not.
//
// Mana Leak's 1998 line is sth/36 (black) and wc98/rb36 (gold), and the two
// answers are not symmetric. Black sits at the dark end of the card's own tone
// range — measured -0.11 to -0.23 live — where no gold border lands, so it
// excludes the gold and confirms the Stronghold printing. White cannot: white,
// gold and silver are all bright, and treating a white read as excluding gold
// once committed a World Championship card by elimination.
func TestBlackExcludesGoldButWhiteDoesNot(t *testing.T) {
	manaLeak := []scryfall.Card{
		{ID: "gold", Name: "Mana Leak", Set: "wc98", CollectorNumber: "rb36",
			ReleasedAt: "1998-08-12", BorderColor: "gold"},
		{ID: "black", Name: "Mana Leak", Set: "sth", CollectorNumber: "36",
			ReleasedAt: "1998-03-02", BorderColor: "black"},
	}
	ranked, rank := rankByScanStrength(manaLeak, "", "", 1998, "black", "", "")
	if rank != scanMatchYearAndMarks {
		t.Fatalf("rank = %v, want year+border: black rules out the gold printing", rank)
	}
	if ranked[0].Set != "sth" {
		t.Errorf("led with %s, want the black-bordered sth", ranked[0].Set)
	}
	// And the reverse still fails closed: white leaves the gold standing, and a
	// winner chosen by elimination is not confirmed by anything.
	if _, rank := rankByScanStrength(manaLeak, "", "", 1998, "white", "", ""); rank != scanMatchNone {
		t.Errorf("rank = %v, want scanMatchNone: white cannot exclude gold", rank)
	}
	// The asymmetry, stated directly.
	if !borderRulesOut(manaLeak[0], "black") {
		t.Error("a black read should exclude a gold printing")
	}
	if borderRulesOut(manaLeak[0], "white") {
		t.Error("a white read must not exclude a gold printing")
	}
}

// Borderless is never excluded, by either answer: its edge is art.
func TestBorderlessIsNeverExcluded(t *testing.T) {
	bl := scryfall.Card{ID: "b", Name: "X", Set: "x", BorderColor: "borderless"}
	for _, read := range []string{"white", "black"} {
		if borderRulesOut(bl, read) {
			t.Errorf("a %s read excluded a borderless printing", read)
		}
	}
}

// A colour the reader cannot read must never rule a printing out.
//
// The reader answers white or black. Gold and silver were attempted, measured
// wrong — an absolute chroma gate called three white-bordered cards gold — and
// removed. So a printing in any other colour stays in contention whatever was
// read, and the card queues for a human.
//
// Mana Leak is the live case: its 1998 line narrows to sth/36 (black) and
// wc98/rb36 (gold), and a "black" read leaves both standing.
func TestBorderNeverRulesOutAColourItCannotRead(t *testing.T) {
	manaLeak := []scryfall.Card{
		{ID: "a", Name: "Mana Leak", Set: "sth", CollectorNumber: "36",
			ReleasedAt: "1998-03-02", BorderColor: "black"},
		{ID: "b", Name: "Mana Leak", Set: "wc98", CollectorNumber: "rb36",
			ReleasedAt: "1998-08-12", BorderColor: "gold"},
	}
	// White is the answer that cannot separate them: gold survives it, and a
	// survivor chosen purely by eliminating its sibling confirms nothing.
	if _, rank := rankByScanStrength(manaLeak, "", "", 1998, "white", "", ""); rank != scanMatchNone {
		t.Errorf("rank = %v, want scanMatchNone: white cannot exclude gold", rank)
	}
	if borderRulesOut(manaLeak[1], "white") {
		t.Error("a gold printing was ruled out by a white read")
	}
}

// It fails closed at both ends.
func TestYearAndBorderFailsClosed(t *testing.T) {
	// A border that rules nothing out added no information.
	allWhite := []scryfall.Card{
		{ID: "a", Name: "X", Set: "p1", CollectorNumber: "1",
			ReleasedAt: "1995-01-01", BorderColor: "white"},
		{ID: "b", Name: "X", Set: "p2", CollectorNumber: "2",
			ReleasedAt: "1995-01-01", BorderColor: "white"},
	}
	if _, rank := rankByScanStrength(allWhite, "", "", 1995, "white", "", ""); rank != scanMatchNone {
		t.Error("a border matching every printing must settle nothing")
	}
	// A border that rules *everything* out disagrees with the whole catalog,
	// which is a reason to distrust the read rather than to pick from nothing.
	if _, rank := rankByScanStrength(allWhite, "", "", 1995, "black", "", ""); rank != scanMatchNone {
		t.Error("a border contradicting every printing must not commit")
	}
	// And the border is never consulted without a year to narrow the field
	// first, because one bit against a whole catalog settles nothing.
	if _, rank := rankByScanStrength(controlMagicPrints(), "", "", 0, "white", "", ""); rank != scanMatchNone {
		t.Error("a border with no year must settle nothing")
	}
}

// The new rank commits on the same terms year-only does.
func TestYearAndBorderIsAutoCommittable(t *testing.T) {
	it := queueItem{
		canonical: "Control Magic",
		prints:    []scryfall.Card{{Name: "Control Magic", Set: "4ed", CollectorNumber: "48"}},
		rank:      scanMatchYearAndMarks,
		match:     cardname.Match{Exact: true},
	}
	auto, _, note := verdict(it)
	if !auto {
		t.Errorf("year+border should commit unattended, queued with: %s", note)
	}
	// A rank that pins nothing at all still queues, so the rank is doing the
	// work rather than the rest of the item happening to pass. (An *ambiguous*
	// number no longer queues — it commits the front printing — which is why
	// this uses scanMatchNone rather than the ambiguous rank it once did.)
	it.rank = scanMatchNone
	if auto, _, _ := verdict(it); auto {
		t.Error("a rank that pinned no printing must still queue")
	}
}

// A phone paired seconds ago is not a question worth asking.
//
// Live report: after typing a six-digit code onto the phone, ctrl+o offered it
// beside the household's other phone — a choice the last six keystrokes had
// already made. Two sources are still the case to test; they are simply two
// phones now rather than a phone and a Continuity camera.
func TestJustPairedPhoneSkipsThePicker(t *testing.T) {
	devices := []scan.Device{
		cam("spare", "Spare iPhone", scan.KindRemote),
		cam("phone", "Billionaires are Parasites", scan.KindRemote),
	}
	sc := &fakeScanner{devices: devices}
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, sc, "", nil)
	m.cameraID = "phone"
	m.cameraName = "Billionaires are Parasites"

	mm, _ := m.onPaired(pairedMsg{})
	got := mm.(model)
	if got.justPairedID != "phone" {
		t.Fatalf("pairing should mark the phone for the next list, got %q", got.justPairedID)
	}

	mm, _ = got.onCameras(camerasMsg{devices: devices})
	got = mm.(model)
	if got.state == stateCameraPick {
		t.Error("the phone just paired; it should open rather than be offered")
	}
	if got.cameraID != "phone" {
		t.Errorf("opened %q, want the phone that was just paired", got.cameraID)
	}
	// One shot: the picker is how a phone gets switched, so the next ctrl+o
	// has to offer the choice again.
	if got.justPairedID != "" {
		t.Error("the just-paired mark must be consumed by the list that used it")
	}
	mm, _ = got.onCameras(camerasMsg{devices: devices})
	if mm.(model).state != stateCameraPick {
		t.Error("a later device list should offer the picker again")
	}
}

// The mark is consumed even when the phone has gone, so it cannot surprise a
// later ctrl+o by silently opening a camera nobody picked.
func TestJustPairedMarkIsConsumedWhenThePhoneIsGone(t *testing.T) {
	devices := []scan.Device{
		cam("spare", "Spare iPhone", scan.KindRemote),
		cam("other", "Someone Else's iPhone", scan.KindRemote),
	}
	sc := &fakeScanner{devices: devices}
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, sc, "", nil)
	m.justPairedID = "phone"

	mm, _ := m.onCameras(camerasMsg{devices: devices})
	got := mm.(model)
	if got.state != stateCameraPick {
		t.Error("a phone that is no longer listed should fall back to the picker")
	}
	if got.justPairedID != "" {
		t.Error("the mark must not survive the list that failed to find it")
	}
}

// An empty capture is only worth reporting when a person asked for it.
func TestEmptyAutoCaptureIsSilent(t *testing.T) {
	sc := &fakeScanner{}
	base := newModel(context.Background(), fakeSearcher{}, noopAdder, sc, "", nil)

	// Hands-free: the trigger fires on a hand mid-swap or on bare desk several
	// times a session. Nobody asked, so nothing is said.
	m := base
	m.session, m.state = &fakeSession{}, stateCapture
	mm, _ := m.onSessionEvent(sessionEventMsg{
		gen: m.sessionGen, ok: true,
		ev: scan.Event{Kind: scan.EventScan, Auto: true},
	})
	got := mm.(model)
	if got.status != "" || got.statusErr {
		t.Errorf("auto capture that read nothing said %q (err=%v), want silence",
			got.status, got.statusErr)
	}
	if len(got.review) != 0 {
		t.Errorf("review = %d, want nothing queued for a frame with no card", len(got.review))
	}

	// Manual: they pressed space and got nothing. That is worth answering.
	m2 := base
	m2.session, m2.state = &fakeSession{}, stateCapturing
	mm2, _ := m2.onSessionEvent(sessionEventMsg{
		gen: m2.sessionGen, ok: true,
		ev: scan.Event{Kind: scan.EventScan, Auto: false},
	})
	got2 := mm2.(model)
	if got2.status == "" || !got2.statusErr {
		t.Error("a manual capture that read nothing should say so")
	}
}

// A better re-read of a queued card replaces it instead of being swallowed.
//
// Observed live: Prodigal Sorcerer's first capture abstained on the border
// ("ring not uniform") and queued as "printing unverified: 23 printings". Its
// nudge echo read the border cleanly, ranked year+border, and was dropped as a
// duplicate — so the worse read won by arriving first.
func TestBetterReReadReplacesTheQueuedEntry(t *testing.T) {
	prod := scryfall.Card{ID: "p", Name: "Prodigal Sorcerer", Set: "4ed",
		CollectorNumber: "94", BorderColor: "white"}

	weak := queueItem{id: 1, canonical: "Prodigal Sorcerer", rank: scanMatchNone,
		prints: []scryfall.Card{prod}, note: "printing unverified: 23 printings"}
	strong := queueItem{id: 2, canonical: "Prodigal Sorcerer", rank: scanMatchYearAndMarks,
		prints: []scryfall.Card{prod}, fromNudge: true,
		match: cardname.Match{Exact: true}}

	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	m.review = []queueItem{weak}
	displaced, ok := m.upgradeQueued(&strong)
	if !ok {
		t.Fatal("a year+border read should displace an unranked queued entry")
	}
	if displaced != scanMatchNone {
		t.Errorf("displaced %v, want the weaker rank it replaced", displaced)
	}
	if len(m.review) != 0 {
		t.Errorf("review = %d, want the weaker entry removed", len(m.review))
	}
}

// The swallow still absorbs what it was built for.
func TestEchoSwallowStillDropsEqualAndWorseReads(t *testing.T) {
	prod := scryfall.Card{ID: "p", Name: "Prodigal Sorcerer", Set: "4ed", CollectorNumber: "94"}
	queued := queueItem{id: 1, canonical: "Prodigal Sorcerer", rank: scanMatchYearAndMarks,
		prints: []scryfall.Card{prod}}

	for _, tc := range []struct {
		name string
		rank scanMatch
	}{
		{"an equal read changes nothing worth the churn", scanMatchYearAndMarks},
		{"a worse read is exactly the echo this absorbs", scanMatchNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
			m.review = []queueItem{queued}
			if _, ok := m.upgradeQueued(&queueItem{
				canonical: "Prodigal Sorcerer", rank: tc.rank, fromNudge: true,
			}); ok {
				t.Error("should not have displaced the queued entry")
			}
			if len(m.review) != 1 {
				t.Error("the queued entry must survive")
			}
		})
	}
}

// A different card never displaces anything, however strong.
func TestUpgradeOnlyMatchesTheSameCard(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	m.review = []queueItem{{id: 1, canonical: "Prodigal Sorcerer", rank: scanMatchNone}}
	if _, ok := m.upgradeQueued(&queueItem{
		canonical: "Control Magic", rank: scanMatchSetAndNumber, fromNudge: true,
	}); ok {
		t.Error("a different card must not displace a queued entry")
	}
	// And an unresolved read has no name to match on.
	if _, ok := m.upgradeQueued(&queueItem{rank: scanMatchSetAndNumber, fromNudge: true}); ok {
		t.Error("an unnamed read must not displace anything")
	}
}

// The card under the cursor is never swapped out mid-cascade.
func TestUpgradeLeavesTheItemBeingReviewedAlone(t *testing.T) {
	prod := scryfall.Card{ID: "p", Name: "Prodigal Sorcerer", Set: "4ed", CollectorNumber: "94"}
	cur := queueItem{id: 1, canonical: "Prodigal Sorcerer", rank: scanMatchNone,
		prints: []scryfall.Card{prod}}
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	m.current = &cur
	m.review = nil
	if _, ok := m.upgradeQueued(&queueItem{
		canonical: "Prodigal Sorcerer", rank: scanMatchYearAndMarks, fromNudge: true,
	}); ok {
		t.Error("the item on screen must not be replaced underneath the operator")
	}
	if m.current == nil || m.current.rank != scanMatchNone {
		t.Error("the reviewed item was modified")
	}
}

// End to end: the queued card is replaced and the better read commits.
//
// The unit tests above prove upgradeQueued picks the right entry; this proves
// the echo then travels the normal path rather than being swallowed on some
// later check, which is where the original bug actually bit.
func TestBetterReReadCommitsAndClearsTheQueue(t *testing.T) {
	prod := scryfall.Card{ID: "p", Name: "Prodigal Sorcerer", Set: "4ed",
		CollectorNumber: "94", BorderColor: "white"}
	var added []Result
	adder := func(r Result) error { added = append(added, r); return nil }

	m := newModel(context.Background(), fakeSearcher{}, adder, &fakeScanner{}, "", nil)
	m.review = []queueItem{{
		id: 1, canonical: "Prodigal Sorcerer", rank: scanMatchNone,
		prints: []scryfall.Card{prod}, note: "printing unverified: 23 printings",
	}}
	// The name was seen a moment ago, which is what arms the swallow.
	m.recentNames = recordName(m.recentNames, "Prodigal Sorcerer", m.now())

	echo := queueItem{
		id: 2, canonical: "Prodigal Sorcerer", rank: scanMatchYearAndMarks,
		prints: []scryfall.Card{prod}, fromNudge: true,
		match: cardname.Match{Exact: true},
	}
	mm, _ := m.onResolveDone(resolveDoneMsg{gen: m.resolveGen, item: echo})
	got := mm.(model)

	if len(added) != 1 {
		t.Fatalf("added %d cards, want the better read committed", len(added))
	}
	if added[0].Card.Set != "4ed" || added[0].Card.CollectorNumber != "94" {
		t.Errorf("committed %s/%s, want 4ed/94", added[0].Card.Set, added[0].Card.CollectorNumber)
	}
	if len(got.review) != 0 {
		t.Errorf("review = %d items, want the weaker entry gone: %+v", len(got.review), got.review)
	}
}

// The winner must match the border, not merely survive it.
//
// Live mis-commit: Mana Leak's 1998 line is sth/36 (black) and wc98/rb36
// (gold). The reader said white. Black was ruled out; gold is never ruled out
// because the reader cannot read gold — so the World Championship printing
// stood alone and committed. It was chosen by eliminating its only sibling,
// with nothing positive behind it, and a WC98 card prices nothing like a
// Stronghold common.
func TestBorderWinnerMustMatchNotMerelySurvive(t *testing.T) {
	manaLeak := []scryfall.Card{
		{ID: "gold", Name: "Mana Leak", Set: "wc98", CollectorNumber: "rb36",
			ReleasedAt: "1998-08-12", BorderColor: "gold"},
		{ID: "black", Name: "Mana Leak", Set: "sth", CollectorNumber: "36",
			ReleasedAt: "1998-03-02", BorderColor: "black"},
	}
	// A white read eliminates the black printing and leaves only the gold one.
	// Elimination is not evidence: nothing here says the card is gold.
	if ranked, rank := rankByScanStrength(manaLeak, "", "", 1998, "white", "", ""); rank != scanMatchNone {
		t.Errorf("rank = %v leading %s/%s, want scanMatchNone: the survivor is a "+
			"colour the reader cannot read, so nothing confirmed it",
			rank, ranked[0].Set, ranked[0].CollectorNumber)
	}
	// The black read is the other half of this and is asserted separately, in
	// TestBlackExcludesGoldButWhiteDoesNot: black is dark enough to exclude a
	// gold border, so it *does* settle this card. The two answers are not
	// symmetric, and that asymmetry is the whole design.
}

// The case the feature exists for still commits: the survivor is the colour
// that was read.
func TestBorderWinnerMatchingStillCommits(t *testing.T) {
	ranked, rank := rankByScanStrength(controlMagicPrints(), "", "", 1995, "white", "", "")
	if rank != scanMatchYearAndMarks {
		t.Fatalf("rank = %v, want year+border", rank)
	}
	if ranked[0].Set != "4ed" || !strings.EqualFold(ranked[0].BorderColor, "white") {
		t.Errorf("led with %s (%s), want the white-bordered 4ed",
			ranked[0].Set, ranked[0].BorderColor)
	}
}

// A set promo is foil-only, so a bullet rules it out.
//
// Both shapes are live review cases, and both are the same trap: the promo and
// the regular printing share a card, a set and a release date, so the year
// cannot separate them. The card itself can — the promo only ever existed as a
// foil, and the regular printing read a nonfoil bullet.
func TestNonfoilRulesOutTheFoilOnlySetPromo(t *testing.T) {
	epiphany := []scryfall.Card{
		{ID: "promo", Name: "Epiphany at the Drownyard", Set: "psoi",
			CollectorNumber: "59s", ReleasedAt: "2016-04-08",
			Finishes: []string{"foil"}},
		{ID: "regular", Name: "Epiphany at the Drownyard", Set: "soi",
			CollectorNumber: "59", ReleasedAt: "2016-04-08",
			Finishes: []string{"nonfoil", "foil"}},
	}
	ranked, rank := rankByScanStrength(epiphany, "", "", 2016, "", "nonfoil", "")
	if rank != scanMatchYearAndMarks {
		t.Fatalf("rank = %v, want year+marks: a nonfoil bullet cannot be a "+
			"foil-only promo", rank)
	}
	if ranked[0].Set != "soi" {
		t.Errorf("led with %s, want the regular soi printing", ranked[0].Set)
	}
}

// The same rule with a wider field: the year narrows four printings to two, and
// the marker settles those.
func TestNonfoilPicksTheRegularPrintingAmongFour(t *testing.T) {
	baral := []scryfall.Card{
		{ID: "tdc", Name: "Baral's Expertise", Set: "tdc", CollectorNumber: "146",
			ReleasedAt: "2025-04-11", Finishes: []string{"nonfoil"}},
		{ID: "otc", Name: "Baral's Expertise", Set: "otc", CollectorNumber: "91",
			ReleasedAt: "2024-04-19", Finishes: []string{"nonfoil"}},
		{ID: "aer", Name: "Baral's Expertise", Set: "aer", CollectorNumber: "29",
			ReleasedAt: "2017-01-20", Finishes: []string{"nonfoil", "foil"}},
		{ID: "paer", Name: "Baral's Expertise", Set: "paer", CollectorNumber: "29s",
			ReleasedAt: "2017-01-20", Finishes: []string{"foil"}},
	}
	ranked, rank := rankByScanStrength(baral, "", "", 2017, "", "nonfoil", "")
	if rank != scanMatchYearAndMarks {
		t.Fatalf("rank = %v, want year+marks", rank)
	}
	if ranked[0].Set != "aer" {
		t.Errorf("led with %s, want aer", ranked[0].Set)
	}
}

// A foil read rules out a printing that never came in foil.
func TestFoilRulesOutNonfoilOnlyPrintings(t *testing.T) {
	cards := []scryfall.Card{
		{ID: "a", Name: "X", Set: "aaa", CollectorNumber: "1",
			ReleasedAt: "2017-01-20", Finishes: []string{"nonfoil"}},
		{ID: "b", Name: "X", Set: "bbb", CollectorNumber: "2",
			ReleasedAt: "2017-01-20", Finishes: []string{"nonfoil", "foil"}},
	}
	ranked, rank := rankByScanStrength(cards, "", "", 2017, "", "foil", "")
	if rank != scanMatchYearAndMarks || ranked[0].Set != "bbb" {
		t.Errorf("rank = %v leading %s, want year+marks on bbb", rank, ranked[0].Set)
	}
}

// Silence is not evidence, and neither is a marker every candidate shares.
func TestFinishNarrowingFailsClosed(t *testing.T) {
	promoPair := []scryfall.Card{
		{ID: "promo", Name: "X", Set: "p", CollectorNumber: "1s",
			ReleasedAt: "2016-04-08", Finishes: []string{"foil"}},
		{ID: "regular", Name: "X", Set: "r", CollectorNumber: "1",
			ReleasedAt: "2016-04-08", Finishes: []string{"nonfoil", "foil"}},
	}
	// An unread marker — an old frame, or a glyph too small — excludes nothing.
	if _, rank := rankByScanStrength(promoPair, "", "", 2016, "", "", ""); rank != scanMatchNone {
		t.Error("an unread finish must settle nothing")
	}
	// A foil read fits both printings, so it separates neither.
	if _, rank := rankByScanStrength(promoPair, "", "", 2016, "", "foil", ""); rank != scanMatchNone {
		t.Error("a marker both printings share must settle nothing")
	}
	// And a finish no printing offers is a read to distrust, not a licence to
	// pick from an empty field.
	if _, rank := rankByScanStrength(promoPair, "", "", 2016, "", "etched", ""); rank != scanMatchNone {
		t.Error("a finish no printing has must not commit")
	}
}

// Border and finish narrow together, and the winner must satisfy both.
func TestBorderAndFinishNarrowTogether(t *testing.T) {
	cards := []scryfall.Card{
		{ID: "a", Name: "X", Set: "aaa", CollectorNumber: "1", ReleasedAt: "2003-01-01",
			BorderColor: "black", Finishes: []string{"foil"}},
		{ID: "b", Name: "X", Set: "bbb", CollectorNumber: "2", ReleasedAt: "2003-01-01",
			BorderColor: "white", Finishes: []string{"nonfoil", "foil"}},
		{ID: "c", Name: "X", Set: "ccc", CollectorNumber: "3", ReleasedAt: "2003-01-01",
			BorderColor: "white", Finishes: []string{"foil"}},
	}
	// White rules out a; nonfoil rules out c. One printing satisfies both.
	ranked, rank := rankByScanStrength(cards, "", "", 2003, "white", "nonfoil", "")
	if rank != scanMatchYearAndMarks || ranked[0].Set != "bbb" {
		t.Errorf("rank = %v leading %s, want year+marks on bbb", rank, ranked[0].Set)
	}
}

// A star names the promo when the promo is the only foil that year.
//
// The mirror of the nonfoil case, and it needs no separate machinery: the same
// filter that drops a foil-only promo on a bullet drops every nonfoil-only
// printing on a star. Real shape, from the catalog — Cultivate 2011, where the
// Commander deck printing never came in foil and the Friday Night Magic promo
// only ever did.
func TestFoilNamesThePromoWhenItIsTheOnlyFoil(t *testing.T) {
	cultivate := []scryfall.Card{
		{ID: "cmd", Name: "Cultivate", Set: "cmd", CollectorNumber: "148",
			ReleasedAt: "2011-06-17", Finishes: []string{"nonfoil"}},
		{ID: "promo", Name: "Cultivate", Set: "f11", CollectorNumber: "8",
			ReleasedAt: "2011-01-01", Finishes: []string{"foil"},
			PromoTypes: []string{"fnm"}},
	}
	ranked, rank := rankByScanStrength(cultivate, "", "", 2011, "", "foil", "")
	if rank != scanMatchYearAndMarks {
		t.Fatalf("rank = %v, want year+marks: a star cannot be a printing that "+
			"never came in foil", rank)
	}
	if ranked[0].Set != "f11" {
		t.Errorf("led with %s, want the foil promo f11", ranked[0].Set)
	}
}

// But a star cannot separate a promo from a regular printing that also foils.
//
// This is the limit of the evidence, not a gap in the rule. Epiphany at the
// Drownyard exists as a foil `soi/59` and a foil-only `psoi/59s`; both are real
// cards a star fits, and preferring the promo would be guessing on the rarer
// one. The nonfoil read separates them and the foil read does not, which is the
// honest asymmetry.
func TestFoilCannotSeparateTwoFoilablePrintings(t *testing.T) {
	epiphany := []scryfall.Card{
		{ID: "promo", Name: "Epiphany at the Drownyard", Set: "psoi",
			CollectorNumber: "59s", ReleasedAt: "2016-04-08",
			Finishes: []string{"foil"}, PromoTypes: []string{"setpromo"}},
		{ID: "regular", Name: "Epiphany at the Drownyard", Set: "soi",
			CollectorNumber: "59", ReleasedAt: "2016-04-08",
			Finishes: []string{"nonfoil", "foil"}},
	}
	if _, rank := rankByScanStrength(epiphany, "", "", 2016, "", "foil", ""); rank != scanMatchNone {
		t.Error("both printings come in foil, so a star settles nothing")
	}
	// And the bullet still does the work it can.
	if _, rank := rankByScanStrength(epiphany, "", "", 2016, "", "nonfoil", ""); rank != scanMatchYearAndMarks {
		t.Error("a bullet must still rule out the foil-only promo")
	}
}

// A bullet breaks a collector-number tie against foil-only promos.
//
// Live: Zahid read a clean 076/269 and still queued as "printing unverified: 5
// printings". Number 76 matches dom/76 and both pdom promos, so the number
// alone is ambiguous — but the card printed a bullet, and both promos are
// foil-only. The narrowing was only running when there was no number at all,
// which is backwards: a number matching several printings is exactly when a
// second signal earns its keep.
func TestNonfoilBreaksACollectorNumberTie(t *testing.T) {
	zahid := []scryfall.Card{
		{ID: "cmm", Name: "Zahid", Set: "cmm", CollectorNumber: "136",
			ReleasedAt: "2023-08-04", Finishes: []string{"nonfoil", "foil"}},
		{ID: "dom", Name: "Zahid", Set: "dom", CollectorNumber: "76",
			ReleasedAt: "2018-04-27", Finishes: []string{"nonfoil", "foil"}},
		{ID: "pdom", Name: "Zahid", Set: "pdom", CollectorNumber: "76",
			ReleasedAt: "2018-04-27", Finishes: []string{"foil"}},
		{ID: "pdoms", Name: "Zahid", Set: "pdom", CollectorNumber: "76s",
			ReleasedAt: "2018-04-27", Finishes: []string{"foil"}},
	}
	// Without the marker the number is genuinely ambiguous, and queuing is right.
	if _, rank := rankByScanStrength(zahid, "", "76", 2018, "", "", ""); rank != scanMatchNumberAmbiguous {
		t.Errorf("rank = %v, want number-ambiguous with no marker to break the tie", rank)
	}
	// With it, the foil-only promo is excluded and the year corroborates.
	ranked, rank := rankByScanStrength(zahid, "", "76", 2018, "", "nonfoil", "")
	if rank != scanMatchNumberAndYear {
		t.Fatalf("rank = %v, want number+year: a bullet cannot be a foil-only promo", rank)
	}
	if ranked[0].Set != "dom" {
		t.Errorf("led with %s/%s, want dom/76", ranked[0].Set, ranked[0].CollectorNumber)
	}
}

// A marker that fits every candidate breaks nothing, and a number that already
// named one printing is untouched.
func TestNumberTieNarrowingFailsClosed(t *testing.T) {
	shared := []scryfall.Card{
		{ID: "a", Name: "X", Set: "aaa", CollectorNumber: "5",
			ReleasedAt: "2018-01-01", Finishes: []string{"nonfoil", "foil"}},
		{ID: "b", Name: "X", Set: "bbb", CollectorNumber: "5",
			ReleasedAt: "2018-01-01", Finishes: []string{"nonfoil", "foil"}},
	}
	if _, rank := rankByScanStrength(shared, "", "5", 2018, "", "nonfoil", ""); rank != scanMatchNumberAmbiguous {
		t.Error("a marker both printings share must not settle a number tie")
	}
	// An exact set match still wins outright; the markings never override it.
	if _, rank := rankByScanStrength(shared, "bbb", "5", 2018, "", "nonfoil", ""); rank != scanMatchSetAndNumber {
		t.Error("set+number must remain the strongest evidence")
	}
}

// The reorder-then-refuse gap, closed.
//
// The ranking moved a printing to the front of the review list and then
// declined to write it down, which meant the queue showed the operator the
// right answer and made them confirm it. Standing preference: false positives
// over false negatives — a wrong printing is one row to correct, a queued card
// is a stop in a session whose point is not stopping.
func TestAmbiguousNumberCommitsTheFrontPrinting(t *testing.T) {
	// Zahid's real shape: 76 matches the regular printing and two foil promos.
	// Newest-first with ties on set code puts `dom` ahead of `pdom`, which is
	// systematic rather than lucky — a promo set is its set's code with a `p`.
	prints := []scryfall.Card{
		{ID: "dom", Name: "Zahid", Set: "dom", CollectorNumber: "76",
			ReleasedAt: "2018-04-27", Finishes: []string{"nonfoil", "foil"}},
		{ID: "pdom", Name: "Zahid", Set: "pdom", CollectorNumber: "76",
			ReleasedAt: "2018-04-27", Finishes: []string{"foil"}},
	}
	it := queueItem{
		canonical: "Zahid", prints: prints, rank: scanMatchNumberAmbiguous,
		match: cardname.Match{Exact: true},
	}
	auto, finish, note := verdict(it)
	if !auto {
		t.Errorf("an ambiguous number should commit the front printing, queued: %s", note)
	}
	// And with the nonfoil default, silence included. `dom/76` comes both ways,
	// so nothing here says which — that is the foil reader's job, not a reason
	// to stop the session. See verdict's comment at the finish.
	if finish != "nonfoil" {
		t.Errorf("finish = %q, want the nonfoil default", finish)
	}
}

// An unread finish is written as nonfoil rather than stopping the session.
//
// Live 2026-08-06, on a pile where every card was a retro-frame foil: Glowrider
// (LGN/15), Trap Digger (SCG/24) and Hard Evidence (H2R/5) each auto-committed
// `nonfoil` because the sparkle reader missed them. Queuing instead was built
// and reversed: the miss is the foil reader's to fix, and making the operator
// answer for it on every retro card is a worse trade than three rows to
// correct. This pins the decision so it is not re-litigated by accident.
func TestUnreadFinishTakesTheNonfoilDefault(t *testing.T) {
	both := scryfall.Card{ID: "h2r5", Name: "Hard Evidence", Set: "h2r",
		CollectorNumber: "5", ReleasedAt: "2024-06-14",
		Finishes: []string{"nonfoil", "foil"}}
	base := queueItem{
		canonical: "Hard Evidence", rank: scanMatchNumberAndYear,
		match: cardname.Match{Exact: true},
	}

	it := base
	it.prints = []scryfall.Card{both}
	if auto, finish, note := verdict(it); !auto || finish != "nonfoil" {
		t.Errorf("auto=%v finish=%q note=%q, want a nonfoil commit", auto, finish, note)
	}

	// And the sparkle, when it does fire, is what makes that default rare.
	it.finishHint = "foil"
	if auto, finish, note := verdict(it); !auto || finish != "foil" {
		t.Errorf("auto=%v finish=%q note=%q, want a foil commit", auto, finish, note)
	}

	// A printing that only ever came one way is not a guess at all.
	nonfoilOnly := both
	nonfoilOnly.Finishes = []string{"nonfoil"}
	it = base
	it.prints = []scryfall.Card{nonfoilOnly}
	if auto, finish, _ := verdict(it); !auto || finish != "nonfoil" {
		t.Errorf("auto=%v finish=%q, want a nonfoil commit", auto, finish)
	}
}

// But never against a year the card actually printed.
// The year strata get the same contradiction check, and they need it more:
// the year is their whole evidence. Live, 2026-08-07: Ornithopter ranked
// year-only on a 2014 read, a misread white border exiled every black row,
// and the head that committed was a 2022 borderless printing whose foil-only
// finish became "evidence" — SLD/604 foil written off a card the phone had
// read as 2014 nonfoil.
func TestYearRankQueuesWhenTheHeadContradictsTheYear(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "sld", Name: "Ornithopter", Set: "sld", CollectorNumber: "604",
			ReleasedAt: "2022-04-22", Finishes: []string{"foil"}},
		{ID: "m15", Name: "Ornithopter", Set: "m15", CollectorNumber: "223",
			ReleasedAt: "2014-07-18", Finishes: []string{"nonfoil", "foil"}},
	}
	for _, rank := range []scanMatch{scanMatchYearOnly, scanMatchYearAndMarks} {
		it := queueItem{
			canonical: "Ornithopter", prints: prints, rank: rank,
			match: cardname.Match{Exact: true},
			raw:   scan.Card{CopyrightYear: 2014},
		}
		if auto, _, note := verdict(it); auto {
			t.Errorf("rank %v: a 2022 head against the 2014 year the rank stands on must queue", rank)
		} else if !strings.Contains(note, "2014") {
			t.Errorf("rank %v: note = %q, should say which year contradicted it", rank, note)
		}
		// The same rank with the year-matching row in front commits as ever.
		it.prints = []scryfall.Card{prints[1], prints[0]}
		if auto, _, _ := verdict(it); !auto {
			t.Errorf("rank %v: a head that agrees with its own year must still commit", rank)
		}
	}
}

func TestAmbiguousNumberQueuesWhenTheYearDisagrees(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "new", Name: "X", Set: "new", CollectorNumber: "76",
			ReleasedAt: "2024-01-01", Finishes: []string{"nonfoil"}},
		{ID: "old", Name: "X", Set: "old", CollectorNumber: "76",
			ReleasedAt: "2018-04-27", Finishes: []string{"nonfoil"}},
	}
	it := queueItem{
		canonical: "X", prints: prints, rank: scanMatchNumberAmbiguous,
		match: cardname.Match{Exact: true},
		raw:   scan.Card{CopyrightYear: 2018},
	}
	if auto, _, note := verdict(it); auto {
		t.Error("committing a 2024 printing against a 2018 copyright line is " +
			"ignoring the card, not guessing without it")
	} else if !strings.Contains(note, "2018") {
		t.Errorf("note = %q, should say which year contradicted it", note)
	}
	// With no year read there is nothing to contradict, so it commits.
	it.raw = scan.Card{}
	if auto, _, _ := verdict(it); !auto {
		t.Error("absence of a year is why the pick is uncertain, not a veto")
	}
}

// The committed finish rides back on the result.
//
// The phone can only report whether it saw a star between the set code and the
// language, which is a weaker claim than what actually got written: plenty of
// foils print no marker at all, and a printing that does not come in foil
// cannot be one however the glyph read. So the finish travels with the price,
// from the side that has the catalog.
func TestResultCarriesTheCommittedFinish(t *testing.T) {
	foil := scryfall.Card{ID: "f", Name: "X", Set: "mh3", CollectorNumber: "301",
		Finishes: []string{"nonfoil", "foil"}}
	for _, tc := range []struct{ name, hint, want string }{
		{"a read marker commits and reports foil", "foil", "foil"},
		{"no marker falls back to nonfoil, and says so", "", "nonfoil"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := finishFromEvidence(foil, tc.hint)
			if got != tc.want {
				t.Fatalf("finish = %q, want %q", got, tc.want)
			}
		})
	}
	// A printing that never came in foil records nonfoil whatever was read,
	// and that is evidence rather than a default.
	nonfoilOnly := scryfall.Card{ID: "n", Name: "X", Set: "cmd",
		CollectorNumber: "1", Finishes: []string{"nonfoil"}}
	got, evidenced := finishFromEvidence(nonfoilOnly, "foil")
	if got != "nonfoil" || !evidenced {
		t.Errorf("finish = %q evidenced = %v, want nonfoil/true: the printing "+
			"has no foil to be", got, evidenced)
	}
}

// The parent believes the phone about why a capture happened.
//
// This replaced a guess. `fromNudge` was inferred here from a clock — whether a
// nudge had been sent in the last four seconds — and the comment on that window
// conceded the flaw: a real scan can race the nudge onto the wire. The phone
// takes three distinct code paths when it re-arms and now says which one.
func TestFireReasonBeatsTheClock(t *testing.T) {
	ev, fs := confidentFixture()
	for _, tc := range []struct {
		name, reason string
		nudgedRecent bool
		wantNudge    bool
	}{
		{"a placement during the nudge window is still a placement",
			scan.FireReplaced, true, false},
		{"a card leaving and another arriving is a placement",
			scan.FireRemoved, true, false},
		{"a nudge is a nudge even outside the window",
			scan.FireNudge, false, true},
		{"no reason falls back to the clock, for older helpers",
			"", true, true},
		{"no reason and no recent nudge is not an echo",
			"", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(context.Background(), fs, noopAdder, &fakeScanner{}, "", nil)
			clock := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
			m.now = func() time.Time { return clock }
			m, _ = openCapture(t, m)
			if tc.nudgedRecent {
				m.nudgeSentAt = clock
			}
			e := ev
			e.FireReason = tc.reason
			mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: e})
			if got := mm.(model).lastScanNudged; got != tc.wantNudge {
				t.Errorf("lastScanNudged = %v, want %v", got, tc.wantNudge)
			}
		})
	}
}

// An old card's serif type costs a glyph or two, and that must still commit.
//
// The case that moved the bar: Prodigal Sorcerer read at 0.88 with its year and
// border already agreeing on a printing, and queued. Pre-1998 titles are small
// serif type and the corpus is full of these — "Amrou Kichkin", "Sisters of che
// Flame" — where two characters of a short name is the whole difference.
//
// Both ends are asserted deliberately. A threshold is only meaningful if
// something still fails it, and 0.63 is a real queued read from a live session
// (Cement Shoes), not an invented number.
func TestUncorroboratedNameBarAdmitsOldSerifSlips(t *testing.T) {
	for _, tc := range []struct {
		name       string
		similarity float64
		wantAuto   bool
	}{
		{"a two-glyph slip on an old title", 0.88, true},
		{"comfortably read", 0.94, true},
		{"the shape of a false match", 0.79, false},
		{"barely related", 0.63, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			it := queueItem{
				canonical: "Prodigal Sorcerer",
				prints:    solRingPrints(),
				// Year and markings agree, but no number — so the name gate is
				// the thing being tested rather than bypassed.
				rank:  scanMatchYearAndMarks,
				match: cardname.Match{Similarity: tc.similarity},
				raw:   scan.Card{Confidence: 0.95},
			}
			auto, _, note := verdict(it)
			if auto != tc.wantAuto {
				t.Errorf("auto = %v at %.2f, want %v (note: %q)",
					auto, tc.similarity, tc.wantAuto, note)
			}
		})
	}
}

// dupCapture answers with the most recent sighting, not the oldest.
//
// It used to walk the slice forwards and return on the first match, which is
// the *oldest* row. That was invisible while the answer was only "is this a
// duplicate, yes or no", and became load-bearing the moment the age mattered:
// with a card banked five times, the age it reported was the age of the first
// sighting, so a floor measured against it would let every repeat through.
func TestDupCaptureReportsTheMostRecentSighting(t *testing.T) {
	now := time.Date(2026, 8, 5, 19, 21, 57, 0, time.UTC)
	var recent []recentCommit
	// The same printing seen three times, a second apart.
	for i := range 3 {
		at := now.Add(time.Duration(i-3) * time.Second)
		recent = recordCommit(recent, "skirk", "nonfoil", i, at, false)
	}
	prior, since, dup := dupCapture(recent, "skirk", now)
	if !dup {
		t.Fatal("three sightings inside the window should read as a duplicate")
	}
	if since != time.Second {
		t.Errorf("since = %v, want 1s — the newest sighting, not the oldest", since)
	}
	if prior.captureSeq != 2 {
		t.Errorf("captureSeq = %d, want 2 — the newest sighting's capture", prior.captureSeq)
	}
}

// touchCommit rolls the anchor forward so a card re-read once a second stays
// suppressed, rather than the third repeat ageing past a fixed floor.
func TestTouchCommitRollsTheAnchorForward(t *testing.T) {
	start := time.Date(2026, 8, 5, 19, 21, 57, 0, time.UTC)
	recent := recordCommit(nil, "skirk", "nonfoil", 1, start, false)

	// The Skirk burst's real gaps. Anchored on the original commit these sum
	// past three seconds by the third repeat; re-anchored on each sighting,
	// none of them ever does.
	at := start
	for _, gap := range []time.Duration{931, 1604, 932, 2595} {
		at = at.Add(gap * time.Millisecond)
		_, since, dup := dupCapture(recent, "skirk", at)
		if !dup {
			t.Fatalf("at +%v the card should still be inside the window", at.Sub(start))
		}
		if since >= sameCardFloor {
			t.Errorf("since = %v at +%v, want under the %v floor",
				since, at.Sub(start), sameCardFloor)
		}
		recent = touchCommit(recent, "skirk", at)
	}
	// And it never banked a second commit while doing it.
	if len(recent) != 1 {
		t.Errorf("recent has %d rows, want 1 — touching must not bank a commit", len(recent))
	}
}

// The fire reasons the Go side matches on are the strings the phone sends.
//
// `FireNudge` said "nudge" for its whole life while the phone's enum case is
// `nudged`, so the branch matching on it never once ran and nudge detection
// silently fell back to the clock the field was added to replace. It surfaced
// as an unreadable nudge re-look queueing for review: the phantom kill is
// gated on `fromNudge`, and `fromNudge` was never true.
//
// These strings are `RearmCause`'s raw values in
// scan/hoard-scan/Sources/CardKit/Trigger/Trigger.swift. Changing one without
// the other is invisible at both compilers and at runtime.
func TestFireReasonsMatchTheWireValues(t *testing.T) {
	for _, tc := range []struct{ constant, wire string }{
		{scan.FireRemoved, "removed"},
		{scan.FireReplaced, "replaced"},
		{scan.FireMoved, "moved"},
		{scan.FireNudge, "nudged"},
	} {
		if tc.constant != tc.wire {
			t.Errorf("constant %q does not match the phone's %q", tc.constant, tc.wire)
		}
	}
}

// The case the language read exists for, and the money it is worth.
//
// Scryfall keeps a foreign-only printing beside its English namesake under one
// collector number, separated by a marker the card does not print: `war/97` is
// Liliana, Dreadhorde General at $7.95 and `war/97★` the Japanese alternate art
// at $112.73. OCR reads "97" either way, so the number alone always picked the
// cheap one and wrote it to the collection without stopping.
func TestLanguagePicksTheForeignOnlySibling(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "en", Set: "war", CollectorNumber: "97", Lang: "en", ReleasedAt: "2019-05-03"},
		{ID: "ja", Set: "war", CollectorNumber: "97★", Lang: "ja", ReleasedAt: "2019-05-03"},
	}

	ranked, r := rankByScanStrength(prints, "war", "97", 0, "", "", "ja")
	if ranked[0].ID != "ja" {
		t.Errorf("picked %q, want the Japanese alternate art the card's own set row names", ranked[0].ID)
	}
	if r != scanMatchSetNumberAndLang {
		t.Errorf("rank = %v, want set+number+lang", r)
	}
	// It is corroborated evidence, so it commits like any other pinned match
	// rather than stopping the session.
	if !corroboratedPrinting(r) || !numberVerified(r) {
		t.Errorf("rank %v must count as corroborated and number-verified", r)
	}

	// An English read takes the unmarked row, as it always did.
	ranked, r = rankByScanStrength(prints, "war", "97", 0, "", "", "en")
	if ranked[0].ID != "en" || r != scanMatchSetNumberAndLang {
		t.Errorf("english: picked %q rank %v, want the unmarked row", ranked[0].ID, r)
	}
}

// A card whose language never read must behave exactly as before: the marked
// sibling is unreachable and the unmarked row wins. Silence is not evidence.
func TestNoLanguageReadKeepsTheOldAnswer(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "en", Set: "war", CollectorNumber: "97", Lang: "en", ReleasedAt: "2019-05-03"},
		{ID: "ja", Set: "war", CollectorNumber: "97★", Lang: "ja", ReleasedAt: "2019-05-03"},
	}
	ranked, r := rankByScanStrength(prints, "war", "97", 0, "", "", "")
	if ranked[0].ID != "en" {
		t.Errorf("picked %q, want the unmarked row when nothing said otherwise", ranked[0].ID)
	}
	if r != scanMatchSetAndNumber {
		t.Errorf("rank = %v, want plain set+number: no language agreed", r)
	}
}

// A catalog built before the language column stores none. Unknown on the
// catalog's side must not read as agreement, or every scan would claim a
// corroboration it never had.
func TestUnknownCatalogLanguageIsNotAgreement(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "en", Set: "war", CollectorNumber: "97", ReleasedAt: "2019-05-03"},
	}
	_, r := rankByScanStrength(prints, "war", "97", 0, "", "", "ja")
	if r != scanMatchSetAndNumber {
		t.Errorf("rank = %v, want plain set+number when the catalog has no language", r)
	}
}

// The marker trim must not let a foreign read reach a row of another number.
func TestLanguageDoesNotWidenTheNumberMatch(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "other", Set: "war", CollectorNumber: "98★", Lang: "ja", ReleasedAt: "2019-05-03"},
	}
	_, r := rankByScanStrength(prints, "war", "97", 0, "", "", "ja")
	if r != scanMatchNone {
		t.Errorf("rank = %v, want none: 98★ is not 97 in any language", r)
	}
}

// The measurement that shaped the rule above.
//
// Scored over scan/corpus the language read answers on a fifth of cards and is
// right four times in five, and its errors are systematic rather than random:
// a line of rules text parses as a set row and donates a language, so a plainly
// English card reads as Italian ("Balance of Power: said it, is en", live).
//
// What makes that safe to act on is the company it keeps. The same fabrication
// invents a set code beside the language, and an invented set code matches no
// printing — so the bogus language arrives attached to a set that rules it out,
// and the marked sibling never enters the running.
func TestAFabricatedLanguageCannotStealAnExactMatch(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "en", Set: "war", CollectorNumber: "97", Lang: "en", ReleasedAt: "2019-05-03"},
		{ID: "it", Set: "war", CollectorNumber: "97★", Lang: "it", ReleasedAt: "2019-05-03"},
	}
	// Prose donated "it" and the set code it came with ("PUT") matches nothing.
	ranked, r := rankByScanStrength(prints, "PUT", "97", 0, "", "", "it")
	if ranked[0].ID != "en" {
		t.Errorf("picked %q, want the exact match: the language came with a set code that checks out against nothing",
			ranked[0].ID)
	}
	if r != scanMatchNumberOnly {
		t.Errorf("rank = %v, want number-only: the set never agreed", r)
	}
}

// A marked sibling is claimed only when the set code agrees too, so a language
// alone can never reach one.
func TestAMarkedSiblingNeedsTheSetCodeAsWell(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "ja", Set: "war", CollectorNumber: "97★", Lang: "ja", ReleasedAt: "2019-05-03"},
	}
	if _, r := rankByScanStrength(prints, "", "97", 0, "", "", "ja"); r != scanMatchNone {
		t.Errorf("rank = %v, want none: no set code vouched for the language", r)
	}
	if _, r := rankByScanStrength(prints, "war", "97", 0, "", "", "ja"); r != scanMatchSetNumberAndLang {
		t.Errorf("rank = %v, want set+number+lang once the set agrees", r)
	}
}

// A number that matched nothing may simply be missing its leading digit.
//
// Live (scan/foil-corpus/session3-telemetry.log): Meltdown read `18`, matched
// none of its four printings, and queued as "printing unverified" — the card is
// mh3/418, and 418 is the only one of the four that ends in 18. The next
// session read the same physical card as 418 and committed it.
//
// Meltdown's real printings, from the catalog, so the "exactly one" guard is
// being exercised against the actual field rather than a convenient pair.
func TestTruncatedNumberMatchesByItsTail(t *testing.T) {
	meltdown := []scryfall.Card{
		{Name: "Meltdown", Set: "sld", CollectorNumber: "2296", ReleasedAt: "2025-11-17"},
		{Name: "Meltdown", Set: "mh3", CollectorNumber: "282", ReleasedAt: "2024-06-14"},
		{Name: "Meltdown", Set: "mh3", CollectorNumber: "418", ReleasedAt: "2024-06-14"},
		{Name: "Meltdown", Set: "usg", CollectorNumber: "203", ReleasedAt: "1998-10-12"},
	}
	ranked, r := rankByScanStrength(meltdown, "", "18", 0, "", "", "")
	if r != scanMatchNumberTail {
		t.Fatalf("rank = %v, want number-tail for 18 against 418", r)
	}
	if ranked[0].CollectorNumber != "418" {
		t.Errorf("front printing = %s, want the tail match 418", ranked[0].CollectorNumber)
	}

	// It is a repaired number, not a verified one. The distinction is what keeps
	// verdict's fallback-line veto and confidence floor standing over it.
	if numberVerified(scanMatchNumberTail) {
		t.Error("a tail match must not read as a verified number")
	}
	if corroboratedPrinting(scanMatchNumberTail) {
		t.Error("a tail match alone is one signal, not two")
	}

	// And it commits, which is the whole point — this is the evidence Meltdown
	// queued on.
	auto, _, note := verdict(queueItem{
		canonical: "Meltdown", prints: ranked, rank: r,
		match: cardname.Match{Exact: true},
	})
	if !auto {
		t.Errorf("a tail-matched printing should commit, queued with: %s", note)
	}
}

// The three misreads the tail match deliberately does not repair.
//
// Kept as a test rather than a comment because the temptation here is a fuzzy
// number match, and this is the measurement that says no: every one of these
// sits one edit from the right answer, and against fields this small an
// edit-distance rule would commit all three — two of them to the wrong printing.
// A tail is the only repair where every digit that was read is still true.
func TestNumberTailMatchLeavesSubstitutionsAlone(t *testing.T) {
	for _, tc := range []struct {
		name, read string
		prints     []scryfall.Card
	}{{
		// h2r/4, so OCR gained a digit rather than losing one.
		name: "Dress Down", read: "14",
		prints: []scryfall.Card{
			{Name: "Dress Down", Set: "plst", CollectorNumber: "MH2-39"},
			{Name: "Dress Down", Set: "h2r", CollectorNumber: "4"},
			{Name: "Dress Down", Set: "mh2", CollectorNumber: "39"},
			{Name: "Dress Down", Set: "mh2", CollectorNumber: "334"},
			{Name: "Dress Down", Set: "pmh2", CollectorNumber: "39s"},
		},
	}, {
		// mh3/421 — a 3 read for a 2, in the middle of the number.
		name: "Unstable Amulet", read: "431",
		prints: []scryfall.Card{
			{Name: "Unstable Amulet", Set: "mh3", CollectorNumber: "421"},
			{Name: "Unstable Amulet", Set: "mh3", CollectorNumber: "142"},
			{Name: "Unstable Amulet", Set: "mh3", CollectorNumber: "514"},
		},
	}, {
		// mh3/426 — a 0 read for a 6, and only two printings to choose between.
		name: "Lion Umbra", read: "420",
		prints: []scryfall.Card{
			{Name: "Lion Umbra", Set: "mh3", CollectorNumber: "426"},
			{Name: "Lion Umbra", Set: "mh3", CollectorNumber: "160"},
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			if _, r := rankByScanStrength(tc.prints, "", tc.read, 0, "", "", ""); r != scanMatchNone {
				t.Errorf("rank = %v, want none: %s is not a tail of any printing", r, tc.read)
			}
		})
	}
}

// Every guard on the tail match, each one the reason it is safe to run at all.
func TestNumberTailMatchFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prints []scryfall.Card
		number string
		year   int
		want   scanMatch
	}{{
		// Two printings end in 14, so which digits went missing is a guess with
		// no answer. It queues, exactly as it does today.
		name: "several tails match",
		prints: []scryfall.Card{
			{Name: "X", Set: "a", CollectorNumber: "414"},
			{Name: "X", Set: "b", CollectorNumber: "514"},
		},
		number: "14", want: scanMatchNone,
	}, {
		// The exact match is found first and the fallback never runs, so the
		// card numbered 14 wins over the card numbered 414. A number that
		// matched must never be second-guessed.
		name: "an exact match outranks its own tail",
		prints: []scryfall.Card{
			{Name: "X", Set: "a", CollectorNumber: "414"},
			{Name: "X", Set: "b", CollectorNumber: "14"},
		},
		number: "14", want: scanMatchNumberOnly,
	}, {
		// One digit against three is barely a claim — 1, 11, 21, 31 and 41 all
		// answer to it — so the "exactly one" guard would be doing all the work.
		name:   "one digit is too little to repair",
		prints: []scryfall.Card{{Name: "X", Set: "a", CollectorNumber: "418"}},
		number: "8", want: scanMatchNone,
	}, {
		// A tail is a question about the printed run of digits. `123a` is a
		// variant, not a card numbered 23 with something appended.
		name:   "a variant suffix is not a tail",
		prints: []scryfall.Card{{Name: "X", Set: "a", CollectorNumber: "123a"}},
		number: "23", want: scanMatchNone,
	}, {
		// The year disagreeing is positive evidence against, not absence of it.
		name: "the copyright year contradicts the repair",
		prints: []scryfall.Card{
			{Name: "X", Set: "a", CollectorNumber: "414", ReleasedAt: "2024-06-14"},
		},
		number: "14", year: 2003, want: scanMatchNone,
	}, {
		// And agreeing is the ordinary case, kept here so the year gate is shown
		// to admit as well as refuse.
		name: "the copyright year agrees",
		prints: []scryfall.Card{
			{Name: "X", Set: "a", CollectorNumber: "414", ReleasedAt: "2024-06-14"},
		},
		number: "14", year: 2024, want: scanMatchNumberTail,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			if _, r := rankByScanStrength(tc.prints, "", tc.number, tc.year, "", "", ""); r != tc.want {
				t.Errorf("rank = %v, want %v", r, tc.want)
			}
		})
	}
}

// A short number that matches exactly is untouched by any of this.
//
// Abiding Grace reads a bare `1` off its H2R copyright row and commits, live,
// every session. The two-digit floor is on the repair path only; putting it
// anywhere else would break the cards the repair exists to help.
func TestShortExactNumbersStillCommit(t *testing.T) {
	prints := []scryfall.Card{
		{Name: "Abiding Grace", Set: "h2r", CollectorNumber: "1", ReleasedAt: "2024-06-14"},
		{Name: "Abiding Grace", Set: "mh2", CollectorNumber: "5", ReleasedAt: "2021-06-18"},
	}
	if _, r := rankByScanStrength(prints, "", "1", 0, "", "", ""); r != scanMatchNumberOnly {
		t.Errorf("rank = %v, want number-only for a bare 1 that matches", r)
	}
}

// A card queues because its footer did not read, and the card is still under
// the camera at that moment. Look again before waiting out a swap.
//
// The failure this exists for is per-photograph, not per-card: measured across
// two sessions of one pile, every card that queued as "printing unverified" in
// one session read its collector number correctly in the other (Charitable Levy
// 390 and Unholy Heat 13 in session 3; Victimize 413, Consuming Corruption 407
// and Lion Umbra 426 in session 4 — each queued in the session it is not listed
// under). One more look roughly squares the per-capture failure rate.
func TestSecondLookOnlyForAnUnverifiedPrinting(t *testing.T) {
	prints := []scryfall.Card{
		{Name: "Unholy Heat", Set: "h2r", CollectorNumber: "13"},
		{Name: "Unholy Heat", Set: "mh2", CollectorNumber: "145"},
	}
	for _, tc := range []struct {
		name string
		it   queueItem
		want bool
	}{{
		name: "nothing pinned a printing",
		it:   queueItem{canonical: "Unholy Heat", prints: prints, rank: scanMatchNone},
		want: true,
	}, {
		// The read is fine and the printing is pinned; whatever queued it was
		// not the footer, so another photograph of the same footer buys nothing.
		name: "the printing verified",
		it:   queueItem{canonical: "Unholy Heat", prints: prints, rank: scanMatchNumberOnly},
		want: false,
	}, {
		// A repaired number is a pinned printing too.
		name: "a tail match pinned it",
		it:   queueItem{canonical: "Unholy Heat", prints: prints, rank: scanMatchNumberTail},
		want: false,
	}, {
		// No name to bound the retry against, so there is no retry.
		name: "nothing identified",
		it:   queueItem{prints: prints, rank: scanMatchNone},
		want: false,
	}, {
		// A lookup that found no printings is not a footer problem.
		name: "no printings at all",
		it:   queueItem{canonical: "Unholy Heat", rank: scanMatchNone},
		want: false,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			var m model
			if got := m.wantsSecondLook(tc.it); got != tc.want {
				t.Errorf("wantsSecondLook = %v, want %v", got, tc.want)
			}
		})
	}
}

// One look, then the card queues like anything else.
//
// The bound is what keeps a card that will never read from holding the session
// open re-photographing itself — the very cards this retry is for are the ones
// most likely to fail twice.
func TestSecondLookIsBoundedToOneAttempt(t *testing.T) {
	it := queueItem{
		canonical: "Dress Down",
		prints:    []scryfall.Card{{Name: "Dress Down", Set: "mh3", CollectorNumber: "414"}},
		rank:      scanMatchNone,
	}
	var m model
	if !m.wantsSecondLook(it) {
		t.Fatal("the first unverified queue should ask for another look")
	}
	m.secondLookFor = it.canonical
	if m.wantsSecondLook(it) {
		t.Error("the same card queueing again has had its look")
	}
	// A different card is a different run of bad reads.
	other := it
	other.canonical = "Charitable Levy"
	if !m.wantsSecondLook(other) {
		t.Error("the bound is per card, not per session")
	}
}

// The retry goes out the moment the phone says it is listening, and not before.
//
// There is no delay constant to test here and that is the point: the phone's own
// held→armed gap is bimodal — measured over one session's 18 captures, four at
// ~130ms and fourteen at 760-855ms — so any constant is either late for the fast
// half or fired into `held` for the slow half. A Rearm sent into `held` is a
// retry that silently never happens, which is the failure mode this shape rules
// out rather than tunes around.
func TestSecondLookWaitsForTheTriggerNotAClock(t *testing.T) {
	sess := &fakeSession{events: make(chan scan.Event, 8)}
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	mm, _ := m.onSession(sessionMsg{session: sess})
	got := mm.(model)
	got.autoCapable = true
	got.secondLookPending = true

	// `held` is the phone still holding the frame it just read. Asking now is
	// asking nobody.
	before := sess.rearms
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true,
		ev: scan.Event{Kind: scan.EventAuto, State: "held"}})
	got = mm.(model)
	if sess.rearms != before {
		t.Errorf("rearms = %d, want none while the trigger is held", sess.rearms-before)
	}
	if !got.secondLookPending {
		t.Error("the retry should still be pending after a held")
	}

	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true,
		ev: scan.Event{Kind: scan.EventAuto, State: "armed"}})
	got = mm.(model)
	if sess.rearms != before+1 {
		t.Fatalf("rearms = %d, want exactly one once the trigger armed", sess.rearms-before)
	}
	if got.secondLookPending {
		t.Error("the retry was sent and must not be pending any more")
	}

	// One retry, not a standing order: every later `armed` is the ordinary
	// rhythm of the session and must not re-fire it.
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true,
		ev: scan.Event{Kind: scan.EventAuto, State: "armed"}})
	if sess.rearms != before+1 {
		t.Errorf("rearms = %d, want the retry spent after one", sess.rearms-before)
	}
}
