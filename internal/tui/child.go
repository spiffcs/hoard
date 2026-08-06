package tui

// Child is the add cascade as an embeddable component: the same model
// tui.Run drives as a standalone program, wrapped so a parent bubbletea
// model (the browser) can run it as a full-screen takeover. The wrapper is
// the only difference — one cascade, two frames around it.
//
// The contract with the parent:
//   - Forward every message the parent doesn't handle itself while the
//     child is active, plus tea.WindowSizeMsg (the child sizes itself into
//     whatever it is given; synthesize one immediately on open so the
//     default 80×22 never renders).
//   - After each Update, check Done(); a done child has already closed its
//     camera session and its tea.Quit was swallowed here — the parent just
//     drops the child and reads Summary().
//   - If the parent tears the child down early (quitting with the cascade
//     open), call Close() first: it shuts the camera session and records
//     what was abandoned, so the receipt stays honest.

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

type Child struct {
	m model
}

// NewChild builds a cascade for embedding. Arguments mirror Run's.
func NewChild(ctx context.Context, s Searcher, add Adder, sc Scanner,
	initialName string, dests []Destination) Child {
	m := newModel(ctx, s, add, sc, initialName, dests)
	m.embedded = true
	return Child{m: m}
}

func (c Child) Init() tea.Cmd { return c.m.Init() }

// Update advances the cascade. When the cascade finishes it swallows the
// model's tea.Quit — returning a nil cmd — so the quit can never reach the
// parent's program; the parent observes Done() instead.
func (c Child) Update(msg tea.Msg) (Child, tea.Cmd) {
	next, cmd := c.m.Update(msg)
	c.m = next.(model)
	if c.m.done {
		return c, nil
	}
	return c, cmd
}

func (c Child) View() string { return c.m.View() }

// Done reports whether the cascade has finished (esc at the name prompt, or
// ctrl+c). A done child holds no open camera session.
func (c Child) Done() bool { return c.m.done }

// Summary is the cascade's receipt so far: auto-adds, reviewed cards,
// skips, discards. Valid at any time, complete once Done. Manual
// type-a-name adds don't enter the summary (it is the scan receipt);
// Added counts those too.
func (c Child) Summary() Summary {
	sum := c.m.summary
	sum.Ignored = c.m.ignored
	return sum
}

// Added is the total number of adds this cascade committed, manual and
// scanned alike.
func (c Child) Added() int { return c.m.addedCount }

// AddedValue is the market value of those adds (qty-weighted; unpriced
// printings contribute nothing).
func (c Child) AddedValue() float64 { return c.m.addedValue }

// Err is the fatal error that ended the cascade, if any.
func (c Child) Err() error { return c.m.err }

// Close force-ends the cascade from outside — the parent is quitting while
// the cascade is still up. It closes the camera session (the helper window
// and its event pump must not outlive the program) and records anything
// queued or still resolving as discarded, the same accounting the
// close-prompt's discard branch keeps, so the exit receipt reflects what
// was abandoned rather than pretending it never happened. Idempotent.
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
