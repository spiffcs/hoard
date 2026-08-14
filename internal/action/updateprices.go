package action

import (
	"context"
	"fmt"

	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

func (d Deps) fetchCollection(ctx context.Context, ids []scryfall.Identifier,
	onChunk func(done, total int, note string)) ([]scryfall.Card, []scryfall.Identifier, error) {
	if d.Resolver != nil && d.Resolver.Fetch != nil {
		return d.Resolver.Fetch(ctx, ids)
	}
	return scryfall.FetchCollectionProgress(ctx, ids, onChunk)
}

func RefreshCards(ctx context.Context, d Deps, p progress.Fn,
	ids []string) (found []scryfall.Card, notFound []scryfall.Identifier, fromCatalog int, err error) {
	need := ids
	if cat := d.Catalog; cat != nil && cat.CardCount() > 0 {
		local, err := cat.Cards(ids)
		if err != nil {
			return nil, nil, 0, err
		}
		undocumented, err := d.Store.IDsNeedingDocuments()
		if err != nil {
			return nil, nil, 0, err
		}
		wantDoc := make(map[string]bool, len(undocumented))
		for _, id := range undocumented {
			wantDoc[id] = true
		}

		need = need[:0:0]
		for _, id := range ids {
			c, ok := local[id]
			if !ok || wantDoc[id] {
				need = append(need, id)
				continue
			}
			found = append(found, c)
		}
		fromCatalog = len(found)
	}

	p.Emit(progress.Event{Step: "refreshing cards",
		Done: int64(fromCatalog), Total: int64(len(ids)), Unit: progress.UnitCards})
	if len(need) == 0 {
		return found, nil, fromCatalog, nil
	}
	idents := make([]scryfall.Identifier, len(need))
	for i, id := range need {
		idents[i] = scryfall.Identifier{ID: id}
	}
	remote, notFound, err := d.fetchCollection(ctx, idents, func(done, total int, note string) {
		p.Emit(progress.Event{Step: "refreshing cards",
			Done: int64(fromCatalog + done), Total: int64(len(ids)),
			Unit: progress.UnitCards, Note: note})
	})
	if err != nil {
		return nil, nil, 0, err
	}
	return append(found, remote...), notFound, fromCatalog, nil
}

type UpdatePricesResult struct {
	Total       int
	Found       int
	FromCatalog int

	NotFound int

	CatalogUsed bool
	Gaps        GapReport

	Refused int

	Repaired int

	Changes []store.PriceChange
}

func UpdatePrices(ctx context.Context, d Deps, p progress.Fn) (UpdatePricesResult, error) {
	res, err := RefreshPrices(ctx, d, p)
	if err != nil || res.Total == 0 {
		return res, err
	}
	return CorrectPrices(ctx, d, p, res)
}

func RefreshPrices(ctx context.Context, d Deps, p progress.Fn) (UpdatePricesResult, error) {
	var res UpdatePricesResult
	ids, err := d.Store.ActivePrintingIDs()
	if err != nil {
		return res, err
	}
	res.Total = len(ids)
	if len(ids) == 0 {
		return res, nil
	}

	p.Emit(progress.Event{Step: "checking catalog"})
	res.CatalogUsed = EnsureCatalog(ctx, d, p)
	src := d
	if !res.CatalogUsed {
		src.Catalog = nil
	}

	found, notFound, fromCatalog, err := RefreshCards(ctx, src, p, ids)
	if err != nil {
		return res, err
	}
	res.Found, res.FromCatalog, res.NotFound = len(found), fromCatalog, len(notFound)
	if err := d.Store.UpsertPrintings(found); err != nil {
		return res, err
	}

	if res.Gaps, err = FillGaps(ctx, d, p); err != nil {
		return res, err
	}

	p.Emit(progress.Event{Step: "recording history"})
	res.Changes, err = d.Store.RecordPrices()
	return res, err
}

func CorrectPrices(ctx context.Context, d Deps, p progress.Fn,
	prior UpdatePricesResult) (UpdatePricesResult, error) {
	res := prior
	var err error
	if res.Refused, err = RefuseContradictedPrices(ctx, d, p); err != nil {
		return res, err
	}
	p.Emit(progress.Event{Step: "repairing the recording"})
	moved, repaired, err := d.Store.RepairRecordedPrices()
	if err != nil {
		return res, err
	}
	res.Repaired = repaired
	if repaired > 0 {
		res.Changes = moved
	}
	return res, nil
}

func RefuseContradictedPrices(ctx context.Context, d Deps, p progress.Fn) (int, error) {
	f := d.pricer().WithProgress(func(msg string) {
		p.Emit(progress.Event{Step: "checking prices against asks", Note: msg})
	})
	sweep, err := f.Contradictions(ctx)
	if err != nil {
		return 0, err
	}
	if err := d.Store.ReplacePriceOverrides(sweep.Overrides, sweep.Checked); err != nil {
		return 0, err
	}
	if n := len(sweep.Overrides); n > 0 {
		p.Emit(progress.Event{Step: "checking prices against asks", Note: fmt.Sprintf(
			"%d refused for sitting below the cheapest ask on their own listing; using the ask instead.", n)})
	}
	return len(sweep.Overrides), nil
}
