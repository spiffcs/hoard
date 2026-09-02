package browse

import (
	"fmt"
	"slices"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func (m *Model) setCards(setCode string) ([]card, error) {
	owned, err := m.store.SetByFinish(setCode)
	if err != nil {
		return nil, err
	}
	missing, err := m.store.SetUnowned(setCode)
	if err != nil {
		return nil, err
	}

	held := map[string]bool{}
	for _, r := range owned {
		held[r.ScryfallID] = true
	}

	gaps := map[string]bool{}
	out := make([]card, 0, len(missing))
	for _, r := range missing {
		gaps[r.ScryfallID] = true
		out = append(out, unownedCard(r))
	}
	for _, p := range m.catalogPrintings(setCode) {
		if held[p.ID] || gaps[p.ID] {
			continue
		}
		gaps[p.ID] = true
		out = append(out, printingCard(p))
	}

	m.setOwned, m.setTotal = len(held), len(held)+len(gaps)
	m.setMissingCost = 0
	for _, c := range out {
		if c.Price != nil {
			m.setMissingCost += *c.Price
		}
	}

	if !m.setUnowned {
		return collectionCards(owned), nil
	}
	return out, nil
}

func (m *Model) catalogPrintings(setCode string) []scryfall.Card {
	if m.setPrints == nil {
		return nil
	}
	prints, err := m.setPrints(m.ctx, setCode)
	if err != nil {
		return nil
	}
	return prints
}

func collectionCards(rows []store.CollectionRow) []card {
	out := make([]card, 0, len(rows))
	for _, r := range store.CollectionByValue(rows) {
		out = append(out, card{
			ScryfallID: r.ScryfallID, Name: r.Name, SetCode: r.SetCode,
			CollectorNumber: r.CollectorNumber, Finish: r.Finish,
			Condition: r.Condition,
			Quantity:  r.Quantity, Price: r.Price(), Value: r.Value,
			AltSource: r.AltSource, ColorIdentity: r.ColorIdentity,
			Treatment: r.Treatment,
		})
	}
	return out
}

func unownedCard(r store.UnownedRow) card {
	c := collectionCards([]store.CollectionRow{r.CollectionRow})[0]
	c.Where = r.Where
	return c
}

func printingCard(p scryfall.Card) card {
	fin, price := offeredFinish(p)
	return card{
		ScryfallID: p.ID, Name: p.Name, SetCode: p.Set,
		CollectorNumber: p.CollectorNumber, Finish: fin, Price: price,
		ColorIdentity: p.ColorIdentity,
		Treatment:     store.FoilTreatmentOf(p.PromoTypes),
		ImageURI:      p.ImageURI,
	}
}

func offeredFinish(p scryfall.Card) (finish.Finish, *float64) {
	switch {
	case slices.Contains(p.Finishes, finish.Foil.String()):
		if !slices.Contains(p.Finishes, finish.Nonfoil.String()) {
			return finish.Foil, p.PriceUSDFoil
		}
	case slices.Contains(p.Finishes, finish.Etched.String()):
		return finish.Etched, p.PriceUSDEtched
	}
	return finish.Nonfoil, p.PriceUSD
}

func (m *Model) toggleSetUnowned() {
	sel := m.selectedContainer()
	if sel == nil || sel.Kind != kindSet {
		m.status, m.statusErr = "pick a set first · B browses by set", true
		return
	}
	was := m.holdingsSortColumns()
	m.setUnowned = !m.setUnowned
	m.keepSortKey(was)
	if err := m.loadCards(); err != nil {
		m.setUnowned = !m.setUnowned
		m.keepSortKey(unownedSortColumns)
		m.setError(err)
		return
	}
	m.deriveView()
	if m.setUnowned {
		m.status, m.statusErr = fmt.Sprintf("%s · %s you do not own · b returns to what you hold",
			sel.Name, ui.PluralCount(m.setTotal-m.setOwned, "printing", "printings")), false
		return
	}
	m.status, m.statusErr = sel.Name+" · what you hold · b shows what you are missing", false
}

func (m Model) setTally() string {
	if m.setUnowned {
		return fmt.Sprintf("%d/%d unowned", m.setTotal-m.setOwned, m.setTotal)
	}
	return fmt.Sprintf("%d/%d owned", m.setOwned, m.setTotal)
}
