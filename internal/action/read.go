package action

import (
	"github.com/spiffcs/hoard/internal/cardfilter"
	"github.com/spiffcs/hoard/internal/export"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
)

type SummaryData struct {
	Binder store.CollectionTotals
	Decks  []store.DeckSummary
}

func (d Deps) Summary() (SummaryData, error) {
	var s SummaryData
	var err error
	if s.Binder, err = d.Store.CollectionTotals(); err != nil {
		return s, err
	}
	s.Decks, err = d.Store.ListDecks()
	return s, err
}

type MoversData struct {
	Observations int
	Oldest       string
	Changes      []store.PriceChange
}

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

func (d Deps) Unpriced() ([]store.UnpricedRow, error) { return d.Store.Unpriced() }

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

func rowSubject(r export.Row) cardfilter.Subject {
	var value float64
	if r.PriceUSD != nil {
		value = float64(r.Count) * *r.PriceUSD
	}
	return cardfilter.Subject{
		ScryfallID: r.ScryfallID,
		Name:       r.Name,
		SetCode:    r.Set,
		Finish:     r.Finish,
		Board:      r.Board,
		Quantity:   r.Count,
		Price:      r.PriceUSD,
		Value:      value,
		Paid:       r.PurchasePrice,
	}
}

func (d Deps) FilteredExportRows(binderRef, deckRef string, f cardfilter.Filter) ([]export.Row, error) {
	rows, err := d.ExportRows(binderRef, deckRef)
	if err != nil || f.Empty() {
		return rows, err
	}

	var allowed map[string]bool
	if f.NeedsCatalog() {
		if allowed, err = d.Store.MatchingCardIDs(f.Traits()); err != nil {
			return nil, err
		}
	}

	kept := make([]export.Row, 0, len(rows))
	for _, r := range rows {
		if f.Matches(rowSubject(r), allowed) {
			kept = append(kept, r)
		}
	}
	return kept, nil
}

func detailFor(details map[string]store.CardDetail, scryfallID string) *store.CardDetail {
	d := details[scryfallID]
	if !d.Enriched {
		return nil
	}
	return &d
}

func (d Deps) binderRows(id int64, name string) ([]export.Row, error) {
	list, err := d.Store.BinderByFinish(id)
	if err != nil {
		return nil, err
	}
	details, err := d.Store.CardDetailsInContainer(id)
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
			Detail:          detailFor(details, r.ScryfallID),
			Lang:            r.Lang,
			ContainerID:     id,
			Container:       name,
			Kind:            "binder",

			Board:         "main",
			PriceUSD:      r.Price(),
			PurchasePrice: r.PurchasePrice,
		}
	}
	return rows, nil
}

func (d Deps) deckRows(id int64, name string) ([]export.Row, error) {
	entries, err := d.Store.DeckEntries(id)
	if err != nil {
		return nil, err
	}
	details, err := d.Store.CardDetailsInContainer(id)
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
			Detail:          detailFor(details, e.Card.ScryfallID),
			Lang:            e.Card.Lang,
			ContainerID:     id,
			Container:       name,
			Kind:            "deck",
			Board:           e.Board,
			PriceUSD:        e.Price(),
			PurchasePrice:   e.PurchasePrice,
		}
	}
	return rows, nil
}
