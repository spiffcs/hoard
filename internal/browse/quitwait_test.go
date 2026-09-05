package browse

import (
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/progress"
)

func startOpCmd(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		return
	}
	for _, c := range batch {
		go c()
	}
}

func TestQuitWaitsForARunningOpBeforeTheStoreCloses(t *testing.T) {
	release := make(chan struct{})
	finished := make(chan struct{})
	m := newTestModel(t, testStore())
	cmd := m.startOp("updating prices", func(ctx context.Context, p progress.Fn) (string, error) {
		<-release
		close(finished)
		return "done", nil
	})
	if cmd == nil {
		t.Fatal("startOp returned no command")
	}
	startOpCmd(cmd)

	m.cancelOp()
	if m.awaitOp(100 * time.Millisecond) {
		select {
		case <-finished:
		default:
			t.Fatal("awaitOp said the op was done while it was still running")
		}
	}

	done := make(chan bool, 1)
	go func() { done <- m.awaitOp(2 * time.Second) }()
	select {
	case <-done:
		t.Fatal("awaitOp returned before the op released")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	select {
	case ok := <-done:
		if !ok {
			t.Error("awaitOp reported the op did not finish, but it did")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("awaitOp never returned after the op finished")
	}
}

func TestAwaitOpGivesUpAfterTheGrace(t *testing.T) {
	stuck := make(chan struct{})
	defer close(stuck)
	m := newTestModel(t, testStore())
	cmd := m.startOp("importing", func(ctx context.Context, p progress.Fn) (string, error) {
		<-stuck
		return "", nil
	})
	startOpCmd(cmd)

	start := time.Now()
	if m.awaitOp(100 * time.Millisecond) {
		t.Error("awaitOp reported success while the op was still stuck")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("awaitOp blocked for %v, want it bounded by the grace", elapsed)
	}
}

func TestQuittingFromTheAddCascadeReleasesAnOpWaitingOnAConfirm(t *testing.T) {
	reply := make(chan bool, 1)
	m := newTestModel(t, testStore())
	m.opUpdatePrices = func(ctx context.Context, p progress.Fn) (string, error) { return "", nil }
	m.startOp("updating prices", m.opUpdatePrices)
	m.deferredAsk = &ConfirmRequest{Question: "Rebuild the catalog?", Reply: reply}

	next, _ := m.handleAddChildKey(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = next.(Model)
	if m.confirm == nil {
		t.Fatal("ctrl-c with an op running did not raise the quit confirmation")
	}
	m.confirm.onYes(&m)

	select {
	case answer := <-reply:
		if answer {
			t.Errorf("reply = %v, want false: quitting must decline, not accept", answer)
		}
	default:
		t.Fatal("the op is still blocked on <-reply; quitting never released it")
	}
}
