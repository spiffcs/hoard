package browse

import (
	"fmt"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

type printingFinish struct {
	id  string
	fin finish.Finish
}

func (m *Model) setCards(setCode string) ([]card, error) {
	owned, err := m.store.SetByFinish(setCode)
	if err != nil {
		return nil, err
	}
	shelved, err := m.store.SetShelvedByFinish(setCode)
	if err != nil {
		return nil, err
	}
	unlisted, err := m.store.SetUnowned(setCode)
	if err != nil {
		return nil, err
	}

	slots := map[printingFinish]bool{}
	held := map[printingFinish]bool{}
	for _, r := range owned {
		held[printingFinish{r.ScryfallID, r.Finish}] = true
		slots[printingFinish{r.ScryfallID, r.Finish}] = true
	}
	waiting := map[printingFinish]store.UnownedRow{}
	for _, r := range shelved {
		waiting[printingFinish{r.ScryfallID, r.Finish}] = r
	}

	out := make([]card, 0, len(unlisted))
	catalogued := map[string]bool{}
	for _, p := range m.catalogPrintings(setCode) {
		catalogued[p.ID] = true
		for _, fin := range sellableFinishes(p) {
			slot := printingFinish{p.ID, fin}
			slots[slot] = true
			if held[slot] {
				continue
			}
			if r, ok := waiting[slot]; ok {
				out = append(out, unownedCard(r))
				continue
			}
			out = append(out, printingCard(p, fin))
		}
	}

	for _, r := range unlisted {
		if catalogued[r.ScryfallID] {
			continue
		}
		slots[printingFinish{r.ScryfallID, r.Finish}] = true
		out = append(out, unownedCard(r))
	}

	m.setOwned, m.setTotal = len(held), len(slots)
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

func printingCard(p scryfall.Card, fin finish.Finish) card {
	return card{
		ScryfallID: p.ID, Name: p.Name, SetCode: p.Set,
		CollectorNumber: p.CollectorNumber, Finish: fin,
		Price:         fin.EffectivePrice(p.PriceUSD, p.PriceUSDFoil, p.PriceUSDEtched),
		ColorIdentity: p.ColorIdentity,
		Treatment:     store.FoilTreatmentOf(p.PromoTypes),
		ImageURI:      p.ImageURI,
	}
}

func sellableFinishes(p scryfall.Card) []finish.Finish {
	if fins := scryfall.Finishes(p); len(fins) > 0 {
		return fins
	}
	return []finish.Finish{finish.Nonfoil}
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
