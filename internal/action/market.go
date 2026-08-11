package action

import (
	"context"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/store"
)

// Arbitrage gathers today's vendor quotes for everything held and ranks
// them. The first read of the day downloads the ~5 MB bundle (narrated as
// byte progress); after that the day-cache answers instantly. minValue
// filters on what you would actually pay — a 900% spread between $0.20 and
// $1.99 is arithmetic, not an opportunity.
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
	// A fresh parse, not the day cache: this runs on the market view's F,
	// and an explicit refetch that silently serves the morning's bundles
	// is a broken promise.
	quotes, err := f.RefreshQuotes(ctx, refs)
	if err != nil {
		return market.Result{}, err
	}
	if err := recordBidQuotes(d.Store, quotes); err != nil {
		return market.Result{}, err
	}
	return market.Collect(owned, quotes, minValue), nil
}

// recordBidQuotes keeps the bid series live: every quotes read — fresh or
// served from the day cache — writes today's Card Kingdom bids into the
// bid history. RecordBids drops repeats, so re-reads are free, and a day
// whose earlier record failed self-heals on the next one.
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

// CardComps serves one printing's per-finish comp sheets from the quotes
// day-cache — the card detail's slice of the market view's data. ok is
// false when no fresh cache covers the holdings; no network either way.
// Keyed by price finish (nonfoil|foil).
func CardComps(d Deps, scryfallID string) (map[string]market.Comp, bool, error) {
	owned, err := d.Store.OwnedByFinish()
	if err != nil || len(owned) == 0 {
		return nil, false, err
	}
	// The whole hoard's refs, not one card's: the day cache answers
	// all-or-nothing against the population it was written for.
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

	// The printing's own attributes, taken from any row that holds it in any
	// finish. Treatment and VendorIDsKnown describe the printing rather than
	// the copy, and AssessComp reads them to decide which vendors can be tied
	// to the product: a bare row without them claims every vendor's quote,
	// including the unverifiable one the comp sheet exists to suppress.
	printing := store.OwnedFinish{ScryfallID: scryfallID}
	for _, held := range owned {
		if held.ScryfallID == scryfallID {
			printing = held
			printing.Copies, printing.Value = 0, 0
			break
		}
	}

	// One sheet per price finish the feed quotes, assessed from the owned
	// row when the finish is held (real copies and value) or a bare one
	// when it is not — the numbers are the card's either way. Etched is its
	// own sheet, not folded into foil: vendors price the etched product
	// separately, so folding it would label one card's price as another's.
	//
	// A nonfoil or foil sheet the feed does not quote comes back empty and
	// drops out below on its own. An etched one does not, and that asymmetry
	// was drawing a product that does not exist: market.quoteFinish reads an
	// etched holding's foil series when no vendor splits the etched product,
	// so a bare etched row on (say) Bitterblossom uma/85 — a printing sold
	// only as nonfoil and foil — came back holding the foil's numbers to the
	// cent and rendered beside it as a separate thing to buy. The fold is
	// right where it lives: it is how a real etched copy gets valued at all.
	// What it cannot answer is whether the etched product exists, so ask that
	// here, before manufacturing the row. A held copy answers yes, and so
	// does a vendor quoting the etched series by name.
	out := map[string]market.Comp{}
	for _, finish := range []string{"nonfoil", "foil", "etched"} {
		o := printing
		heldInFinish := false
		o.Finish = finish
		for _, held := range owned {
			if held.ScryfallID == scryfallID && held.Finish == finish {
				o, heldInFinish = held, true
				break
			}
		}
		if finish == "etched" && !heldInFinish && !quotesFinish(qs, "etched") {
			continue
		}
		c := market.AssessComp(o, qs)
		if c.HasMarket || c.HasCK || c.HasManapool || c.HasBuylist || c.LowFrom != "" {
			out[finish] = c
		}
	}
	return out, true, nil
}

// quotesFinish reports whether any vendor priced this finish by name — the
// evidence that the product is one the trade actually sells separately,
// rather than one the finish fold would invent a price for.
func quotesFinish(qs []mtgjson.Quote, finish string) bool {
	for _, q := range qs {
		if q.Finish == finish && q.Price > 0 {
			return true
		}
	}
	return false
}

// MarketCached is Arbitrage from today's quote cache alone: no network,
// no archive parse. ok is false when no fresh cache covers the holdings —
// the caller decides whether to fetch for real. This is what lets a
// restarted session show the vendor comparison an earlier one fetched;
// the data lives in the pricing day-cache (owned-quotes.json beside the
// MTGJSON bundles), pruned nightly with them.
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
