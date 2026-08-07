package tui

import (
	"context"
	"testing"

	"github.com/spiffcs/hoard/internal/cardname"
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

// The decision ceiling flushes a held flash without waiting for the nudge.
//
// The nudge clock answers "has the scene gone quiet" at swap cadence — 5.5s,
// doubled by echo backoff — and a held review flash was riding it. Measured
// across four sessions, every second look that ever rescued a card landed
// within 0.9s of its queue, so a stop still unanswered at 2.5s is a stop the
// operator should be told about.
func TestDecisionCeilingFlushesTheHeldFlash(t *testing.T) {
	ev := confidentEvent()
	ev.SetCode, ev.CollectorNumber = "", ""
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
	if m.deferredFlashFor != "Sol Ring" || len(sess.results) != 0 {
		t.Fatalf("setup: want the flash held, got deferred=%q results=%+v",
			m.deferredFlashFor, sess.results)
	}

	// The ceiling lapses: the flash lands now, not at the nudge.
	mm, _ = m.Update(flashDeadlineMsg{name: "Sol Ring"})
	m = mm.(model)
	if len(sess.results) != 1 || sess.results[0].Tier != tierReview {
		t.Fatalf("results = %+v, want the review flash at the ceiling", sess.results)
	}
	if m.deferredFlashFor != "" {
		t.Error("the flash was sent and must not still be held")
	}

	// A stale deadline — the flash long since resolved — is a no-op.
	mm, _ = m.Update(flashDeadlineMsg{name: "Sol Ring"})
	m = mm.(model)
	if len(sess.results) != 1 {
		t.Errorf("results = %+v, want no second flash from a stale deadline", sess.results)
	}
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

// An unverified printing holds the phone's review flash until the second look
// has had its turn.
//
// The card goes into the queue immediately — if the retry never answers it must
// already be there — but the *phone* is told nothing yet. A "Needs Review" flash
// is a stop, and flashing one on a card we are about to photograph again showed
// the operator a stop that the retry usually removes. Live: every card that
// queued this way in one session read correctly in another.
//
// Here the retry never comes, so the quiet period expires and the flash lands
// after all. That timeout is the guarantee that holding it is safe.
func TestHeldReviewFlashLandsWhenTheRetryNeverComes(t *testing.T) {
	ev := confidentEvent()
	ev.SetCode, ev.CollectorNumber = "", ""
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

	if len(m.review) != 1 {
		t.Fatalf("setup: want the card queued, got review=%d", len(m.review))
	}
	if m.deferredFlashFor != "Sol Ring" {
		t.Fatalf("deferredFlashFor = %q, want the flash held for the card", m.deferredFlashFor)
	}
	if sess.chimes != 0 || len(sess.results) != 0 {
		t.Fatalf("chimes=%d results=%+v, want the phone told nothing yet",
			sess.chimes, sess.results)
	}

	// The quiet period elapses with no capture in it, which is what "the retry
	// never came" looks like from here.
	m.autoCapable = true
	mm, _ = m.onNudge(nudgeMsg{gen: m.nudgeGen})
	m = mm.(model)

	if len(sess.results) != 1 {
		t.Fatalf("results=%+v, want the held flash sent once the retry lapsed", sess.results)
	}
	if r := sess.results[0]; r.Tier != tierReview || r.Amount != nil || r.Total != nil {
		t.Errorf("result = %+v, want a bare needs-review flash", r)
	}
	if m.deferredFlashFor != "" {
		t.Error("the held flash was sent and must not still be held")
	}
}

// A queue reason a second look cannot fix flashes at once, exactly as before.
//
// The hold is scoped to an unverified printing — the failure another photograph
// repairs. Here the printing is pinned and it is the *name* that is shaky, so
// there is nothing for a retry to improve on and delaying the flash would be
// latency bought for nothing.
func TestAShakyNameFlashesImmediately(t *testing.T) {
	ev := confidentEvent()
	ev.SetCode, ev.CollectorNumber = "", "123"
	fs := fakeSearcher{
		fuzzy: map[string]string{"Sol Ring": "Sol Ring"},
		match: map[string]cardname.Match{"Sol Ring": {Similarity: 0.79}},
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

	if len(m.review) != 1 {
		t.Fatalf("setup: want the card queued, got review=%d", len(m.review))
	}
	if m.deferredFlashFor != "" {
		t.Errorf("deferredFlashFor = %q, want nothing held: the printing verified",
			m.deferredFlashFor)
	}
	if len(sess.results) != 1 || sess.results[0].Tier != tierReview {
		t.Errorf("results = %+v, want the review flash sent at once", sess.results)
	}
}

// A manual add while the camera is open syncs the HUD total silently: total
// only, no tier, no sound — it never asked a question on the camera window.
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

// Confirming a queued card answers its resolve-time question: the confirmed
// amount (qty-weighted) flashes with its tier's sound, total riding along.
func TestReviewConfirmCelebratesAmount(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	m, sess := hudSession(t, m)
	card := scryfall.Card{ID: "a", Name: "Sol Ring", Set: "mh3",
		CollectorNumber: "123", Finishes: []string{"nonfoil"}, PriceUSD: price(12)}
	m.current = &queueItem{} // the cascade is reviewing a queued card
	m.chosen, m.finish = &card, "nonfoil"
	m.qtyInput.SetValue("2")
	mm, _ := m.confirmAdd()
	m = mm.(model)

	if m.addedValue != 24.00 {
		t.Fatalf("setup: addedValue = %v", m.addedValue)
	}
	if sess.chimes != 0 || len(sess.results) != 1 {
		t.Fatalf("chimes=%d results=%+v, want one celebration", sess.chimes, sess.results)
	}
	r := sess.results[0]
	if r.Amount == nil || *r.Amount != 24.00 || r.Tier != tierJackpot {
		t.Errorf("result = %+v, want the landed $24.00 as a jackpot", r)
	}
	if r.Total == nil || *r.Total != 24.00 {
		t.Errorf("total = %v, want 24.00", priceStr(r.Total))
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

// The payoff: the retry reads the card properly, and the phone never hears
// about a review that turned out not to be one.
//
// This is the whole reason the flash is held. Before, a card whose footer
// failed one photograph flashed "Needs Review" on the phone and then quietly
// committed a second later — a stop the operator saw and reacted to, for a
// question that had already answered itself.
func TestARescuedCardNeverFlashesReview(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "a", Name: "Sol Ring", Set: "mh3", CollectorNumber: "123",
			Finishes: []string{"nonfoil"}, PriceUSD: price(25)},
		{ID: "b", Name: "Sol Ring", Set: "c21", CollectorNumber: "263",
			Finishes: []string{"nonfoil"}, PriceUSD: price(25)},
	}
	fs := fakeSearcher{
		fuzzy:  map[string]string{"Sol Ring": "Sol Ring"},
		prints: map[string][]scryfall.Card{"Sol Ring": prints},
	}
	m := newModel(context.Background(), fs, noopAdder, &fakeScanner{}, "", nil)
	m, sess := hudSession(t, m)

	// First look: no collector number survived, so the printing is unverified.
	blind := confidentEvent()
	blind.SetCode, blind.CollectorNumber = "", ""
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: blind})
	m = resolve(t, mm.(model), blind.CardList()[0])
	if len(m.review) != 1 || m.deferredFlashFor != "Sol Ring" {
		t.Fatalf("setup: want a queued card with its flash held, review=%d held=%q",
			len(m.review), m.deferredFlashFor)
	}

	// Second look: the number reads, the card commits, and upgradeQueued takes
	// the queued entry back out.
	good := confidentEvent()
	good.SetCode, good.CollectorNumber = "", "123"
	mm, _ = m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: good})
	m = resolve(t, mm.(model), good.CardList()[0])

	if len(m.review) != 0 {
		t.Fatalf("review = %+v, want the queued entry replaced by the commit", m.review)
	}
	if m.deferredFlashFor != "" {
		t.Error("the held flash is moot once the card committed and must be cleared")
	}
	for _, r := range sess.results {
		if r.Tier == tierReview {
			t.Fatalf("a needs-review flash reached the phone: %+v", sess.results)
		}
	}
}
