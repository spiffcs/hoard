package browse

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

type costBasis struct {
	copies   int
	spent    float64
	uncosted int
}

func (c costBasis) average() float64 {
	if c.copies == 0 {
		return 0
	}
	return c.spent / float64(c.copies)
}

func costBasisByFinish(holdings []store.Holding) map[finish.Finish]costBasis {
	out := map[finish.Finish]costBasis{}
	for _, h := range holdings {
		c := out[h.Finish]
		if h.PurchasePrice == nil {
			c.uncosted += h.Quantity
		} else {
			c.copies += h.Quantity
			c.spent += float64(h.Quantity) * *h.PurchasePrice
		}
		out[h.Finish] = c
	}
	return out
}

func (m Model) costBasisLines(holdings []store.Holding) []string {
	byFinish := costBasisByFinish(holdings)
	fins := make([]finish.Finish, 0, len(byFinish))
	for f, c := range byFinish {
		if c.copies > 0 {
			fins = append(fins, f)
		}
	}
	slices.SortFunc(fins, func(a, b finish.Finish) int {
		return strings.Compare(a.String(), b.String())
	})

	var out []string
	for _, f := range fins {
		c := byFinish[f]
		copies := fmt.Sprintf("%s copies", ui.Count(c.copies))
		if c.copies == 1 {
			copies = "1 copy"
		}
		if c.uncosted > 0 {
			copies = fmt.Sprintf("%d of %d copies", c.copies, c.copies+c.uncosted)
		}
		out = append(out, m.theme.Help.Render(fmt.Sprintf("  %s · %s average paid · %s",
			f.String(), ui.Money(c.average()), copies)))
	}
	return out
}
