package action

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

type BackfillResult struct {
	Printings int

	Unmapped, Unquoted int
	Inserted, Cards    int

	HadHistorySince string

	AlreadyToday string

	BidInserted, BidCards int
}

func backfillKey(owned []store.OwnedFinish, days int) string {
	ids := make([]string, 0, len(owned))
	for _, o := range owned {
		ids = append(ids, o.ScryfallID+"|"+o.Finish.String())
	}
	sort.Strings(ids)
	day := time.Now().Format("2006-01-02")
	return ContentHash(fmt.Appendf(nil, "backfill|v10|%s|%d|%s", day, days, strings.Join(ids, ",")))
}

func BackfillPrices(ctx context.Context, d Deps, p progress.Fn, days int) (BackfillResult, error) {

	if days <= 0 || days > 90 {
		days = 90
	}
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

	key := backfillKey(owned, days)
	if when, _, done, lerr := d.Store.ImportedAt(key); lerr != nil {
		return res, lerr
	} else if done {
		res.Printings = len(owned)
		res.AlreadyToday = when
		return res, nil
	}

	refs := make([]pricing.Ref, len(owned))
	printings := map[string]bool{}
	for i, o := range owned {
		refs[i] = pricing.Ref{ScryfallID: o.ScryfallID, SetCode: o.SetCode,
			MTGJSONUUID: o.MTGJSONUUID, Finish: o.Finish}
		printings[o.ScryfallID] = true
	}
	res.Printings = len(printings)

	p.Emit(progress.Event{Step: "downloading price history",
		Note: fmt.Sprintf("fetching %d days of prices for %s printings from MTGJSON (a large download)",
			days, ui.Count(len(printings)))})
	f := d.pricer().
		WithProgress(func(msg string) {
			p.Emit(progress.Event{Step: "downloading price history", Note: msg})
		}).
		WithBytes(func(done, total int64) {
			p.Emit(progress.Event{Step: "downloading price history",
				Done: done, Total: total, Unit: progress.UnitBytes})
		})
	byCard, resolvable, err := f.History(ctx, refs, days)
	if err != nil {
		return res, err
	}
	res.Unmapped = res.Printings - resolvable
	res.Unquoted = resolvable - len(byCard)

	retail := make(map[string][]mtgjson.Observation, len(byCard))
	bids := make(map[string][]mtgjson.Observation, len(byCard))
	for id, h := range byCard {
		if len(h.Retail) > 0 {
			retail[id] = h.Retail
		}
		if len(h.Bids) > 0 {
			bids[id] = h.Bids
		}
	}

	if days < 90 {
		cutoff := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
		clipWindow(retail, cutoff)
		clipWindow(bids, cutoff)
	}

	p.Emit(progress.Event{Step: "recording history"})

	res.Inserted, res.Cards, err = d.Store.BackfillPrices(retail)
	if err != nil {
		return res, err
	}
	res.BidInserted, res.BidCards, err = d.Store.BackfillBids(bids)
	if err != nil {
		return res, err
	}
	if res.Inserted+res.BidInserted > 0 {

		p.Emit(progress.Event{Step: "compacting the database"})
		if err := d.Store.Compact(); err != nil {
			return res, err
		}
	}
	err = d.Store.RecordReceipt(store.ImportReceipt{
		Hash: key, File: "backfill " + time.Now().Format("2006-01-02"), Cards: res.Cards,
	})
	return res, err
}

func clipWindow(byCard map[string][]mtgjson.Observation, cutoff string) {
	for id, obs := range byCard {
		kept := obs[:0]
		for _, o := range obs {
			if o.Date >= cutoff {
				kept = append(kept, o)
			}
		}
		if len(kept) == 0 {
			delete(byCard, id)
			continue
		}
		byCard[id] = kept
	}
}
