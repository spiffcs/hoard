package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func repeatFixture() (queueItem, recentCommit) {
	card := scryfall.Card{ID: "sol", Name: "Sol Ring", Set: "c21",
		CollectorNumber: "263", Finishes: []string{"nonfoil", "foil"}}
	it := queueItem{id: 1, canonical: "Sol Ring",
		prints: []scryfall.Card{card}, finishHint: "foil"}
	prior := recentCommit{scryfallID: "sol", finish: finish.Nonfoil, finishGuessed: true}
	return it, prior
}

func TestAFailedFinishCorrectionIsQueuedForReviewNotSwallowed(t *testing.T) {
	ra := &recordingAdder{err: errors.New("database is locked")}
	it, prior := repeatFixture()
	m := model{adder: ra.add, now: time.Now}

	next, _ := m.suppressRepeat(it, finish.Foil, prior, time.Now(), "a repeat", false)
	got := next.(model)

	if len(got.review) != 1 {
		t.Fatalf("review = %+v, want the card queued once the correction failed", got.review)
	}
	if !strings.Contains(got.review[0].note, "database is locked") {
		t.Errorf("queued note = %q, want it to name the failure", got.review[0].note)
	}
	if strings.Contains(got.status, "Still seeing") {
		t.Errorf("status = %q, want the failure surfaced rather than a duplicate prompt",
			got.status)
	}
	if !strings.Contains(got.status, "Sol Ring") || !got.statusErr {
		t.Errorf("status = %q (err=%v), want it to name the card and read as a failure",
			got.status, got.statusErr)
	}
	if got.pending != nil {
		t.Error("a failed correction must not be offered as a second copy")
	}
}

func TestASucceedingFinishCorrectionStillCorrects(t *testing.T) {
	ra := &recordingAdder{}
	it, prior := repeatFixture()
	m := model{adder: ra.add, now: time.Now}

	next, _ := m.suppressRepeat(it, finish.Foil, prior, time.Now(), "a repeat", false)
	got := next.(model)

	if len(ra.got) != 1 || ra.got[0].Finish != finish.Foil {
		t.Fatalf("adder saw %+v, want one foil correction", ra.got)
	}
	if len(got.review) != 0 {
		t.Errorf("review = %+v, want nothing queued when the correction lands", got.review)
	}
	if !strings.Contains(got.status, "Corrected") {
		t.Errorf("status = %q, want it to report the correction", got.status)
	}
}
