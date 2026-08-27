package ui

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/progress"
)

type screen struct {
	lines []string
	row   int
	col   int
}

var escape = regexp.MustCompile(`^\x1b\[([0-9;]*)([A-Za-z])`)

func (s *screen) at(row int) string {
	for len(s.lines) <= row {
		s.lines = append(s.lines, "")
	}
	return s.lines[row]
}

func (s *screen) set(row int, text string) {
	s.at(row)
	s.lines[row] = text
}

func (s *screen) put(text string) {
	line := []rune(s.at(s.row))
	for len(line) < s.col {
		line = append(line, ' ')
	}
	for i, r := range []rune(text) {
		if s.col+i < len(line) {
			line[s.col+i] = r
		} else {
			line = append(line, r)
		}
	}
	s.set(s.row, string(line))
	s.col += len([]rune(text))
}

func (s *screen) write(out string) {
	for len(out) > 0 {
		switch {
		case strings.HasPrefix(out, "\n"):
			s.row++
			s.col = 0
			s.at(s.row)
			out = out[1:]
		case strings.HasPrefix(out, "\r"):
			s.col = 0
			out = out[1:]
		case strings.HasPrefix(out, "\x1b"):
			m := escape.FindStringSubmatch(out)
			if m == nil {
				out = out[1:]
				continue
			}
			n, _ := strconv.Atoi(m[1])
			switch m[2] {
			case "A":
				s.row = max(s.row-n, 0)
			case "K":
				s.set(s.row, "")
			}
			out = out[len(m[0]):]
		default:
			i := strings.IndexAny(out, "\r\n\x1b")
			if i < 0 {
				i = len(out)
			}
			s.put(out[:i])
			out = out[i:]
		}
	}
}

func (s *screen) rows() int {
	n := len(s.lines)
	for n > 0 && strings.TrimSpace(s.lines[n-1]) == "" {
		n--
	}
	return n
}

func (s *screen) text() string { return strings.Join(s.lines[:s.rows()], "\n") }

func render(out string) *screen {
	s := &screen{}
	s.write(out)
	return s
}

func brief(s string) string {
	if len([]rune(s)) > 500 {
		return string([]rune(s)[:500]) + "… (truncated)"
	}
	return s
}

func steadyBuild(fn progress.Fn) {
	for i := 1; i <= 25; i++ {
		fn(progress.Event{Step: "downloading catalog", Done: int64(i) * 3 << 20,
			Total: 75 << 20, Unit: progress.UnitBytes, Detail: Count(i*4000) + " cards"})
	}
	for i := 1; i <= 20; i++ {
		fn(progress.Event{Step: "mapping card ids", Done: int64(i) * 5000,
			Total: 100000, Unit: progress.UnitCards})
	}
	for i := 1; i <= 334; i++ {
		fn(progress.Event{Step: "downloading price history", Done: int64(i) * 400 << 10,
			Total: 143 << 20, Unit: progress.UnitBytes,
			Detail: "set " + strconv.Itoa(i) + "/334"})
		if i%2 == 0 {
			fn(progress.Event{Step: "downloading price history",
				Note:      "skipping set t" + strconv.Itoa(i) + ": mtgjson has no such set",
				NoteGroup: "sets are not in MTGJSON, so their printings are unpriced"})
		}
	}
	fn(progress.Event{Step: "recording history"})
	fn(progress.Event{Step: "compacting the database"})
}

func TestTheWholeRunFitsInOneWindow(t *testing.T) {
	var sb strings.Builder
	p := NewPrinterSize(&sb, true, 100, 40)
	p.interval = 0
	steadyBuild(p.Fn())
	p.Close()

	scr := render(sb.String())
	if scr.rows() > 6 {
		t.Errorf("the run used %d rows, want at most 6 (five steps and a warning summary):\n%s",
			scr.rows(), brief(scr.text()))
	}
}

func TestFinishedStepsStayOnScreen(t *testing.T) {
	var sb strings.Builder
	p := NewPrinterSize(&sb, true, 100, 40)
	p.interval = 0
	steadyBuild(p.Fn())
	p.Close()

	scr := render(sb.String())
	if scr.rows() > 6 {
		t.Fatalf("the run used %d rows, want at most 6", scr.rows())
	}
	for _, want := range []string{
		"downloading catalog", "mapping card ids", "downloading price history",
		"recording history", "compacting the database", "75.0/75.0 MB", "100,000 cards",
	} {
		if !strings.Contains(scr.text(), want) {
			t.Errorf("the finished screen lost %q:\n%s", want, brief(scr.text()))
		}
	}
}

func TestDetailRidesOnTheStepLine(t *testing.T) {
	var sb strings.Builder
	p := NewPrinterSize(&sb, true, 100, 40)
	p.interval = 0
	fn := p.Fn()
	for i := 1; i <= 334; i++ {
		fn(progress.Event{Step: "downloading price history", Done: int64(i), Total: 334,
			Unit: progress.UnitSets, Detail: "set " + strconv.Itoa(i) + "/334"})
	}
	p.Close()

	scr := render(sb.String())
	if scr.rows() != 1 {
		t.Errorf("334 detail updates used %d rows, want 1", scr.rows())
	}
	if !strings.Contains(scr.text(), "set 334/334") {
		t.Errorf("the line lost the current activity:\n%s", brief(scr.text()))
	}
	if strings.Contains(scr.text(), "set 333/334") {
		t.Errorf("a stale detail is still on screen:\n%s", brief(scr.text()))
	}
}

func TestRepeatedNotesAreTalliedNotScrolled(t *testing.T) {
	var sb strings.Builder
	p := NewPrinterSize(&sb, true, 100, 40)
	p.interval = 0
	fn := p.Fn()
	for i := range 178 {
		fn(progress.Event{Step: "downloading price history",
			Note:      "skipping set t" + strconv.Itoa(i) + ": mtgjson has no such set",
			NoteGroup: "sets are not in MTGJSON, so their printings are unpriced"})
	}
	p.Close()

	scr := render(sb.String())
	if scr.rows() > 2 {
		t.Errorf("178 warnings used %d rows, want at most 2 "+
			"(the step line and one summary line):\n%s", scr.rows(), brief(scr.text()))
	}
	if !strings.Contains(scr.text(), "! 178 sets are not in MTGJSON, so their printings are unpriced") {
		t.Errorf("no summary of the suppressed warnings:\n%s", brief(scr.text()))
	}
	if strings.Contains(scr.text(), "skipping set t100") {
		t.Errorf("individual warnings are still on screen:\n%s", brief(scr.text()))
	}
}

func TestUngroupedNotesSurviveIntoTheSummary(t *testing.T) {
	var sb strings.Builder
	p := NewPrinterSize(&sb, true, 100, 40)
	p.interval = 0
	p.Fn()(progress.Event{Step: "downloading price history",
		Note: "rate limited; retrying in 30s"})
	p.Close()

	if !strings.Contains(render(sb.String()).text(), "rate limited; retrying in 30s") {
		t.Errorf("a one-off warning was swallowed:\n%s", brief(sb.String()))
	}
}

func TestSilentStepsAnimate(t *testing.T) {
	var sb strings.Builder
	p := NewPrinterSize(&sb, true, 100, 40)
	p.interval = 0
	p.Fn()(progress.Event{Step: "compacting the database"})

	before := render(sb.String()).text()
	seen := map[string]bool{before: true}
	for range 4 {
		p.spin()
		seen[render(sb.String()).text()] = true
	}
	p.Close()

	if len(seen) < 3 {
		t.Errorf("a silent step showed %d distinct frames, want it to animate "+
			"so it does not look hung:\n%s", len(seen), brief(render(sb.String()).text()))
	}
	if got := render(sb.String()).rows(); got != 1 {
		t.Errorf("spinning used %d rows, want 1", got)
	}
}

func TestBlockFallsBackWhenItCannotFit(t *testing.T) {
	var sb strings.Builder
	p := NewPrinterSize(&sb, true, 100, 3)
	p.interval = 0
	steadyBuild(p.Fn())
	p.Close()

	if regexp.MustCompile(`\x1b\[\d+A`).MatchString(sb.String()) {
		t.Errorf("a block taller than the 3-row terminal still redrew in place, "+
			"which corrupts the screen:\n%s", brief(sb.String()))
	}
}

func TestBlockLinesNeverExceedTheWidth(t *testing.T) {
	var sb strings.Builder
	p := NewPrinterSize(&sb, true, 60, 40)
	p.interval = 0
	steadyBuild(p.Fn())
	p.Close()

	scr := render(sb.String())
	for i, line := range strings.Split(scr.text(), "\n") {
		if n := len([]rune(line)); n > 60 {
			t.Errorf("row %d is %d cells wide, want at most 60 — it wraps and "+
				"breaks the block: %q", i, n, brief(line))
		}
	}
	if !strings.Contains(scr.text(), "downloading price history") {
		t.Errorf("clamping dropped the step name, which must always survive:\n%s",
			brief(scr.text()))
	}
}

func TestTheStepLineStaysUncluttered(t *testing.T) {
	var sb strings.Builder
	p := NewPrinterSize(&sb, true, 160, 40)
	p.interval = 0
	fn := p.Fn()
	fn(progress.Event{Step: "downloading price history", Done: 60 << 20, Total: 143 << 20,
		Unit: progress.UnitBytes, Detail: "resolving card ids \u00b7 set 137/334 (mb2)"})
	for range 142 {
		fn(progress.Event{Step: "downloading price history",
			Note:      "skipping set tx: mtgjson has no such set",
			NoteGroup: "sets are not in MTGJSON, so their printings are unpriced"})
	}
	p.Close()

	scr := render(sb.String())
	line := strings.Split(scr.text(), "\n")[0]
	if strings.Contains(line, "!") {
		t.Errorf("the step line still carries a warning tally: %q", line)
	}
	if !strings.Contains(line, "set 137/334 (mb2)") {
		t.Errorf("the step line lost the current activity: %q", line)
	}
	if !strings.Contains(scr.text(), "! 142 sets are not in MTGJSON") {
		t.Errorf("the end-of-run summary lost the tally:\n%s", brief(scr.text()))
	}
}

func TestOverlongDetailIsTruncatedNotDropped(t *testing.T) {
	var sb strings.Builder
	p := NewPrinterSize(&sb, true, 80, 40)
	p.interval = 0
	p.Fn()(progress.Event{Step: "downloading price history",
		Detail: "fetching 30 days of prices for 108,122 printings from MTGJSON (a large download)"})
	p.Close()

	line := strings.Split(render(sb.String()).text(), "\n")[0]
	if !strings.Contains(line, "\u00b7 fetching 30 days") {
		t.Errorf("the detail was dropped wholesale instead of truncated, so the "+
			"step goes silent: %q", line)
	}
	if !strings.HasSuffix(line, "\u2026") {
		t.Errorf("no ellipsis marking where the detail was cut: %q", line)
	}
	if n := len([]rune(line)); n > 80 {
		t.Errorf("the truncated line is %d cells, want at most 80: %q", n, line)
	}
}

func TestDetailIsDroppedWhenThereIsNoRoomToTruncate(t *testing.T) {
	var sb strings.Builder
	p := NewPrinterSize(&sb, true, 34, 40)
	p.interval = 0
	p.Fn()(progress.Event{Step: "downloading price history",
		Detail: "fetching 30 days of prices"})
	p.Close()

	line := strings.Split(render(sb.String()).text(), "\n")[0]
	if strings.Contains(line, "\u2026") {
		t.Errorf("a stub ellipsis with nothing before it is pure noise: %q", line)
	}
	if !strings.Contains(line, "downloading price history") {
		t.Errorf("the step name must always survive: %q", line)
	}
}

func TestFinishedStepsDropTheirActivityNote(t *testing.T) {
	var sb strings.Builder
	p := NewPrinterSize(&sb, true, 100, 40)
	p.interval = 0
	fn := p.Fn()
	fn(progress.Event{Step: "mapping card ids",
		Detail: "fetching every set's identifiers from MTGJSON in one file"})
	fn(progress.Event{Step: "mapping card ids", Done: 492, Total: 492, Unit: progress.UnitCards})
	fn(progress.Event{Step: "recording history"})
	p.Close()

	line := strings.Split(render(sb.String()).text(), "\n")[0]
	if strings.Contains(line, "fetching") {
		t.Errorf("a finished step still shows the activity it was mid-way through: %q", line)
	}
	if !strings.Contains(line, "492/492 cards") {
		t.Errorf("the finished step lost its counts: %q", line)
	}
}

func TestFinishedStepsKeepTheirTally(t *testing.T) {
	var sb strings.Builder
	p := NewPrinterSize(&sb, true, 100, 40)
	p.interval = 0
	fn := p.Fn()
	fn(progress.Event{Step: "downloading catalog", Done: 74 << 20, Total: 74 << 20,
		Unit: progress.UnitBytes, Detail: "108,122 cards"})
	fn(progress.Event{Step: "recording history"})
	p.Close()

	line := strings.Split(render(sb.String()).text(), "\n")[0]
	if !strings.Contains(line, "108,122 cards") {
		t.Errorf("the finished download lost the tally it was counting, which is "+
			"the whole point of the line: %q", line)
	}
}
