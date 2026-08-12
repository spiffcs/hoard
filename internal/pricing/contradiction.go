package pricing

// Refusing a market price that no sale stands behind.
//
// TCGplayer's market price — the figure Scryfall republishes as its USD
// price, and therefore the number behind most of a hoard's value — is a
// volume-weighted average over *completed sales*. Where a product has not
// sold, that average is taken over an empty set and the figure that comes
// back is not a low price but a non-price.
//
// Observed live: Aragorn and Arwen, Wedded (hoc #68, surge foil, released
// two days after it was read) carried a market price of $0.56 while the
// cheapest ask on the very same product was $97.55, the midpoint $100.07,
// and two other vendors quoted $59.99 and $120.51. Nothing was listed
// anywhere near $0.56 and nothing had been sold at all. Every guard hoard
// owned stayed quiet, because Scryfall and MTGJSON's tcgplayer bucket both
// carried that one number and agreeing with yourself is not corroboration.
//
// The fix is to prefer the cheapest ask, which is a figure a buyer could
// actually transact at. The cost of that trade is worth naming: a market
// price moves only when someone really buys, while the cheapest ask is a
// single listing and one $0.01 listing moves it for free. Substituting the
// ask swaps a robust-but-degenerate number for a live-but-attackable one,
// which is why the substituted figure is passed back through the
// cross-vendor guard in mtgjson rather than trusted on its own.

import (
	"context"
	"strings"

	"github.com/spiffcs/hoard/internal/mtgjson"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/tcgcsv"
)

// OverrideSource and OverrideReason are stored beside every correction and
// shown wherever a corrected price appears, so a substituted figure is never
// mistaken for one the catalog supplied.
const (
	OverrideSource = "tcgplayer-low"
	OverrideReason = "below-cheapest-ask"
)

// Contradicted reports whether a market price is contradicted by the
// cheapest ask standing on the same product.
//
// The bare inequality is not the test. A market price under the cheapest
// ask is ordinary — an illiquid old foil whose last sale undercuts today's
// listings is the normal state of a thin market, and 38 of the 39 owned
// products quoting market under low are exactly that, topping out at 3.3x.
// Only the magnitude separates a thin market from no market, so the
// judgement is the same ratio hoard already uses to throw out troll
// listings; see mtgjson.ListingOutlierRatio for the measurement behind it.
func Contradicted(market, low float64) bool {
	return market > 0 && low > 0 && low/market >= mtgjson.ListingOutlierRatio
}

// Resolve reduces a TCGplayer quote to the figure hoard should use, and
// reports the market price when it was refused.
//
// refused is zero when nothing was refused, so a caller that only wants the
// price can ignore it and a caller recording the refusal can test it. A
// contradicted quote with no ask to fall back on keeps its market price:
// hoard has detected a problem it cannot correct, and silently zeroing a
// held card is a worse answer than a wrong one it can report.
func Resolve(q tcgcsv.Quote) (price, refused float64) {
	if Contradicted(q.Market, q.Low) {
		return q.Low, q.Market
	}
	return q.Market, 0
}

// productFor picks the TCGplayer product whose listings price one held
// finish, and the subtype row within it.
//
// A printing sold as a single product answers with its own two subtype rows:
// Normal prices the non-foil copy and Foil the foil one. Where TCGplayer
// splits the treatment out — a ripple or surge foil, an etched card — the
// split product is the foil copy and the base product's Foil row, if it has
// one, describes something the owner does not hold. Getting this wrong would
// compare one card's price against another card's asks, which is exactly the
// mistake productVerified exists to prevent on the comps sheet.
func productFor(c store.PriceCandidate) (product, subtype string) {
	switch c.Finish {
	case "etched":
		if c.EtchedProductID != "" {
			return c.EtchedProductID, tcgcsv.Foil
		}
		fallthrough // an etched copy the feed knows only as a foil
	case "foil":
		if c.AltProductID != "" {
			return c.AltProductID, tcgcsv.Foil
		}
		return c.ProductID, tcgcsv.Foil
	default:
		return c.ProductID, tcgcsv.Normal
	}
}

// ContradictionSweep is one pass's findings and its reach.
//
// Checked names every printing whose TCGplayer group was actually read, and is
// what makes the result safe to apply. Corrections must be retired when the
// evidence for them lapses, but only a printing that was genuinely re-examined
// has had its evidence re-examined: clearing a correction because the network
// was down would report the refused price as the card's value again and call
// that a fix. So Checked, not the whole hoard, is the scope the store deletes
// within — a group that failed to download keeps yesterday's answer.
type ContradictionSweep struct {
	Overrides []store.PriceOverride
	Checked   []string
}

// Contradictions checks every owned printing against the asks standing on its
// own TCGplayer product and returns the corrections that should be in force.
//
// The comparison is against the *uncorrected* price (store.PriceCandidate's
// Price), never the effective one, so the sweep cannot read back a correction
// it wrote yesterday and conclude the problem has gone away. Within its reach
// the answer is complete rather than incremental, so a card whose feed price
// has since become sane simply stops appearing and stops being corrected.
//
// Everything here fails soft, like the overlay it shares a client with: an
// unreachable tcgcsv, a set TCGplayer groups differently, or a product the
// catalog has no row for all mean no correction, not a broken price run.
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
			continue // an unpriced finish has nothing to contradict
		}
		gid, ok := groups[strings.ToUpper(c.SetCode)]
		if !ok {
			continue // a set TCGplayer groups differently
		}
		byGroup[gid] = append(byGroup[gid], c)
	}

	seen := map[string]bool{}
	for gid, group := range byGroup {
		if ctx.Err() != nil {
			break
		}
		quotes, err := tcgcsv.GroupQuotes(ctx, opts, gid)
		if err != nil {
			f.say("skipping the contradicted-price check for group %d: %v", gid, err)
			continue
		}
		for _, c := range group {
			// Checked covers the printing whether or not it was corrected: a
			// clean read is exactly the evidence that retires a stale
			// correction, and recording only the hits would make corrections
			// permanent.
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
