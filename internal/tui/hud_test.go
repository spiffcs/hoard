package tui

import (
	"context"
	"testing"

	"github.com/spiffcs/hoard/internal/scan"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func TestTierFor(t *testing.T) {
	cases := []struct {
		price        *float64
		win, jackpot float64
		want         string
	}{
		{nil, 1, 20, tierUnpriced},
		{price(0), 1, 20, tierBulk},
		{price(0.99), 1, 20, tierBulk},
		{price(1.00), 1, 20, tierWin},
		{price(19.99), 1, 20, tierWin},
		{price(20.00), 1, 20, tierJackpot},
		{price(500), 1, 20, tierJackpot},
		// Custom thresholds move the boundaries with them.
		{price(3), 5, 50, tierBulk},
		{price(5), 5, 50, tierWin},
		{price(50), 5, 50, tierJackpot},
	}
	for _, c := range cases {
		if got := tierFor(c.price, c.win, c.jackpot); got != c.want {
			t.Errorf("tierFor(%v, %v, %v) = %q, want %q",
				priceStr(c.price), c.win, c.jackpot, got, c.want)
		}
	}
}

func TestHudThresholdsFromEnv(t *testing.T) {
	t.Setenv("HOARD_SCAN_WIN", "5")
	t.Setenv("HOARD_SCAN_JACKPOT", "50")
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	if m.hudWin != 5 || m.hudJackpot != 50 {
		t.Errorf("thresholds = %v/%v, want 5/50", m.hudWin, m.hudJackpot)
	}
	// Garbage degrades to the defaults rather than breaking scanning.
	t.Setenv("HOARD_SCAN_WIN", "cheap")
	t.Setenv("HOARD_SCAN_JACKPOT", "")
	m = newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	if m.hudWin != defaultWinThreshold || m.hudJackpot != defaultJackpotThreshold {
		t.Errorf("thresholds = %v/%v, want the defaults", m.hudWin, m.hudJackpot)
	}
}

func TestGuessPrice(t *testing.T) {
	priced := scryfall.Card{PriceUSD: price(2), PriceUSDFoil: price(9)}
	if p := guessPrice(queueItem{}); p != nil {
		t.Errorf("no printings should guess nil, got %v", *p)
	}
	if p := guessPrice(queueItem{prints: []scryfall.Card{priced}}); p == nil || *p != 2 {
		t.Errorf("markerless frame should price nonfoil, got %v", priceStr(p))
	}
	if p := guessPrice(queueItem{prints: []scryfall.Card{priced}, finishHint: "foil"}); p == nil || *p != 9 {
		t.Errorf("foil marker should price foil, got %v", priceStr(p))
	}
}

// hudSession is openCapture plus a ready event advertising the hud feature.
func hudSession(t *testing.T, m model) (model, *fakeSession) {
	t.Helper()
	got, sess := openCapture(t, m)
	mm, _ := got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true,
		ev: scan.Event{Kind: scan.EventReady, Device: "iPhone", Features: []string{"hud"}}})
	got = mm.(model)
	if !got.hudCapable {
		t.Fatal("setup: ready event with hud did not set hudCapable")
	}
	return got, sess
}

// An auto-commit celebrates once — amount, tier, and the post-commit total in
// one result — and never also chimes.
func TestAutoCommitCelebratesWithTotal(t *testing.T) {
	ev, _ := confidentFixture()
	fs := fakeSearcher{
		fuzzy: map[string]string{"Sol Ring": "Sol Ring"},
		prints: map[string][]scryfall.Card{"Sol Ring": {
			{ID: "a", Name: "Sol Ring", Set: "mh3", CollectorNumber: "123",
				Finishes: []string{"nonfoil"}, PriceUSD: price(3.25)},
		}},
	}
	m := newModel(context.Background(), fs, noopAdder, &fakeScanner{}, "", nil)
	m, sess := hudSession(t, m)
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	m = resolve(t, mm.(model), ev.CardList()[0])

	if m.addedCount != 1 {
		t.Fatalf("setup: auto-add did not commit")
	}
	if sess.chimes != 0 {
		t.Errorf("chimes = %d, want 0: the tier sound replaces the chime", sess.chimes)
	}
	if len(sess.results) != 1 {
		t.Fatalf("results = %+v, want exactly one", sess.results)
	}
	r := sess.results[0]
	if r.Amount == nil || *r.Amount != 3.25 || r.Tier != tierWin {
		t.Errorf("result = %+v, want a $3.25 win", r)
	}
	if r.Total == nil || *r.Total != 3.25 {
		t.Errorf("total = %v, want the post-commit 3.25", priceStr(r.Total))
	}
}

// A queue-bound resolve celebrates its estimated price without a total — the
// money lands only when the review confirms the card.
func TestQueuedCelebratesWithoutTotal(t *testing.T) {
	ev := confidentEvent()
	ev.SetCode, ev.CollectorNumber = "", "" // unpinned printing: queue-bound
	fs := fakeSearcher{
		fuzzy: map[string]string{"Sol Ring": "Sol Ring"},
		prints: map[string][]scryfall.Card{"Sol Ring": {
			{ID: "a", Name: "Sol Ring", Set: "mh3", CollectorNumber: "123",
				Finishes: []string{"nonfoil"}, PriceUSD: price(25)},
			{ID: "b", Name: "Sol Ring", Set: "c21", CollectorNumber: "263",
				Finishes: []string{"nonfoil"}, PriceUSD: price(25)},
		}},
	}
	m := newModel(context.Background(), fs, noopAdder, &fakeScanner{}, "", nil)
	m, sess := hudSession(t, m)
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	m = resolve(t, mm.(model), ev.CardList()[0])

	if len(m.review) != 1 || m.addedCount != 0 {
		t.Fatalf("setup: want a queued card, got review=%d added=%d", len(m.review), m.addedCount)
	}
	if sess.chimes != 0 || len(sess.results) != 1 {
		t.Fatalf("chimes=%d results=%+v, want one silent-chime result", sess.chimes, sess.results)
	}
	r := sess.results[0]
	if r.Amount == nil || *r.Amount != 25 || r.Tier != tierJackpot {
		t.Errorf("result = %+v, want a $25 jackpot", r)
	}
	if r.Total != nil {
		t.Errorf("queued card carried a total (%v); it must wait for the confirm", *r.Total)
	}
}

// A confirm (review or manual) while the camera is open syncs the HUD total
// silently: total only, no tier, no sound.
func TestConfirmAddSyncsHudTotal(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	m, sess := hudSession(t, m)
	card := scryfall.Card{ID: "a", Name: "Sol Ring", Set: "mh3",
		CollectorNumber: "123", Finishes: []string{"nonfoil"}, PriceUSD: price(2.00)}
	m.chosen, m.finish = &card, "nonfoil"
	m.qtyInput.SetValue("3")
	mm, _ := m.confirmAdd()
	m = mm.(model)

	if m.addedValue != 6.00 {
		t.Fatalf("setup: addedValue = %v", m.addedValue)
	}
	if sess.chimes != 0 || len(sess.results) != 1 {
		t.Fatalf("chimes=%d results=%+v, want one silent result", sess.chimes, sess.results)
	}
	r := sess.results[0]
	if r.Tier != "" || r.Amount != nil {
		t.Errorf("confirm result = %+v, want total-only (the card already celebrated)", r)
	}
	if r.Total == nil || *r.Total != 6.00 {
		t.Errorf("total = %v, want 6.00", priceStr(r.Total))
	}
}

// An unpriced card resolves as a shrug: unpriced tier, no amount, and no
// total — $0-because-unpriced must never read as bulk-with-$0.00.
func TestUnpricedResolvesAsUnpriced(t *testing.T) {
	ev, fs := confidentFixture() // solRingPrints carry no prices
	m := newModel(context.Background(), fs, noopAdder, &fakeScanner{}, "", nil)
	m, sess := hudSession(t, m)
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	m = resolve(t, mm.(model), ev.CardList()[0])

	if m.addedCount != 1 {
		t.Fatalf("setup: auto-add did not commit")
	}
	if len(sess.results) != 1 {
		t.Fatalf("results = %+v, want one", sess.results)
	}
	r := sess.results[0]
	if r.Tier != tierUnpriced || r.Amount != nil || r.Total != nil {
		t.Errorf("result = %+v, want a bare unpriced shrug", r)
	}
}

// A helper that never advertised the hud feature keeps getting the plain
// chime — never a result it would answer with an error event.
func TestNoHudFeatureFallsBackToChime(t *testing.T) {
	ev, fs := confidentFixture()
	m := newModel(context.Background(), fs, noopAdder, &fakeScanner{}, "", nil)
	m, sess := openCapture(t, m)
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true,
		ev: scan.Event{Kind: scan.EventReady, Device: "iPhone", Features: []string{"auto"}}})
	m = mm.(model)
	mm, _ = m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	m = resolve(t, mm.(model), ev.CardList()[0])

	if m.addedCount != 1 {
		t.Fatalf("setup: auto-add did not commit")
	}
	if sess.chimes != 1 || len(sess.results) != 0 {
		t.Errorf("chimes=%d results=%d, want the plain chime and no results",
			sess.chimes, len(sess.results))
	}
}

// The silence rules survive the upgrade: a nudge echo of an already-processed
// card makes no sound and no flash.
func TestDropsSendNoResult(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	m, sess := hudSession(t, m)
	m.recentNames = recordName(nil, "Sol Ring", m.now())

	mm, _ := m.Update(resolveDoneMsg{gen: m.resolveGen,
		item: queueItem{fromNudge: true, canonical: "Sol Ring"}})
	m = mm.(model)

	if len(m.review) != 0 {
		t.Fatalf("the echo joined the queue")
	}
	if sess.chimes != 0 || len(sess.results) != 0 {
		t.Errorf("chimes=%d results=%d, want total silence on a drop",
			sess.chimes, len(sess.results))
	}
}

// Review confirms routinely happen after the camera closed; the total sync
// must cope with the session being gone.
func TestConfirmAfterCameraClosedDoesNotPanic(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	m.hudCapable = true // was capable while the camera lived
	card := scryfall.Card{ID: "a", Name: "Sol Ring", Set: "mh3",
		CollectorNumber: "123", Finishes: []string{"nonfoil"}, PriceUSD: price(2.00)}
	m.chosen, m.finish = &card, "nonfoil"
	m.qtyInput.SetValue("1")
	if mm, _ := m.confirmAdd(); mm.(model).addedValue != 2.00 {
		t.Errorf("confirm with no session should still account the value")
	}
}
