package action

import (
	"context"
	"fmt"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/scryfall"
)

const addedHistoryDays = 90

func CompleteAdd(ctx context.Context, d Deps, c scryfall.Card, fin finish.Finish) error {
	doc, unfinished := d.cardDocument(ctx, c)
	if doc != nil {
		if err := d.Store.UpsertPrintings([]scryfall.Card{*doc}); err != nil {
			return err
		}
		c = *doc
	}

	refs := []pricing.Ref{{ScryfallID: c.ID, SetCode: c.Set, Finish: fin}}
	if _, err := BackfillPrintings(ctx, d, refs, addedHistoryDays); err != nil {
		return err
	}
	if _, err := d.Store.RecordPrices(); err != nil {
		return err
	}
	return unfinished
}

func (d Deps) cardDocument(ctx context.Context, c scryfall.Card) (*scryfall.Card, error) {
	if len(c.Raw) > 0 || c.Set == "" || c.CollectorNumber == "" {
		return nil, nil
	}
	doc, err := d.Resolver.FetchCard(ctx, c.Set, c.CollectorNumber, c.Lang)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, fmt.Errorf("scryfall has no card %s/%s", c.Set, c.CollectorNumber)
	}
	if doc.ID != c.ID {
		return nil, fmt.Errorf("scryfall's %s/%s is a different printing (%s), not %s",
			c.Set, c.CollectorNumber, doc.ID, c.ID)
	}
	return doc, nil
}
