package action

import (
	"context"
	"fmt"

	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/resolve"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/watchsource"
)

type WatchImportOptions struct {
	Data []byte

	Display string
}

type WatchImportResult struct {
	Rows       int
	Created    int
	Updated    int
	Refinished int

	Held       int
	Unresolved []string
}

func WatchImport(ctx context.Context, d Deps, p progress.Fn, o WatchImportOptions) (WatchImportResult, error) {
	var res WatchImportResult
	rows, err := watchsource.Parse(o.Data)
	if err != nil {
		return res, fmt.Errorf("%s: %w", o.Display, err)
	}
	res.Rows = len(rows)

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
