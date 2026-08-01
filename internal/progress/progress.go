// Package progress defines the event seam between long-running operations
// and whatever is watching them — the CLI's stderr printer, the browser's
// status line, or nothing at all.
//
// The contract's one invariant: events are droppable narration. A consumer
// may coalesce them (Throttled), keep only the newest (Mailbox), or ignore
// them entirely (a nil Fn). Anything a caller must act on — errors, counts
// that appear in output, skipped items — travels in the operation's returned
// result, never only in an event.
//
// This is a leaf package: stdlib only, imported by both the action layer and
// the TUI without dragging either's dependencies into the other.
package progress

import (
	"sync"
	"time"
)

// Unit says what an Event's Done/Total count.
type Unit string

const (
	UnitCards Unit = "cards"
	UnitBytes Unit = "bytes"
	UnitSets  Unit = "sets"
	UnitRows  Unit = "rows"
)

// Event is one observation of an operation in flight.
type Event struct {
	// Step is a stable, human-readable phase label ("downloading catalog",
	// "refreshing cards"). Stable per run — counts never appear in it — so a
	// consumer can key state on it; a new Step value means the previous step
	// ended.
	Step string

	// Done and Total are cumulative units through this step. Total == 0
	// means indeterminate. Total may grow between events (a second resolve
	// pass discovering more work); consumers re-read it every event.
	Done, Total int64
	Unit        Unit

	// Note is transient prose worth showing but not keeping: "rate limited,
	// retrying in 30s", "skipping set PLST: mtgjson has no such set".
	Note string
}

// Fn receives events. Nil is silent — operations call through Emit rather
// than checking. Called synchronously on the operation's own goroutine;
// implementations must return quickly and must never block. UIs bridge
// through a Mailbox instead of doing work here.
type Fn func(Event)

// Emit calls fn if there is one. The nil check lives here so every producer
// call site stays one line.
func (fn Fn) Emit(ev Event) {
	if fn != nil {
		fn(ev)
	}
}

// Throttled returns a Fn that forwards count-bearing updates at most once
// per interval, but always forwards a Step transition or a Note immediately
// — those mark moments, not rates, and dropping one loses information a
// consumer cannot reconstruct from the next event.
func Throttled(fn Fn, interval time.Duration) Fn {
	if fn == nil {
		return nil
	}
	var mu sync.Mutex
	var lastStep string
	var lastSent time.Time
	return func(ev Event) {
		mu.Lock()
		defer mu.Unlock()
		if ev.Step != lastStep || ev.Note != "" || time.Since(lastSent) >= interval {
			lastStep = ev.Step
			lastSent = time.Now()
			fn(ev)
		}
	}
}

// Mailbox bridges a callback-producing worker to a channel-consuming UI. The
// producer side never blocks: a full mailbox drops the stale event and keeps
// the newest, because a UI redrawing at frame rate only ever wants the
// latest state. Close releases any consumer blocked on C; sends after Close
// are discarded.
type Mailbox struct {
	ch   chan Event
	done chan struct{}
	once sync.Once
}

func NewMailbox() *Mailbox {
	return &Mailbox{ch: make(chan Event, 1), done: make(chan struct{})}
}

// Fn is the producer side, handed to the operation as its progress callback.
func (m *Mailbox) Fn() Fn { return m.send }

// C is the consumer side: at most one pending event, always the newest.
func (m *Mailbox) C() <-chan Event { return m.ch }

// Done is closed when the mailbox closes, so a consumer can select against
// hanging on C forever after the operation ends.
func (m *Mailbox) Done() <-chan struct{} { return m.done }

// Close ends the mailbox; idempotent. The operation's runner calls it after
// the operation returns, unblocking any waiting consumer.
func (m *Mailbox) Close() { m.once.Do(func() { close(m.done) }) }

func (m *Mailbox) send(ev Event) {
	for {
		select {
		case <-m.done:
			return
		case m.ch <- ev:
			return
		default:
			// Full: discard the stale event and try again. The loop, rather
			// than a single retry, closes the race where the consumer drains
			// the channel between our discard and our send.
			select {
			case <-m.ch:
			default:
			}
		}
	}
}
