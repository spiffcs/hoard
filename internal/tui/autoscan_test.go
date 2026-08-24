package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/cardname"
	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scan"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func TestNudgeDelayClearsTheObservedSwap(t *testing.T) {

	if nudgeDelay < 3856*time.Millisecond {
		t.Errorf("nudge delay %v is below the fastest observed swap (3856ms), "+
			"so it will fire mid-swap", nudgeDelay)
	}

	if nudgeDelay > 7047*time.Millisecond {
		t.Errorf("nudge delay %v exceeds the p75 swap (7047ms), so a parked "+
			"card waits on the timer rather than the timer catching it", nudgeDelay)
	}
}

func TestNudgeBacksOffAndCaps(t *testing.T) {
	if got := nudgeBackoff(0); got != nudgeDelay {
		t.Errorf("nudgeBackoff(0) = %v, want the base delay %v", got, nudgeDelay)
	}

	if got, want := nudgeBackoff(1), 2*nudgeDelay; got != want {
		t.Errorf("nudgeBackoff(1) = %v, want %v", got, want)
	}

	capped := nudgeDelay << nudgeBackoffSteps
	if got := nudgeBackoff(nudgeBackoffSteps + 5); got != capped {
		t.Errorf("nudgeBackoff past the cap = %v, want %v", got, capped)
	}
	if capped > time.Minute {
		t.Errorf("the capped recheck is %v, longer than anyone waits before "+
			"deciding the scanner has stopped working", capped)
	}
}

func TestNameTimeoutFitsTheShutterToResultBudget(t *testing.T) {

	const budget = 700 * time.Millisecond
	const phoneReadMedian = 447 * time.Millisecond
	if phoneReadMedian+nameTimeout > budget {
		t.Errorf("median phone read (%v) plus name timeout %v exceeds the %v budget",
			phoneReadMedian, nameTimeout, budget)
	}
}

func TestYearAndBorderPicksBetweenSameNumberPrintings(t *testing.T) {
	for _, tc := range []struct {
		name, border, wantSet string
		wantRank              scanMatch
	}{
		{"white picks the white-bordered printing", "white", "4ed", scanMatchYearAndMarks},
		{"black picks the black-bordered printing", "black", "4bb", scanMatchYearAndMarks},

		{"no border settles nothing", "", "", scanMatchNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ranked, rank := rankByScanStrength(controlMagicPrints(), "", "", 1995, tc.border, "", "", "", nil)
			if rank != tc.wantRank {
				t.Fatalf("rank = %v, want %v", rank, tc.wantRank)
			}
			if tc.wantSet != "" && ranked[0].Set != tc.wantSet {
				t.Errorf("led with %s, want %s", ranked[0].Set, tc.wantSet)
			}
		})
	}
}

func TestFrameFamilySeparatesRetroTwins(t *testing.T) {
	twins := []scryfall.Card{
		{ID: "reg", Name: "Brainsurge", Set: "mh3", CollectorNumber: "106",
			ReleasedAt: "2024-06-14", Frame: "2015", Finishes: []string{"nonfoil", "foil"}},
		{ID: "retro", Name: "Brainsurge", Set: "mh3", CollectorNumber: "399",
			ReleasedAt: "2024-06-14", Frame: "1997", Finishes: []string{"nonfoil", "foil"}},
	}

	ranked, rank := rankByScanStrength(twins, "", "", 2024, "", "", "", "retro", nil)
	if rank != scanMatchYearAndFrame {
		t.Fatalf("rank = %v, want year+frame", rank)
	}
	if ranked[0].ID != "retro" {
		t.Errorf("led with %s, want the retro-frame row", ranked[0].ID)
	}

	ranked, rank = rankByScanStrength(twins, "", "", 0, "", "", "", "retro", nil)
	if rank != scanMatchYearAndFrame || ranked[0].ID != "retro" {
		t.Errorf("rank = %v head = %s, want year+frame with no year read",
			rank, ranked[0].ID)
	}

	if _, rank := rankByScanStrength(twins, "", "", 2024, "", "", "", "", nil); rank != scanMatchNone {
		t.Errorf("rank = %v, want none without a frame read", rank)
	}

	unknown := []scryfall.Card{
		{ID: "reg", Name: "Brainsurge", Set: "mh3", CollectorNumber: "106",
			ReleasedAt: "2024-06-14", Frame: "2015", Finishes: []string{"nonfoil"}},
		{ID: "mystery", Name: "Brainsurge", Set: "mh3", CollectorNumber: "399",
			ReleasedAt: "2024-06-14", Finishes: []string{"nonfoil"}},
	}
	if _, rank := rankByScanStrength(unknown, "", "", 2024, "", "", "", "retro", nil); rank != scanMatchNone {
		t.Errorf("rank = %v, want none: an unknown frame must not win by elimination", rank)
	}

	crossSet := []scryfall.Card{
		{ID: "reg", Name: "Victimize", Set: "mh3", CollectorNumber: "106",
			ReleasedAt: "2024-06-14", Frame: "2015", Finishes: []string{"nonfoil"}},
		{ID: "mh3r", Name: "Victimize", Set: "mh3", CollectorNumber: "413",
			ReleasedAt: "2024-06-14", Frame: "1997", Finishes: []string{"nonfoil", "foil"}},
		{ID: "spgr", Name: "Victimize", Set: "spg", CollectorNumber: "13",
			ReleasedAt: "2024-06-14", Frame: "1997", Finishes: []string{"nonfoil", "foil"}},
	}
	if _, rank := rankByScanStrength(crossSet, "", "", 2024, "", "", "", "retro", nil); rank != scanMatchNone {
		t.Errorf("rank = %v, want none: two frame-agreeing rows and no prior is still a tie", rank)
	}
	ranked, rank = rankByScanStrength(crossSet, "", "", 2024, "", "", "", "retro", []string{"mh3"})
	if rank != scanMatchYearAndFrame || ranked[0].ID != "mh3r" {
		t.Errorf("rank = %v head = %s, want year+frame picking the session's set",
			rank, ranked[0].ID)
	}

	if _, rank := rankByScanStrength(crossSet, "", "", 2024, "", "", "", "", []string{"mh3"}); rank != scanMatchNone {
		t.Errorf("rank = %v, want none: the prior must never pick without physical evidence", rank)
	}
}

func TestAnUnmatchedNumberFallsBackToTheYear(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "retro", Name: "Lion Umbra", Set: "mh3", CollectorNumber: "426",
			ReleasedAt: "2024-06-14", BorderColor: "black", Finishes: []string{"nonfoil", "foil"}},
		{ID: "old", Name: "Lion Umbra", Set: "arb", CollectorNumber: "12",
			ReleasedAt: "2009-04-30", BorderColor: "black", Finishes: []string{"nonfoil", "foil"}},
	}
	ranked, rank := rankByScanStrength(prints, "", "420", 2024, "black", "foil", "", "", nil)
	if rank != scanMatchYearOnly {
		t.Fatalf("rank = %v, want year-only: 420 matches nothing but 2024 names one printing", rank)
	}
	if ranked[0].Set != "mh3" {
		t.Errorf("led with %s, want the 2024 printing", ranked[0].Set)
	}

	lone := prints[:1]
	if _, rank := rankByScanStrength(lone, "", "999", 0, "", "", "", "", nil); rank != scanMatchNone {
		t.Errorf("rank = %v, want none: a lone printing does not rescue a number that matched nothing", rank)
	}

	if _, rank := rankByScanStrength(lone, "", "", 0, "", "", "", "", nil); rank != scanMatchSinglePrint {
		t.Errorf("rank = %v, want single-print", rank)
	}
}

func TestBlackExcludesGoldButWhiteDoesNot(t *testing.T) {
	manaLeak := []scryfall.Card{
		{ID: "gold", Name: "Mana Leak", Set: "wc98", CollectorNumber: "rb36",
			ReleasedAt: "1998-08-12", BorderColor: "gold"},
		{ID: "black", Name: "Mana Leak", Set: "sth", CollectorNumber: "36",
			ReleasedAt: "1998-03-02", BorderColor: "black"},
	}
	ranked, rank := rankByScanStrength(manaLeak, "", "", 1998, "black", "", "", "", nil)
	if rank != scanMatchYearAndMarks {
		t.Fatalf("rank = %v, want year+border: black rules out the gold printing", rank)
	}
	if ranked[0].Set != "sth" {
		t.Errorf("led with %s, want the black-bordered sth", ranked[0].Set)
	}

	if _, rank := rankByScanStrength(manaLeak, "", "", 1998, "white", "", "", "", nil); rank != scanMatchNone {
		t.Errorf("rank = %v, want scanMatchNone: white cannot exclude gold", rank)
	}

	if !borderRulesOut(manaLeak[0], "black") {
		t.Error("a black read should exclude a gold printing")
	}
	if borderRulesOut(manaLeak[0], "white") {
		t.Error("a white read must not exclude a gold printing")
	}
}

func TestBorderlessIsNeverExcluded(t *testing.T) {
	bl := scryfall.Card{ID: "b", Name: "X", Set: "x", BorderColor: "borderless"}
	for _, read := range []string{"white", "black"} {
		if borderRulesOut(bl, read) {
			t.Errorf("a %s read excluded a borderless printing", read)
		}
	}
}

func TestBorderNeverRulesOutAColourItCannotRead(t *testing.T) {
	manaLeak := []scryfall.Card{
		{ID: "a", Name: "Mana Leak", Set: "sth", CollectorNumber: "36",
			ReleasedAt: "1998-03-02", BorderColor: "black"},
		{ID: "b", Name: "Mana Leak", Set: "wc98", CollectorNumber: "rb36",
			ReleasedAt: "1998-08-12", BorderColor: "gold"},
	}

	if _, rank := rankByScanStrength(manaLeak, "", "", 1998, "white", "", "", "", nil); rank != scanMatchNone {
		t.Errorf("rank = %v, want scanMatchNone: white cannot exclude gold", rank)
	}
	if borderRulesOut(manaLeak[1], "white") {
		t.Error("a gold printing was ruled out by a white read")
	}
}

func TestYearAndBorderFailsClosed(t *testing.T) {

	allWhite := []scryfall.Card{
		{ID: "a", Name: "X", Set: "p1", CollectorNumber: "1",
			ReleasedAt: "1995-01-01", BorderColor: "white"},
		{ID: "b", Name: "X", Set: "p2", CollectorNumber: "2",
			ReleasedAt: "1995-01-01", BorderColor: "white"},
	}
	if _, rank := rankByScanStrength(allWhite, "", "", 1995, "white", "", "", "", nil); rank != scanMatchNone {
		t.Error("a border matching every printing must settle nothing")
	}

	if _, rank := rankByScanStrength(allWhite, "", "", 1995, "black", "", "", "", nil); rank != scanMatchNone {
		t.Error("a border contradicting every printing must not commit")
	}

	if _, rank := rankByScanStrength(controlMagicPrints(), "", "", 0, "white", "", "", "", nil); rank != scanMatchNone {
		t.Error("a border with no year must settle nothing")
	}
}

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

	it.rank = scanMatchNone
	if auto, _, _ := verdict(it); auto {
		t.Error("a rank that pinned no printing must still queue")
	}
}

func TestJustPairedPhoneOpensWithoutADeviceList(t *testing.T) {
	devices := []scan.Device{
		cam("spare", "Spare iPhone", scan.KindRemote),
		cam("phone", "Billionaires are Parasites", scan.KindRemote),
	}
	sc := &fakeScanner{devices: devices}
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, sc, "", nil)
	m.cameraID = "phone"
	m.cameraName = "Billionaires are Parasites"

	mm, cmd := m.onPaired(pairedMsg{})
	got := mm.(model)
	if got.state == stateCameraPick {
		t.Error("the phone just paired; it should open rather than be offered")
	}
	if got.cameraID != "phone" {
		t.Errorf("opened %q, want the phone that was just paired", got.cameraID)
	}

	if got.justPairedID != "" {
		t.Errorf("the just-paired mark must be consumed by the scan that used it, got %q",
			got.justPairedID)
	}
	runCmds(cmd)
	if sc.listed != 0 {
		t.Errorf("the network was browsed %d time(s) for a phone the pairing just reached", sc.listed)
	}
	if sc.usedDevice != "phone" {
		t.Errorf("opened %q, want the just-paired phone", sc.usedDevice)
	}

	mm, _ = got.onCameras(camerasMsg{devices: devices})
	if mm.(model).state != stateCameraPick {
		t.Error("a later device list should offer the picker again")
	}
}

func TestJustPairedMarkIsConsumedWhenTheOpenFails(t *testing.T) {
	sc := &fakeScanner{
		devices: []scan.Device{
			cam("spare", "Spare iPhone", scan.KindRemote),
			cam("other", "Someone Else's iPhone", scan.KindRemote),
		},
		openErr: errors.New(`"phone" is not on this network right now`),
	}
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, sc, "", nil)
	m.justPairedID = "phone"

	runCmds(m.beginScan())
	if m.justPairedID != "" {
		t.Error("the mark must not survive the scan that used it")
	}
	if sc.usedDevice != "phone" {
		t.Errorf("opened %q, want the just-paired phone", sc.usedDevice)
	}

	runCmds(m.beginScan())
	if sc.listed != 1 {
		t.Errorf("a later scan should ask for a device list, listed = %d", sc.listed)
	}
}

func TestEmptyAutoCaptureIsSilent(t *testing.T) {
	sc := &fakeScanner{}
	base := newModel(context.Background(), fakeSearcher{}, noopAdder, sc, "", nil)

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

func TestUpgradeOnlyMatchesTheSameCard(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	m.review = []queueItem{{id: 1, canonical: "Prodigal Sorcerer", rank: scanMatchNone}}
	if _, ok := m.upgradeQueued(&queueItem{
		canonical: "Control Magic", rank: scanMatchSetAndNumber, fromNudge: true,
	}); ok {
		t.Error("a different card must not displace a queued entry")
	}

	if _, ok := m.upgradeQueued(&queueItem{rank: scanMatchSetAndNumber, fromNudge: true}); ok {
		t.Error("an unnamed read must not displace anything")
	}
}

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

func TestBorderWinnerMustMatchNotMerelySurvive(t *testing.T) {
	manaLeak := []scryfall.Card{
		{ID: "gold", Name: "Mana Leak", Set: "wc98", CollectorNumber: "rb36",
			ReleasedAt: "1998-08-12", BorderColor: "gold"},
		{ID: "black", Name: "Mana Leak", Set: "sth", CollectorNumber: "36",
			ReleasedAt: "1998-03-02", BorderColor: "black"},
	}

	if ranked, rank := rankByScanStrength(manaLeak, "", "", 1998, "white", "", "", "", nil); rank != scanMatchNone {
		t.Errorf("rank = %v leading %s/%s, want scanMatchNone: the survivor is a "+
			"colour the reader cannot read, so nothing confirmed it",
			rank, ranked[0].Set, ranked[0].CollectorNumber)
	}

}

func TestBorderWinnerMatchingStillCommits(t *testing.T) {
	ranked, rank := rankByScanStrength(controlMagicPrints(), "", "", 1995, "white", "", "", "", nil)
	if rank != scanMatchYearAndMarks {
		t.Fatalf("rank = %v, want year+border", rank)
	}
	if ranked[0].Set != "4ed" || !strings.EqualFold(ranked[0].BorderColor, "white") {
		t.Errorf("led with %s (%s), want the white-bordered 4ed",
			ranked[0].Set, ranked[0].BorderColor)
	}
}

func TestNonfoilRulesOutTheFoilOnlySetPromo(t *testing.T) {
	epiphany := []scryfall.Card{
		{ID: "promo", Name: "Epiphany at the Drownyard", Set: "psoi",
			CollectorNumber: "59s", ReleasedAt: "2016-04-08",
			Finishes: []string{"foil"}},
		{ID: "regular", Name: "Epiphany at the Drownyard", Set: "soi",
			CollectorNumber: "59", ReleasedAt: "2016-04-08",
			Finishes: []string{"nonfoil", "foil"}},
	}
	ranked, rank := rankByScanStrength(epiphany, "", "", 2016, "", "nonfoil", "", "", nil)
	if rank != scanMatchYearAndMarks {
		t.Fatalf("rank = %v, want year+marks: a nonfoil bullet cannot be a "+
			"foil-only promo", rank)
	}
	if ranked[0].Set != "soi" {
		t.Errorf("led with %s, want the regular soi printing", ranked[0].Set)
	}
}

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
	ranked, rank := rankByScanStrength(baral, "", "", 2017, "", "nonfoil", "", "", nil)
	if rank != scanMatchYearAndMarks {
		t.Fatalf("rank = %v, want year+marks", rank)
	}
	if ranked[0].Set != "aer" {
		t.Errorf("led with %s, want aer", ranked[0].Set)
	}
}

func TestFoilRulesOutNonfoilOnlyPrintings(t *testing.T) {
	cards := []scryfall.Card{
		{ID: "a", Name: "X", Set: "aaa", CollectorNumber: "1",
			ReleasedAt: "2017-01-20", Finishes: []string{"nonfoil"}},
		{ID: "b", Name: "X", Set: "bbb", CollectorNumber: "2",
			ReleasedAt: "2017-01-20", Finishes: []string{"nonfoil", "foil"}},
	}
	ranked, rank := rankByScanStrength(cards, "", "", 2017, "", "foil", "", "", nil)
	if rank != scanMatchYearAndMarks || ranked[0].Set != "bbb" {
		t.Errorf("rank = %v leading %s, want year+marks on bbb", rank, ranked[0].Set)
	}
}

func TestFinishNarrowingFailsClosed(t *testing.T) {
	promoPair := []scryfall.Card{
		{ID: "promo", Name: "X", Set: "p", CollectorNumber: "1s",
			ReleasedAt: "2016-04-08", Finishes: []string{"foil"}},
		{ID: "regular", Name: "X", Set: "r", CollectorNumber: "1",
			ReleasedAt: "2016-04-08", Finishes: []string{"nonfoil", "foil"}},
	}

	if _, rank := rankByScanStrength(promoPair, "", "", 2016, "", "", "", "", nil); rank != scanMatchNone {
		t.Error("an unread finish must settle nothing")
	}

	if _, rank := rankByScanStrength(promoPair, "", "", 2016, "", "foil", "", "", nil); rank != scanMatchNone {
		t.Error("a marker both printings share must settle nothing")
	}

	if _, rank := rankByScanStrength(promoPair, "", "", 2016, "", "etched", "", "", nil); rank != scanMatchNone {
		t.Error("a finish no printing has must not commit")
	}
}

func TestBorderAndFinishNarrowTogether(t *testing.T) {
	cards := []scryfall.Card{
		{ID: "a", Name: "X", Set: "aaa", CollectorNumber: "1", ReleasedAt: "2003-01-01",
			BorderColor: "black", Finishes: []string{"foil"}},
		{ID: "b", Name: "X", Set: "bbb", CollectorNumber: "2", ReleasedAt: "2003-01-01",
			BorderColor: "white", Finishes: []string{"nonfoil", "foil"}},
		{ID: "c", Name: "X", Set: "ccc", CollectorNumber: "3", ReleasedAt: "2003-01-01",
			BorderColor: "white", Finishes: []string{"foil"}},
	}

	ranked, rank := rankByScanStrength(cards, "", "", 2003, "white", "nonfoil", "", "", nil)
	if rank != scanMatchYearAndMarks || ranked[0].Set != "bbb" {
		t.Errorf("rank = %v leading %s, want year+marks on bbb", rank, ranked[0].Set)
	}
}

func TestFoilNamesThePromoWhenItIsTheOnlyFoil(t *testing.T) {
	cultivate := []scryfall.Card{
		{ID: "cmd", Name: "Cultivate", Set: "cmd", CollectorNumber: "148",
			ReleasedAt: "2011-06-17", Finishes: []string{"nonfoil"}},
		{ID: "promo", Name: "Cultivate", Set: "f11", CollectorNumber: "8",
			ReleasedAt: "2011-01-01", Finishes: []string{"foil"},
			PromoTypes: []string{"fnm"}},
	}
	ranked, rank := rankByScanStrength(cultivate, "", "", 2011, "", "foil", "", "", nil)
	if rank != scanMatchYearAndMarks {
		t.Fatalf("rank = %v, want year+marks: a star cannot be a printing that "+
			"never came in foil", rank)
	}
	if ranked[0].Set != "f11" {
		t.Errorf("led with %s, want the foil promo f11", ranked[0].Set)
	}
}

func TestFoilCannotSeparateTwoFoilablePrintings(t *testing.T) {
	epiphany := []scryfall.Card{
		{ID: "promo", Name: "Epiphany at the Drownyard", Set: "psoi",
			CollectorNumber: "59s", ReleasedAt: "2016-04-08",
			Finishes: []string{"foil"}, PromoTypes: []string{"setpromo"}},
		{ID: "regular", Name: "Epiphany at the Drownyard", Set: "soi",
			CollectorNumber: "59", ReleasedAt: "2016-04-08",
			Finishes: []string{"nonfoil", "foil"}},
	}
	if _, rank := rankByScanStrength(epiphany, "", "", 2016, "", "foil", "", "", nil); rank != scanMatchNone {
		t.Error("both printings come in foil, so a star settles nothing")
	}

	if _, rank := rankByScanStrength(epiphany, "", "", 2016, "", "nonfoil", "", "", nil); rank != scanMatchYearAndMarks {
		t.Error("a bullet must still rule out the foil-only promo")
	}
}

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

	if _, rank := rankByScanStrength(zahid, "", "76", 2018, "", "", "", "", nil); rank != scanMatchNumberAmbiguous {
		t.Errorf("rank = %v, want number-ambiguous with no marker to break the tie", rank)
	}

	ranked, rank := rankByScanStrength(zahid, "", "76", 2018, "", "nonfoil", "", "", nil)
	if rank != scanMatchNumberAndYear {
		t.Fatalf("rank = %v, want number+year: a bullet cannot be a foil-only promo", rank)
	}
	if ranked[0].Set != "dom" {
		t.Errorf("led with %s/%s, want dom/76", ranked[0].Set, ranked[0].CollectorNumber)
	}
}

func TestNumberTieNarrowingFailsClosed(t *testing.T) {
	shared := []scryfall.Card{
		{ID: "a", Name: "X", Set: "aaa", CollectorNumber: "5",
			ReleasedAt: "2018-01-01", Finishes: []string{"nonfoil", "foil"}},
		{ID: "b", Name: "X", Set: "bbb", CollectorNumber: "5",
			ReleasedAt: "2018-01-01", Finishes: []string{"nonfoil", "foil"}},
	}
	if _, rank := rankByScanStrength(shared, "", "5", 2018, "", "nonfoil", "", "", nil); rank != scanMatchNumberAmbiguous {
		t.Error("a marker both printings share must not settle a number tie")
	}

	if _, rank := rankByScanStrength(shared, "bbb", "5", 2018, "", "nonfoil", "", "", nil); rank != scanMatchSetAndNumber {
		t.Error("set+number must remain the strongest evidence")
	}
}

func TestAmbiguousNumberCommitsTheFrontPrinting(t *testing.T) {

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
	auto, fin, note := verdict(it)
	if !auto {
		t.Errorf("an ambiguous number should commit the front printing, queued: %s", note)
	}

	if fin != finish.Nonfoil {
		t.Errorf("fin = %q, want the nonfoil default", fin)
	}
}

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
	if auto, fin, note := verdict(it); !auto || fin != finish.Nonfoil {
		t.Errorf("auto=%v fin=%q note=%q, want a nonfoil commit", auto, fin, note)
	}

	it.finishHint = "foil"
	if auto, fin, note := verdict(it); !auto || fin != finish.Foil {
		t.Errorf("auto=%v fin=%q note=%q, want a foil commit", auto, fin, note)
	}

	nonfoilOnly := both
	nonfoilOnly.Finishes = []string{"nonfoil"}
	it = base
	it.prints = []scryfall.Card{nonfoilOnly}
	if auto, fin, _ := verdict(it); !auto || fin != finish.Nonfoil {
		t.Errorf("auto=%v fin=%q, want a nonfoil commit", auto, fin)
	}
}

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

	it.raw = scan.Card{}
	if auto, _, _ := verdict(it); !auto {
		t.Error("absence of a year is why the pick is uncertain, not a veto")
	}
}

func TestResultCarriesTheCommittedFinish(t *testing.T) {
	foil := scryfall.Card{ID: "f", Name: "X", Set: "mh3", CollectorNumber: "301",
		Finishes: []string{"nonfoil", "foil"}}
	for _, tc := range []struct {
		name, hint string
		want       finish.Finish
	}{
		{"a read marker commits and reports foil", "foil", finish.Foil},
		{"no marker falls back to nonfoil, and says so", "", finish.Nonfoil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := finishFromEvidence(foil, tc.hint)
			if got != tc.want {
				t.Fatalf("finish = %q, want %q", got, tc.want)
			}
		})
	}

	nonfoilOnly := scryfall.Card{ID: "n", Name: "X", Set: "cmd",
		CollectorNumber: "1", Finishes: []string{"nonfoil"}}
	got, evidenced := finishFromEvidence(nonfoilOnly, "foil")
	if got != finish.Nonfoil || !evidenced {
		t.Errorf("finish = %q evidenced = %v, want nonfoil/true: the printing "+
			"has no foil to be", got, evidenced)
	}
}

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
		{"a nudge inside the window is an echo",
			scan.FireNudge, true, true},
		{"a nudge outside the window is a card someone put down",
			scan.FireNudge, false, false},
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

func TestDupCaptureReportsTheMostRecentSighting(t *testing.T) {
	now := time.Date(2026, 8, 5, 19, 21, 57, 0, time.UTC)
	var recent []recentCommit

	for i := range 3 {
		at := now.Add(time.Duration(i-3) * time.Second)
		recent = recordCommit(recent, scryfall.Card{ID: "skirk"}, finish.Nonfoil, i, at, false, false)
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

func TestTouchCommitRollsTheAnchorForward(t *testing.T) {
	start := time.Date(2026, 8, 5, 19, 21, 57, 0, time.UTC)
	recent := recordCommit(nil, scryfall.Card{ID: "skirk"}, finish.Nonfoil, 1, start, false, false)

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

	if len(recent) != 1 {
		t.Errorf("recent has %d rows, want 1 — touching must not bank a commit", len(recent))
	}
}

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

func TestLanguagePicksTheForeignOnlySibling(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "en", Set: "war", CollectorNumber: "97", Lang: "en", ReleasedAt: "2019-05-03"},
		{ID: "ja", Set: "war", CollectorNumber: "97★", Lang: "ja", ReleasedAt: "2019-05-03"},
	}

	ranked, r := rankByScanStrength(prints, "war", "97", 0, "", "", "ja", "", nil)
	if ranked[0].ID != "ja" {
		t.Errorf("picked %q, want the Japanese alternate art the card's own set row names", ranked[0].ID)
	}
	if r != scanMatchSetNumberAndLang {
		t.Errorf("rank = %v, want set+number+lang", r)
	}

	if !corroboratedPrinting(r) || !numberVerified(r) {
		t.Errorf("rank %v must count as corroborated and number-verified", r)
	}

	ranked, r = rankByScanStrength(prints, "war", "97", 0, "", "", "en", "", nil)
	if ranked[0].ID != "en" || r != scanMatchSetNumberAndLang {
		t.Errorf("english: picked %q rank %v, want the unmarked row", ranked[0].ID, r)
	}
}

func TestNoLanguageReadKeepsTheOldAnswer(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "en", Set: "war", CollectorNumber: "97", Lang: "en", ReleasedAt: "2019-05-03"},
		{ID: "ja", Set: "war", CollectorNumber: "97★", Lang: "ja", ReleasedAt: "2019-05-03"},
	}
	ranked, r := rankByScanStrength(prints, "war", "97", 0, "", "", "", "", nil)
	if ranked[0].ID != "en" {
		t.Errorf("picked %q, want the unmarked row when nothing said otherwise", ranked[0].ID)
	}
	if r != scanMatchSetAndNumber {
		t.Errorf("rank = %v, want plain set+number: no language agreed", r)
	}
}

func TestUnknownCatalogLanguageIsNotAgreement(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "en", Set: "war", CollectorNumber: "97", ReleasedAt: "2019-05-03"},
	}
	_, r := rankByScanStrength(prints, "war", "97", 0, "", "", "ja", "", nil)
	if r != scanMatchSetAndNumber {
		t.Errorf("rank = %v, want plain set+number when the catalog has no language", r)
	}
}

func TestLanguageDoesNotWidenTheNumberMatch(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "other", Set: "war", CollectorNumber: "98★", Lang: "ja", ReleasedAt: "2019-05-03"},
	}
	_, r := rankByScanStrength(prints, "war", "97", 0, "", "", "ja", "", nil)
	if r != scanMatchNone {
		t.Errorf("rank = %v, want none: 98★ is not 97 in any language", r)
	}
}

func TestAFabricatedLanguageCannotStealAnExactMatch(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "en", Set: "war", CollectorNumber: "97", Lang: "en", ReleasedAt: "2019-05-03"},
		{ID: "it", Set: "war", CollectorNumber: "97★", Lang: "it", ReleasedAt: "2019-05-03"},
	}

	ranked, r := rankByScanStrength(prints, "PUT", "97", 0, "", "", "it", "", nil)
	if ranked[0].ID != "en" {
		t.Errorf("picked %q, want the exact match: the language came with a set code that checks out against nothing",
			ranked[0].ID)
	}
	if r != scanMatchNumberOnly {
		t.Errorf("rank = %v, want number-only: the set never agreed", r)
	}
}

func TestAMarkedSiblingNeedsTheSetCodeAsWell(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "ja", Set: "war", CollectorNumber: "97★", Lang: "ja", ReleasedAt: "2019-05-03"},
	}
	if _, r := rankByScanStrength(prints, "", "97", 0, "", "", "ja", "", nil); r != scanMatchNone {
		t.Errorf("rank = %v, want none: no set code vouched for the language", r)
	}
	if _, r := rankByScanStrength(prints, "war", "97", 0, "", "", "ja", "", nil); r != scanMatchSetNumberAndLang {
		t.Errorf("rank = %v, want set+number+lang once the set agrees", r)
	}
}

func TestTruncatedNumberMatchesByItsTail(t *testing.T) {
	meltdown := []scryfall.Card{
		{Name: "Meltdown", Set: "sld", CollectorNumber: "2296", ReleasedAt: "2025-11-17"},
		{Name: "Meltdown", Set: "mh3", CollectorNumber: "282", ReleasedAt: "2024-06-14"},
		{Name: "Meltdown", Set: "mh3", CollectorNumber: "418", ReleasedAt: "2024-06-14"},
		{Name: "Meltdown", Set: "usg", CollectorNumber: "203", ReleasedAt: "1998-10-12"},
	}
	ranked, r := rankByScanStrength(meltdown, "", "18", 0, "", "", "", "", nil)
	if r != scanMatchNumberTail {
		t.Fatalf("rank = %v, want number-tail for 18 against 418", r)
	}
	if ranked[0].CollectorNumber != "418" {
		t.Errorf("front printing = %s, want the tail match 418", ranked[0].CollectorNumber)
	}

	if numberVerified(scanMatchNumberTail) {
		t.Error("a tail match must not read as a verified number")
	}
	if corroboratedPrinting(scanMatchNumberTail) {
		t.Error("a tail match alone is one signal, not two")
	}

	auto, _, note := verdict(queueItem{
		canonical: "Meltdown", prints: ranked, rank: r,
		match: cardname.Match{Exact: true},
	})
	if !auto {
		t.Errorf("a tail-matched printing should commit, queued with: %s", note)
	}
}

func TestNumberTailMatchLeavesSubstitutionsAlone(t *testing.T) {
	for _, tc := range []struct {
		name, read string
		prints     []scryfall.Card
	}{{

		name: "Dress Down", read: "14",
		prints: []scryfall.Card{
			{Name: "Dress Down", Set: "plst", CollectorNumber: "MH2-39"},
			{Name: "Dress Down", Set: "h2r", CollectorNumber: "4"},
			{Name: "Dress Down", Set: "mh2", CollectorNumber: "39"},
			{Name: "Dress Down", Set: "mh2", CollectorNumber: "334"},
			{Name: "Dress Down", Set: "pmh2", CollectorNumber: "39s"},
		},
	}, {

		name: "Unstable Amulet", read: "431",
		prints: []scryfall.Card{
			{Name: "Unstable Amulet", Set: "mh3", CollectorNumber: "421"},
			{Name: "Unstable Amulet", Set: "mh3", CollectorNumber: "142"},
			{Name: "Unstable Amulet", Set: "mh3", CollectorNumber: "514"},
		},
	}, {

		name: "Lion Umbra", read: "420",
		prints: []scryfall.Card{
			{Name: "Lion Umbra", Set: "mh3", CollectorNumber: "426"},
			{Name: "Lion Umbra", Set: "mh3", CollectorNumber: "160"},
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			if _, r := rankByScanStrength(tc.prints, "", tc.read, 0, "", "", "", "", nil); r != scanMatchNone {
				t.Errorf("rank = %v, want none: %s is not a tail of any printing", r, tc.read)
			}
		})
	}
}

func TestNumberTailMatchFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		prints []scryfall.Card
		number string
		year   int
		want   scanMatch
	}{{

		name: "several tails match",
		prints: []scryfall.Card{
			{Name: "X", Set: "a", CollectorNumber: "414"},
			{Name: "X", Set: "b", CollectorNumber: "514"},
		},
		number: "14", want: scanMatchNone,
	}, {

		name: "an exact match outranks its own tail",
		prints: []scryfall.Card{
			{Name: "X", Set: "a", CollectorNumber: "414"},
			{Name: "X", Set: "b", CollectorNumber: "14"},
		},
		number: "14", want: scanMatchNumberOnly,
	}, {

		name:   "one digit is too little to repair",
		prints: []scryfall.Card{{Name: "X", Set: "a", CollectorNumber: "418"}},
		number: "8", want: scanMatchNone,
	}, {

		name:   "a variant suffix is not a tail",
		prints: []scryfall.Card{{Name: "X", Set: "a", CollectorNumber: "123a"}},
		number: "23", want: scanMatchNone,
	}, {

		name: "the copyright year contradicts the repair",
		prints: []scryfall.Card{
			{Name: "X", Set: "a", CollectorNumber: "414", ReleasedAt: "2024-06-14"},
		},
		number: "14", year: 2003, want: scanMatchNone,
	}, {

		name: "the copyright year agrees",
		prints: []scryfall.Card{
			{Name: "X", Set: "a", CollectorNumber: "414", ReleasedAt: "2024-06-14"},
		},
		number: "14", year: 2024, want: scanMatchNumberTail,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			if _, r := rankByScanStrength(tc.prints, "", tc.number, tc.year, "", "", "", "", nil); r != tc.want {
				t.Errorf("rank = %v, want %v", r, tc.want)
			}
		})
	}
}

func TestShortExactNumbersStillCommit(t *testing.T) {
	prints := []scryfall.Card{
		{Name: "Abiding Grace", Set: "h2r", CollectorNumber: "1", ReleasedAt: "2024-06-14"},
		{Name: "Abiding Grace", Set: "mh2", CollectorNumber: "5", ReleasedAt: "2021-06-18"},
	}
	if _, r := rankByScanStrength(prints, "", "1", 0, "", "", "", "", nil); r != scanMatchNumberOnly {
		t.Errorf("rank = %v, want number-only for a bare 1 that matches", r)
	}
}

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

		name: "the printing verified",
		it:   queueItem{canonical: "Unholy Heat", prints: prints, rank: scanMatchNumberOnly},
		want: false,
	}, {

		name: "a tail match pinned it",
		it:   queueItem{canonical: "Unholy Heat", prints: prints, rank: scanMatchNumberTail},
		want: false,
	}, {

		name: "nothing identified",
		it:   queueItem{prints: prints, rank: scanMatchNone},
		want: false,
	}, {

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

	other := it
	other.canonical = "Charitable Levy"
	if !m.wantsSecondLook(other) {
		t.Error("the bound is per card, not per session")
	}
}

func TestSecondLookRearmsIntoHeldAtQueueTime(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "a", Name: "Dress Down", Set: "mh2", CollectorNumber: "46",
			ReleasedAt: "2021-06-18", Finishes: []string{"nonfoil"}},
		{ID: "b", Name: "Dress Down", Set: "h2r", CollectorNumber: "4",
			ReleasedAt: "2024-08-02", Finishes: []string{"nonfoil"}},
	}
	fs := fakeSearcher{
		fuzzy: map[string]string{
			"Dress Down": "Dress Down", "Charitable Levy": "Charitable Levy"},
		prints: map[string][]scryfall.Card{
			"Dress Down": prints,
			"Charitable Levy": {
				{ID: "c", Name: "Charitable Levy", Set: "mh3", CollectorNumber: "390",
					ReleasedAt: "2024-06-14", Finishes: []string{"nonfoil"}},
				{ID: "d", Name: "Charitable Levy", Set: "m3c", CollectorNumber: "12",
					ReleasedAt: "2024-06-14", Finishes: []string{"nonfoil"}},
			},
		},
	}
	sess := &fakeSession{events: make(chan scan.Event, 8)}
	m := newModel(context.Background(), fs, noopAdder, &fakeScanner{}, "", nil)
	mm, _ := m.onSession(sessionMsg{session: sess})
	got := mm.(model)
	got.autoCapable = true

	got.autoState = "held"
	blind := scan.Card{Name: "Dress Down", Candidates: []string{"Dress Down"},
		Confidence: 0.95, Source: "crop"}
	mm, _ = got.Update(got.resolveCardCmd(1, blind, 1)())
	got = mm.(model)
	if len(got.review) != 1 {
		t.Fatalf("setup: blind read should queue, review = %d", len(got.review))
	}
	if sess.rearms != 1 {
		t.Fatalf("rearms = %d, want exactly one sent into held at queue time", sess.rearms)
	}

	got.autoState = "armed"
	blind2 := scan.Card{Name: "Charitable Levy", Candidates: []string{"Charitable Levy"},
		Confidence: 0.95, Source: "crop"}
	mm, _ = got.Update(got.resolveCardCmd(2, blind2, 1)())
	got = mm.(model)
	if len(got.review) != 2 {
		t.Fatalf("setup: second blind read should queue, review = %d", len(got.review))
	}
	if sess.rearms != 1 {
		t.Errorf("rearms = %d, want none sent while armed", sess.rearms)
	}

	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true,
		ev: scan.Event{Kind: scan.EventAuto, State: "armed"}})
	if sess.rearms != 1 {
		t.Errorf("rearms = %d, want no retry riding on state reports", sess.rearms)
	}
}
