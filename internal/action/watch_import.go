package action

import (
	"context"
	"fmt"

	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/resolve"
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
	// Held counts rows that named only a card and were pointed at a printing
	// the collection holds, instead of at whichever printing Scryfall names
	// for a bare name. See preferHeld.
	Held       int
	Unresolved []string
}

// WatchImport stands every watch in a CSV or JSON watch list, resolving the
// whole file through the same one-pass pipeline collection import uses — no
// fuzzy matching, because an alert about the wrong printing is worse than no
// alert. Unresolvable rows are skipped and reported. Each card joins the
// catalog so update-prices keeps its price fresh even when no copy is owned.
//
// A row that names only a card is pointed at a printing the collection holds
// where there is one — the same rule `watch add` follows, for the same reason,
// and reported as a count because a bulk receipt cannot say it row by row.
func WatchImport(ctx context.Context, d Deps, p progress.Fn, o WatchImportOptions) (WatchImportResult, error) {
	var res WatchImportResult
	rows, err := watchsource.Parse(o.Data)
	if err != nil {
		return res, fmt.Errorf("%s: %w", o.Display, err)
	}
	res.Rows = len(rows)

	// A row that names only a card has the same problem `watch add` has, and
	// worse odds of being noticed: a file of bare names resolves to whatever
	// printing Scryfall considers each one's newest, and a bulk receipt says
	// only how many watches stood. Rows that named a set and number, or an id,
	// asked for a printing and keep it.
	reqs := resolve.Requests(rows)
	wanted := make(map[int]string, len(reqs))
	for i := range reqs {
		if reqs[i].Ident.ID != "" || reqs[i].Ident.Set != "" {
			continue
		}
		held, err := preferHeld(d, reqs[i].Name, reqs[i].Finish, &reqs[i])
		if err != nil {
			return res, err
		}
		if len(held) > 0 {
			wanted[i] = held[0].ScryfallID
		}
	}

	rr, err := d.resolver(p).Resolve(ctx, reqs)
	if err != nil {
		return res, err
	}
	// Counted from what came back rather than from what was asked for: an id
	// Scryfall no longer knows falls through to the name retry, and a receipt
	// claiming a held printing for that row would be a lie of exactly the kind
	// this change exists to remove.
	for i, id := range wanted {
		if rr.Matches[i].OK && rr.Matches[i].Card.ID == id {
			res.Held++
		}
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
		ins = append(ins, store.WatchInput{
			ScryfallID: m.Card.ID, Display: m.Card.Name,
			Finish: watchFinish(m.Finish, m.Card), Op: r.Op, Threshold: r.Threshold,
			Pct: r.Pct, MinMove: r.MinMove, WindowDays: r.WindowDays,
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
