package action

import (
	"context"
	"fmt"

	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/resolve"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

// watchFinish is the price finish a watch stores for a resolved match.
//
// A watch tracks a price series, not a copy, so it names the series that
// actually exists: etched only where the printing has an etched figure of its
// own, since a finish the printing lacks has no price to ever cross. This is
// the same rule ownedByPriceFinish applies to holdings, so a card's watch and
// its movers row follow the same series.
func watchFinish(finish string, c scryfall.Card) string {
	switch {
	case finish == "etched" && c.PriceUSDEtched != nil:
		return "etched"
	case scryfall.PricedAsFoil(finish):
		return "foil"
	default:
		return "nonfoil"
	}
}

// WatchBound is one direction of one watch: an op and what turns it on.
//
// Which field carries that depends on the op's units. under and over are
// dollar lines and read Threshold; drop and rise are movements and read Pct,
// as a fraction, with MinMove a floor in dollars and WindowDays the lookback
// the movement is measured over. The store refuses a bound that fills the
// wrong one, so a mistake here is a loud error rather than a watch that
// evaluates against zero.
type WatchBound struct {
	Op         string // under|over|drop|rise
	Threshold  float64
	Pct        float64
	MinMove    float64
	WindowDays int
}

// WatchAddOptions is one card's watches.
//
// Bounds is a slice because a band — alert outside $1 to $5 — is two rows:
// the store keys on (card, finish, op), so the two directions are separate
// watches with separate ids. It is one card, though, and therefore one
// resolve. Looping the whole call per direction instead named the card twice,
// which is two /cards/collection requests paced half a second apart by the
// shared limiter for a question already answered.
type WatchAddOptions struct {
	Name   string
	Foil   bool
	Bounds []WatchBound
}

// WatchAddResult is the printing the watches pinned.
type WatchAddResult struct {
	Card   scryfall.Card
	Finish string // the price finish actually watched: nonfoil|foil
	// Stood counts the bounds actually written. It is less than len(Bounds)
	// only alongside an error, and it is there so the caller can say which
	// direction stood: a band whose second write failed did not do nothing,
	// and reporting it as nothing makes the retry add a row twice.
	Stood int
	// Held is every printing of the name the collection holds, best watch
	// candidate first — empty when the card is not held at all. Owned reports
	// whether Card is one of them.
	//
	// Both are here so the caller can *say what happened* rather than what was
	// intended. Preferring a held printing is a request the resolve pipeline
	// can decline — a Scryfall id the API no longer knows falls through to the
	// name retry, which lands wherever it lands — so Owned is computed from the
	// printing that came back, not from the one that was asked for.
	Held  []store.HeldPrinting
	Owned bool
}

// preferHeld points a watch at a printing the collection actually holds.
//
// This is the whole of the fix for "watch add watches a printing you don't
// own", and the reason it lives here rather than in resolve is that resolve is
// right as it stands. A bare name goes to Scryfall's /cards/collection as a
// name identifier and Scryfall answers with its own choice of printing —
// hoard never picks, and never had a line of code that could. For `deck add`
// and `import` that is correct: a decklist that says "Bitterblossom" means the
// card, and any printing of it plays the same. A watch is the one caller for
// which it is wrong, because a watch's entire premise is *my* copy's price.
//
// So the preference is expressed as an identifier, not as a filter over
// answers: resolving by the held printing's Scryfall id costs the same single
// request the name did, and the typed name rides along as Request.Name so
// resolve's existing name-retry still catches an id Scryfall has forgotten.
func preferHeld(d Deps, name, finish string, req *resolve.Request) ([]store.HeldPrinting, error) {
	held, err := d.Store.HeldPrintingsOfName(name, finish)
	if err != nil || len(held) == 0 {
		return nil, err
	}
	req.Ident = scryfall.Identifier{ID: held[0].ScryfallID}
	return held, nil
}

// WatchAdd resolves a card name once, now, through the same pipeline deck
// add and import use — the check never fuzzy-matches, because an alert
// about the wrong printing is worse than no alert — and stands every
// threshold asked for against that one printing. The card joins the catalog
// so update-prices keeps its price fresh even when no copy is owned:
// watching a card you are hoping to buy is the --under case entirely.
//
// Which printing a bare name means is the question preferHeld answers: a held
// one where the collection has one, and Scryfall's own pick otherwise. Both
// outcomes are reported on the result, because the second one is a
// substitution and the caller has to be able to say so.
func WatchAdd(ctx context.Context, d Deps, p progress.Fn, o WatchAddOptions) (WatchAddResult, error) {
	var res WatchAddResult
	if len(o.Bounds) == 0 {
		return res, fmt.Errorf("a watch needs at least one threshold")
	}
	finish := "nonfoil"
	if o.Foil {
		finish = "foil"
	}
	req := resolve.Request{Ident: scryfall.Identifier{Name: o.Name}, Name: o.Name, Finish: finish}
	held, err := preferHeld(d, o.Name, finish, &req)
	if err != nil {
		return res, err
	}
	rr, err := d.resolver(p).Resolve(ctx, []resolve.Request{req})
	if err != nil {
		return res, err
	}
	if len(rr.Matches) == 0 || !rr.Matches[0].OK {
		return res, fmt.Errorf("no card matches %q", o.Name)
	}
	m := rr.Matches[0]
	finish = watchFinish(m.Finish, m.Card)
	res.Card, res.Finish, res.Held = m.Card, finish, held
	for _, h := range held {
		if h.ScryfallID == m.Card.ID {
			res.Owned = true
			break
		}
	}

	if err := d.Store.UpsertPrintings(rr.Found); err != nil {
		return res, err
	}
	// Each direction upserts on its own, keyed (card, finish, op), so
	// re-adding one direction adjusts that one and leaves the other standing.
	// Stood grows as they land, because a failure partway through has to be
	// reportable as what it is.
	for _, b := range o.Bounds {
		if err := d.Store.AddWatchInput(store.WatchInput{
			ScryfallID: m.Card.ID, Display: m.Card.Name, Finish: finish,
			Op: b.Op, Threshold: b.Threshold,
			Pct: b.Pct, MinMove: b.MinMove, WindowDays: b.WindowDays,
		}); err != nil {
			return res, err
		}
		res.Stood++
	}
	return res, nil
}

// WatchAnchorable reports whether a printing has any observation in the series
// a percent watch would anchor on, so `watch add` can say at the time that a
// movement cannot be measured here.
//
// A percent watch reads one vendor for both ends of the movement it reports,
// and a printing that vendor does not price gets no percent watch at all. On
// the owner's database that is 9 of 1,968 held priced series — rare, and
// silent unless said out loud, which is the failure mode this exists to avoid.
func (d Deps) WatchAnchorable(scryfallID, finish string) (bool, error) {
	return d.Store.HasAnchorSeries(scryfallID, finish)
}

// WatchCheck evaluates every watch against stored prices — no network.
func (d Deps) WatchCheck() (fired []store.WatchStatus, checked int, err error) {
	return d.Store.CheckWatches()
}

// WatchList returns every watch with its current price.
func (d Deps) WatchList() ([]store.WatchStatus, error) { return d.Store.ListWatches() }

// WatchRemove deletes one watch by id or unique name fragment, returning
// what was removed for the confirmation line.
func (d Deps) WatchRemove(ref string) (store.WatchStatus, error) {
	w, err := d.Store.WatchByRef(ref)
	if err != nil {
		return store.WatchStatus{}, err
	}
	return w, d.Store.RemoveWatch(w.ID)
}
