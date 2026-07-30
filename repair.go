package main

// `hoard repair-finishes`: correcting entries recorded in a finish the printing
// does not come in.

import (
	"context"
	"fmt"
	"os"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// cmdRepairFinishes corrects entries recorded in a finish that does not exist.
//
// A decklist with no foil marker imports as "normal", but plenty of printings
// are foil-only: precon commanders and Duel Decks reprints among them. Such an
// entry asks for a price that cannot exist, so the card sits at $0.00 forever
// and no amount of price fetching will help. Scryfall knows which finishes a
// printing comes in; hoard fetches that on every price refresh and has been
// discarding it.
func cmdRepairFinishes(ctx context.Context, st *store.Store) error {
	ids, err := st.AllPrintingIDs()
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		fmt.Println("No cards yet; nothing to repair.")
		return nil
	}

	cat := openCatalog()
	if cat != nil {
		defer cat.Close()
	}
	found, _, _, err := refreshCards(ctx, cat, st, ids)
	if err != nil {
		return err
	}
	available := make(map[string][]string, len(found))
	for _, c := range found {
		available[c.ID] = c.Finishes
	}

	fixed, ambiguous, err := st.RepairFinishes(available)
	if err != nil {
		return err
	}

	env := ui.Detect(os.Stdout)
	if len(fixed) == 0 && len(ambiguous) == 0 {
		fmt.Println(env.Dim()("Every card is recorded in a finish it actually comes in."))
		return nil
	}

	if len(fixed) > 0 {
		t := ui.Table{
			Env:    env,
			Header: true,
			Cols: []ui.Col{
				{Title: "NAME", Align: ui.Left, Flex: true, Min: 16},
				{Title: "SET/NUM", Align: ui.Left, Priority: 4, Style: env.Dim()},
				{Title: "QTY", Align: ui.Right},
				{Title: "WAS", Align: ui.Left, Style: env.Dim()},
				{Title: "NOW", Align: ui.Left},
				{Title: "IN", Align: ui.Left, Flex: true, Min: 10, Max: 30,
					Priority: 5, Style: env.Dim()},
			},
		}
		for _, f := range fixed {
			t.Add(ui.C(f.Name), ui.C(f.SetCode+"/"+f.CollectorNumber),
				ui.C(ui.Count(f.Quantity)), ui.C(f.From), ui.C(f.To), ui.C(f.Container))
		}
		if _, err := t.WriteTo(os.Stdout); err != nil {
			return err
		}
		fmt.Println(env.Dim()(fmt.Sprintf(
			"\nCorrected %s entries. Run hoard update-prices to value them.", ui.Count(len(fixed)))))
	}
	for _, a := range ambiguous {
		fmt.Println(env.Dim()(fmt.Sprintf(
			"  left alone: %s (%s/%s) is recorded as %s but comes in %s",
			a.Name, a.SetCode, a.CollectorNumber, a.From, a.To)))
	}
	return nil
}
