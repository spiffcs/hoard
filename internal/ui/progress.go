package ui

// Printer renders progress events for the CLI, on stderr by convention.
//
// Two modes, chosen by the caller (main knows whether stderr is a TTY):
// at a terminal, one in-place updating line per step; piped, plain lines
// only — no carriage returns, no ANSI — so `2>err.txt` yields greppable
// text. The piped shape is a compatibility surface: scripts may watch it.

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/spiffcs/hoard/internal/progress"
)

// barCells is the progress bar's width. Narrow on purpose: the line also
// carries the step name, the counts, and sometimes a note.
const progressBarCells = 12

// Printer turns progress events into terminal output. Close finalizes any
// in-place line; forgetting it leaves the shell prompt glued to the bar.
type Printer struct {
	w   io.Writer
	tty bool
	// interval is the minimum gap between count-only updates: fast enough
	// to feel live at a TTY, slow enough not to spam a log when piped.
	interval time.Duration

	mu       sync.Mutex
	step     string
	last     progress.Event
	counted  bool // the current step has shown counts
	lineOpen bool // TTY: an unfinalized \r line is on screen
}

// NewPrinter builds a renderer for w. tty selects the in-place mode; pass
// the result of a Stat character-device check on the real destination.
func NewPrinter(w io.Writer, tty bool) *Printer {
	interval := 10 * time.Second
	if tty {
		interval = 100 * time.Millisecond
	}
	return &Printer{w: w, tty: tty, interval: interval}
}

// Fn is the callback to hand an operation. Count-only events are throttled;
// step transitions and notes always render.
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
			// Piped: a count line at most every interval (Throttled already
			// paced us), so a long download leaves a visible heartbeat.
			fmt.Fprintf(p.w, "  %s: %s\n", ev.Step, counts(ev))
		}
	} else if p.tty && ev.Step != "" {
		p.redraw(ev)
	}
}

// Close finalizes the output: the open TTY line gets its newline, and a
// piped step that showed counts states its final figure.
func (p *Printer) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.finishStep()
	p.step = ""
}

// finishStep ends the current step's rendering. At a TTY the in-place line
// is redrawn one final time and released; piped, nothing — the periodic
// count lines already told the story, and the operation's own summary
// (stdout) states the totals.
func (p *Printer) finishStep() {
	if p.tty && p.lineOpen {
		p.redraw(p.last)
		fmt.Fprint(p.w, "\n")
		p.lineOpen = false
	}
}

// closeLine breaks out of an in-place line so a full line (a note) can
// print without splicing into the bar.
func (p *Printer) closeLine() {
	if p.tty && p.lineOpen {
		fmt.Fprint(p.w, "\n")
		p.lineOpen = false
	}
}

// redraw paints the in-place line: erase, step, bar when determinate,
// counts when any.
func (p *Printer) redraw(ev progress.Event) {
	var b strings.Builder
	b.WriteString("\r\x1b[2K  ")
	b.WriteString(ev.Step)
	if ev.Total > 0 {
		frac := float64(ev.Done) / float64(ev.Total)
		bar := Bar(frac, progressBarCells)
		b.WriteString(" " + bar + strings.Repeat("░", max(progressBarCells-len([]rune(bar)), 0)))
	}
	if ev.Done > 0 {
		b.WriteString(" " + counts(ev))
	}
	fmt.Fprint(p.w, b.String())
	p.lineOpen = true
}

// counts renders "done/total unit" (or just "done unit" when the total is
// unknown), bytes as megabytes because nobody reasons in raw byte counts.
func counts(ev progress.Event) string {
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
