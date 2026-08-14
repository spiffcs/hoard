package browse

import (
	"github.com/spiffcs/hoard/internal/finish"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

var settlingNow = time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)

func daysBefore(n int) string {
	return settlingNow.AddDate(0, 0, -n).Format(time.DateOnly)
}

func settlingStore() *fakeStore {
	st := testStore()
	st.sets = []store.SetSummary{
		{Code: "hob", Name: "The Hobbit", ReleasedAt: daysBefore(-2), Copies: 3, Value: 30},
		{Code: "uma", Name: "Ultimate Masters", ReleasedAt: daysBefore(3000), Copies: 5, Value: 270},
	}
	st.movers = []store.PriceChange{
		{ScryfallID: "hob-id", Name: "Mountain-king", SetCode: "hob", CollectorNumber: "7",
			ReleasedAt: daysBefore(-2), Finish: finish.Nonfoil, Copies: 1, Old: 10, New: 20},
		{ScryfallID: "uma-id", Name: "Bitterblossom", SetCode: "uma", CollectorNumber: "85",
			ReleasedAt: daysBefore(3000), Finish: finish.Nonfoil, Copies: 1, Old: 10, New: 8},
	}
	return st
}

func pinWindow(t *testing.T, days int) {
	t.Helper()
	prev := store.SettlingDays()
	t.Cleanup(func() { store.SetSettlingDays(prev) })
	store.SetSettlingDays(days)
}

func onSettlingSets(t *testing.T, st *fakeStore) Model {
	t.Helper()
	m, err := New(st, WithEnv(ui.Env{Color: true}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !m.setsMode {
		t.Fatal("expected the browser to open on the sets lens")
	}
	m.clock = func() time.Time { return settlingNow }
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	return next.(Model)
}

func onSettlingMovers(t *testing.T, m Model) Model {
	t.Helper()
	m = key(m, "v")
	if m.view != viewMovers {
		t.Fatalf("view = %v, want movers", m.view)
	}
	return m
}

func TestSidebarMarksOnlySettlingSets(t *testing.T) {
	m := onSettlingSets(t, settlingStore())
	lines := m.containerLines(containerPaneWidth)

	var hob, uma string
	for _, l := range lines {
		switch {
		case strings.Contains(l, "Hobbit"):
			hob = l
		case strings.Contains(l, "Ultimate"):
			uma = l
		}
	}
	if hob == "" || uma == "" {
		t.Fatalf("sidebar = %q, want both set rows", lines)
	}
	if !strings.HasPrefix(strings.TrimSpace(ansi.Strip(hob)), settlingMark) {
		t.Errorf("settling row = %q, want it to lead with %q", hob, settlingMark)
	}
	if strings.Contains(uma, settlingMark) {
		t.Errorf("settled row = %q, want no mark", uma)
	}

	for _, l := range lines {
		if strings.Contains(l, allCardsName) && strings.Contains(l, settlingMark) {
			t.Errorf("All Cards row = %q, want no mark", l)
		}
	}
}

func TestSidebarMarkKeepsTheRowsAligned(t *testing.T) {
	marked := onSettlingSets(t, settlingStore()).containerLines(containerPaneWidth)

	quiet := settlingStore()
	for i := range quiet.sets {
		quiet.sets[i].ReleasedAt = daysBefore(3000)
	}
	plain := onSettlingSets(t, quiet).containerLines(containerPaneWidth)

	for _, lines := range [][]string{marked, plain} {
		want := ansi.StringWidth(lines[1])
		for i, l := range lines[1:] {
			w := ansi.StringWidth(l)
			if w != want {
				t.Errorf("row %d width = %d, want %d (%q)", i+1, w, want, l)
			}
			if w > containerPaneWidth {
				t.Errorf("row %d width = %d, over the %d-cell pane (%q)",
					i+1, w, containerPaneWidth, l)
			}
		}
	}

	for i, l := range plain[1:] {
		if strings.HasPrefix(ansi.Strip(l), " ") {
			t.Errorf("unmarked row %d = %q, want no gutter reserved for a mark "+
				"no row is wearing", i+1, l)
		}
	}

	if !strings.HasPrefix(ansi.Strip(marked[1]), " ") &&
		!strings.HasPrefix(ansi.Strip(marked[1]), settlingMark) {
		t.Errorf("marked row = %q, want the mark column present", marked[1])
	}
}

func TestSidebarMarksASetWhoseNameIsTruncated(t *testing.T) {
	st := settlingStore()
	long := "Marvel Super Heroes Secret Wars Commander"
	st.sets[0] = store.SetSummary{Code: "hob", Name: long,
		ReleasedAt: daysBefore(1), Copies: 3, Value: 30}

	var row string
	for _, l := range onSettlingSets(t, st).containerLines(containerPaneWidth) {
		if strings.Contains(ansi.Strip(l), "Marvel") {
			row = ansi.Strip(l)
		}
	}
	if row == "" {
		t.Fatal("no row for the long-named set")
	}
	if !strings.Contains(row, "…") {
		t.Fatalf("row = %q, want a name long enough to truncate — the fixture no "+
			"longer exercises the bug", row)
	}
	if !strings.HasPrefix(strings.TrimSpace(row), settlingMark) {
		t.Errorf("row = %q, want the mark to survive the truncation", row)
	}
}

func TestMoversNetHoldsOutSettlingSetsAtAllCards(t *testing.T) {
	m := onSettlingMovers(t, onSettlingSets(t, settlingStore()))
	if sel := m.selectedContainer(); sel == nil || sel.Kind != kindAllCards {
		t.Fatalf("selection = %+v, want All Cards", sel)
	}
	if len(m.filteredMovers) != 2 {
		t.Fatalf("filteredMovers = %d, want both rows still listed", len(m.filteredMovers))
	}

	_, totals := m.viewHeader()

	if !strings.Contains(totals, "-$2.00") {
		t.Errorf("totals = %q, want the net to be the settled row alone (-$2.00)", totals)
	}
	if strings.Contains(totals, "$8.00") {
		t.Errorf("totals = %q, want the settling set held out of the net", totals)
	}
	if !strings.Contains(totals, "1 settling set held out") {
		t.Errorf("totals = %q, want the count of sets held out", totals)
	}

	if !strings.Contains(totals, "2 moved") {
		t.Errorf("totals = %q, want both rows counted", totals)
	}
}

func TestMoversNetIsWholeInTheSettlingSetsOwnView(t *testing.T) {
	m := onSettlingSets(t, settlingStore())
	m = atSet(t, m, "hob")
	m = onSettlingMovers(t, m)

	if len(m.filteredMovers) != 1 {
		t.Fatalf("filteredMovers = %d, want the set's own row", len(m.filteredMovers))
	}
	_, totals := m.viewHeader()
	if !strings.Contains(totals, "+$10.00") {
		t.Errorf("totals = %q, want the set's own movement in full", totals)
	}
	if strings.Contains(totals, "held out") {
		t.Errorf("totals = %q, want nothing held out in the set's own view", totals)
	}
}

func TestMoversNetInASettledSetsView(t *testing.T) {
	m := onSettlingSets(t, settlingStore())
	m = atSet(t, m, "uma")
	m = onSettlingMovers(t, m)

	_, totals := m.viewHeader()
	if !strings.Contains(totals, "-$2.00") {
		t.Errorf("totals = %q, want the set's own movement", totals)
	}
	if strings.Contains(totals, "held out") {
		t.Errorf("totals = %q, want nothing held out", totals)
	}
}

func TestNothingSettlingChangesNothing(t *testing.T) {
	st := settlingStore()
	for i := range st.sets {
		st.sets[i].ReleasedAt = daysBefore(3000)
	}
	for i := range st.movers {
		st.movers[i].ReleasedAt = daysBefore(3000)
	}
	m := onSettlingSets(t, st)

	for _, l := range m.containerLines(containerPaneWidth) {
		if strings.Contains(l, settlingMark) {
			t.Errorf("sidebar row = %q, want no mark", l)
		}
	}
	if strings.Contains(m.helpLine(), settlingMark+" new set") {
		t.Errorf("help = %q, want no legend for a symbol nowhere on screen", m.helpLine())
	}
	_, totals := onSettlingMovers(t, m).viewHeader()
	if strings.Contains(totals, "held out") {
		t.Errorf("totals = %q, want no held-out clause", totals)
	}
	if !strings.Contains(totals, "+$8.00") {
		t.Errorf("totals = %q, want the plain sum of both rows", totals)
	}
}

func TestSettlingLegendAppearsWithTheMark(t *testing.T) {
	m := onSettlingSets(t, settlingStore())
	if !strings.Contains(m.helpLine(), settlingMark+" new set") {
		t.Errorf("help = %q, want the legend while a set is settling", m.helpLine())
	}
}

func TestSettlingWindowIsInThePalette(t *testing.T) {
	find := func(t *testing.T, m Model) bool {
		t.Helper()
		m.openPalette()
		for _, mt := range m.palette.matches {
			if m.commands[mt.index].id == "movers.settling" {
				return true
			}
		}
		return false
	}
	sets := onSettlingSets(t, settlingStore())
	if !find(t, sets) {
		t.Error("SetSettlingWindow missing from the palette on the sets lens")
	}
	if !find(t, onSettlingMovers(t, sets)) {
		t.Error("SetSettlingWindow missing from the palette on the movers view")
	}

	holdings := atAllCards(t, newTestModel(t, settlingStore()))
	if find(t, holdings) {
		t.Error("SetSettlingWindow offered on the holdings pane, where it shows nothing")
	}
}

func TestSetSettlingWindowMovesTheNetAndPersists(t *testing.T) {
	pinWindow(t, store.DefaultSettlingDays)
	st := settlingStore()
	m := onSettlingMovers(t, onSettlingSets(t, st))

	if _, totals := m.viewHeader(); !strings.Contains(totals, "1 settling set held out") {
		t.Fatalf("totals = %q, want the default window holding the fixture out", totals)
	}

	m.promptSetSettlingWindow()
	if m.prompt == nil {
		t.Fatal("no prompt opened")
	}
	if m.prompt.text != strconv.Itoa(store.DefaultSettlingDays) {
		t.Errorf("prefill = %q, want the window in force", m.prompt.text)
	}
	m.prompt.commit(&m, "0")

	if got := store.SettlingDays(); got != 0 {
		t.Fatalf("window = %d, want 0", got)
	}
	_, totals := m.viewHeader()
	if strings.Contains(totals, "held out") {
		t.Errorf("totals = %q, want nothing held out at a zero window", totals)
	}
	if !strings.Contains(totals, "+$8.00") {
		t.Errorf("totals = %q, want the plain sum of both rows", totals)
	}
	if got := st.settings[setSettlingDays]; got != "0" {
		t.Errorf("persisted %q, want \"0\" — a window moved today is the window tomorrow", got)
	}

	for _, l := range m.containerLines(containerPaneWidth) {
		if strings.Contains(l, settlingMark) {
			t.Errorf("sidebar row = %q, want no mark at a zero window", l)
		}
	}
}

func TestSettlingWindowSaysWhenTheEnvironmentWillWinNextLaunch(t *testing.T) {
	pinWindow(t, store.DefaultSettlingDays)
	t.Setenv(store.SettlingDaysEnv, "7")
	st := settlingStore()
	m := onSettlingMovers(t, onSettlingSets(t, st))

	m.promptSetSettlingWindow()
	m.prompt.commit(&m, "60")

	if got := store.SettlingDays(); got != 60 {
		t.Errorf("window = %d, want the dial to win for this run", got)
	}
	if !strings.Contains(m.status, store.SettlingDaysEnv) {
		t.Errorf("status = %q, want it to name the variable that outranks this "+
			"at the next launch", m.status)
	}

	if got := st.settings[setSettlingDays]; got != "60" {
		t.Errorf("persisted %q, want \"60\"", got)
	}

	t.Run("silent when nothing outranks it", func(t *testing.T) {
		pinWindow(t, store.DefaultSettlingDays)
		t.Setenv(store.SettlingDaysEnv, "")
		m := onSettlingMovers(t, onSettlingSets(t, settlingStore()))
		m.promptSetSettlingWindow()
		m.prompt.commit(&m, "60")
		if strings.Contains(m.status, store.SettlingDaysEnv) {
			t.Errorf("status = %q, want no notice when nothing outranks the dial", m.status)
		}
	})
}

func TestParseSettlingDaysRefusesWhatTheDialCannotSay(t *testing.T) {
	for _, ok := range []string{"0", "30", " 90 ", "3650"} {
		if _, err := parseSettlingDays(ok); err != nil {
			t.Errorf("parseSettlingDays(%q) = %v, want accepted", ok, err)
		}
	}
	for _, bad := range []string{"", "  ", "-1", "90d", "ninety", "9.5"} {
		if _, err := parseSettlingDays(bad); err == nil {
			t.Errorf("parseSettlingDays(%q) accepted, want a correction", bad)
		}
	}
}

func TestSettlingWindowStartupPrecedence(t *testing.T) {
	t.Run("stored preference when the environment is quiet", func(t *testing.T) {
		pinWindow(t, store.DefaultSettlingDays)
		st := settlingStore()
		st.settings = map[string]string{setSettlingDays: "30"}
		onSettlingSets(t, st)
		if got := store.SettlingDays(); got != 30 {
			t.Errorf("window = %d, want the stored 30", got)
		}
	})

	t.Run("environment outranks the stored preference", func(t *testing.T) {
		pinWindow(t, store.DefaultSettlingDays)
		t.Setenv(store.SettlingDaysEnv, "7")
		st := settlingStore()
		st.settings = map[string]string{setSettlingDays: "30"}
		onSettlingSets(t, st)
		if got := store.SettlingDays(); got == 30 {
			t.Error("stored preference overrode the environment at startup")
		}
	})

	t.Run("a garbled environment does not discard the stored preference", func(t *testing.T) {
		pinWindow(t, store.DefaultSettlingDays)
		t.Setenv(store.SettlingDaysEnv, "banana")
		st := settlingStore()
		st.settings = map[string]string{setSettlingDays: "45"}
		onSettlingSets(t, st)
		if got := store.SettlingDays(); got != 45 {
			t.Errorf("window = %d, want the stored 45 — an unusable variable is "+
				"not an instruction, and must not outrank a saved preference", got)
		}
	})

	t.Run("a garbled preference leaves the default standing", func(t *testing.T) {
		pinWindow(t, store.DefaultSettlingDays)
		st := settlingStore()
		st.settings = map[string]string{setSettlingDays: "soon"}
		onSettlingSets(t, st)
		if got := store.SettlingDays(); got != store.DefaultSettlingDays {
			t.Errorf("window = %d, want the default", got)
		}
	})
}

func TestHelpRowsKeyNamesSettling(t *testing.T) {
	settling := onSettlingSets(t, settlingStore())

	st := settlingStore()
	for i := range st.sets {
		st.sets[i].ReleasedAt = daysBefore(3000)
	}
	quiet := onSettlingSets(t, st)

	a, b := settling.helpRowsKeyFor(), quiet.helpRowsKeyFor()
	if a == b {
		t.Fatalf("both states key to %+v, so the memo would serve one height for "+
			"two different help lines", a)
	}
}
