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
			ranked, rank := rankByScanStrength(controlMagicPrints(), "", "", 1995, tc.border, "")
			if rank != tc.wantRank {
				t.Fatalf("rank = %v, want %v", rank, tc.wantRank)
			}
			if tc.wantSet != "" && ranked[0].Set != tc.wantSet {
				t.Errorf("led with %s, want %s", ranked[0].Set, tc.wantSet)
			}
		})
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
	ranked, rank := rankByScanStrength(manaLeak, "", "", 1998, "black", "")
	if rank != scanMatchYearAndMarks {
		t.Fatalf("rank = %v, want year+border: black rules out the gold printing", rank)
	}
	if ranked[0].Set != "sth" {
		t.Errorf("led with %s, want the black-bordered sth", ranked[0].Set)
	}
	// And the reverse still fails closed: white leaves the gold standing, and a
	// winner chosen by elimination is not confirmed by anything.
	if _, rank := rankByScanStrength(manaLeak, "", "", 1998, "white", ""); rank != scanMatchNone {
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
	if _, rank := rankByScanStrength(manaLeak, "", "", 1998, "white", ""); rank != scanMatchNone {
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
	if _, rank := rankByScanStrength(allWhite, "", "", 1995, "white", ""); rank != scanMatchNone {
		t.Error("a border matching every printing must settle nothing")
	}
	// A border that rules *everything* out disagrees with the whole catalog,
	// which is a reason to distrust the read rather than to pick from nothing.
	if _, rank := rankByScanStrength(allWhite, "", "", 1995, "black", ""); rank != scanMatchNone {
		t.Error("a border contradicting every printing must not commit")
	}
	// And the border is never consulted without a year to narrow the field
	// first, because one bit against a whole catalog settles nothing.
	if _, rank := rankByScanStrength(controlMagicPrints(), "", "", 0, "white", ""); rank != scanMatchNone {
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
	displaced, ok := m.upgradeQueued(strong)
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
			if _, ok := m.upgradeQueued(queueItem{
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
	if _, ok := m.upgradeQueued(queueItem{
		canonical: "Control Magic", rank: scanMatchSetAndNumber, fromNudge: true,
	}); ok {
		t.Error("a different card must not displace a queued entry")
	}
	// And an unresolved read has no name to match on.
	if _, ok := m.upgradeQueued(queueItem{rank: scanMatchSetAndNumber, fromNudge: true}); ok {
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
	if _, ok := m.upgradeQueued(queueItem{
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
	if ranked, rank := rankByScanStrength(manaLeak, "", "", 1998, "white", ""); rank != scanMatchNone {
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
	ranked, rank := rankByScanStrength(controlMagicPrints(), "", "", 1995, "white", "")
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
	ranked, rank := rankByScanStrength(epiphany, "", "", 2016, "", "nonfoil")
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
	ranked, rank := rankByScanStrength(baral, "", "", 2017, "", "nonfoil")
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
	ranked, rank := rankByScanStrength(cards, "", "", 2017, "", "foil")
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
	if _, rank := rankByScanStrength(promoPair, "", "", 2016, "", ""); rank != scanMatchNone {
		t.Error("an unread finish must settle nothing")
	}
	// A foil read fits both printings, so it separates neither.
	if _, rank := rankByScanStrength(promoPair, "", "", 2016, "", "foil"); rank != scanMatchNone {
		t.Error("a marker both printings share must settle nothing")
	}
	// And a finish no printing offers is a read to distrust, not a licence to
	// pick from an empty field.
	if _, rank := rankByScanStrength(promoPair, "", "", 2016, "", "etched"); rank != scanMatchNone {
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
	ranked, rank := rankByScanStrength(cards, "", "", 2003, "white", "nonfoil")
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
	ranked, rank := rankByScanStrength(cultivate, "", "", 2011, "", "foil")
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
	if _, rank := rankByScanStrength(epiphany, "", "", 2016, "", "foil"); rank != scanMatchNone {
		t.Error("both printings come in foil, so a star settles nothing")
	}
	// And the bullet still does the work it can.
	if _, rank := rankByScanStrength(epiphany, "", "", 2016, "", "nonfoil"); rank != scanMatchYearAndMarks {
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
	if _, rank := rankByScanStrength(zahid, "", "76", 2018, "", ""); rank != scanMatchNumberAmbiguous {
		t.Errorf("rank = %v, want number-ambiguous with no marker to break the tie", rank)
	}
	// With it, the foil-only promo is excluded and the year corroborates.
	ranked, rank := rankByScanStrength(zahid, "", "76", 2018, "", "nonfoil")
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
	if _, rank := rankByScanStrength(shared, "", "5", 2018, "", "nonfoil"); rank != scanMatchNumberAmbiguous {
		t.Error("a marker both printings share must not settle a number tie")
	}
	// An exact set match still wins outright; the markings never override it.
	if _, rank := rankByScanStrength(shared, "bbb", "5", 2018, "", "nonfoil"); rank != scanMatchSetAndNumber {
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
	auto, _, note := verdict(it)
	if !auto {
		t.Errorf("an ambiguous number should commit the front printing, queued: %s", note)
	}
}

// But never against a year the card actually printed.
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
	seq, since, dup := dupCapture(recent, "skirk", "nonfoil", now)
	if !dup {
		t.Fatal("three sightings inside the window should read as a duplicate")
	}
	if since != time.Second {
		t.Errorf("since = %v, want 1s — the newest sighting, not the oldest", since)
	}
	if seq != 2 {
		t.Errorf("captureSeq = %d, want 2 — the newest sighting's capture", seq)
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
		_, since, dup := dupCapture(recent, "skirk", "nonfoil", at)
		if !dup {
			t.Fatalf("at +%v the card should still be inside the window", at.Sub(start))
		}
		if since >= sameCardFloor {
			t.Errorf("since = %v at +%v, want under the %v floor",
				since, at.Sub(start), sameCardFloor)
		}
		recent = touchCommit(recent, "skirk", "nonfoil", at)
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
