package tui

import (
	"context"
	"testing"

	"github.com/spiffcs/hoard/internal/artindex"
	"github.com/spiffcs/hoard/internal/cardname"
	"github.com/spiffcs/hoard/internal/scan"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func TestArtDecisiveFailsClosed(t *testing.T) {
	m := func(d int) artindex.Match { return artindex.Match{ScryfallID: "x", Distance: d} }
	if !artDecisive(m(3), m(20)) {
		t.Error("a close, clear winner should be decisive")
	}
	if artDecisive(m(12), m(40)) {
		t.Error("a distant winner is not a match however clear its lead")
	}
	if artDecisive(m(3), m(8)) {
		t.Error("a near-tie must fail closed — that is where wrong picks live")
	}
	if artDecisive(m(11), m(12)) {
		t.Error("both gates missed must fail closed")
	}
}

// A decisive art match replaces the queued entry and commits, through the
// same path as any better read — and it carries the printing the picture
// chose, not the one OCR guessed at.
func TestArtMatchCommitsThroughTheOrdinaryPath(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "a", Name: "Victimize", Set: "mh3", CollectorNumber: "413",
			ReleasedAt: "2024-06-14", Frame: "1997", Finishes: []string{"foil"}},
		{ID: "b", Name: "Victimize", Set: "usg", CollectorNumber: "165",
			ReleasedAt: "1998-10-12", Finishes: []string{"nonfoil"}},
	}
	fs := fakeSearcher{
		fuzzy:  map[string]string{"Victimize": "Victimize"},
		prints: map[string][]scryfall.Card{"Victimize": prints},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	// A name-only read against 19-printings-shaped ambiguity: queues.
	blind := scan.Card{Name: "Victimize", Candidates: []string{"Victimize"},
		Confidence: 0.95, Source: "crop"}
	mm, _ := m.Update(m.resolveCardCmd(1, blind, 1)())
	got := mm.(model)
	if len(got.review) != 1 {
		t.Fatalf("setup: blind read should queue, review = %d", len(got.review))
	}

	// The picture answers: MH3/413, decisively. The synthetic re-read is
	// what artMatchCmd builds on a decisive Best.
	art := artMatchMsg{gen: got.resolveGen, item: queueItem{
		id: 1, siblings: 1, captureSeq: got.captureSeq,
		canonical: "Victimize", match: cardname.Match{Exact: true},
		prints: prints[:1], rank: scanMatchArt,
		raw: scan.Card{SetCode: "mh3", CollectorNumber: "413"},
	}}
	mm, _ = got.Update(art)
	got = mm.(model)
	if len(ra.got) != 1 {
		t.Fatalf("adds = %d, want the art match committed (review=%+v)", len(ra.got), got.review)
	}
	if ra.got[0].Card.ID != "a" {
		t.Errorf("committed %s, want the picture's printing", ra.got[0].Card.ID)
	}
	// Foil-only printing → the finish is evidence, not a guess.
	if ra.got[0].Finish != "foil" || ra.got[0].FinishGuessed {
		t.Errorf("finish = %s guessed=%v, want foil evidenced via the sole finish",
			ra.got[0].Finish, ra.got[0].FinishGuessed)
	}
	if len(got.review) != 0 {
		t.Errorf("review = %d, want the queued entry replaced by the commit", len(got.review))
	}

	// A stale generation is dropped — a discarded session's straggler.
	stale := art
	stale.gen = got.resolveGen - 1
	before := len(ra.got)
	mm, _ = got.Update(stale)
	if len(ra.got) != before {
		t.Error("a stale art match must not commit")
	}
}
