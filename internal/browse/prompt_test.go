package browse

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func openPrompt(t *testing.T, p *prompt) Model {
	t.Helper()
	m := newTestModel(t, testStore())
	m.prompt = p
	return m
}

func TestPromptTypingAndCommit(t *testing.T) {
	var committed string
	m := openPrompt(t, &prompt{
		label:  "new binder name",
		commit: func(_ *Model, text string) tea.Cmd { committed = text; return nil },
	})
	for _, r := range "Trade" {
		m = key(m, string(r))
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeySpace})
	m = next.(Model)
	m = key(m, "S")
	if !strings.Contains(m.statusLine(), "new binder name: Trade S▏") {
		t.Errorf("prompt line = %q", m.statusLine())
	}

	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if committed != "Trade S" {
		t.Errorf("committed %q, want the typed text", committed)
	}
	if m.prompt != nil {
		t.Error("prompt still open after commit")
	}
}

func TestPromptValidateRefusesAndShows(t *testing.T) {
	m := openPrompt(t, &prompt{
		label:    "threshold",
		validate: func(s string) error { return fmt.Errorf("say a number") },
		commit:   func(*Model, string) tea.Cmd { t.Fatal("commit ran despite validation"); return nil },
	})
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.prompt == nil {
		t.Fatal("refused prompt closed anyway")
	}
	if !strings.Contains(m.statusLine(), "say a number") {
		t.Errorf("prompt line = %q, want the validation error shown", m.statusLine())
	}

	m = key(m, "4")
	if m.prompt.err != "" {
		t.Errorf("err = %q after typing, want cleared", m.prompt.err)
	}
}

func TestPromptEscCancelsAndEditingKeys(t *testing.T) {
	m := openPrompt(t, &prompt{label: "x"})
	for _, r := range "abc" {
		m = key(m, string(r))
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	m = next.(Model)
	if m.prompt.text != "ab" {
		t.Errorf("text = %q after backspace", m.prompt.text)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = next.(Model)
	if m.prompt.text != "" {
		t.Errorf("text = %q after ctrl+u, want wiped", m.prompt.text)
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.prompt != nil {
		t.Error("esc did not close the prompt")
	}
	if m.status != "cancelled" {
		t.Errorf("status = %q, want cancelled", m.status)
	}
}

func TestModePrecedence(t *testing.T) {
	m := newTestModel(t, testStore())
	if m.mode() != modeBrowse {
		t.Errorf("fresh model mode = %v, want browse", m.mode())
	}
	m.detail = &detail{}
	if m.mode() != modeDetail {
		t.Errorf("mode = %v, want detail", m.mode())
	}
	m.filtering = true
	if m.mode() != modeFilter {
		t.Errorf("mode = %v, want filter over detail", m.mode())
	}
	m.prompt = &prompt{label: "x"}
	if m.mode() != modePrompt {
		t.Errorf("mode = %v, want prompt over filter", m.mode())
	}
	m.confirm = &pendingConfirm{prompt: "sure?"}
	if m.mode() != modeConfirm {
		t.Errorf("mode = %v, want confirm over everything", m.mode())
	}
}
