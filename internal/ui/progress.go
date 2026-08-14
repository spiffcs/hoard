package ui

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/spiffcs/hoard/internal/progress"
)

const progressBarCells = 12

type Printer struct {
	w   io.Writer
	tty bool

	interval time.Duration

	mu       sync.Mutex
	step     string
	last     progress.Event
	counted  bool
	lineOpen bool
}

func NewPrinter(w io.Writer, tty bool) *Printer {
	interval := 10 * time.Second
	if tty {
		interval = 100 * time.Millisecond
	}
	return &Printer{w: w, tty: tty, interval: interval}
}

func (p *Printer) Fn() progress.Fn {
	return progress.Throttled(p.handle, p.interval)
}

func (p *Printer) handle(ev progress.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if ev.Step != p.step {
		p.finishStep()
		p.step = ev.Step
		p.counted = false
		if !p.tty && ev.Step != "" {
			fmt.Fprintf(p.w, "  %s...\n", ev.Step)
		}
	}
	if ev.Note != "" {
		p.closeLine()
		fmt.Fprintf(p.w, "  %s\n", ev.Note)
	}
	p.last = ev

	if ev.Done > 0 {
		p.counted = true
		if p.tty {
			p.redraw(ev)
		} else if ev.Note == "" && ev.Step == p.step {

			fmt.Fprintf(p.w, "  %s: %s\n", ev.Step, ProgressCounts(ev))
		}
	} else if p.tty && ev.Step != "" {
		p.redraw(ev)
	}
}

func (p *Printer) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.finishStep()
	p.step = ""
}

func (p *Printer) finishStep() {
	if p.tty && p.lineOpen {
		p.redraw(p.last)
		fmt.Fprint(p.w, "\n")
		p.lineOpen = false
	}
}

func (p *Printer) closeLine() {
	if p.tty && p.lineOpen {
		fmt.Fprint(p.w, "\n")
		p.lineOpen = false
	}
}

func (p *Printer) redraw(ev progress.Event) {
	var b strings.Builder
	b.WriteString("\r\x1b[2K  ")
	b.WriteString(ev.Step)
	if ev.Total > 0 {
		frac := float64(ev.Done) / float64(ev.Total)
		b.WriteString(" " + ProgressBar(frac, progressBarCells))
	}
	if ev.Done > 0 {
		b.WriteString(" " + ProgressCounts(ev))
	}
	fmt.Fprint(p.w, b.String())
	p.lineOpen = true
}

func ProgressBar(frac float64, cells int) string {
	bar := Bar(frac, cells)
	return bar + strings.Repeat("░", max(cells-len([]rune(bar)), 0))
}

func ProgressCounts(ev progress.Event) string {
	if ev.Unit == progress.UnitBytes {
		if ev.Total > 0 {
			return fmt.Sprintf("%.1f/%.1f MB", mb(ev.Done), mb(ev.Total))
		}
		return fmt.Sprintf("%.1f MB", mb(ev.Done))
	}
	if ev.Total > 0 {
		return fmt.Sprintf("%s/%s %s", Count(int(ev.Done)), Count(int(ev.Total)), ev.Unit)
	}
	return fmt.Sprintf("%s %s", Count(int(ev.Done)), ev.Unit)
}

func mb(n int64) float64 { return float64(n) / (1024 * 1024) }
