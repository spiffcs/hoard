package pricing

import (
	"context"
	"strings"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/tcgcsv"
)

const (
	OverrideSource = "tcgplayer-low"
	OverrideReason = "below-cheapest-ask"
)

func Contradicted(market, low float64) bool {
	return market > 0 && low > 0 && low/market >= mtgjson.ListingOutlierRatio
}

func Resolve(q tcgcsv.Quote) (price, refused float64) {
	if Contradicted(q.Market, q.Low) {
		return q.Low, q.Market
	}
	return q.Market, 0
}

func productFor(c store.PriceCandidate) (product, subtype string) {
	switch c.Finish {
	case finish.Etched:
		if c.EtchedProductID != "" {
			return c.EtchedProductID, tcgcsv.Foil
		}
		fallthrough
	case finish.Foil:
		if c.AltProductID != "" {
			return c.AltProductID, tcgcsv.Foil
		}
		return c.ProductID, tcgcsv.Foil
	default:
		return c.ProductID, tcgcsv.Normal
	}
}

type ContradictionSweep struct {
	Overrides []store.PriceOverride
	Checked   []string
}

func (f *Fetcher) Contradictions(ctx context.Context) (ContradictionSweep, error) {
	var sweep ContradictionSweep
	cands, err := f.st.PriceCandidates()
	if err != nil {
		return sweep, err
	}
	if len(cands) == 0 {
		return sweep, nil
	}
	opts := f.tcgcsvOptions()
	groups, err := tcgcsv.Groups(ctx, opts)
	if err != nil {
		f.say("skipping the contradicted-price check: %v", err)
		return sweep, nil
	}

	byGroup := map[int][]store.PriceCandidate{}
	for _, c := range cands {
		if c.Price <= 0 {
			continue
		}
		gid, ok := groups[strings.ToUpper(c.SetCode)]
		if !ok {
			continue
		}
		byGroup[gid] = append(byGroup[gid], c)
	}

	gids := make([]int, 0, len(byGroup))
	for gid := range byGroup {
		gids = append(gids, gid)
	}
	byGID, err := tcgcsv.GroupQuotesBulk(ctx, opts, gids)
	if err != nil {
		f.say("skipping the contradicted-price check: %v", err)
		return sweep, nil
	}

	seen := map[string]bool{}
	for gid, group := range byGroup {
		if ctx.Err() != nil {
			break
		}
		quotes, ok := byGID[gid]
		if !ok {
			continue
		}
		for _, c := range group {

			if !seen[c.ScryfallID] {
				seen[c.ScryfallID] = true
				sweep.Checked = append(sweep.Checked, c.ScryfallID)
			}
			product, subtype := productFor(c)
			q, ok := quotes[product][subtype]
			if !ok || !Contradicted(c.Price, q.Low) {
				continue
			}
			sweep.Overrides = append(sweep.Overrides, store.PriceOverride{
				ScryfallID: c.ScryfallID,
				Finish:     c.Finish,
				Price:      q.Low,
				Refused:    c.Price,
				Source:     OverrideSource,
				Reason:     OverrideReason,
			})
		}
	}
	return sweep, nil
}
