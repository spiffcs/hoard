package browse

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/progress"
)

// ask stages a bridge question on the model and returns its reply channel.
func ask(t *testing.T, m Model, q string) (Model, chan bool) {
	t.Helper()
	reply := make(chan bool, 1)
	next, _ := m.Update(opConfirmMsg{req: ConfirmRequest{Question: q, Reply: reply}})
	return next.(Model), reply
}

func mustReply(t *testing.T, reply chan bool, want bool) {
	t.Helper()
	select {
	case got := <-reply:
		if got != want {
			t.Fatalf("reply = %v, want %v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("no reply delivered — the asking worker would hang")
	}
}

func TestOpConfirmStagesQuestionAndYesAnswersTrue(t *testing.T) {
	m := newTestModel(t, testStore())
	m, reply := ask(t, m, "download the catalog (1.2 GB)?")

	if m.confirm == nil || m.confirm.prompt != "download the catalog (1.2 GB)?" {
		t.Fatalf("confirm = %+v, want the staged question", m.confirm)
	}
	if !strings.Contains(m.statusLine(), "y confirm · any other key declines") {
		t.Errorf("statusLine = %q, want the bridge wording", m.statusLine())
	}
	m = key(m, "y")
	mustReply(t, reply, true)
	if m.confirm != nil {
		t.Error("confirm not cleared after the answer")
	}
}

func TestOpConfirmAnyOtherKeyDeclines(t *testing.T) {
	for _, k := range []string{"n", "esc", "enter", "x"} {
		m := newTestModel(t, testStore())
		m, reply := ask(t, m, "download?")
		switch k {
		case "esc":
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
			m = next.(Model)
		case "enter":
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(Model)
		default:
			m = key(m, k)
		}
		mustReply(t, reply, false)
		if m.confirm != nil {
			t.Errorf("%s: confirm not cleared", k)
		}
	}
}

func TestOpConfirmCtrlCDeclinesAndCancelsOp(t *testing.T) {
	opCtx := make(chan struct{})
	m := opModel(t, func(ctx context.Context, p progress.Fn) (string, error) {
		<-ctx.Done()
		close(opCtx)
		return "", ctx.Err()
	})
	m.ctx = context.Background()
	// Start the op for real so its goroutine is live and watching its ctx.
	cmd := (&m).startOp("updating prices", m.opUpdatePrices)
	// Run the op's command tree so its goroutine is live; the done msg it
	// eventually yields is irrelevant here.
	go func() {
		queue := []tea.Cmd{cmd}
		for len(queue) > 0 {
			c := queue[0]
			queue = queue[1:]
			if c == nil {
				continue
			}
			if batch, ok := c().(tea.BatchMsg); ok {
				queue = append(queue, batch...)
			}
		}
	}()

	m, reply := ask(t, m, "download?")
	next, quitCmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(Model)

	mustReply(t, reply, false)
	select {
	case <-opCtx:
	case <-time.After(time.Second):
		t.Fatal("ctrl+c must cancel the op ctx so the worker unwinds before the store closes")
	}
	if quitCmd == nil {
		t.Fatal("ctrl+c must still quit")
	}
}

func TestOpConfirmDefersBehindUserConfirmThenStages(t *testing.T) {
	m := newTestModel(t, testStore())
	// A user-staged confirm is up…
	m.confirm = &pendingConfirm{prompt: "remove deck?",
		onYes: func(*Model) tea.Cmd { return nil }}
	// …when the bridge question arrives.
	m, reply := ask(t, m, "download?")
	if m.confirm.prompt != "remove deck?" {
		t.Fatal("the user confirm must not be preempted")
	}
	if m.deferredAsk == nil {
		t.Fatal("the bridge question must be parked, not dropped")
	}

	// Resolving the user confirm (decline) stages the parked question.
	m = key(m, "n")
	if m.confirm == nil || m.confirm.prompt != "download?" {
		t.Fatalf("confirm = %+v, want the parked question staged", m.confirm)
	}
	m = key(m, "y")
	mustReply(t, reply, true)
}

func TestOpConfirmPumpReArms(t *testing.T) {
	ch := make(chan ConfirmRequest, 1)
	m, err := New(testStore(), WithConfirm(ch))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ctx = context.Background()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = next.(Model)

	for i, q := range []string{"first?", "second?"} {
		reply := make(chan bool, 1)
		ch <- ConfirmRequest{Question: q, Reply: reply}
		// The pump cmd (from Init, then from each delivery) reads the channel.
		pumpCmd := awaitConfirm(m.ctx, m.confirmCh)
		msg := pumpCmd()
		if msg == nil {
			t.Fatalf("question %d: pump returned nothing", i)
		}
		nxt, _ := m.Update(msg)
		m = nxt.(Model)
		if m.confirm == nil || m.confirm.prompt != q {
			t.Fatalf("question %d not staged", i)
		}
		m = key(m, "y")
		mustReply(t, reply, true)
	}
}

func TestRemovalConfirmKeepsRemoveWording(t *testing.T) {
	m := newTestModel(t, testStore())
	m.confirm = &pendingConfirm{prompt: "remove x?",
		help:  "y remove · any other key cancels",
		onYes: func(*Model) tea.Cmd { return nil }}
	if !strings.Contains(m.statusLine(), "y remove") {
		t.Errorf("statusLine = %q, want the removal wording", m.statusLine())
	}
	// And nowhere else: the help line under it must not say it again.
	if m.helpLine() != "" {
		t.Errorf("helpLine = %q, want the question to carry its keys alone", m.helpLine())
	}
}

// The whole bridge, end to end: a worker calling the main-side closure gets
// its answer from the browser's keys.
func TestConfirmBridgeRoundTrip(t *testing.T) {
	ch := make(chan ConfirmRequest, 1)
	ctx := context.Background()
	confirm := func(q string) bool {
		reply := make(chan bool, 1)
		select {
		case ch <- ConfirmRequest{Question: q, Reply: reply}:
		case <-ctx.Done():
			return false
		}
		select {
		case a := <-reply:
			return a
		case <-ctx.Done():
			return false
		}
	}

	m, err := New(testStore(), WithConfirm(ch))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ctx = ctx
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	m = next.(Model)

	answered := make(chan bool, 1)
	go func() { answered <- confirm("download the catalog?") }()

	msg := awaitConfirm(m.ctx, m.confirmCh)()
	if msg == nil {
		t.Fatal("pump delivered nothing")
	}
	nxt, _ := m.Update(msg)
	m = nxt.(Model)
	if m.confirm == nil {
		t.Fatal("question not staged")
	}
	m = key(m, "y")

	select {
	case got := <-answered:
		if !got {
			t.Fatal("worker heard no, browser said yes")
		}
	case <-time.After(time.Second):
		t.Fatal("worker never heard the answer")
	}
}

// A bridge question parked behind a user-staged confirm must be answered on
// the way out: ctrl+c on that confirm replied to the confirm's own asker and
// quit, leaving the parked worker blocked on a reply forever.
func TestQuitAnswersTheParkedBridgeQuestion(t *testing.T) {
	m := newTestModel(t, testStore())
	m.stageQuit() // a user confirm is up…
	m, reply := ask(t, m, "download?")
	if m.deferredAsk == nil {
		t.Fatal("setup: the ask should be parked behind the open confirm")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	_ = next
	mustReply(t, reply, false)
}

// The Reply capacity contract was a doc comment; now it fails at the door.
// A zero-cap channel turns every answer into a blocking send against a
// worker that may have given up — a hang later, in someone else's key
// handler, is strictly worse than a panic naming the wiring bug.
func TestZeroCapReplyChannelIsRejectedLoudly(t *testing.T) {
	m := newTestModel(t, testStore())
	defer func() {
		if recover() == nil {
			t.Fatal("a zero-capacity Reply channel must be refused at staging")
		}
	}()
	_, _ = m.Update(opConfirmMsg{req: ConfirmRequest{Question: "q?", Reply: make(chan bool)}})
}
