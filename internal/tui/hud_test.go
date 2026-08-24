package tui

import (
	"context"
	"testing"

	"github.com/spiffcs/hoard/internal/cardname"
	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scan"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func TestTierFor(t *testing.T) {

	cases := []struct {
		price *float64
		want  string
	}{
		{nil, tierUnpriced},
		{price(0), tierBulk},
		{price(0.99), tierBulk},
		{price(1.00), tierWin},
		{price(19.99), tierWin},
		{price(20.00), tierJackpot},
		{price(500), tierJackpot},
	}
	for _, c := range cases {
		if got := tierFor(c.price); got != c.want {
			t.Errorf("tierFor(%v) = %q, want %q", priceStr(c.price), got, c.want)
		}
	}
}

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

	mm, _ = m.Update(flashDeadlineMsg{name: "Sol Ring"})
	m = mm.(model)
	if len(sess.results) != 1 || sess.results[0].Tier != tierReview {
		t.Fatalf("results = %+v, want the review flash at the ceiling", sess.results)
	}
	if m.deferredFlashFor != "" {
		t.Error("the flash was sent and must not still be held")
	}

	mm, _ = m.Update(flashDeadlineMsg{name: "Sol Ring"})
	m = mm.(model)
	if len(sess.results) != 1 {
		t.Errorf("results = %+v, want no second flash from a stale deadline", sess.results)
	}
}

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

func TestConfirmAddSyncsHudTotal(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	m, sess := hudSession(t, m)
	card := scryfall.Card{ID: "a", Name: "Sol Ring", Set: "mh3",
		CollectorNumber: "123", Finishes: []string{"nonfoil"}, PriceUSD: price(2.00)}
	m.chosen, m.finish = &card, finish.Nonfoil
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

func TestReviewConfirmCelebratesAmount(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	m, sess := hudSession(t, m)
	card := scryfall.Card{ID: "a", Name: "Sol Ring", Set: "mh3",
		CollectorNumber: "123", Finishes: []string{"nonfoil"}, PriceUSD: price(12)}
	m.current = &queueItem{}
	m.chosen, m.finish = &card, finish.Nonfoil
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

func TestUnpricedResolvesAsUnpriced(t *testing.T) {
	ev, fs := confidentFixture()
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

func TestConfirmAfterCameraClosedDoesNotPanic(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	m.hudCapable = true
	card := scryfall.Card{ID: "a", Name: "Sol Ring", Set: "mh3",
		CollectorNumber: "123", Finishes: []string{"nonfoil"}, PriceUSD: price(2.00)}
	m.chosen, m.finish = &card, finish.Nonfoil
	m.qtyInput.SetValue("1")
	if mm, _ := m.confirmAdd(); mm.(model).addedValue != 2.00 {
		t.Errorf("confirm with no session should still account the value")
	}
}

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

	blind := confidentEvent()
	blind.SetCode, blind.CollectorNumber = "", ""
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: blind})
	m = resolve(t, mm.(model), blind.CardList()[0])
	if len(m.review) != 1 || m.deferredFlashFor != "Sol Ring" {
		t.Fatalf("setup: want a queued card with its flash held, review=%d held=%q",
			len(m.review), m.deferredFlashFor)
	}

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
