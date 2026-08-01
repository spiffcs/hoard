package action

import (
	"context"
	"fmt"

	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/ui"
)

// BackfillResult is what one history import did, and what it could not
// reach.
//
// The misses are not filler. Movers joins a card against its own baseline,
// so a printing with no backfilled history simply stops appearing in any
// window that predates hoard — the list quietly gets shorter rather than
// visibly incomplete. Counting the skips is the only place that becomes
// visible.
type BackfillResult struct {
	// Printings is how many distinct printings were asked about; zero means
	// nothing is owned and the operation did nothing.
	Printings int
	// Unmapped printings have no MTGJSON id and were skipped; Unquoted have
	// an id but no TCGplayer history — the same gap `unpriced` reports.
	Unmapped, Unquoted int
	Inserted, Cards    int
	// HadHistorySince is the oldest existing observation (RFC 3339), empty
	// when history was empty before this run.
	HadHistorySince string
}

// BackfillPrices loads the ~90 days of prices MTGJSON kept while hoard was
// not watching, so a fresh hoard can answer "what moved this month"
// immediately. Only what is held gets backfilled — reconstructing history
// for cards nobody owns is not worth the wait. The ~150 MB archive download
// reports determinate byte progress; it used to be the longest silence in
// the program.
func BackfillPrices(ctx context.Context, d Deps, p progress.Fn) (BackfillResult, error) {
	var res BackfillResult
	owned, err := d.Store.OwnedByFinish()
	if err != nil {
		return res, err
	}
	if len(owned) == 0 {
		return res, nil
	}
	_, oldest, err := d.Store.PriceHistoryDepth()
	if err != nil {
		return res, err
	}
	res.HadHistorySince = oldest

	refs := make([]pricing.Ref, len(owned))
	printings := map[string]bool{}
	for i, o := range owned {
		refs[i] = pricing.Ref{ScryfallID: o.ScryfallID, SetCode: o.SetCode, MTGJSONUUID: o.MTGJSONUUID}
		printings[o.ScryfallID] = true
	}
	res.Printings = len(printings)

	p.Emit(progress.Event{Step: "downloading price history",
		Note: fmt.Sprintf("fetching 90 days of prices for %s printings from MTGJSON (~150 MB)",
			ui.Count(len(printings)))})
	f := pricing.New(d.Store, d.CacheDir).
		WithProgress(func(msg string) {
			p.Emit(progress.Event{Step: "downloading price history", Note: msg})
		}).
		WithBytes(func(done, total int64) {
			p.Emit(progress.Event{Step: "downloading price history",
				Done: done, Total: total, Unit: progress.UnitBytes})
		})
	byCard, resolvable, err := f.History(ctx, refs)
	if err != nil {
		return res, err
	}
	res.Unmapped = res.Printings - resolvable
	res.Unquoted = resolvable - len(byCard)

	p.Emit(progress.Event{Step: "recording history"})
	res.Inserted, res.Cards, err = d.Store.BackfillPrices(byCard, oldest)
	return res, err
}
