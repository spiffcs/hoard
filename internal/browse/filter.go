package browse

import (
	"github.com/spiffcs/hoard/internal/cardfilter"
	"github.com/spiffcs/hoard/internal/store"
)

func (c card) subject() cardfilter.Subject {
	return cardfilter.Subject{
		ScryfallID: c.ScryfallID,
		Name:       c.Name,
		SetCode:    c.SetCode,
		Finish:     c.Finish,
		Board:      c.Board,
		Quantity:   c.Quantity,
		Price:      c.Price,
		Value:      c.Value,
	}
}

func moverAsCard(c store.PriceChange) card {
	return card{
		ScryfallID:      c.ScryfallID,
		Name:            c.Name,
		SetCode:         c.SetCode,
		CollectorNumber: c.CollectorNumber,
		Finish:          c.Finish,
		Quantity:        c.Copies,
		Price:           &c.New,
		Value:           float64(c.Copies) * c.New,
		ColorIdentity:   c.ColorIdentity,
		Treatment:       c.Treatment,
	}
}

func unsupportedOnMovers(f cardfilter.Filter) string {
	if f.Uses("board") {
		return "board"
	}
	return ""
}

func watchAsCard(w store.WatchStatus) card {
	return card{
		ScryfallID:      w.ScryfallID,
		Name:            w.Name,
		SetCode:         w.SetCode,
		CollectorNumber: w.CollectorNumber,
		Finish:          w.Finish,
		Price:           w.PriceUSD,
		Treatment:       w.Treatment,
	}
}

func unpricedAsCard(r store.UnpricedRow) card {
	return card{
		ScryfallID:      r.ScryfallID,
		Name:            r.Name,
		SetCode:         r.SetCode,
		CollectorNumber: r.CollectorNumber,
		Finish:          r.Finish,
		Quantity:        r.Copies,
		ColorIdentity:   r.ColorIdentity,
		Treatment:       r.Treatment,
	}
}

func unsupportedOnWatches(f cardfilter.Filter) string {
	switch {
	case f.Uses("board"):
		return "board"
	case f.Uses("qty"):
		return "qty"
	case f.Uses("value"):
		return "value"
	}
	return ""
}

func marketAsCard(c store.OwnedFinish) card {
	return card{
		ScryfallID:      c.ScryfallID,
		Name:            c.Name,
		SetCode:         c.SetCode,
		CollectorNumber: c.CollectorNumber,
		Finish:          c.Finish,
		Quantity:        c.Copies,
		Value:           c.Value,
		ColorIdentity:   c.ColorIdentity,
		Treatment:       c.Treatment,
	}
}

func unsupportedOnMarket(f cardfilter.Filter) (key, why string) {
	switch {
	case f.Uses("price"):
		return "price", "a row here carries four prices, not one"
	case f.Uses("board"):
		return "board", "a market row sums every board"
	}
	return "", ""
}

func (m Model) filterMatchCount() int {
	switch m.view {
	case viewHoldings:
		return len(m.filteredCards)
	case viewMovers:
		return len(m.filteredMovers)
	case viewWatches:

		return m.watchTotalRows()
	case viewMarket:

		return len(m.marketAllRows) + len(m.marketAllComps)
	}
	return -1
}

func (m Model) filterUnsupported() string {
	switch m.view {
	case viewMovers:
		if k := unsupportedOnMovers(m.filter); k != "" {
			return k + ": does not apply on movers · a mover row sums every board"
		}
	case viewWatches:
		if k := unsupportedOnWatches(m.filter); k != "" {
			return k + ": does not apply on the watches screen · a watch is a line on a printing, not on copies"
		}
	case viewMarket:

		if k, why := unsupportedOnMarket(m.filter); k != "" {
			return k + ": does not apply on market · " + why
		}
	}
	return ""
}

func trendAsCard(r store.TrendRow) card {
	return card{
		ScryfallID:      r.ScryfallID,
		Name:            r.Name,
		SetCode:         r.SetCode,
		CollectorNumber: r.CollectorNumber,
		Finish:          r.Finish,
		Quantity:        r.Copies,
		Price:           &r.Last,
		Value:           float64(r.Copies) * r.Last,
		ColorIdentity:   r.ColorIdentity,
		Treatment:       r.Treatment,
	}
}
