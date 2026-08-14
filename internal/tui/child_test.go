package tui

import (
	"context"
	"errors"
	"github.com/spiffcs/hoard/internal/finish"
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

func TestCaptureAdvertisesAndHonoursCtrlD(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	if v := m.View(); !strings.Contains(v, "c close camera · ctrl+d done") {
		t.Fatalf("capture help line is missing ctrl+d:\n%s", v)
	}
	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlD})
	got := mm.(model)
	if !got.done {
		t.Fatalf("ctrl+d did not finish from the camera: state = %v", got.state)
	}
	if got.session != nil {
		t.Fatal("the camera session outlived the finish")
	}
}

func TestCtrlDPausesTheQueueWhenEmbedded(t *testing.T) {
	c := NewChild(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	c.m.review = []queueItem{
		{id: 4, canonical: "Sol Ring", captureSeq: 7},
		{id: 5, ocrLine: "brainstorrn", captureSeq: 8},
	}

	c.m.current = &queueItem{id: 3, canonical: "Counterspell", captureSeq: 6}

	c, _ = c.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	if !c.Done() {
		t.Fatal("ctrl+d did not finish")
	}
	p := c.Pending()
	if p.Len() != 3 {
		t.Fatalf("Pending().Len() = %d, want the walked card back with the other two", p.Len())
	}
	if p.items[0].canonical != "Counterspell" {
		t.Fatalf("the card in hand is not at the head of the queue: %+v", p.items[0])
	}
	for _, e := range c.Summary().Entries {
		if e.Kind == "discarded" {
			t.Fatalf("a paused queue must not read as discarded: %q", e.Line)
		}
	}

	next := NewChild(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	next.Restore(p)
	next, _ = next.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	if got := len(next.m.review); got != 3 {
		t.Fatalf("restored queue holds %d cards, want 3", got)
	}
	v := next.View()
	if !strings.Contains(v, "3 scanned cards are still waiting for review") {
		t.Fatalf("the restored session doesn't say what it is holding:\n%s", v)
	}
	if !strings.Contains(v, "tab review queue (3)") {
		t.Fatalf("the restored queue has no advertised door:\n%s", v)
	}

	if next.m.nextResolveID < 5 || next.m.captureSeq < 8 {
		t.Fatalf("counters not advanced past the restored cards: id=%d seq=%d",
			next.m.nextResolveID, next.m.captureSeq)
	}
}

func TestRestoredQueueWalksFromTheNamePrompt(t *testing.T) {
	c := NewChild(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	c.Restore(Pending{items: []queueItem{{id: 1, canonical: "Sol Ring"}}})
	c, _ = c.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	c, _ = c.Update(tea.KeyMsg{Type: tea.KeyTab})
	if c.m.state != stateQueueReview {
		t.Fatalf("tab did not open the queue: state = %v", c.m.state)
	}
	c, _ = c.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if c.m.state != stateName {
		t.Fatalf("esc out of the queue landed on %v, want the name prompt", c.m.state)
	}
	if c.m.session != nil {
		t.Fatal("no camera should have been opened by any of that")
	}
}

func TestLeaveGateStatesWhatWasSaved(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	m.addedCount = 12
	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = mm.(model)
	if m.state != stateLeaveConfirm {
		t.Fatalf("esc did not open the gate: state = %v", m.state)
	}
	v := m.View()
	if !strings.Contains(v, "12 cards automatically saved to the database") {
		t.Fatalf("the gate does not say what was saved:\n%s", v)
	}
	if !strings.Contains(v, "quit add session?") {
		t.Fatalf("the gate lost its prompt:\n%s", v)
	}

	one := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	one.addedCount = 1
	mm, _ = one.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if v := mm.(model).View(); !strings.Contains(v, "1 card automatically saved") {
		t.Fatalf("singular not handled:\n%s", v)
	}
}

func TestChildEscGatesLeavingBehindY(t *testing.T) {
	c := NewChild(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	c, _ = c.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if c.Done() {
		t.Fatal("a bare esc must ask, not finish")
	}
	if v := c.View(); !strings.Contains(v, "quit add session?") {
		t.Fatalf("no leave gate on screen:\n%s", v)
	}

	c, _ = c.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if c.Done() {
		t.Fatal("a stray key on the gate must stay")
	}

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

	c.Close()
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

func price(v float64) *float64 { return &v }

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

func TestConfirmAddWeighsQty(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	card := scryfall.Card{ID: "a", Name: "Sol Ring", Set: "mh3",
		CollectorNumber: "123", Finishes: []string{"nonfoil"}, PriceUSD: price(2.00)}
	m.chosen = &card
	m.finish = finish.Nonfoil
	m.qtyInput.SetValue("3")
	mm, _ := m.confirmAdd()
	m = mm.(model)
	if m.addedValue != 6.00 {
		t.Fatalf("addedValue = %v, want 6.00", m.addedValue)
	}
}

func TestAddedValueHiddenWhenUnpriced(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	m.addedCount = 2
	if got := m.sessionTally(); got != "2 added this session" {
		t.Fatalf("sessionTally = %q", got)
	}
}

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

func TestEscHelpWording(t *testing.T) {
	c := NewChild(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	if v := c.View(); !strings.Contains(v, "ctrl+d done · esc back") {
		t.Fatalf("embedded help missing the done/back wording:\n%s", v)
	}
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	if v := m.View(); !strings.Contains(v, "esc quit") {
		t.Fatalf("standalone help missing esc quit wording:\n%s", v)
	}
}
