package action

import (
	"context"
	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"

	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/store"
)

type RepairResult struct {
	Total int
	Fixed []store.FinishFix

	Ambiguous []store.FinishFix
}

func RepairFinishes(ctx context.Context, d Deps, p progress.Fn) (RepairResult, error) {
	var res RepairResult
	ids, err := d.Store.ActivePrintingIDs()
	if err != nil {
		return res, err
	}
	res.Total = len(ids)
	if len(ids) == 0 {
		return res, nil
	}

	found, _, _, err := RefreshCards(ctx, d, p, ids)
	if err != nil {
		return res, err
	}
	available := make(map[string][]finish.Finish, len(found))
	for _, c := range found {
		available[c.ID] = scryfall.Finishes(c)
	}

	res.Fixed, res.Ambiguous, err = d.Store.RepairFinishes(available)
	return res, err
}
