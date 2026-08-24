package action

import (
	"context"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/store"
)

func Market(ctx context.Context, d Deps, p progress.Fn, minValue float64) (market.Result, error) {
	owned, err := d.Store.OwnedByFinish()
	if err != nil || len(owned) == 0 {
		return market.Result{}, err
	}
	refs := make([]pricing.Ref, len(owned))
	for i, o := range owned {
		refs[i] = pricing.Ref{ScryfallID: o.ScryfallID, SetCode: o.SetCode, MTGJSONUUID: o.MTGJSONUUID}
	}
	f := d.pricer().
		WithProgress(func(msg string) {
			p.Emit(progress.Event{Step: "reading vendor prices", Note: msg})
		}).
		WithBytes(func(done, total int64) {
			p.Emit(progress.Event{Step: "reading vendor prices",
				Done: done, Total: total, Unit: progress.UnitBytes})
		})

	quotes, err := f.RefreshQuotes(ctx, refs)
	if err != nil {
		return market.Result{}, err
	}
	if err := recordBidQuotes(d.Store, quotes); err != nil {
		return market.Result{}, err
	}
	return market.Collect(owned, quotes, minValue), nil
}

func recordBidQuotes(st *store.Store, quotes map[string][]mtgjson.Quote) error {
	var bids []store.BidObservation
	for sid, qs := range quotes {
		for _, q := range qs {
			if q.Kind != mtgjson.Buylist || q.Provider != "cardkingdom" || q.Price <= 0 {
				continue
			}
			bids = append(bids, store.BidObservation{
				ScryfallID: sid, Finish: q.Finish, Source: q.Provider, Price: q.Price,
			})
		}
	}
	_, err := st.RecordBids(bids)
	return err
}

func CardComps(d Deps, scryfallID string) (map[finish.Finish]market.Comp, bool, error) {
	owned, err := d.Store.OwnedByFinish()
	if err != nil || len(owned) == 0 {
		return nil, false, err
	}

	refs := make([]pricing.Ref, len(owned))
	for i, o := range owned {
		refs[i] = pricing.Ref{ScryfallID: o.ScryfallID, SetCode: o.SetCode, MTGJSONUUID: o.MTGJSONUUID}
	}
	quotes, ok := d.pricer().CachedQuotes(refs)
	if !ok {
		return nil, false, nil
	}
	qs := quotes[scryfallID]
	if len(qs) == 0 {
		return nil, true, nil
	}

	printing := store.OwnedFinish{ScryfallID: scryfallID}
	for _, held := range owned {
		if held.ScryfallID == scryfallID {
			printing = held
			printing.Copies, printing.Value = 0, 0
			break
		}
	}

	out := map[finish.Finish]market.Comp{}
	for _, fin := range finish.All() {
		o := printing
		heldInFinish := false
		o.Finish = fin
		for _, held := range owned {
			if held.ScryfallID == scryfallID && held.Finish == fin {
				o, heldInFinish = held, true
				break
			}
		}
		if fin == finish.Etched && !heldInFinish && !quotesFinish(qs, finish.Etched) {
			continue
		}
		c := market.AssessComp(o, qs)
		if c.HasMarket || c.HasCK || c.HasManapool || c.HasBuylist || c.LowFrom != "" {
			out[fin] = c
		}
	}
	return out, true, nil
}

func quotesFinish(qs []mtgjson.Quote, fin finish.Finish) bool {
	for _, q := range qs {
		if q.Finish == fin && q.Price > 0 {
			return true
		}
	}
	return false
}

func MarketCached(d Deps, minValue float64) (market.Result, bool, error) {
	owned, err := d.Store.OwnedByFinish()
	if err != nil || len(owned) == 0 {
		return market.Result{}, false, err
	}
	refs := make([]pricing.Ref, len(owned))
	for i, o := range owned {
		refs[i] = pricing.Ref{ScryfallID: o.ScryfallID, SetCode: o.SetCode, MTGJSONUUID: o.MTGJSONUUID}
	}
	quotes, ok := d.pricer().CachedQuotes(refs)
	if !ok {
		return market.Result{}, false, nil
	}
	return market.Collect(owned, quotes, minValue), true, nil
}
