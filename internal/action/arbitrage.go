package action

import (
	"context"

	"github.com/spiffcs/hoard/internal/arbitrage"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/progress"
)

// Arbitrage gathers today's vendor quotes for everything held and ranks
// them. The first read of the day downloads the ~5 MB bundle (narrated as
// byte progress); after that the day-cache answers instantly. minValue
// filters on what you would actually pay — a 900% spread between $0.20 and
// $1.99 is arithmetic, not an opportunity.
func Arbitrage(ctx context.Context, d Deps, p progress.Fn, minValue float64) (arbitrage.Result, error) {
	owned, err := d.Store.OwnedByFinish()
	if err != nil || len(owned) == 0 {
		return arbitrage.Result{}, err
	}
	refs := make([]pricing.Ref, len(owned))
	for i, o := range owned {
		refs[i] = pricing.Ref{ScryfallID: o.ScryfallID, SetCode: o.SetCode, MTGJSONUUID: o.MTGJSONUUID}
	}
	f := pricing.New(d.Store, d.CacheDir).
		WithProgress(func(msg string) {
			p.Emit(progress.Event{Step: "reading vendor prices", Note: msg})
		}).
		WithBytes(func(done, total int64) {
			p.Emit(progress.Event{Step: "reading vendor prices",
				Done: done, Total: total, Unit: progress.UnitBytes})
		})
	quotes, err := f.Quotes(ctx, refs)
	if err != nil {
		return arbitrage.Result{}, err
	}
	return arbitrage.Collect(owned, quotes, minValue), nil
}
