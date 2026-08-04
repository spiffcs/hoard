package action

import (
	"context"
	"fmt"

	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/resolve"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/watchsource"
)

// WatchImportOptions is one watch-list import as requested.
type WatchImportOptions struct {
	Data []byte
	// Display names the source in errors — a path, or "stdin".
	Display string
}

// WatchImportResult is everything the import stood, adjusted and skipped —
// the frontend renders it verbatim.
type WatchImportResult struct {
	Rows       int // rows the file parsed to
	Created    int // watches that did not exist before
	Updated    int // thresholds adjusted on watches already standing
	Refinished int // rows whose claimed finish had no price and was corrected
	Unresolved []string
}

// WatchImport stands every watch in a CSV or JSON watch list, resolving the
// whole file through the same one-pass pipeline collection import uses — no
// fuzzy matching, because an alert about the wrong printing is worse than no
// alert. Unresolvable rows are skipped and reported. Each card joins the
// catalog so update-prices keeps its price fresh even when no copy is owned.
func WatchImport(ctx context.Context, d Deps, p progress.Fn, o WatchImportOptions) (WatchImportResult, error) {
	var res WatchImportResult
	rows, err := watchsource.Parse(o.Data)
	if err != nil {
		return res, fmt.Errorf("%s: %w", o.Display, err)
	}
	res.Rows = len(rows)

	rr, err := d.resolver(p).Resolve(ctx, resolve.Requests(rows))
	if err != nil {
		return res, err
	}
	res.Refinished, res.Unresolved = rr.Refinished, rr.Unresolved
	if err := d.Store.UpsertPrintings(rr.Found); err != nil {
		return res, err
	}

	ins := make([]store.WatchInput, 0, len(rows))
	for i, r := range rows {
		m := rr.Matches[i]
		if !m.OK {
			continue
		}
		// The watch stores a price finish: an etched-only printing is priced
		// as foil, and a finish the printing lacks has no price to ever cross.
		finish := "nonfoil"
		if scryfall.PricedAsFoil(m.Finish) {
			finish = "foil"
		}
		ins = append(ins, store.WatchInput{
			ScryfallID: m.Card.ID, Display: m.Card.Name,
			Finish: finish, Op: r.Op, Threshold: r.Threshold,
		})
	}
	if res.Created, res.Updated, err = d.Store.AddWatches(ins); err != nil {
		return res, err
	}
	if n := len(res.Unresolved); n > 0 {
		return res, fmt.Errorf("%d rows were skipped: %w", n, ErrPartial)
	}
	return res, nil
}
