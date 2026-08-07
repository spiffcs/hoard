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

// WatchAddOptions is one threshold to stand.
type WatchAddOptions struct {
	Name      string
	Foil      bool
	Op        string // under|over
	Threshold float64
}

// WatchAddResult is the printing the watch pinned.
type WatchAddResult struct {
	Card   scryfall.Card
	Finish string // the price finish actually watched: nonfoil|foil
}

// WatchAdd resolves a card name once, now, through the same pipeline deck
// add and import use — the check never fuzzy-matches, because an alert
// about the wrong printing is worse than no alert — and stands the
// threshold. The card joins the catalog so update-prices keeps its price
// fresh even when no copy is owned: watching a card you are hoping to buy
// is the --under case entirely.
func WatchAdd(ctx context.Context, d Deps, p progress.Fn, o WatchAddOptions) (WatchAddResult, error) {
	var res WatchAddResult
	finish := "nonfoil"
	if o.Foil {
		finish = "foil"
	}
	rr, err := d.resolver(p).Resolve(ctx, []resolve.Request{
		{Ident: scryfall.Identifier{Name: o.Name}, Name: o.Name, Finish: finish}})
	if err != nil {
		return res, err
	}
	if len(rr.Matches) == 0 || !rr.Matches[0].OK {
		return res, fmt.Errorf("no card matches %q", o.Name)
	}
	m := rr.Matches[0]
	finish = watchFinish(m.Finish, m.Card)
	res.Card, res.Finish = m.Card, finish

	if err := d.Store.UpsertPrintings(rr.Found); err != nil {
		return res, err
	}
	return res, d.Store.AddWatch(m.Card.ID, m.Card.Name, finish, o.Op, o.Threshold)
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
