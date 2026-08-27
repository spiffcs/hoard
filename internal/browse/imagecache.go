package browse

import (
	"sync"

	"github.com/spiffcs/hoard/internal/ui"
)

const renderCacheSize = 48

type imageKey struct {
	id     string
	cols   int
	tier   ui.ImageTier
	imgID  int
	aspect float64
}

type imageEntry struct {
	lines    []string
	transmit string
}

type renderCache struct {
	mu    sync.Mutex
	cap   int
	order []imageKey
	byKey map[imageKey]imageEntry
}

func newRenderCache(capacity int) *renderCache {
	return &renderCache{cap: capacity, byKey: map[imageKey]imageEntry{}}
}

func (c *renderCache) get(k imageKey) (imageEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.byKey[k]
	return e, ok
}

func (c *renderCache) put(k imageKey, e imageEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, seen := c.byKey[k]; !seen {
		c.order = append(c.order, k)
		for len(c.order) > c.cap {
			delete(c.byKey, c.order[0])
			c.order = c.order[1:]
		}
	}
	c.byKey[k] = e
}

var renders = newRenderCache(renderCacheSize)
