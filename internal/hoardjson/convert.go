package hoardjson

// Converters from hoard's domain types to the document model. Each returns a
// complete Document so a command's JSON path is one call and one Write.
//
// Every slice is allocated non-nil: an empty result must emit [], because null
// tells a consumer "field missing", not "nothing matched".

import (
	"math"

	"github.com/spiffcs/hoard/internal/export"
	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
)

func envelope(kind Kind) Document {
	return Document{SchemaVersion: SchemaVersion, Kind: kind}
}

// cents rounds a dollar amount to whole cents. The store's sums accumulate
// binary-float noise (a $6,069.77 collection totals 6069.7699999999995), and
// a money field is cent-denominated by nature — the noise is an artifact, not
// information, and it would churn diffs of otherwise-unchanged documents.
// Ratios (spread, liquidity) are not money and stay unrounded.
// jsonCondition omits an unassessed condition rather than emitting the word.
// The document's rule is that absent means unknown — a consumer that sees no
// condition knows nobody has said, which is exactly what "unknown" would have
// told it at the cost of a field on almost every row.
func jsonCondition(c string) string {
	if c == "unknown" {
		return ""
	}
	return c
}

func cents(v float64) float64 { return math.Round(v*100) / 100 }

func centsPtr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	c := cents(*v)
	return &c
}

// FromSummary builds the summary document, decks ordered by value as the
// summary table lists them.
func FromSummary(binder store.CollectionTotals, decks []store.DeckSummary) Document {
	sorted := store.DecksByValue(decks)
	s := &Summary{
		Binder: Totals{
			DistinctCards: binder.DistinctCards,
			TotalCopies:   binder.TotalCopies,
			ValueUsd:      cents(binder.Value),
		},
		Decks: make([]DeckTotals, 0, len(sorted)),
	}
	copies, value := binder.TotalCopies, binder.Value
	for _, d := range sorted {
		s.Decks = append(s.Decks, DeckTotals{Name: d.Name, Totals: Totals{
			DistinctCards: d.DistinctCards,
			TotalCopies:   d.TotalCopies,
			ValueUsd:      cents(d.Value),
		}})
		copies += d.TotalCopies
		value += d.Value
	}
	s.Total = GrandTotal{TotalCopies: copies, ValueUsd: cents(value)}

	doc := envelope(KindSummary)
	doc.Summary = s
	return doc
}

// FromExportRows builds the holdings document from export rows, in the
// export's canonical order.
func FromExportRows(rows []export.Row) Document {
	h := &Holdings{Rows: make([]Holding, 0, len(rows))}
	for _, r := range export.Sorted(rows) {
		h.Rows = append(h.Rows, Holding{
			Card: Card{
				Name:          r.Name,
				ScryfallID:    r.ScryfallID,
				MTGJSONUUID:   r.MTGJSONUUID,
				SetCode:       r.Set,
				Number:        r.CollectorNumber,
				Finish:        r.Finish,
				Condition:     jsonCondition(r.Condition),
				Lang:          r.Lang,
				ColorIdentity: r.ColorIdentity,
			},
			Count:         r.Count,
			Container:     r.Container,
			ContainerKind: r.Kind,
			Board:         r.Board,
			PriceUsd:      centsPtr(r.PriceUSD),
		})
	}
	doc := envelope(KindHoldings)
	doc.Holdings = h
	return doc
}

// FromSnapshot builds the interchange document for one whole hoard: the
// catalog, container identity and watches from the snapshot, and the holdings
// from the same export rows every other holdings emission uses.
//
// Prices on a Printing are carried verbatim rather than rounded to cents. Every
// other document here is a report, where cent-rounding removes float noise a
// reader would only be distracted by; this one is a copy of the catalog on its
// way into another database, and a copy should be a copy.
func FromSnapshot(snap store.Snapshot, rows []export.Row) Document {
	h := &Hoard{
		DatabaseVersion: snap.Version,
		Printings:       make([]Printing, 0, len(snap.Printings)),
		Containers:      make([]Container, 0, len(snap.Containers)),
		Watches:         make([]Watch, 0, len(snap.Watches)),
	}
	for _, p := range snap.Printings {
		h.Printings = append(h.Printings, Printing{
			ScryfallID:     p.Card.ID,
			Name:           p.Card.Name,
			SetCode:        p.Card.Set,
			Number:         p.Card.CollectorNumber,
			ScryfallURL:    p.Card.ScryfallURL,
			UpdatedAt:      p.UpdatedAt,
			MTGJSONUUID:    p.MTGJSONUUID,
			PriceUsd:       p.Card.PriceUSD,
			PriceUsdFoil:   p.Card.PriceUSDFoil,
			PriceUsdEtched: p.Card.PriceUSDEtched,
			Raw:            p.Card.Raw,
		})
	}
	for _, c := range snap.Containers {
		// The document speaks the export's vocabulary — "binder", not the
		// storage layer's "collection" — so a container name and kind mean
		// the same thing here as in the holdings rows beside them.
		kind := "binder"
		if c.Kind == store.KindDeck {
			kind = "deck"
		}
		h.Containers = append(h.Containers, Container{
			Name: c.Name, Kind: kind, Source: c.Source,
			SourceID: c.SourceID, SourceURL: c.SourceURL, Format: c.Format,
		})
	}
	for _, w := range snap.Watches {
		h.Watches = append(h.Watches, Watch{
			Card: Card{
				Name:        w.Name,
				ScryfallID:  w.ScryfallID,
				MTGJSONUUID: w.MTGJSONUUID,
				SetCode:     w.SetCode,
				Number:      w.CollectorNumber,
				Finish:      w.Finish,
				Lang:        w.Lang,
			},
			Op:        w.Op,
			Threshold: w.Threshold,
			Display:   w.Display,
			CreatedAt: w.CreatedAt,
		})
	}
	h.Holdings = *FromExportRows(rows).Holdings

	doc := envelope(KindHoard)
	doc.Hoard = h
	return doc
}

// FromUnpriced builds the unpriced document.
func FromUnpriced(rows []store.UnpricedRow) Document {
	u := &Unpriced{Rows: make([]UnpricedRow, 0, len(rows))}
	for _, r := range rows {
		u.Rows = append(u.Rows, UnpricedRow{
			Card: Card{
				Name:          r.Name,
				ScryfallID:    r.ScryfallID,
				MTGJSONUUID:   r.MTGJSONUUID,
				SetCode:       r.SetCode,
				Number:        r.CollectorNumber,
				Finish:        r.Finish,
				Lang:          r.Lang,
				ColorIdentity: r.ColorIdentity,
			},
			Copies:     r.Copies,
			Containers: r.Containers,
		})
	}
	doc := envelope(KindUnpriced)
	doc.Unpriced = u
	return doc
}

// FromMovers builds the movers document: every change, ordered by absolute
// impact, largest first. The display's per-section truncation is a reading
// aid, not part of the data.
func FromMovers(since, recordedSince string, changes []store.PriceChange) Document {
	m := &Movers{
		Since:         since,
		RecordedSince: recordedSince,
		Changes:       make([]PriceChange, 0, len(changes)),
	}
	for _, c := range store.MoversByImpact(changes) {
		m.Changes = append(m.Changes, PriceChange{
			Card: Card{
				Name:          c.Name,
				ScryfallID:    c.ScryfallID,
				SetCode:       c.SetCode,
				Number:        c.CollectorNumber,
				Finish:        c.Finish,
				Lang:          c.Lang,
				ColorIdentity: c.ColorIdentity,
			},
			Copies:    c.Copies,
			OldUsd:    cents(c.Old),
			NewUsd:    cents(c.New),
			ImpactUsd: cents(c.TotalDelta()),
			Source:    c.Source,
		})
	}
	doc := envelope(KindMovers)
	doc.Movers = m
	return doc
}

// FromValuation builds the report document from the same data the text
// report renders, so the two can never state different figures.
func FromValuation(d report.ValuationData) Document {
	r := &Report{
		AsOf:        d.AsOf,
		Binder:      Totals{DistinctCards: d.Binder.DistinctCards, TotalCopies: d.Binder.TotalCopies, ValueUsd: cents(d.Binder.Value)},
		Binders:     make([]DeckTotals, 0, len(d.Binders)),
		TopHoldings: make([]ReportHolding, 0, len(d.Top)),
		Sources:     make([]SourceCount, 0, len(d.Sources)),
		Unpriced:    UnpricedCount{Printings: d.Unpriced.Printings, Copies: d.Unpriced.Copies},
	}
	deckCopies, deckValue := 0, 0.0
	for _, dk := range d.Decks {
		deckCopies += dk.TotalCopies
		deckValue += dk.Value
	}
	r.Decks = DeckAggregate{Count: len(d.Decks), TotalCopies: deckCopies, ValueUsd: cents(deckValue)}
	r.Total = GrandTotal{TotalCopies: d.Binder.TotalCopies + deckCopies,
		ValueUsd: cents(d.Binder.Value + deckValue)}
	for _, b := range d.Binders {
		r.Binders = append(r.Binders, DeckTotals{Name: b.Name, Totals: Totals{
			DistinctCards: b.DistinctCards, TotalCopies: b.TotalCopies, ValueUsd: cents(b.Value)}})
	}
	for _, o := range d.Top {
		h := ReportHolding{
			Card: Card{
				Name:        o.Name,
				ScryfallID:  o.ScryfallID,
				MTGJSONUUID: o.MTGJSONUUID,
				SetCode:     o.SetCode,
				Number:      o.CollectorNumber,
				Finish:      o.Finish,
				Lang:        o.Lang,
			},
			Copies:   o.Copies,
			ValueUsd: cents(o.Value),
		}
		// A zero-value row is an unpriced one: the store values what it
		// cannot price at exactly zero, and a per-copy price of $0.00 would
		// claim knowledge the absence is there to deny.
		if o.Value > 0 && o.Copies > 0 {
			p := cents(o.Value / float64(o.Copies))
			h.PriceUsd = &p
		}
		r.TopHoldings = append(r.TopHoldings, h)
	}
	for _, sc := range d.Sources {
		r.Sources = append(r.Sources, SourceCount{
			Source: sc.Source, Printings: sc.Printings, Copies: sc.Copies})
	}
	doc := envelope(KindReport)
	doc.Report = r
	return doc
}

// FromWatchCheck builds the watch document from one check's results.
func FromWatchCheck(checked int, fired []store.WatchStatus) Document {
	w := &WatchCheck{Checked: checked, Fired: make([]FiredWatch, 0, len(fired))}
	for _, f := range fired {
		price := 0.0
		if f.PriceUSD != nil {
			price = *f.PriceUSD
		}
		w.Fired = append(w.Fired, FiredWatch{
			Card: Card{
				Name:        f.Name,
				ScryfallID:  f.ScryfallID,
				MTGJSONUUID: f.MTGJSONUUID,
				SetCode:     f.SetCode,
				Number:      f.CollectorNumber,
				Finish:      f.Finish,
				Lang:        f.Lang,
			},
			Op:           f.Op,
			ThresholdUsd: cents(f.Threshold),
			PriceUsd:     cents(price),
		})
	}
	doc := envelope(KindWatch)
	doc.Watch = w
	return doc
}

// FromMarket builds the arbitrage document: the full ranking of every
// opportunity, per question, without the display's top-N truncation.
func FromMarket(res market.Result) Document {
	a := &Market{
		ComparedPrintings: res.Compared,
		Opportunities:     make([]Opportunity, 0, len(res.Opportunities)),
		Comps:             make([]Comp, 0, len(res.Comps)),
	}
	for _, c := range res.Comps {
		jc := Comp{
			Card: Card{
				Name:          c.Card.Name,
				ScryfallID:    c.Card.ScryfallID,
				MTGJSONUUID:   c.Card.MTGJSONUUID,
				SetCode:       c.Card.SetCode,
				Number:        c.Card.CollectorNumber,
				Finish:        c.Card.Finish,
				Lang:          c.Card.Lang,
				ColorIdentity: c.Card.ColorIdentity,
			},
			Copies:   c.Card.Copies,
			ValueUsd: cents(c.Card.Value),
			LowUsd:   cents(c.Low),
			LowFrom:  c.LowFrom,
		}
		if c.HasMarket {
			jc.MarketUsd = centsPtr(&c.Market)
		}
		if c.HasCK {
			jc.CardKingdomUsd = centsPtr(&c.CK)
		}
		if c.HasManapool {
			jc.ManapoolUsd = centsPtr(&c.Manapool)
		}
		if c.HasBuylist {
			jc.BuylistUsd, jc.BuylistTo = centsPtr(&c.Buylist), c.BuylistTo
		}
		if c.HasSpread() {
			s := c.Spread()
			jc.Spread = &s
		}
		a.Comps = append(a.Comps, jc)
	}
	for _, r := range market.Rows(res, len(res.Opportunities)) {
		op := Opportunity{
			Card: Card{
				Name:          r.Card.Name,
				ScryfallID:    r.Card.ScryfallID,
				MTGJSONUUID:   r.Card.MTGJSONUUID,
				SetCode:       r.Card.SetCode,
				Number:        r.Card.CollectorNumber,
				Finish:        r.Card.Finish,
				Lang:          r.Card.Lang,
				ColorIdentity: r.Card.ColorIdentity,
			},
			Copies:   r.Card.Copies,
			ValueUsd: cents(r.Card.Value),
			Kind:     r.Kind.String(),
			BuyUsd:   cents(r.BuyAt),
			BuyFrom:  r.BuyFrom,
		}
		if r.HasMarket {
			market, below := cents(r.Market), r.BelowMarket()
			op.MarketUsd, op.BelowMarket = &market, &below
		}
		if r.HasBuy {
			sell, profit := cents(r.SellAt), cents(r.Profit())
			op.SellUsd, op.SellTo = &sell, r.SellTo
			op.ProfitUsd = &profit
			if r.HasMarket {
				liq := r.Liquidity()
				op.Liquidity = &liq
			}
		}
		a.Opportunities = append(a.Opportunities, op)
	}
	doc := envelope(KindMarket)
	doc.Market = a
	return doc
}
