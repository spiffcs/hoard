package ui

import "testing"

func TestParsesTerminalSizeReports(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		kind byte
		h, w int
		ok   bool
	}{
		{"cell size", "\x1b[6;26;13t", '6', 26, 13, true},
		{"window size", "\x1b[4;1040;624t", '4', 1040, 624, true},
		{"noise around it", "q\x1b[6;34;17tzz", '6', 34, 17, true},
		{"wrong report kind", "\x1b[4;1040;624t", '6', 0, 0, false},
		{"too few params", "\x1b[6;26t", '6', 0, 0, false},
		{"zero dimension", "\x1b[6;26;0t", '6', 0, 0, false},
		{"unterminated", "\x1b[6;26;13", '6', 0, 0, false},
		{"empty", "", '6', 0, 0, false},
	} {
		h, w, ok := parseSizeReport([]byte(tc.in), tc.kind)
		if ok != tc.ok || h != tc.h || w != tc.w {
			t.Errorf("%s: parseSizeReport(%q, %q) = %d, %d, %v; want %d, %d, %v",
				tc.name, tc.in, tc.kind, h, w, ok, tc.h, tc.w, tc.ok)
		}
	}
}

func TestCellAspectPrefersTheDirectCellReport(t *testing.T) {
	if got := cellAspectFrom(26, 13, 0, 0, 0, 0); got != 2 {
		t.Errorf("cell 13x26 gave aspect %v, want 2", got)
	}
	if got := cellAspectFrom(27, 10, 0, 0, 0, 0); got != 2.7 {
		t.Errorf("cell 10x27 gave aspect %v, want 2.7", got)
	}
}

func TestCellAspectFallsBackToTheWindowGrid(t *testing.T) {
	if got := cellAspectFrom(0, 0, 1040, 624, 40, 48); got != 2 {
		t.Errorf("1040x624 over 48x40 cells gave aspect %v, want 2", got)
	}
	if got := cellAspectFrom(0, 0, 1040, 624, 0, 48); got != 0 {
		t.Errorf("a zero row count must not divide: got %v", got)
	}
}

func TestImplausibleCellAspectsAreRejected(t *testing.T) {
	for _, tc := range []struct {
		name string
		h, w int
	}{
		{"wider than tall", 10, 100},
		{"absurdly tall", 100, 10},
		{"zero width", 26, 0},
		{"zero height", 0, 13},
	} {
		if got := cellAspectFrom(tc.h, tc.w, 0, 0, 0, 0); got != 0 {
			t.Errorf("%s (%dx%d) gave aspect %v, want 0 so the caller keeps its default",
				tc.name, tc.w, tc.h, got)
		}
	}
}

func TestReadsGhosttysActualReply(t *testing.T) {
	reply := []byte("\x1b[6;27;14t\x1b[4;1755;3010t")

	cellH, cellW, ok := parseSizeReport(reply, '6')
	if !ok || cellH != 27 || cellW != 14 {
		t.Fatalf("cell report = %d, %d, %v; want 27, 14, true", cellH, cellW, ok)
	}
	winH, winW, ok := parseSizeReport(reply, '4')
	if !ok || winH != 1755 || winW != 3010 {
		t.Fatalf("window report = %d, %d, %v; want 1755, 3010, true", winH, winW, ok)
	}

	measured := 27.0 / 14.0
	if got := cellAspectFrom(cellH, cellW, winH, winW, 65, 215); got != measured {
		t.Errorf("aspect = %v, want %v", got, measured)
	}
	if grid := cellAspectFrom(0, 0, winH, winW, 65, 215); grid != measured {
		t.Errorf("window-grid fallback = %v, want the same %v", grid, measured)
	}
}
