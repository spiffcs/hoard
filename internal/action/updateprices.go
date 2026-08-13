package action

import (
	"context"
	"fmt"

	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

// fetchCollection is the network seam every card fetch goes through: tests
// set Deps.Resolver.Fetch (the same seam the resolve pipeline has always
// had), production falls through to the real chunked endpoint with progress.
func (d Deps) fetchCollection(ctx context.Context, ids []scryfall.Identifier,
	onChunk func(done, total int, note string)) ([]scryfall.Card, []scryfall.Identifier, error) {
	if d.Resolver != nil && d.Resolver.Fetch != nil {
		return d.Resolver.Fetch(ctx, ids)
	}
	return scryfall.FetchCollectionProgress(ctx, ids, onChunk)
}

// RefreshCards resolves every id to a card, preferring the local catalog.
//
// This is the single place the cache policy lives. Two kinds of id still have
// to come from the API and both are bounded:
//
//   - ids the catalog has never seen, i.e. printings newer than its last build
//   - ids whose stored Scryfall document is missing, since the catalog carries
//     prices and identity but not the whole response
//
// The second set empties after one refresh rather than recurring, so the
// steady state is a single listing request and no card lookups at all.
// Progress counts cards: Done starts at the catalog's contribution, so the
// bar opens at "everything local answered instantly" and climbs through the
// network chunks.
func RefreshCards(ctx context.Context, d Deps, p progress.Fn,
	ids []string) (found []scryfall.Card, notFound []scryfall.Identifier, fromCatalog int, err error) {
	need := ids
	if cat := d.Catalog; cat != nil && cat.CardCount() > 0 {
		local, err := cat.Cards(ids)
		if err != nil {
			return nil, nil, 0, err
		}
		undocumented, err := d.Store.IDsNeedingDocuments()
		if err != nil {
			return nil, nil, 0, err
		}
		wantDoc := make(map[string]bool, len(undocumented))
		for _, id := range undocumented {
			wantDoc[id] = true
		}

		need = need[:0:0]
		for _, id := range ids {
			c, ok := local[id]
			if !ok || wantDoc[id] {
				need = append(need, id)
				continue
			}
			found = append(found, c)
		}
		fromCatalog = len(found)
	}

	p.Emit(progress.Event{Step: "refreshing cards",
		Done: int64(fromCatalog), Total: int64(len(ids)), Unit: progress.UnitCards})
	if len(need) == 0 {
		return found, nil, fromCatalog, nil
	}
	idents := make([]scryfall.Identifier, len(need))
	for i, id := range need {
		idents[i] = scryfall.Identifier{ID: id}
	}
	remote, notFound, err := d.fetchCollection(ctx, idents, func(done, total int, note string) {
		p.Emit(progress.Event{Step: "refreshing cards",
			Done: int64(fromCatalog + done), Total: int64(len(ids)),
			Unit: progress.UnitCards, Note: note})
	})
	if err != nil {
		return nil, nil, 0, err
	}
	return append(found, remote...), notFound, fromCatalog, nil
}

// UpdatePricesResult is one refresh, in the numbers the frontends print.
type UpdatePricesResult struct {
	// Total is every printing in the hoard; zero means there was nothing to
	// update and the operation did nothing at all.
	Total       int
	Found       int
	FromCatalog int
	// NotFound counts identifiers Scryfall no longer answers for.
	NotFound int
	// CatalogUsed reports whether prices came from a current local catalog;
	// false means every price was fetched live.
	CatalogUsed bool
	Gaps        GapReport
	// Refused counts prices this run declined to report because the asks on
	// their own TCGplayer product contradicted them, and substituted the
	// cheapest of those asks for instead.
	Refused int
	// Repaired counts observations the correction pass rewrote, added or
	// withdrew after the fact — the cost of recording history before the
	// corrections rather than behind them. Almost always zero.
	Repaired int
	// Changes feeds the movers rendering: what moved since the last refresh.
	Changes []store.PriceChange
}

// UpdatePrices refreshes every stored printing's prices, fills what Scryfall
// cannot price from MTGJSON, and records the observation.
//
// It is the two halves below, run back to back, and that is what a batch caller
// wants: `hoard update-prices` has one result to print and no reason to show
// its work early. A frontend that renders — the browser — runs them separately
// so the second is not in front of the first's numbers; see RefreshPrices.
func UpdatePrices(ctx context.Context, d Deps, p progress.Fn) (UpdatePricesResult, error) {
	res, err := RefreshPrices(ctx, d, p)
	if err != nil || res.Total == 0 {
		return res, err
	}
	return CorrectPrices(ctx, d, p, res)
}

// RefreshPrices is everything a price update can do without the network's
// slowest question: today's figures, from the catalog where it can and
// Scryfall where it must, plus the MTGJSON fallback for what neither prices.
//
// Split from the correction pass because of what that pass costs. tcgcsv holds
// every request 250 ms apart, so re-checking a hoard's prices against the asks
// on their own listings is one paced download per owned TCGplayer group — 21
// seconds for a hundred groups, measured, and 21 seconds is not a thing to make
// someone look at a spinner for before they may look at their collection.
//
// What is on screen in between is refreshed prices under the *previous* run's
// corrections, which stay in force until CorrectPrices replaces them. So the
// gap never shows a price hoard already knows to be degenerate; it shows one
// whose correction has not yet been re-earned.
//
// The history is written here, at the end, rather than travelling with the
// corrections. An observation taken before them can be wrong — a refused $0.56
// entering the series shows in movers as a crash and a recovery that never
// happened — but corrections are rare enough (one printing in 2,104, measured)
// that holding every card's history behind a pass that almost never changes
// anything is the worse trade. CorrectPrices repairs the handful that need it;
// see store.RepairRecordedPrices, which re-decides this recording rather than
// patching rows, because a correction can also mean no movement happened at
// all and the row must be withdrawn.
func RefreshPrices(ctx context.Context, d Deps, p progress.Fn) (UpdatePricesResult, error) {
	var res UpdatePricesResult
	ids, err := d.Store.ActivePrintingIDs()
	if err != nil {
		return res, err
	}
	res.Total = len(ids)
	if len(ids) == 0 {
		return res, nil
	}

	// Prices are only taken from the catalog when it is current. A stale one
	// is still fine for everything else — identity, finishes — but this
	// operation exists to refresh prices and must not report success over
	// yesterday's. A declined download therefore drops the catalog entirely:
	// its cards carry prices, and upserting them would record stale figures.
	p.Emit(progress.Event{Step: "checking catalog"})
	res.CatalogUsed = EnsureCatalog(ctx, d, p)
	src := d
	if !res.CatalogUsed {
		src.Catalog = nil
	}

	found, notFound, fromCatalog, err := RefreshCards(ctx, src, p, ids)
	if err != nil {
		return res, err
	}
	res.Found, res.FromCatalog, res.NotFound = len(found), fromCatalog, len(notFound)
	if err := d.Store.UpsertPrintings(found); err != nil {
		return res, err
	}

	// Scryfall's results are already committed above, so a failure in the
	// fallback pass costs nothing that was just fetched.
	// The gap fill must precede the recording: a card priced from MTGJSON this
	// run has its effective price only once that pass has committed, and
	// recording first would log the gap and call the fill a change next time.
	if res.Gaps, err = FillGaps(ctx, d, p); err != nil {
		return res, err
	}

	p.Emit(progress.Event{Step: "recording history"})
	res.Changes, err = d.Store.RecordPrices()
	return res, err
}

// CorrectPrices re-earns the corrections today's asks justify, and puts the
// recording right where they changed it.
//
// The repair is the price of recording first, and it is a small one: the sweep
// leaves most hoards' history untouched, and where it does not, re-deciding one
// instant's observations is a few statements over rows already in memory. What
// it must not be is a patch — see store.RepairRecordedPrices for the case that
// forces a full re-decision, where a correction lands back on the previously
// recorded price and the honest answer is that nothing moved.
//
// The corrected view of what moved replaces the one the recording reported, so
// a caller that prints movers prints the figures hoard stands behind. prior
// carries RefreshPrices' counts through so the caller ends up with one
// filled-in result either way.
func CorrectPrices(ctx context.Context, d Deps, p progress.Fn,
	prior UpdatePricesResult) (UpdatePricesResult, error) {
	res := prior
	var err error
	if res.Refused, err = RefuseContradictedPrices(ctx, d, p); err != nil {
		return res, err
	}
	p.Emit(progress.Event{Step: "repairing the recording"})
	moved, repaired, err := d.Store.RepairRecordedPrices()
	if err != nil {
		return res, err
	}
	res.Repaired = repaired
	if repaired > 0 {
		res.Changes = moved
	}
	return res, nil
}

// RefuseContradictedPrices replaces the price corrections in force with the
// ones today's asks justify, and reports how many are standing.
//
// A failure to reach TCGplayer's catalog leaves the corrections alone rather
// than clearing them: the previous answer is stale but was measured, where an
// empty sweep from an unreachable source is not evidence that anything has
// been fixed. Only a sweep that actually ran may retire a correction.
func RefuseContradictedPrices(ctx context.Context, d Deps, p progress.Fn) (int, error) {
	f := d.pricer().WithProgress(func(msg string) {
		p.Emit(progress.Event{Step: "checking prices against asks", Note: msg})
	})
	sweep, err := f.Contradictions(ctx)
	if err != nil {
		return 0, err
	}
	if err := d.Store.ReplacePriceOverrides(sweep.Overrides, sweep.Checked); err != nil {
		return 0, err
	}
	if n := len(sweep.Overrides); n > 0 {
		p.Emit(progress.Event{Step: "checking prices against asks", Note: fmt.Sprintf(
			"%d refused for sitting below the cheapest ask on their own listing; using the ask instead.", n)})
	}
	return len(sweep.Overrides), nil
}
