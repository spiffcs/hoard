package browse

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/cardname"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/tui"
)

// childSearcher is the smallest tui.Searcher that lets a type-a-name add
// run end to end: one card, one printing, one finish.
type childSearcher struct{}

func (childSearcher) Autocomplete(context.Context, string) ([]string, error) { return nil, nil }
func (childSearcher) SearchPrints(_ context.Context, name string) ([]scryfall.Card, error) {
	return []scryfall.Card{{ID: "sr", Name: name, Set: "mh3", SetName: "Modern Horizons 3",
		CollectorNumber: "123", Finishes: []string{"nonfoil"}}}, nil
}
func (childSearcher) NamedFuzzy(context.Context, string) (*scryfall.Card, cardname.Match, error) {
	return nil, cardname.Match{}, errors.New("not in this test")
}

func noopChildAdder(tui.Result) error { return nil }

// recordingChildAdder counts what the embedded cascade persists.
type recordingChildAdder struct{ got []tui.Result }

func (r *recordingChildAdder) add(res tui.Result) error {
	r.got = append(r.got, res)
	return nil
}

func withTestCascade(adder *recordingChildAdder) Option {
	return WithAddCascade(func() (tui.Child, error) {
		return tui.NewChild(context.Background(), childSearcher{}, adder.add, nil, "", nil), nil
	})
}

// newCascadeModel is newTestModel with options.
func newCascadeModel(t *testing.T, st Store, opts ...Option) Model {
	t.Helper()
	m, err := New(st, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ctx = context.Background()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	return next.(Model)
}

// pump executes a command tree and feeds the resulting messages back through
// the model — the test-side stand-in for bubbletea's loop. Spinner ticks are
// dropped rather than fed back: feeding one re-arms a sleeping tick and the
// animation is irrelevant to every assertion here.
func pump(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for i := 0; i < 64 && len(queue) > 0; i++ {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		switch msg := c().(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case spinner.TickMsg, cursor.BlinkMsg:
			// Animation traffic: feeding either back re-arms a sleeping
			// tick, and neither affects any assertion here.
		default:
			next, c2 := m.Update(msg)
			m = next.(Model)
			queue = append(queue, c2)
		}
	}
	return m
}

func TestAddOpensEmbeddedCascadeWhenInjected(t *testing.T) {
	ra := &recordingChildAdder{}
	m := newCascadeModel(t, &fakeStore{}, withTestCascade(ra))

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = next.(Model)
	if m.mode() != modeAddChild {
		t.Fatalf("mode = %v, want modeAddChild", m.mode())
	}
	if cmd == nil {
		t.Fatal("opening the cascade should return its Init cmd")
	}
	if v := m.View(); !strings.Contains(v, "Card name") && !strings.Contains(v, "name") {
		t.Fatalf("View should show the cascade's name prompt, got:\n%s", v)
	}
}

func TestAddUnavailableWithoutConstructorShowsStatus(t *testing.T) {
	m := newTestModel(t, &fakeStore{})
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = next.(Model)
	if cmd != nil || m.addChild != nil {
		t.Fatal("without a constructor, `a` must refuse rather than open or quit")
	}
	if !strings.Contains(m.status, "unavailable") || !m.statusErr {
		t.Fatalf("status = %q, want the unavailable refusal", m.status)
	}
}

func TestAddCascadeEscAtNameReturnsToBrowse(t *testing.T) {
	ra := &recordingChildAdder{}
	m := newCascadeModel(t, &fakeStore{}, withTestCascade(ra))

	m = key(m, "a")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.addChild != nil {
		t.Fatal("esc at the empty name prompt should close the cascade")
	}
	if m.mode() != modeBrowse {
		t.Fatalf("mode = %v, want browse", m.mode())
	}
	if m.status != "no cards added" {
		t.Fatalf("status = %q, want the empty receipt", m.status)
	}
}

// typeChild types text into the active cascade.
func typeChild(m Model, s string) Model {
	for _, r := range s {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	return m
}

// addOneCard drives a full type-a-name add through the embedded cascade:
// name, then enter through the pickers until the adder records the commit.
func addOneCard(t *testing.T, m Model, ra *recordingChildAdder, name string) Model {
	t.Helper()
	before := len(ra.got)
	m = typeChild(m, name)
	for i := 0; i < 8 && len(ra.got) == before; i++ {
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
		m = next.(Model)
		m = pump(t, m, cmd)
	}
	if len(ra.got) != before+1 {
		t.Fatalf("cascade never committed %q (adds %d)", name, len(ra.got)-before)
	}
	return m
}

func TestAddCascadeCommitsAndReceiptLands(t *testing.T) {
	ra := &recordingChildAdder{}
	m := newCascadeModel(t, &fakeStore{}, withTestCascade(ra))

	m = key(m, "a")
	m = addOneCard(t, m, ra, "Sol Ring")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)

	if m.addChild != nil {
		t.Fatal("cascade should be closed")
	}
	if m.status != "added 1" {
		t.Fatalf("status = %q, want %q", m.status, "added 1")
	}
	if ra.got[0].Card.Name != "Sol Ring" || ra.got[0].Qty != 1 {
		t.Fatalf("adder got %+v", ra.got[0])
	}
}

func TestAddCascadeReceiptAccumulatesAcrossInvocations(t *testing.T) {
	ra := &recordingChildAdder{}
	m := newCascadeModel(t, &fakeStore{}, withTestCascade(ra))

	for _, name := range []string{"Sol Ring", "Ancient Tomb"} {
		m = key(m, "a")
		m = addOneCard(t, m, ra, name)
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		m = next.(Model)
	}
	if len(ra.got) != 2 {
		t.Fatalf("adds = %d, want 2", len(ra.got))
	}
	// Manual adds keep the summary empty (it is the scan receipt), but the
	// accumulator must survive both invocations without resetting.
	if len(m.addSummary.Entries) != 0 {
		t.Fatalf("manual adds should not fabricate scan entries: %+v", m.addSummary.Entries)
	}
}

func TestCtrlCDuringAddTearsDownAndQuits(t *testing.T) {
	ra := &recordingChildAdder{}
	m := newCascadeModel(t, &fakeStore{}, withTestCascade(ra))

	m = key(m, "a")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(Model)
	if m.addChild != nil {
		t.Fatal("ctrl+c must tear the cascade down before quitting")
	}
	if cmd == nil {
		t.Fatal("ctrl+c must quit")
	}
	if msg := cmd(); msg != (tea.QuitMsg{}) {
		t.Fatalf("cmd = %v, want quit", msg)
	}
}

func TestCtrlCDuringAddWithOpRunningStagesConfirm(t *testing.T) {
	ra := &recordingChildAdder{}
	m := newCascadeModel(t, &fakeStore{}, withTestCascade(ra),
		WithUpdatePrices(func(ctx context.Context, _ progress.Fn) (string, error) {
			return "done", nil
		}))

	// Start the op but never run its command: m.op stays live, no goroutine.
	(&m).startOp("updating prices", m.opUpdatePrices)
	if m.op == nil {
		t.Fatal("setup: op not started")
	}
	m = key(m, "a")
	if m.mode() != modeAddChild {
		t.Fatal("embedded add must open even while an op runs")
	}

	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(Model)
	if m.confirm == nil {
		t.Fatal("ctrl+c with an op running must stage the quit confirm")
	}
	if m.addChild == nil {
		t.Fatal("the cascade stays alive while the question is up")
	}
	if v := m.View(); !strings.Contains(v, "still running") {
		t.Fatal("the confirm must render over the cascade view")
	}

	// Declining returns to the cascade.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	m = next.(Model)
	if m.confirm != nil || m.addChild == nil {
		t.Fatal("declining must return to the live cascade")
	}

	// Accepting tears down and quits.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(Model)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = next.(Model)
	if m.addChild != nil {
		t.Fatal("accepting the quit must tear the cascade down")
	}
	if cmd == nil {
		t.Fatal("accepting the quit must quit")
	}
}

func TestOpCompletesBehindTheCascade(t *testing.T) {
	ra := &recordingChildAdder{}
	m := newCascadeModel(t, &fakeStore{}, withTestCascade(ra),
		WithUpdatePrices(func(ctx context.Context, _ progress.Fn) (string, error) {
			return "prices updated", nil
		}))

	(&m).startOp("updating prices", m.opUpdatePrices)
	if m.op == nil {
		t.Fatal("setup: op not started")
	}
	gen := m.op.gen
	m = key(m, "a")

	next, _ := m.Update(opDoneMsg{gen: gen, outcome: opOutcome{summary: "prices updated"}})
	m = next.(Model)
	if m.op != nil {
		t.Fatal("the op must complete while the cascade covers the panes")
	}
	if m.addChild == nil {
		t.Fatal("op completion must not disturb the cascade")
	}
}

func TestResizeReachesEmbeddedCascade(t *testing.T) {
	ra := &recordingChildAdder{}
	m := newCascadeModel(t, &fakeStore{}, withTestCascade(ra))
	m = key(m, "a")

	next, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	m = next.(Model)
	if m.addChild == nil {
		t.Fatal("resize must not close the cascade")
	}
	if m.width != 40 {
		t.Fatal("browse must track the size too")
	}
}
