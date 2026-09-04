package browse

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/store"
)

// curveDeckModel is a deck with a real shape to it: a run of one- and
// two-drops, a gap at three, a fatty past the top bucket, lands that stay out
// of the bars, and a sideboard the curve must ignore.
func curveDeckModel(t *testing.T, width, height int) (Model, *fakeStore) {
	t.Helper()
	f := testStore()
	f.deckCards[201] = []store.EntryView{
		entry("Lightning Bolt", "main", finish.Nonfoil, 4, 5),
		entry("Noble Hierarch", "main", finish.Nonfoil, 2, 60),
		entry("Counterspell", "main", finish.Nonfoil, 3, 2),
		entry("Wrath of God", "main", finish.Nonfoil, 2, 8),
		entry("Ulamog", "main", finish.Nonfoil, 1, 30),
		entry("Wasteland", "main", finish.Nonfoil, 4, 50),
		entry("Pyroblast", "side", finish.Nonfoil, 3, 1),
	}
	f.mana = map[string]int{
		"Lightning Bolt-id": 1, "Noble Hierarch-id": 1, "Counterspell-id": 2,
		"Wrath of God-id": 4, "Ulamog-id": 11, "Pyroblast-id": 1,
	}
	f.lands = map[string]bool{"Wasteland-id": true}

	m := newTestModel(t, f)
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m = next.(Model)
	m = cursorOn(t, m, "Cheap Deck")
	if err := m.loadCards(); err != nil {
		t.Fatalf("loadCards: %v", err)
	}
	m.focus = paneCards
	return m, f
}

func frameLine(t *testing.T, view, want string) int {
	t.Helper()
	for i, line := range strings.Split(view, "\n") {
		if strings.Contains(ansi.Strip(line), want) {
			return i
		}
	}
	t.Fatalf("no line holding %q in frame:\n%s", want, view)
	return -1
}

func TestAWideDeckViewDrawsTheCurveBesideTheTable(t *testing.T) {
	m, _ := curveDeckModel(t, 140, 30)

	view := m.View()
	at := frameLine(t, view, "MANA CURVE")
	line := ansi.Strip(strings.Split(view, "\n")[at])

	if !strings.Contains(line, "NAME") {
		t.Errorf("the curve is not beside the card table, its line reads %q", line)
	}
	if !strings.Contains(ansi.Strip(view), "lands") {
		t.Errorf("the curve never tallied the lands:\n%s", view)
	}
}

func TestTheCurveCountsCopiesOfMainboardSpells(t *testing.T) {
	m, _ := curveDeckModel(t, 140, 30)

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "12 spells") {
		t.Errorf("the curve does not name its 12 mainboard spells:\n%s", view)
	}
	row := func(label string) string {
		t.Helper()
		for _, line := range m.curveLines(curveWidth) {
			if fields := strings.Fields(ansi.Strip(line)); len(fields) > 0 && fields[0] == label {
				return strings.TrimSpace(ansi.Strip(line))
			}
		}
		t.Fatalf("no %q row in the curve:\n%s", label, strings.Join(m.curveLines(curveWidth), "\n"))
		return ""
	}

	for _, want := range []struct{ label, copies string }{
		{"1", "6"}, {"2", "3"}, {"4", "2"}, {"7+", "1"}, {"lands", "4"},
	} {
		if got := row(want.label); !strings.HasSuffix(got, want.copies) {
			t.Errorf("the %q row reads %q, want it to end in %s copies",
				want.label, got, want.copies)
		}
	}
	if got := row("3"); got != "3" {
		t.Errorf("the empty three-drop row reads %q, want the label alone — a gap in "+
			"the curve is drawn, but with no bar and no count", got)
	}
}

func TestTheCurveKeepsTheSideboardOutOfIt(t *testing.T) {
	m, _ := curveDeckModel(t, 140, 30)

	if view := ansi.Strip(m.View()); strings.Contains(view, "15 spells") {
		t.Errorf("the sideboard's three Pyroblasts were counted:\n%s", view)
	}
}

func TestANarrowDeckViewDropsTheCurveBelowTheList(t *testing.T) {
	m, _ := curveDeckModel(t, 78, 30)

	view := m.View()
	curve := frameLine(t, view, "MANA CURVE")
	rule := frameLine(t, view, strings.Repeat("─", 20))
	last := frameLine(t, view, "Wasteland")

	if line := ansi.Strip(strings.Split(view, "\n")[curve]); strings.Contains(line, "NAME") {
		t.Errorf("the curve is still beside the table at width 78: %q", line)
	}
	if curve < last {
		t.Errorf("the curve is on line %d, above the card rows ending at %d", curve, last)
	}
	if curve > rule {
		t.Errorf("the curve is on line %d, below the frame's rule at %d — it scrolled off",
			curve, rule)
	}
}

func TestTheCursorBarStopsBeforeTheCurve(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(prev)

	m, _ := curveDeckModel(t, 140, 30)
	m = cardCursorOn(t, m, "Noble Hierarch")

	var barred string
	for line := range strings.SplitSeq(m.View(), "\n") {
		if strings.Contains(line, "\x1b[7m") {
			barred = line
			break
		}
	}
	if barred == "" {
		t.Fatalf("no cursor bar in the frame:\n%s", m.View())
	}
	if !strings.Contains(ansi.Strip(barred), "█") {
		t.Fatalf("the cursor row carries no curve content to run under: %q", ansi.Strip(barred))
	}
	highlighted := barred[:strings.LastIndex(barred, "\x1b[0m")]
	if strings.Contains(highlighted, "█") {
		t.Errorf("the cursor bar runs under the curve: %q", barred)
	}
}

func TestABinderShowsNoCurve(t *testing.T) {
	m, _ := curveDeckModel(t, 140, 30)
	m = atAllCards(t, m)

	if view := ansi.Strip(m.View()); strings.Contains(view, "MANA CURVE") {
		t.Errorf("the collection drew a mana curve:\n%s", view)
	}
}

func TestTheCurveFollowsACardOffTheMainboard(t *testing.T) {
	m, _ := curveDeckModel(t, 140, 30)
	m = cardCursorOn(t, m, "Counterspell")

	m = key(m, "b")

	if m.statusErr {
		t.Fatalf("b was refused: %q", m.status)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "11 spells") {
		t.Errorf("the curve still counts 12 spells after one left the main deck:\n%s", view)
	}
}

func TestTheCurveNeverOverflowsTheFrame(t *testing.T) {
	for _, w := range []int{60, 72, 84, 96, 110, 132, 160, 200} {
		for _, h := range []int{10, 14, 20, 30} {
			m, _ := curveDeckModel(t, w, h)
			view := m.View()
			lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
			for i, line := range lines {
				if got := ansi.StringWidth(line); got > w {
					t.Errorf("%dx%d: line %d is %d cells wide:\n%s", w, h, i, got, view)
				}
			}
			if len(lines) > h {
				t.Errorf("%dx%d: frame is %d lines tall", w, h, len(lines))
			}
		}
	}
}

func longCurveDeckModel(t *testing.T, width, height int) Model {
	t.Helper()
	f := testStore()
	f.deckCards[201] = nil
	f.mana = map[string]int{}
	for i := range 40 {
		name := "Filler " + strconv.Itoa(i)
		f.deckCards[201] = append(f.deckCards[201],
			entry(name, "main", finish.Nonfoil, 1, float64(i+1)))
		f.mana[name+"-id"] = i % 6
	}
	m := newTestModel(t, f)
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m = next.(Model)
	m = cursorOn(t, m, "Cheap Deck")
	if err := m.loadCards(); err != nil {
		t.Fatalf("loadCards: %v", err)
	}
	m.focus = paneCards
	return m
}

func TestADeckLongerThanThePaneStillShowsTheCurve(t *testing.T) {
	m := longCurveDeckModel(t, 78, 30)

	view := m.View()
	curve := frameLine(t, view, "MANA CURVE")
	rule := frameLine(t, view, strings.Repeat("─", 20))

	if curve > rule {
		t.Errorf("the curve is on line %d, past the frame's rule at %d — a long list "+
			"pushed it off screen", curve, rule)
	}
	if rows := m.cardListRows(); rows >= m.visibleRows() {
		t.Errorf("the card list kept %d of %d rows, so it never made room for the curve",
			rows, m.visibleRows())
	}
}

func TestTheHeaderCountsTheRowsTheCurveLeftRoomFor(t *testing.T) {
	m := longCurveDeckModel(t, 80, 24)

	view := ansi.Strip(m.View())
	drawn := 0
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "Filler ") && strings.Contains(line, "mh3/1") {
			drawn++
		}
	}

	want := fmt.Sprintf("1–%d of 40", drawn)
	if !strings.Contains(view, want) {
		t.Errorf("the header does not say %q while drawing %d card rows:\n%s",
			want, drawn, view)
	}
}
