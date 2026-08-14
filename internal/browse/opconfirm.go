package browse

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

type ConfirmRequest struct {
	Question string
	Reply    chan<- bool
}

func WithConfirm(ch <-chan ConfirmRequest) Option {
	return func(m *Model) { m.confirmCh = ch }
}

type opConfirmMsg struct{ req ConfirmRequest }

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

func (m Model) onOpConfirm(msg opConfirmMsg) (tea.Model, tea.Cmd) {

	if cap(msg.req.Reply) < 1 {
		panic("browse: ConfirmRequest.Reply must have capacity ≥ 1")
	}
	if m.confirm != nil {
		req := msg.req
		m.deferredAsk = &req
	} else {
		m.stageConfirmRequest(msg.req)
	}
	return m, awaitConfirm(m.ctx, m.confirmCh)
}

func (m *Model) declineDeferredAsk() {
	if m.deferredAsk != nil {
		m.deferredAsk.Reply <- false
		m.deferredAsk = nil
	}
}

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
