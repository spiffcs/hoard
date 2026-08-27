package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/spiffcs/hoard/internal/progress"
)

func TestPrinterPipedGolden(t *testing.T) {
	var sb strings.Builder
	p := NewPrinter(&sb, false)
	p.interval = 0
	fn := p.Fn()

	fn(progress.Event{Step: "checking catalog"})
	fn(progress.Event{Step: "refreshing cards", Done: 75, Total: 300, Unit: progress.UnitCards})
	fn(progress.Event{Step: "refreshing cards", Done: 150, Total: 300, Unit: progress.UnitCards,
		Note: "rate limited; retrying in 30s"})
	fn(progress.Event{Step: "refreshing cards", Done: 300, Total: 300, Unit: progress.UnitCards})
	fn(progress.Event{Step: "downloading catalog", Done: 12 << 20, Total: 77 << 20, Unit: progress.UnitBytes})
	p.Close()

	want := strings.Join([]string{
		"  checking catalog...",
		"  refreshing cards...",
		"  refreshing cards: 75/300 cards",
		"  rate limited; retrying in 30s",
		"  refreshing cards: 300/300 cards",
		"  downloading catalog...",
		"  downloading catalog: 12.0/77.0 MB",
		"",
	}, "\n")
	if sb.String() != want {
		t.Errorf("piped output:\n%q\nwant:\n%q", sb.String(), want)
	}
	if strings.ContainsAny(sb.String(), "\r\x1b") {
		t.Error("piped output contains carriage returns or ANSI")
	}
}

func TestPrinterPipedNoteOnStepTransition(t *testing.T) {
	var sb strings.Builder
	p := NewPrinter(&sb, false)
	p.interval = 0
	fn := p.Fn()
	fn(progress.Event{Step: "resolving", Note: "skipping set PLST"})
	p.Close()
	want := "  resolving...\n  skipping set PLST\n"
	if sb.String() != want {
		t.Errorf("output %q, want %q", sb.String(), want)
	}
}

func TestPrinterTTY(t *testing.T) {
	var sb strings.Builder
	p := NewPrinterSize(&sb, true, 100, 40)
	p.interval = 0
	fn := p.Fn()

	fn(progress.Event{Step: "refreshing cards", Done: 150, Total: 300, Unit: progress.UnitCards})
	fn(progress.Event{Step: "saving"})
	p.Close()

	out := sb.String()
	if !strings.Contains(out, "\r\x1b[2K") || !strings.Contains(out, "\x1b[") {
		t.Errorf("no in-place redraw of the block: %q", out)
	}

	scr := render(out)
	if scr.rows() != 2 {
		t.Errorf("the run used %d rows, want 2 (one per step): %q", scr.rows(), out)
	}
	if !strings.Contains(scr.text(), "150/300 cards") {
		t.Errorf("no counts on the line: %s", scr.text())
	}
	if !strings.Contains(scr.text(), "█") || !strings.Contains(scr.text(), "░") {
		t.Errorf("no bar with track: %s", scr.text())
	}
	if !strings.Contains(scr.text(), "refreshing cards") ||
		!strings.Contains(scr.text(), "saving") {
		t.Errorf("a finished step left the screen: %s", scr.text())
	}
}

func TestPrinterThrottlesCountEvents(t *testing.T) {
	var sb strings.Builder
	p := NewPrinter(&sb, false)
	p.interval = time.Hour
	fn := p.Fn()
	fn(progress.Event{Step: "s", Done: 1, Total: 10, Unit: progress.UnitRows})
	fn(progress.Event{Step: "s", Done: 2, Total: 10, Unit: progress.UnitRows})
	fn(progress.Event{Step: "s", Done: 3, Total: 10, Unit: progress.UnitRows})
	p.Close()
	if got := strings.Count(sb.String(), "rows"); got != 1 {
		t.Errorf("%d count lines, want 1 (the step-transition event):\n%s", got, sb.String())
	}
}

func TestPrinterPipedCarriesTheDetail(t *testing.T) {
	var sb strings.Builder
	p := NewPrinter(&sb, false)
	p.interval = 0
	fn := p.Fn()
	fn(progress.Event{Step: "downloading catalog", Done: 12 << 20, Total: 77 << 20,
		Unit: progress.UnitBytes, Detail: "5,000 cards"})
	p.Close()
	if !strings.Contains(sb.String(), "5,000 cards") {
		t.Errorf("piped output %q lost the running tally", sb.String())
	}
}
