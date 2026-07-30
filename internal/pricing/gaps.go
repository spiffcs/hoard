package pricing

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/spiffcs/hoard/internal/store"
)

// recheckAfter is how long a "MTGJSON has no price for this" answer is trusted.
//
// Usually permanent — a printing no vendor stocks stays unstocked — but not
// always, since MTGJSON adds vendors and vendors add stock. A week keeps the
// daily refresh clear of a 50 MB scan while still noticing a card that becomes
// priceable.
const recheckAfter = 7 * 24 * time.Hour

// GapReport is what one gap-filling pass did.
type GapReport struct {
	// Gaps is how many printings had no price for a finish actually held.
	Gaps int
	// Skipped is true when every gap had been asked about recently, so nothing
	// was downloaded.
	Skipped bool
	// Filled counts gaps actually closed, and Remaining those still unpriced.
	Filled    int
	Remaining int
	// Sources names the vendors that supplied the prices, for attribution.
	Sources []string
}

// FillGaps prices the printings Scryfall could not, from MTGJSON.
//
// Asking about one card costs a scan of MTGJSON's whole daily bundle, and cards
// nothing can price are largely permanent, so the scan is skipped when every
// remaining gap was asked about recently. If even one is due, the scan runs and
// re-asks about all of them — by then the cost is paid.
func (f *Fetcher) FillGaps(ctx context.Context) (GapReport, error) {
	gaps, err := f.st.UnpricedByOwnedFinish()
	if err != nil || len(gaps) == 0 {
		return GapReport{}, err
	}
	report := GapReport{Gaps: len(gaps)}

	cutoff := time.Now().UTC().Add(-recheckAfter).Format(time.RFC3339)
	report.Skipped = true
	for _, g := range gaps {
		if g.CheckedAt == nil || *g.CheckedAt < cutoff {
			report.Skipped = false
			break
		}
	}
	if report.Skipped {
		return report, nil
	}

	refs := make([]Ref, len(gaps))
	asked := make([]string, len(gaps))
	for i, g := range gaps {
		refs[i] = Ref{ScryfallID: g.ScryfallID, SetCode: g.SetCode}
		asked[i] = g.ScryfallID
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
	delete(sources, "") // a finish this card had no vendor for
	if err := f.st.UpsertAltPrices(alts); err != nil {
		return report, err
	}
	// Record the asking, so tomorrow's run does not pay for the same scan to
	// learn the same thing. Cards MTGJSON did price stop being gaps anyway.
	if err := f.st.RecordPriceGapChecks(asked); err != nil {
		return report, err
	}

	// Count gaps closed, not rows written: MTGJSON often prices a card only in
	// the finish you do not own, which stores a good row that closes nothing.
	remaining, err := f.st.UnpricedByOwnedFinish()
	if err != nil {
		return report, err
	}
	report.Remaining = len(remaining)
	report.Filled = len(gaps) - len(remaining)
	report.Sources = slices.Sorted(maps.Keys(sources))
	return report, nil
}
