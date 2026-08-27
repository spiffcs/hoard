package ui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/spiffcs/hoard/internal/progress"
)

const progressBarCells = 12

const minBlockHeight = 8

const minDetailCells = 12

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧"}

type stepLine struct {
	name   string
	last   progress.Event
	detail string
	tally  bool

	groups []string
	counts map[string]int
	loose  []string
}

func (s *stepLine) note(ev progress.Event) {
	if ev.NoteGroup == "" {
		s.loose = append(s.loose, ev.Note)
		return
	}
	if s.counts == nil {
		s.counts = map[string]int{}
	}
	if s.counts[ev.NoteGroup] == 0 {
		s.groups = append(s.groups, ev.NoteGroup)
	}
	s.counts[ev.NoteGroup]++
}

type Printer struct {
	w   io.Writer
	tty bool

	interval time.Duration

	width, height int

	mu       sync.Mutex
	steps    []*stepLine
	drawn    int
	frame    int
	lastDraw time.Time
	loose    bool
	closed   bool

	stop chan struct{}
	once sync.Once

	step     string
	counted  bool
	lineOpen bool
}

func NewPrinter(w io.Writer, tty bool) *Printer {
	p := NewPrinterSize(w, tty, terminalWidth(os.Stderr), terminalHeight(os.Stderr))
	if p.tty && !p.loose {
		go p.animate()
	}
	return p
}

func NewPrinterSize(w io.Writer, tty bool, width, height int) *Printer {
	interval := 10 * time.Second
	if tty {
		interval = 100 * time.Millisecond
	}
	return &Printer{
		w: w, tty: tty, interval: interval, width: width, height: height,
		loose: !tty || height < minBlockHeight,
		stop:  make(chan struct{}),
	}
}

func (p *Printer) Fn() progress.Fn {
	if p.tty && !p.loose {
		return p.handle
	}
	return progress.Throttled(p.handle, p.interval)
}

func (p *Printer) animate() {
	t := time.NewTicker(100 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-p.stop:
			return
		case <-t.C:
			p.spin()
		}
	}
}

func (p *Printer) spin() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed || len(p.steps) == 0 {
		return
	}
	p.frame++
	p.draw()
}

func (p *Printer) handle(ev progress.Event) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.tty && !p.loose {
		p.record(ev)
		if time.Since(p.lastDraw) >= p.interval {
			p.draw()
		}
		return
	}
	p.appendOnly(ev)
}

func (p *Printer) record(ev progress.Event) {
	s := p.current(ev.Step)
	if ev.Note != "" {
		s.note(ev)
	}
	if ev.Detail != "" {
		s.detail = ev.Detail
		s.tally = ev.Done > 0 || ev.Total > 0
	}
	if ev.Done > 0 || ev.Total > 0 {
		s.last = ev
	}
	if p.tty && len(p.steps) >= p.height {
		p.loose = true
	}
}

func (p *Printer) current(step string) *stepLine {
	if n := len(p.steps); n > 0 && p.steps[n-1].name == step {
		return p.steps[n-1]
	}
	for _, s := range p.steps {
		if s.name == step {
			return s
		}
	}
	s := &stepLine{name: step}
	p.steps = append(p.steps, s)
	return s
}

func (p *Printer) draw() {
	var b strings.Builder
	if p.drawn > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", p.drawn)
	}
	for i, s := range p.steps {
		current := i == len(p.steps)-1
		b.WriteString("\r\x1b[2K")
		b.WriteString(p.render(s, current && !p.closed, current))
		b.WriteString("\n")
	}
	p.drawn = len(p.steps)
	p.lastDraw = time.Now()
	fmt.Fprint(p.w, b.String())
}

func (p *Printer) render(s *stepLine, active, current bool) string {
	mark := "✓"
	if active {
		mark = spinFrames[p.frame%len(spinFrames)]
	}
	head := "  " + mark + " " + s.name

	var bar, counts, detail string
	if s.last.Total > 0 {
		bar = " " + ProgressBar(float64(s.last.Done)/float64(s.last.Total), progressBarCells)
	}
	if s.last.Done > 0 {
		counts = " " + ProgressCounts(s.last)
	}
	if s.detail != "" && (current || s.tally) {
		detail = " · " + s.detail
	}

	bases := []string{head + bar + counts, head + counts, head}
	for _, base := range bases {
		if line, ok := p.fit(base, detail); ok {
			return line
		}
	}
	for _, base := range bases {
		if p.width <= 0 || len([]rune(base)) <= p.width {
			return base
		}
	}
	return head
}

func (p *Printer) fit(base, detail string) (string, bool) {
	if p.width <= 0 || len([]rune(base+detail)) <= p.width {
		return base + detail, true
	}
	room := p.width - len([]rune(base))
	if room < 0 || detail == "" {
		return "", false
	}
	if room < minDetailCells {
		return "", false
	}
	return base + string([]rune(detail)[:room-1]) + "…", true
}

func (p *Printer) appendOnly(ev progress.Event) {
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
	p.record(ev)

	if ev.Done > 0 {
		p.counted = true
		if p.tty {
			p.redraw(ev)
		} else if ev.Note == "" && ev.Step == p.step {
			fmt.Fprintf(p.w, "  %s: %s%s\n", ev.Step, ProgressCounts(ev), suffix(ev))
		}
	} else if p.tty && ev.Step != "" {
		p.redraw(ev)
	}
}

func suffix(ev progress.Event) string {
	if ev.Detail == "" {
		return ""
	}
	return " · " + ev.Detail
}

func (p *Printer) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	p.once.Do(func() { close(p.stop) })

	if p.tty && p.drawn > 0 {
		p.draw()
		p.summarize()
		return
	}
	p.finishStep()
	p.step = ""
}

func (p *Printer) summarize() {
	for _, s := range p.steps {
		for _, g := range s.groups {
			p.warnLine(Count(s.counts[g]) + " " + g)
		}
		for _, n := range s.loose {
			p.warnLine(n)
		}
	}
}

func (p *Printer) warnLine(msg string) {
	if !strings.HasSuffix(msg, ".") {
		msg += "."
	}
	for _, line := range wrap("  ! "+msg, "    ", p.width) {
		fmt.Fprintln(p.w, line)
	}
}

func wrap(s, indent string, width int) []string {
	if width <= 0 || len([]rune(s)) <= width {
		return []string{s}
	}
	lead := s[:len(s)-len(strings.TrimLeft(s, " "))]
	var out []string
	line := lead
	for _, word := range strings.Fields(s) {
		switch {
		case strings.TrimSpace(line) == "":
			line += word
		case len([]rune(line))+1+len([]rune(word)) <= width:
			line += " " + word
		default:
			out = append(out, line)
			line = indent + word
		}
	}
	return append(out, line)
}

func (p *Printer) finishStep() {
	if p.tty && p.lineOpen {
		if n := len(p.steps); n > 0 {
			p.redraw(p.steps[n-1].last)
		}
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
	b.WriteString(suffix(ev))
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
