package pricing

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/spiffcs/hoard/internal/store"
)

const recheckAfter = 7 * 24 * time.Hour

type GapReport struct {
	Gaps int

	Skipped bool

	Filled    int
	Remaining int

	Sources []string
}

func (f *Fetcher) FillGaps(ctx context.Context) (GapReport, error) {
	gaps, err := f.st.UnpricedByOwnedFinish()
	if err != nil {
		return GapReport{}, err
	}

	altPriced, err := f.st.AltPricedOwned()
	if err != nil {
		return GapReport{}, err
	}
	if len(gaps) == 0 && len(altPriced) == 0 {
		return GapReport{}, nil
	}
	report := GapReport{Gaps: len(gaps)}

	cutoff := time.Now().UTC().Add(-recheckAfter).Format(time.RFC3339)
	report.Skipped = len(altPriced) == 0
	for _, g := range gaps {
		if g.CheckedAt == nil || *g.CheckedAt < cutoff {
			report.Skipped = false
			break
		}
	}
	if report.Skipped {
		return report, nil
	}

	refs := make([]Ref, 0, len(gaps)+len(altPriced))
	asked := make([]string, 0, len(gaps))
	for _, g := range gaps {
		refs = append(refs, Ref{ScryfallID: g.ScryfallID, SetCode: g.SetCode})

		asked = append(asked, g.ScryfallID)
	}
	for _, g := range altPriced {
		refs = append(refs, Ref{ScryfallID: g.ScryfallID, SetCode: g.SetCode})
	}
	prices, err := f.Prices(ctx, refs)
	if err != nil {
		return report, err
	}

	alts := make([]store.AltPrice, 0, len(prices))
	sources := map[string]bool{}
	for id, p := range prices {
		alts = append(alts, store.AltPrice{
			ScryfallID:    id,
			MTGJSONUUID:   p.UUID,
			PriceUSD:      p.USD,
			PriceUSDFoil:  p.Foil,
			SourceUSD:     p.USDSource,
			SourceUSDFoil: p.FoilSource,
		})
		sources[p.USDSource] = true
		sources[p.FoilSource] = true
	}
	delete(sources, "")
	if err := f.st.UpsertAltPrices(alts); err != nil {
		return report, err
	}

	if err := f.st.RecordPriceGapChecks(asked); err != nil {
		return report, err
	}

	remaining, err := f.st.UnpricedByOwnedFinish()
	if err != nil {
		return report, err
	}
	report.Remaining = len(remaining)
	report.Filled = max(len(gaps)-len(remaining), 0)
	report.Sources = slices.Sorted(maps.Keys(sources))
	return report, nil
}
