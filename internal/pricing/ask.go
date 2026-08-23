package pricing

import (
	"context"
	"strings"

	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/tcgcsv"
)

const askLowOfMid = 0.50

func Ask(q tcgcsv.Quote) (float64, bool) {
	if q.Direct > 0 {
		return q.Direct, true
	}
	if q.Low <= 0 || q.Mid <= 0 {
		return 0, false
	}
	if q.Low < q.Mid*askLowOfMid {
		return 0, false
	}
	return q.Low, true
}

func (f *Fetcher) asks(ctx context.Context, refs []Ref) map[string][]mtgjson.Quote {
	want := make(map[string]bool, len(refs))
	for _, r := range refs {
		want[r.ScryfallID] = true
	}
	cands, err := f.st.PriceCandidates()
	if err != nil {
		f.say("skipping listing prices: %v", err)
		return nil
	}

	opts := f.tcgcsvOptions()
	groups, err := tcgcsv.Groups(ctx, opts)
	if err != nil {
		f.say("skipping listing prices: %v", err)
		return nil
	}

	byGroup := map[int][]store.PriceCandidate{}
	for _, c := range cands {
		if !want[c.ScryfallID] {
			continue
		}
		gid, ok := groups[strings.ToUpper(c.SetCode)]
		if !ok {
			continue
		}
		byGroup[gid] = append(byGroup[gid], c)
	}
	if len(byGroup) == 0 {
		return nil
	}

	gids := make([]int, 0, len(byGroup))
	for gid := range byGroup {
		gids = append(gids, gid)
	}
	byGID, err := tcgcsv.GroupQuotesBulk(ctx, opts, gids)
	if err != nil {
		f.say("skipping listing prices: %v", err)
		return nil
	}

	out := map[string][]mtgjson.Quote{}
	for gid, group := range byGroup {
		quotes, ok := byGID[gid]
		if !ok {
			continue
		}
		for _, c := range group {
			product, subtype := productFor(c)
			q, ok := quotes[product][subtype]
			if !ok {
				continue
			}
			ask, ok := Ask(q)
			if !ok {
				continue
			}
			out[c.ScryfallID] = append(out[c.ScryfallID], mtgjson.Quote{
				Provider: market.MarketProvider, Kind: mtgjson.Ask,
				Finish: c.Finish, Price: ask,
			})
		}
	}
	return out
}
