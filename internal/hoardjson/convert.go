package hoardjson

// Converters from hoard's domain types to the document model. Each returns a
// complete Document so a command's JSON path is one call and one Write.
//
// Every slice is allocated non-nil: an empty result must emit [], because null
// tells a consumer "field missing", not "nothing matched".

import (
	"math"

	"github.com/spiffcs/hoard/internal/arbitrage"
	"github.com/spiffcs/hoard/internal/export"
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
				Name:        r.Name,
				ScryfallID:  r.ScryfallID,
				MTGJSONUUID: r.MTGJSONUUID,
				SetCode:     r.Set,
				Number:      r.CollectorNumber,
				Finish:      r.Finish,
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

// FromUnpriced builds the unpriced document.
func FromUnpriced(rows []store.UnpricedRow) Document {
	u := &Unpriced{Rows: make([]UnpricedRow, 0, len(rows))}
	for _, r := range rows {
		u.Rows = append(u.Rows, UnpricedRow{
			Card: Card{
				Name:        r.Name,
				ScryfallID:  r.ScryfallID,
				MTGJSONUUID: r.MTGJSONUUID,
				SetCode:     r.SetCode,
				Number:      r.CollectorNumber,
				Finish:      r.Finish,
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
				Name:       c.Name,
				ScryfallID: c.ScryfallID,
				SetCode:    c.SetCode,
				Number:     c.CollectorNumber,
				Finish:     c.Finish,
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

// FromArbitrage builds the arbitrage document: the full ranking of every
// opportunity, per question, without the display's top-N truncation.
func FromArbitrage(res arbitrage.Result) Document {
	a := &Arbitrage{
		ComparedPrintings: res.Compared,
		Opportunities:     make([]Opportunity, 0, len(res.Opportunities)),
	}
	for _, r := range arbitrage.Rows(res, len(res.Opportunities)) {
		op := Opportunity{
			Card: Card{
				Name:        r.Card.Name,
				ScryfallID:  r.Card.ScryfallID,
				MTGJSONUUID: r.Card.MTGJSONUUID,
				SetCode:     r.Card.SetCode,
				Number:      r.Card.CollectorNumber,
				Finish:      r.Card.Finish,
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
	doc := envelope(KindArbitrage)
	doc.Arbitrage = a
	return doc
}
