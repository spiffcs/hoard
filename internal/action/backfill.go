package action

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/store"
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
	// AlreadyToday, when non-empty, is the timestamp of an earlier run today
	// against the same holdings: the archive only changes daily, so the run
	// was skipped before the download instead of after it.
	AlreadyToday string
}

// backfillKey is the ledger identity of one backfill: the archive's day,
// the window asked for, and the exact holdings it would cover. A re-run the
// same day with the same cards and window is provably a no-op; adding a
// card — or asking for a deeper window — changes the key and forces a real
// run.
//
// The v2 salt retires the receipts written while the store's import bound
// was hoard-wide: those runs recorded "done" after discarding everything
// for cards added since the hoard's first observation, and honoring them
// would hide the fix for a day.
func backfillKey(owned []store.OwnedFinish, days int) string {
	ids := make([]string, 0, len(owned))
	for _, o := range owned {
		ids = append(ids, o.ScryfallID)
	}
	sort.Strings(ids)
	day := time.Now().Format("2006-01-02")
	return ContentHash([]byte(fmt.Sprintf("backfill|v2|%s|%d|%s", day, days, strings.Join(ids, ","))))
}

// BackfillPrices loads the ~90 days of prices MTGJSON kept while hoard was
// not watching, so a fresh hoard can answer "what moved this month"
// immediately. Only what is held gets backfilled — reconstructing history
// for cards nobody owns is not worth the wait. The ~150 MB archive download
// reports determinate byte progress; it used to be the longest silence in
// the program.
func BackfillPrices(ctx context.Context, d Deps, p progress.Fn, days int) (BackfillResult, error) {
	// The archive holds ~90 days; days narrows what gets recorded (the
	// download costs the same either way). Zero or out-of-range means all
	// of it.
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

	// The 31-second no-op guard: MTGJSON's archive changes once a day, so a
	// second run today against the same holdings would download and parse
	// ~150 MB to insert nothing. The ledger remembers the first run.
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
		refs[i] = pricing.Ref{ScryfallID: o.ScryfallID, SetCode: o.SetCode, MTGJSONUUID: o.MTGJSONUUID}
		printings[o.ScryfallID] = true
	}
	res.Printings = len(printings)

	p.Emit(progress.Event{Step: "downloading price history",
		Note: fmt.Sprintf("fetching %d days of prices for %s printings from MTGJSON (a large download)",
			days, ui.Count(len(printings)))})
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

	// Narrow the archive to the asked-for window before recording. ISO
	// dates compare as strings.
	if days < 90 {
		cutoff := time.Now().AddDate(0, 0, -days).Format("2006-01-02")
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

	p.Emit(progress.Event{Step: "recording history"})
	// The store bounds each series to the era before that card's own live
	// history — a card added yesterday gets its archive depth even when the
	// hoard has watched other cards for months.
	res.Inserted, res.Cards, err = d.Store.BackfillPrices(byCard)
	if err != nil {
		return res, err
	}
	err = d.Store.RecordReceipt(store.ImportReceipt{
		Hash: key, File: "backfill " + time.Now().Format("2006-01-02"), Cards: res.Cards,
	})
	return res, err
}
