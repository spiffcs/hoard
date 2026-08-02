package browse

// The confirm bridge: an operation's synchronous yes/no question, answered
// by the browser's confirm surface.
//
// action.Deps.Confirm is a blocking call on the op goroutine (today: the
// catalog-download questions inside update-prices). The browser's confirm
// is asynchronous Elm-loop state. main bridges them with a channel: its
// Deps.Confirm closure sends a ConfirmRequest and blocks on the Reply;
// browse's pump delivers the request as a message, stages it as an ordinary
// pendingConfirm, and the answer flows back through the channel.
//
// There is deliberately no generation guard. A request can only come from a
// goroutine that is still blocked waiting for its answer — a "stale"
// confirm request cannot exist, and answering is mandatory: every request
// gets exactly one reply on every exit path (y, any other key, ctrl+c).

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

// ConfirmRequest is an op-originated yes/no question. Reply must have
// capacity ≥ 1: browse writes exactly once and must never block on a
// worker that gave up.
type ConfirmRequest struct {
	Question string
	Reply    chan<- bool
}

// WithConfirm supplies the channel op goroutines ask questions on.
func WithConfirm(ch <-chan ConfirmRequest) Option {
	return func(m *Model) { m.confirmCh = ch }
}

type opConfirmMsg struct{ req ConfirmRequest }

// awaitConfirm is the long-lived pump: armed once from Init, re-armed on
// every delivery, released by ctx when the program ends.
func awaitConfirm(ctx context.Context, ch <-chan ConfirmRequest) tea.Cmd {
	if ch == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		select {
		case r, ok := <-ch:
			if !ok {
				return nil
			}
			return opConfirmMsg{req: r}
		case <-ctx.Done():
			return nil
		}
	}
}

// onOpConfirm stages the question, or parks it if a confirm is already up
// (the only collision: a user-staged confirm was open when the op asked —
// at most one request can exist, so a single slot suffices). Either way the
// pump re-arms.
func (m Model) onOpConfirm(msg opConfirmMsg) (tea.Model, tea.Cmd) {
	if m.confirm != nil {
		req := msg.req
		m.deferredAsk = &req
	} else {
		m.stageConfirmRequest(msg.req)
	}
	return m, awaitConfirm(m.ctx, m.confirmCh)
}

// stageConfirmRequest turns a bridge request into an ordinary confirm whose
// both branches answer the asker. Any key but y declines — the same reading
// a non-interactive CLI run gives the question.
func (m *Model) stageConfirmRequest(req ConfirmRequest) {
	reply := req.Reply
	m.confirm = &pendingConfirm{
		prompt: req.Question,
		help:   "y confirm · any other key declines",
		onYes: func(*Model) tea.Cmd {
			reply <- true
			return nil
		},
		onNo: func(*Model) {
			reply <- false
		},
	}
}
