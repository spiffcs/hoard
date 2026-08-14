package browse

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func reportModel(t *testing.T, lines []string) Model {
	t.Helper()
	m, err := New(testStore(), WithReport(func(top, width int) ([]string, error) {
		return lines, nil
	}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	return next.(Model)
}

func manyLines(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("line %d", i+1)
	}
	return out
}

func TestReportCommandOpensTextView(t *testing.T) {
	m := reportModel(t, []string{"hoard valuation", "total $123.45"})
	m, _ = runPaletteCommand(t, m, "report.view")
	if m.text == nil {
		t.Fatal("report did not open the text takeover")
	}
	if m.mode() != modeText {
		t.Fatalf("mode = %v, want modeText", m.mode())
	}
	v := m.View()
	if !strings.Contains(v, "hoard valuation") || !strings.Contains(v, "valuation report") {
		t.Fatalf("View missing report content or title:\n%s", v)
	}
}

func TestTextViewScrollClamps(t *testing.T) {
	m := reportModel(t, manyLines(100))
	m, _ = runPaletteCommand(t, m, "report.view")
	rows := m.textRows()
	maxOffset := 100 - rows

	m = key(m, "up")
	if m.text.offset != 0 {
		t.Fatalf("offset = %d after up at top", m.text.offset)
	}
	m = key(m, "G")
	if m.text.offset != maxOffset {
		t.Fatalf("offset = %d after G, want %d", m.text.offset, maxOffset)
	}
	m = key(m, "down")
	if m.text.offset != maxOffset {
		t.Fatalf("offset = %d after down at bottom", m.text.offset)
	}
	m = key(m, "g")
	if m.text.offset != 0 {
		t.Fatalf("offset = %d after g", m.text.offset)
	}
	m = key(m, "pgdown")
	if m.text.offset != rows {
		t.Fatalf("offset = %d after pgdown, want %d", m.text.offset, rows)
	}
	if v := m.View(); !strings.Contains(v, fmt.Sprintf("lines %d–", rows+1)) {
		t.Fatalf("footer wrong:\n%s", v)
	}
}

func TestTextViewEscRestoresPanes(t *testing.T) {
	m := reportModel(t, manyLines(3))
	m, _ = runPaletteCommand(t, m, "report.view")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.text != nil || m.mode() != modeBrowse {
		t.Fatal("esc must close the takeover")
	}
}

func TestTextViewQIsInertAndCtrlCQuits(t *testing.T) {
	m := reportModel(t, manyLines(3))
	m, _ = runPaletteCommand(t, m, "report.view")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = next.(Model)
	if cmd != nil || m.text == nil {
		t.Fatal("q must do nothing in the takeover — quitting is ctrl+c's job")
	}
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd == nil {
		t.Fatal("ctrl+c must quit from the takeover")
	}
}

func TestTextViewPaletteReopens(t *testing.T) {
	m := reportModel(t, manyLines(3))
	m, _ = runPaletteCommand(t, m, "report.view")
	m = key(m, ":")
	if m.text != nil || m.palette == nil {
		t.Fatal(": must close the takeover and open the palette")
	}
}

func TestConfirmOutranksTextView(t *testing.T) {
	m := reportModel(t, manyLines(3))
	m, _ = runPaletteCommand(t, m, "report.view")
	m.confirm = &pendingConfirm{prompt: "sure?", onYes: func(*Model) tea.Cmd { return nil }}
	if m.mode() != modeConfirm {
		t.Fatalf("mode = %v, want confirm above text", m.mode())
	}
}

func TestReportEntryHiddenWithoutFn(t *testing.T) {
	m := newTestModel(t, testStore())
	for i := range m.commands {
		if m.commands[i].id == "report.view" && m.commands[i].applies(&m) {
			t.Fatal("report entry must hide without an injected fn")
		}
	}
}

func TestTextViewNarrowWidthRenders(t *testing.T) {
	m := reportModel(t, manyLines(40))
	m, _ = runPaletteCommand(t, m, "report.view")
	next, _ := m.Update(tea.WindowSizeMsg{Width: 20, Height: 8})
	m = next.(Model)
	if v := m.View(); v == "" {
		t.Fatal("narrow render must not vanish")
	}
}

func TestOpConfirmIsVisibleOverTheTextTakeover(t *testing.T) {
	m := reportModel(t, manyLines(30))
	m, _ = runPaletteCommand(t, m, "report.view")
	if m.text == nil {
		t.Fatal("setup: no takeover open")
	}
	m, reply := ask(t, m, "download the catalog (1.2 GB)?")
	if m.mode() != modeConfirm {
		t.Fatalf("mode = %v, want modeConfirm over the takeover", m.mode())
	}
	if v := m.View(); !strings.Contains(v, "download the catalog") {
		t.Fatalf("the question that owns the keyboard is not on screen:\n%s", v)
	}
	m = key(m, "y")
	mustReply(t, reply, true)
	if m.text == nil {
		t.Error("answering the confirm should leave the takeover up")
	}
}
