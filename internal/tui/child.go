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
//   - Take Pending() off a done child and Restore() it into the next one, so
//     scans that finished without an answer survive between cascades instead
//     of being re-scanned. A parent that skips this drops them silently.

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

// Pending is a cascade's unanswered review queue, in transit between one
// cascade and the next.
//
// Opaque on purpose: a queued scan is a half-finished piece of the cascade's
// own state machine — the OCR line, the ranked printings its background
// resolution already fetched, the evidence that stopped it short of an
// auto-commit — and the only thing that can read it is the cascade. The
// parent's job is to hold it, not to know what it is.
//
// It lives in memory for exactly as long as the parent process does. A queued
// scan was never written to anything, and the cards are still on the desk:
// persisting a stale opinion about a pile that has since been put away would
// be worse than asking for it again.
type Pending struct{ items []queueItem }

// Len is how many cards are waiting, for a caller that wants to say so.
func (p Pending) Len() int { return len(p.items) }

// Pending hands back the scans this cascade never got an answer for, so a
// parent can carry them into the next one. Finishing with ctrl+d leaves the
// queue standing for exactly this; Close (the parent is quitting) does not,
// because nothing survives that.
func (c Child) Pending() Pending { return Pending{items: c.m.review} }

// Restore seeds a fresh cascade with a previous one's unanswered scans, so
// re-entering the add screen picks the pile back up where it was left rather
// than opening on an empty queue the operator has to re-scan into.
//
// Call before the first Update. The restored cards keep their own ids and
// capture sequence numbers and the counters move up past them: those numbers
// tell the duplicate rules which sightings came from one frame (a fanned
// playset) and which came from different ones, and a new capture reusing a
// restored number would make the two indistinguishable.
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
