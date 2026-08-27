package progress

import (
	"sync"
	"time"
)

type Unit string

const (
	UnitCards Unit = "cards"
	UnitBytes Unit = "bytes"
	UnitSets  Unit = "sets"
	UnitRows  Unit = "rows"
)

type Event struct {
	Step string

	Done, Total int64
	Unit        Unit

	Detail string

	Note      string
	NoteGroup string
}

type Fn func(Event)

func (fn Fn) Emit(ev Event) {
	if fn != nil {
		fn(ev)
	}
}

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

type Mailbox struct {
	ch   chan Event
	done chan struct{}
	once sync.Once
}

func NewMailbox() *Mailbox {
	return &Mailbox{ch: make(chan Event, 1), done: make(chan struct{})}
}

func (m *Mailbox) Fn() Fn { return m.send }

func (m *Mailbox) C() <-chan Event { return m.ch }

func (m *Mailbox) Done() <-chan struct{} { return m.done }

func (m *Mailbox) Close() { m.once.Do(func() { close(m.done) }) }

func (m *Mailbox) send(ev Event) {
	for {
		select {
		case <-m.done:
			return
		case m.ch <- ev:
			return
		default:

			select {
			case <-m.ch:
			default:
			}
		}
	}
}
