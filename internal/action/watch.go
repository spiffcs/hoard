package action

import (
	"context"
	"fmt"
	"github.com/spiffcs/hoard/internal/finish"

	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/resolve"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

func watchFinish(fin finish.Finish, c scryfall.Card) finish.Finish {
	switch {
	case fin == finish.Etched && c.PriceUSDEtched != nil:
		return finish.Etched
	case fin.UsesFoilPricing():
		return finish.Foil
	default:
		return finish.Nonfoil
	}
}

type WatchBound struct {
	Op         string
	Threshold  float64
	Pct        float64
	MinMove    float64
	WindowDays int
}

type WatchAddOptions struct {
	Name   string
	Foil   bool
	Bounds []WatchBound
}

type WatchAddResult struct {
	Card   scryfall.Card
	Finish finish.Finish

	Stood int

	Held  []store.HeldPrinting
	Owned bool
}

func preferHeld(d Deps, name string, fin finish.Finish, req *resolve.Request) ([]store.HeldPrinting, error) {
	held, err := d.Store.HeldPrintingsOfName(name, fin)
	if err != nil || len(held) == 0 {
		return nil, err
	}
	req.Ident = scryfall.Identifier{ID: held[0].ScryfallID}
	return held, nil
}

func WatchAdd(ctx context.Context, d Deps, p progress.Fn, o WatchAddOptions) (WatchAddResult, error) {
	var res WatchAddResult
	if len(o.Bounds) == 0 {
		return res, fmt.Errorf("a watch needs at least one threshold")
	}
	fin := finish.Nonfoil
	if o.Foil {
		fin = finish.Foil
	}
	req := resolve.Request{Ident: scryfall.Identifier{Name: o.Name}, Name: o.Name, Finish: fin}
	held, err := preferHeld(d, o.Name, fin, &req)
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
	fin = watchFinish(m.Finish, m.Card)
	res.Card, res.Finish, res.Held = m.Card, fin, held
	for _, h := range held {
		if h.ScryfallID == m.Card.ID {
			res.Owned = true
			break
		}
	}

	if err := d.Store.UpsertPrintings(rr.Found); err != nil {
		return res, err
	}

	for _, b := range o.Bounds {
		if err := d.Store.AddWatchInput(store.WatchInput{
			ScryfallID: m.Card.ID, Display: m.Card.Name, Finish: fin,
			Op: b.Op, Threshold: b.Threshold,
			Pct: b.Pct, MinMove: b.MinMove, WindowDays: b.WindowDays,
		}); err != nil {
			return res, err
		}
		res.Stood++
	}
	return res, nil
}

func (d Deps) WatchAnchorable(scryfallID string, fin finish.Finish) (bool, error) {
	return d.Store.HasAnchorSeries(scryfallID, fin)
}

func (d Deps) WatchCheck() (fired []store.WatchStatus, checked int, err error) {
	return d.Store.CheckWatches()
}

func (d Deps) WatchList() ([]store.WatchStatus, error) { return d.Store.ListWatches() }

func (d Deps) WatchRemove(ref string) (store.WatchStatus, error) {
	w, err := d.Store.WatchByRef(ref)
	if err != nil {
		return store.WatchStatus{}, err
	}
	return w, d.Store.RemoveWatch(w.ID)
}
