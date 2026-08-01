package action

import (
	"context"

	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/store"
)

// RepairResult is what a finish repair corrected and what it refused to
// guess at.
type RepairResult struct {
	// Total is every printing checked; zero means the hoard is empty and
	// nothing ran.
	Total int
	Fixed []store.FinishFix
	// Ambiguous entries come in several finishes none of which is the
	// recorded one — correcting those would be inventing an answer.
	Ambiguous []store.FinishFix
}

// RepairFinishes corrects entries recorded in a finish their printing does
// not come in.
//
// A decklist with no foil marker imports as "nonfoil", but plenty of
// printings are foil-only: precon commanders and Duel Decks reprints among
// them. Such an entry asks for a price that cannot exist, so the card sits
// at $0.00 forever and no amount of price fetching will help. Scryfall knows
// which finishes a printing comes in.
func RepairFinishes(ctx context.Context, d Deps, p progress.Fn) (RepairResult, error) {
	var res RepairResult
	ids, err := d.Store.AllPrintingIDs()
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
	available := make(map[string][]string, len(found))
	for _, c := range found {
		available[c.ID] = c.Finishes
	}

	res.Fixed, res.Ambiguous, err = d.Store.RepairFinishes(available)
	return res, err
}
