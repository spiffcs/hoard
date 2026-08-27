package action

import (
	"sync"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/store"
)

type CompsCache struct {
	owned func() ([]store.OwnedFinish, error)
	ver   func() (int64, error)
	comps func([]store.OwnedFinish, string) (map[finish.Finish]market.Comp, bool, error)

	mu     sync.Mutex
	rows   []store.OwnedFinish
	at     int64
	loaded bool
}

func NewCompsCache(d Deps) *CompsCache {
	return &CompsCache{
		owned: d.Store.OwnedByFinish,
		ver:   d.Store.DataVersion,
		comps: func(owned []store.OwnedFinish, id string) (map[finish.Finish]market.Comp, bool, error) {
			return CardCompsWith(d, owned, id)
		},
	}
}

func (c *CompsCache) Comps(scryfallID string) (map[finish.Finish]market.Comp, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	v, err := c.ver()
	if err != nil {
		return nil, false, err
	}
	if !c.loaded || v != c.at {
		rows, err := c.owned()
		if err != nil {
			return nil, false, err
		}
		c.rows, c.at, c.loaded = rows, v, true
	}
	return c.comps(c.rows, scryfallID)
}
