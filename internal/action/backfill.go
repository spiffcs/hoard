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

// backfillKey is the ledger identity of one backfill: the archive's day and
// the exact holdings it would cover. A re-run the same day with the same
// cards is provably a no-op; adding a card changes the key and forces a
// real run.
func backfillKey(owned []store.OwnedFinish) string {
	ids := make([]string, 0, len(owned))
	for _, o := range owned {
		ids = append(ids, o.ScryfallID)
	}
	sort.Strings(ids)
	day := time.Now().Format("2006-01-02")
	return ContentHash([]byte("backfill|" + day + "|" + strings.Join(ids, ",")))
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

	// The 31-second no-op guard: MTGJSON's archive changes once a day, so a
	// second run today against the same holdings would download and parse
	// ~150 MB to insert nothing. The ledger remembers the first run.
	key := backfillKey(owned)
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
	if err != nil {
		return res, err
	}
	err = d.Store.RecordReceipt(store.ImportReceipt{
		Hash: key, File: "backfill " + time.Now().Format("2006-01-02"), Cards: res.Cards,
	})
	return res, err
}
