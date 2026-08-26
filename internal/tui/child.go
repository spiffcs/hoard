package tui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

type Child struct {
	m model
}

func NewChild(ctx context.Context, s Searcher, add Adder, sc Scanner,
	initialName string, dests []Destination, opts ...Option) Child {
	m := newModel(ctx, s, add, sc, initialName, dests, opts...)
	m.embedded = true
	return Child{m: m}
}

func (c Child) Init() tea.Cmd { return c.m.Init() }

func (c Child) Update(msg tea.Msg) (Child, tea.Cmd) {
	next, cmd := c.m.Update(msg)
	c.m = next.(model)
	if c.m.done {
		return c, nil
	}
	return c, cmd
}

func (c Child) View() string { return c.m.View() }

func (c Child) Done() bool { return c.m.done }

func (c Child) Summary() Summary {
	sum := c.m.summary
	sum.Ignored = c.m.ignored
	return sum
}

func (c Child) Added() int { return c.m.addedCount }

func (c Child) AddedValue() float64 { return c.m.addedValue }

type Pending struct{ items []queueItem }

func (p Pending) Len() int { return len(p.items) }

func (c Child) Pending() Pending { return Pending{items: c.m.review} }

func (c *Child) Restore(p Pending) {
	if len(p.items) == 0 {
		return
	}
	c.m.review = append(c.m.review, p.items...)
	for _, it := range p.items {
		c.m.nextResolveID = max(c.m.nextResolveID, it.id)
		c.m.captureSeq = max(c.m.captureSeq, it.captureSeq)
	}
	c.m.status = fmt.Sprintf(
		"%d scanned cards are still waiting for review · tab opens the queue", len(p.items))
	c.m.statusErr = false
}

func (c Child) Err() error { return c.m.err }

func (c *Child) Close() {
	c.m.closeSession()
	c.m.resolveGen++
	dropped := len(c.m.review) + c.m.resolving
	c.m.resolving = 0
	c.m.review = nil
	if dropped > 0 {
		c.m.summary.add("discarded", fmt.Sprintf("%d scanned cards discarded unprocessed", dropped))
	}
	c.m.done = true
}
