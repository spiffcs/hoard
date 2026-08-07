package action

// The fast read capabilities. No steps, no confirms — they exist so every
// row of docs/parity.md names one function both frontends call, and so the
// TUI's palette has a single injection point per capability. Binder
// create/rename/remove are the deliberate exception: both frontends call
// the store directly (the browser through its Editor interface, whose undo
// closures need the store methods), and a wrapper here would be ceremony.

import (
	"github.com/spiffcs/hoard/internal/export"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
)

// SummaryData is the hoard's totals: the loose collection and every deck.
type SummaryData struct {
	Binder store.CollectionTotals
	Decks  []store.DeckSummary
}

// Summary reads the totals the bare command and the JSON document share.
func (d Deps) Summary() (SummaryData, error) {
	var s SummaryData
	var err error
	if s.Binder, err = d.Store.CollectionTotals(); err != nil {
		return s, err
	}
	s.Decks, err = d.Store.ListDecks()
	return s, err
}

// MoversData is every price change since a cutoff, plus how deep the
// history behind the answer actually runs — an empty result means "nothing
// moved" only when observations exist at all.
type MoversData struct {
	Observations int
	Oldest       string // RFC 3339; empty when no history
	Changes      []store.PriceChange
}

// Movers reports what moved between the cutoff (RFC 3339) and now.
func (d Deps) Movers(cutoff string) (MoversData, error) {
	var m MoversData
	var err error
	if m.Observations, m.Oldest, err = d.Store.PriceHistoryDepth(); err != nil {
		return m, err
	}
	if m.Observations == 0 {
		return m, nil
	}
	m.Changes, err = d.Store.Movers(cutoff)
	return m, err
}

// Unpriced lists the holdings no source can price.
func (d Deps) Unpriced() ([]store.UnpricedRow, error) { return d.Store.Unpriced() }

// Valuation assembles the dated valuation report: totals, every binder, the
// top holdings, and where each price came from.
func (d Deps) Valuation(top int) (report.ValuationData, error) {
	var v report.ValuationData
	var err error
	if v.AsOf, err = d.Store.LatestPriceStamp(); err != nil {
		return v, err
	}
	if v.Binder, err = d.Store.CollectionTotals(); err != nil {
		return v, err
	}
	if v.Binders, err = d.Store.ListBinders(); err != nil {
		return v, err
	}
	if v.Decks, err = d.Store.ListDecks(); err != nil {
		return v, err
	}
	owned, err := d.Store.OwnedByFinish()
	if err != nil {
		return v, err
	}
	sorted := report.SortOwned(owned)
	v.Top = sorted[:min(len(sorted), max(top, 0))]

	sources, err := d.Store.PriceSources()
	if err != nil {
		return v, err
	}
	for _, sc := range sources {
		if sc.Source == "" {
			v.Unpriced = sc
			continue
		}
		v.Sources = append(v.Sources, sc)
	}
	return v, nil
}

// ExportRows collects the requested holdings; empty refs mean everything —
// every binder, then every deck.
func (d Deps) ExportRows(binderRef, deckRef string) ([]export.Row, error) {
	if binderRef != "" {
		b, err := d.Store.BinderByRef(binderRef)
		if err != nil {
			return nil, err
		}
		return d.binderRows(b.ID, b.Name)
	}
	if deckRef != "" {
		dk, err := d.Store.DeckByRef(deckRef)
		if err != nil {
			return nil, err
		}
		return d.deckRows(dk.ID, dk.Name)
	}

	var rows []export.Row
	binders, err := d.Store.ListBinders()
	if err != nil {
		return nil, err
	}
	for _, b := range binders {
		br, err := d.binderRows(b.ID, b.Name)
		if err != nil {
			return nil, err
		}
		rows = append(rows, br...)
	}
	decks, err := d.Store.ListDecks()
	if err != nil {
		return nil, err
	}
	for _, dk := range decks {
		dr, err := d.deckRows(dk.ID, dk.Name)
		if err != nil {
			return nil, err
		}
		rows = append(rows, dr...)
	}
	return rows, nil
}

func (d Deps) binderRows(id int64, name string) ([]export.Row, error) {
	list, err := d.Store.BinderByFinish(id)
	if err != nil {
		return nil, err
	}
	rows := make([]export.Row, len(list))
	for i, r := range list {
		rows[i] = export.Row{
			Count:           r.Quantity,
			Name:            r.Name,
			Set:             r.SetCode,
			CollectorNumber: r.CollectorNumber,
			Finish:          r.Finish,
			Condition:       r.Condition,
			ScryfallID:      r.ScryfallID,
			MTGJSONUUID:     r.MTGJSONUUID,
			ColorIdentity:   r.ColorIdentity,
			Lang:            r.Lang,
			Container:       name,
			Kind:            "binder",
			// BinderByFinish sums across boards, and binder entries only
			// ever hold 'main' — there is no per-row board to report.
			Board:    "main",
			PriceUSD: r.Price(),
		}
	}
	return rows, nil
}

func (d Deps) deckRows(id int64, name string) ([]export.Row, error) {
	entries, err := d.Store.DeckEntries(id)
	if err != nil {
		return nil, err
	}
	rows := make([]export.Row, len(entries))
	for i, e := range entries {
		rows[i] = export.Row{
			Count:           e.Quantity,
			Name:            e.Card.Name,
			Set:             e.Card.SetCode,
			CollectorNumber: e.Card.CollectorNumber,
			Finish:          e.Finish,
			Condition:       e.Condition,
			ScryfallID:      e.Card.ScryfallID,
			MTGJSONUUID:     e.Card.MTGJSONUUID,
			ColorIdentity:   e.Card.ColorIdentity,
			Lang:            e.Card.Lang,
			Container:       name,
			Kind:            "deck",
			Board:           e.Board,
			PriceUSD:        e.Price(),
		}
	}
	return rows, nil
}
