package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/scan"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func TestChildCtrlDSetsDoneAndSwallowsQuit(t *testing.T) {
	c := NewChild(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	c, cmd := c.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if !c.Done() {
		t.Fatal("ctrl+d should finish the cascade")
	}
	if cmd != nil {
		t.Fatalf("the quit must be swallowed, got cmd %v", cmd)
	}
}

// esc walks the leave gate: the first press asks, a single y leaves, and
// any other key stays in the session.
func TestChildEscGatesLeavingBehindY(t *testing.T) {
	c := NewChild(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	c, _ = c.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if c.Done() {
		t.Fatal("a bare esc must ask, not finish")
	}
	if v := c.View(); !strings.Contains(v, "quit add session?") {
		t.Fatalf("no leave gate on screen:\n%s", v)
	}
	// A stray key cancels back to the session.
	c, _ = c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if c.Done() {
		t.Fatal("a stray key on the gate must stay")
	}
	// esc then y leaves.
	c, _ = c.Update(tea.KeyMsg{Type: tea.KeyEsc})
	c, cmd := c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if !c.Done() {
		t.Fatal("y on the gate must leave the session")
	}
	if cmd != nil {
		t.Fatalf("the quit must be swallowed, got cmd %v", cmd)
	}
}

func TestChildCtrlCSetsDoneAndClosesSession(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	m, sess := openCapture(t, m)
	c := Child{m: m}

	c, cmd := c.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !c.Done() {
		t.Fatal("ctrl+c should finish the cascade")
	}
	if cmd != nil {
		t.Fatalf("the quit must be swallowed, got cmd %v", cmd)
	}
	if !sess.closed {
		t.Fatal("ctrl+c must close the camera session — there is no Run safety net embedded")
	}
}

func TestChildCloseRecordsDiscardsAndClosesSession(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	m, sess := openCapture(t, m)
	m.review = []queueItem{{ocrLine: "x"}, {ocrLine: "y"}}
	m.resolving = 1
	gen := m.resolveGen
	c := Child{m: m}

	c.Close()
	if !sess.closed {
		t.Fatal("Close must shut the camera session")
	}
	if !c.Done() {
		t.Fatal("a closed child is done")
	}
	if c.m.resolveGen != gen+1 {
		t.Fatal("Close must void in-flight resolves")
	}
	if c.m.resolving != 0 || c.m.review != nil {
		t.Fatalf("queue not cleared: resolving=%d review=%v", c.m.resolving, c.m.review)
	}
	if got := c.Summary().Count("discarded"); got != 1 {
		t.Fatalf("discarded entries = %d, want 1", got)
	}

	c.Close() // idempotent: nothing to double-count
	if got := c.Summary().Count("discarded"); got != 1 {
		t.Fatalf("second Close added entries: discarded = %d", got)
	}
}

func TestChildSummaryAndErrPassThrough(t *testing.T) {
	c := NewChild(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	c.m.summary.add("auto", "Sol Ring (MH3/123) nonfoil — $1.20")
	c.m.err = errors.New("boom")
	if got := c.Summary().Count("auto"); got != 1 {
		t.Fatalf("auto entries = %d, want 1", got)
	}
	if c.Err() == nil || c.Err().Error() != "boom" {
		t.Fatalf("Err = %v, want boom", c.Err())
	}
}

func TestChildUpdateKeepsNonQuitCmds(t *testing.T) {
	c := NewChild(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	for _, r := range "Sol Ring" {
		c, _ = c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	c, cmd := c.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if c.Done() {
		t.Fatal("entering a name must not finish the cascade")
	}
	if cmd == nil {
		t.Fatal("enter on a name should return the search command")
	}
}

// price is a test-literal pointer.
func price(v float64) *float64 { return &v }

// pumpChild executes a command tree into the child, dropping animation
// ticks (feeding one back re-arms a sleeping timer).
func pumpChild(c Child, cmd tea.Cmd) Child {
	queue := []tea.Cmd{cmd}
	for i := 0; i < 64 && len(queue) > 0; i++ {
		cc := queue[0]
		queue = queue[1:]
		if cc == nil {
			continue
		}
		switch msg := cc().(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case spinner.TickMsg, cursor.BlinkMsg:
		default:
			var c2 tea.Cmd
			c, c2 = c.Update(msg)
			queue = append(queue, c2)
		}
	}
	return c
}

// A manual add of a priced card lands in the running value, the tally
// fragment, and the accessor.
func TestAddedValueAccumulates(t *testing.T) {
	fs := fakeSearcher{prints: map[string][]scryfall.Card{"Sol Ring": {
		{ID: "a", Name: "Sol Ring", Set: "mh3", CollectorNumber: "123",
			Finishes: []string{"nonfoil"}, PriceUSD: price(1.50)},
	}}}
	c := NewChild(context.Background(), fs, noopAdder, nil, "", nil)
	for _, r := range "Sol Ring" {
		c, _ = c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	for i := 0; i < 8 && c.Added() == 0; i++ {
		var cmd tea.Cmd
		c, cmd = c.Update(tea.KeyMsg{Type: tea.KeyEnter})
		c = pumpChild(c, cmd)
	}
	if c.Added() != 1 {
		t.Fatalf("adds = %d, want 1", c.Added())
	}
	if c.AddedValue() != 1.50 {
		t.Fatalf("AddedValue = %v, want 1.50", c.AddedValue())
	}
	if v := c.View(); !strings.Contains(v, "added this session ($1.50)") {
		t.Fatalf("name view missing the value tally:\n%s", v)
	}
}

// confirmAdd weighs the value by quantity.
func TestConfirmAddWeighsQty(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	card := scryfall.Card{ID: "a", Name: "Sol Ring", Set: "mh3",
		CollectorNumber: "123", Finishes: []string{"nonfoil"}, PriceUSD: price(2.00)}
	m.chosen = &card
	m.finish = "nonfoil"
	m.qtyInput.SetValue("3")
	mm, _ := m.confirmAdd()
	m = mm.(model)
	if m.addedValue != 6.00 {
		t.Fatalf("addedValue = %v, want 6.00", m.addedValue)
	}
}

// An unpriced card counts in the tally but adds no value, and the money
// fragment stays hidden rather than reading "$0.00".
func TestAddedValueHiddenWhenUnpriced(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	m.addedCount = 2
	if got := m.sessionTally(); got != "2 added this session" {
		t.Fatalf("sessionTally = %q", got)
	}
}

// Auto-commits accumulate value too, weighted by the printing's finish.
func TestAutoCommitAccumulatesValue(t *testing.T) {
	ev := scan.Event{Kind: scan.EventScan, Name: "Sol Ring",
		Candidates: []string{"Sol Ring"}, SetCode: "MH3", CollectorNumber: "123",
		Confidence: 0.95, BandAnchored: true}
	fs := fakeSearcher{
		fuzzy: map[string]string{"Sol Ring": "Sol Ring"},
		prints: map[string][]scryfall.Card{"Sol Ring": {
			{ID: "a", Name: "Sol Ring", Set: "mh3", CollectorNumber: "123",
				Finishes: []string{"nonfoil"}, PriceUSD: price(3.25)},
		}},
	}
	m := newModel(context.Background(), fs, noopAdder, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	m = mm.(model)
	mm, _ = m.Update(m.resolveCardCmd(m.nextResolveID, ev.CardList()[0], 1)())
	m = mm.(model)

	if m.addedCount != 1 {
		t.Fatalf("auto-add did not commit: count=%d", m.addedCount)
	}
	if m.addedValue != 3.25 {
		t.Fatalf("addedValue = %v, want 3.25", m.addedValue)
	}
	if v := m.View(); !strings.Contains(v, "($3.25)") {
		t.Fatalf("capture view missing the value tally:\n%s", v)
	}
}

// Embedded, the name prompt's help says esc goes back to the browser;
// standalone it says quit — the same key, labeled for what it does.
func TestEscHelpWording(t *testing.T) {
	c := NewChild(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	if v := c.View(); !strings.Contains(v, "ctrl+d done adding · esc leave (asks first)") {
		t.Fatalf("embedded help missing the done/leave wording:\n%s", v)
	}
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	if v := m.View(); !strings.Contains(v, "esc quit (asks first)") {
		t.Fatalf("standalone help missing esc quit wording:\n%s", v)
	}
}
