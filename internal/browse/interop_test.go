package browse

import (
	"context"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/progress"
)

func deckAddModel(t *testing.T, fn DeckAddFunc) Model {
	t.Helper()
	m, err := New(testStore(), WithDeckAddByURL(fn))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ctx = context.Background()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	return next.(Model)
}

// typePrompt types into the open prompt and commits with enter.
func typePrompt(t *testing.T, m Model, text string) (Model, tea.Cmd) {
	t.Helper()
	if m.prompt == nil {
		t.Fatal("no prompt open")
	}
	for _, r := range text {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(Model)
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	return next.(Model), cmd
}

func TestDeckURLPromptValidates(t *testing.T) {
	m := deckAddModel(t, func(context.Context, progress.Fn, string) (OpReport, error) {
		return OpReport{}, nil
	})
	m, _ = runPaletteCommand(t, m, "deck.add-url")
	if m.prompt == nil {
		t.Fatal("command did not open the URL prompt")
	}

	for _, bad := range []string{"not a url", "ftp://deck.example/x", "https://"} {
		mm, cmd := typePrompt(t, m, bad)
		if cmd != nil || mm.prompt == nil || mm.prompt.err == "" {
			t.Fatalf("%q: want a validation refusal, got err=%q", bad, mm.prompt.err)
		}
		// Wipe for the next round.
		next, _ := mm.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
		m = next.(Model)
	}
}

func TestDeckURLCommitStartsOpWithURL(t *testing.T) {
	var gotURL string
	m := deckAddModel(t, func(_ context.Context, _ progress.Fn, u string) (OpReport, error) {
		gotURL = u
		return OpReport{Summary: `imported deck "Krenko" (archidekt) · 98 cards resolved`}, nil
	})
	m, _ = runPaletteCommand(t, m, "deck.add-url")
	m, cmd := typePrompt(t, m, "https://archidekt.com/decks/123")
	if m.prompt != nil {
		t.Fatal("prompt should close on commit")
	}
	if m.op == nil {
		t.Fatal("commit did not start the op")
	}
	done := findOpDone(t, cmd)
	if gotURL != "https://archidekt.com/decks/123" {
		t.Fatalf("url = %q", gotURL)
	}
	next, _ := m.Update(done)
	m = next.(Model)
	if !strings.Contains(m.status, "imported deck") {
		t.Fatalf("status = %q", m.status)
	}
	if m.text != nil {
		t.Fatal("no report lines → no takeover")
	}
}

func TestOpReportAutoOpensTextView(t *testing.T) {
	m := deckAddModel(t, func(context.Context, progress.Fn, string) (OpReport, error) {
		return OpReport{
			Summary: "imported deck · 2 unresolved",
			Report:  []string{"2 cards could not be resolved and were skipped:", "", "Foo", "Bar"},
		}, nil
	})
	m, _ = runPaletteCommand(t, m, "deck.add-url")
	m, cmd := typePrompt(t, m, "https://archidekt.com/decks/9")
	done := findOpDone(t, cmd)
	next, _ := m.Update(done)
	m = next.(Model)
	if m.text == nil {
		t.Fatal("a report outcome must open the text takeover")
	}
	if v := m.View(); !strings.Contains(v, "Foo") || !strings.Contains(v, "importing deck") {
		t.Fatalf("takeover missing content:\n%s", v)
	}
	// esc reveals refreshed panes with the summary still on the status line.
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(Model)
	if m.text != nil || !strings.Contains(m.status, "imported deck") {
		t.Fatalf("after esc: text=%v status=%q", m.text, m.status)
	}
}

func TestOpOutcomeConfirmIsStaged(t *testing.T) {
	m := opModel(t, nil)
	ran := false
	cmd := (&m).startOpReport("importing x", func(context.Context, progress.Fn) (opOutcome, error) {
		return opOutcome{
			summary: "already imported",
			confirm: &pendingConfirm{
				prompt: "imported before. Import it again?",
				help:   "y import again · any other key cancels",
				onYes:  func(*Model) tea.Cmd { ran = true; return nil },
			},
		}, nil
	})
	done := findOpDone(t, cmd)
	next, _ := m.Update(done)
	m = next.(Model)
	if m.confirm == nil || !strings.Contains(m.confirm.prompt, "Import it again") {
		t.Fatalf("confirm = %+v, want the follow-up staged", m.confirm)
	}
	if !strings.Contains(m.helpLine(), "y import again") {
		t.Errorf("helpLine = %q", m.helpLine())
	}
	m = key(m, "y")
	if !ran {
		t.Fatal("y must run the follow-up")
	}
}

func importModel(t *testing.T, fn ImportFunc) Model {
	t.Helper()
	m, err := New(testStore(), WithImportFile(fn))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ctx = context.Background()
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	return next.(Model)
}

func TestImportPromptValidatesPath(t *testing.T) {
	m := importModel(t, func(context.Context, progress.Fn, string, bool) (OpReport, error) {
		return OpReport{}, nil
	})
	m, _ = runPaletteCommand(t, m, "import.file")
	if m.prompt == nil {
		t.Fatal("command did not open the path prompt")
	}

	mm, cmd := typePrompt(t, m, "/no/such/file.csv")
	if cmd != nil || mm.prompt == nil || mm.prompt.err != "no such file" {
		t.Fatalf("missing file: err=%q", mm.prompt.err)
	}
	next, _ := mm.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	mm = next.(Model)
	mm, cmd = typePrompt(t, mm, t.TempDir())
	if cmd != nil || mm.prompt == nil || mm.prompt.err != "that is a directory" {
		t.Fatalf("directory: err=%q", mm.prompt.err)
	}
}

func TestImportHappyPathOpensReportOverlay(t *testing.T) {
	var gotPath string
	var gotAgain bool
	m := importModel(t, func(_ context.Context, _ progress.Fn, path string, again bool) (OpReport, error) {
		gotPath, gotAgain = path, again
		return OpReport{
			Summary: "imported 120 cards (manabox format) · 118 rows resolved",
			Report:  []string{"120 into Binder"},
		}, nil
	})
	f := t.TempDir() + "/cards.csv"
	if err := os.WriteFile(f, []byte("Name\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, _ = runPaletteCommand(t, m, "import.file")
	m, cmd := typePrompt(t, m, f)
	done := findOpDone(t, cmd)
	next, _ := m.Update(done)
	m = next.(Model)

	if gotPath != f || gotAgain {
		t.Fatalf("closure got path=%q again=%v", gotPath, gotAgain)
	}
	if m.text == nil || !strings.Contains(m.View(), "120 into Binder") {
		t.Fatal("import outcome must open the report overlay")
	}
	if !strings.Contains(m.status, "imported 120 cards") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestImportAlreadyImportedStagesConfirmAndReruns(t *testing.T) {
	calls := []bool{}
	m := importModel(t, func(_ context.Context, _ progress.Fn, _ string, again bool) (OpReport, error) {
		calls = append(calls, again)
		if !again {
			return OpReport{AlreadyImported: "already imported on 2026-07-30 (312 cards)"}, nil
		}
		return OpReport{Summary: "imported 312 cards (hoard format) · 312 rows resolved"}, nil
	})
	f := t.TempDir() + "/cards.csv"
	if err := os.WriteFile(f, []byte("Name\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, _ = runPaletteCommand(t, m, "import.file")
	m, cmd := typePrompt(t, m, f)
	next, _ := m.Update(findOpDone(t, cmd))
	m = next.(Model)

	if m.confirm == nil || !strings.Contains(m.confirm.prompt, "Import it again?") {
		t.Fatalf("confirm = %+v, want the run-again question", m.confirm)
	}
	// y re-runs with again=true.
	nxt, cmd2 := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	m = nxt.(Model)
	next2, _ := m.Update(findOpDone(t, cmd2))
	m = next2.(Model)
	if len(calls) != 2 || calls[0] || !calls[1] {
		t.Fatalf("calls = %v, want [false true]", calls)
	}
	if !strings.Contains(m.status, "imported 312 cards") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestImportAlreadyImportedDeclineCancels(t *testing.T) {
	calls := 0
	m := importModel(t, func(context.Context, progress.Fn, string, bool) (OpReport, error) {
		calls++
		return OpReport{AlreadyImported: "already imported on 2026-07-30 (312 cards)"}, nil
	})
	f := t.TempDir() + "/cards.csv"
	if err := os.WriteFile(f, []byte("Name\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, _ = runPaletteCommand(t, m, "import.file")
	m, cmd := typePrompt(t, m, f)
	next, _ := m.Update(findOpDone(t, cmd))
	m = next.(Model)
	m = key(m, "n")
	if calls != 1 {
		t.Fatalf("decline must not re-run: calls = %d", calls)
	}
	if m.status != "cancelled" {
		t.Fatalf("status = %q", m.status)
	}
}

type exportCall struct{ binderRef, deckRef, format, path string }

func exportModel(t *testing.T, calls *[]exportCall) Model {
	t.Helper()
	m, err := New(testStore(), WithExport(func(binderRef, deckRef, format, path string) (string, error) {
		*calls = append(*calls, exportCall{binderRef, deckRef, format, path})
		return "exported 42 rows to " + path, nil
	}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	m.ctx = context.Background()
	// Step past the merged all-cards row: these tests export the binder.
	m.cursor[paneContainers] = 1
	if err := m.loadCards(); err != nil {
		t.Fatalf("loadCards: %v", err)
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	return next.(Model)
}

func TestExportContainerPrefillsSlugAndRefsByID(t *testing.T) {
	var calls []exportCall
	m := exportModel(t, &calls)
	m, _ = runPaletteCommand(t, m, "export.container")
	if m.prompt == nil {
		t.Fatal("no path prompt")
	}
	if !strings.HasPrefix(m.prompt.text, "~/hoard-") || !strings.HasSuffix(m.prompt.text, ".csv") {
		t.Fatalf("prefill = %q", m.prompt.text)
	}

	// Commit to a real temp dir; then the format prompt, prefilled csv.
	dir := t.TempDir()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = next.(Model)
	m, _ = typePrompt(t, m, dir+"/out.csv")
	if m.prompt == nil || m.prompt.text != "csv" {
		t.Fatalf("format prompt = %+v", m.prompt)
	}
	m, _ = typePrompt(t, m, "") // accept the prefill
	if m.confirm != nil {
		t.Fatal("no overwrite confirm for a fresh file")
	}
	if len(calls) != 1 {
		t.Fatalf("export not run: %v", calls)
	}
	c := calls[0]
	// The default binder is the selected container in the test store.
	if c.binderRef == "" || c.deckRef != "" {
		t.Fatalf("refs = %+v, want a binder id ref", c)
	}
	if c.format != "csv" || c.path != dir+"/out.csv" {
		t.Fatalf("call = %+v", c)
	}
	if !strings.Contains(m.status, "exported 42 rows") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestExportJSONPrefillFromExtension(t *testing.T) {
	var calls []exportCall
	m := exportModel(t, &calls)
	m, _ = runPaletteCommand(t, m, "export.all")
	dir := t.TempDir()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = next.(Model)
	m, _ = typePrompt(t, m, dir+"/everything.json")
	if m.prompt == nil || m.prompt.text != "json" {
		t.Fatalf("format prefill = %+v", m.prompt)
	}
	m, _ = typePrompt(t, m, "")
	if len(calls) != 1 || calls[0].binderRef != "" || calls[0].deckRef != "" {
		t.Fatalf("export-all call = %+v", calls)
	}
	if calls[0].format != "json" {
		t.Fatalf("format = %q", calls[0].format)
	}
}

func TestExportValidatesDirAndFormat(t *testing.T) {
	var calls []exportCall
	m := exportModel(t, &calls)
	m, _ = runPaletteCommand(t, m, "export.all")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = next.(Model)
	mm, cmd := typePrompt(t, m, "/no/such/dir/out.csv")
	if cmd != nil || mm.prompt == nil || mm.prompt.err != "no such directory" {
		t.Fatalf("dir validation: err=%q", mm.prompt.err)
	}
	nxt, _ := mm.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	mm = nxt.(Model)
	mm, _ = typePrompt(t, mm, t.TempDir()+"/x.csv")
	nxt, _ = mm.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	mm = nxt.(Model)
	mm, cmd = typePrompt(t, mm, "xml")
	if cmd != nil || mm.prompt == nil || mm.prompt.err == "" {
		t.Fatalf("format validation: err=%q", mm.prompt.err)
	}
	if len(calls) != 0 {
		t.Fatal("nothing should have run")
	}
}

func TestExportOverwriteConfirmOnlyWhenFileExists(t *testing.T) {
	var calls []exportCall
	m := exportModel(t, &calls)
	f := t.TempDir() + "/exists.csv"
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m, _ = runPaletteCommand(t, m, "export.all")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = next.(Model)
	m, _ = typePrompt(t, m, f)
	m, _ = typePrompt(t, m, "")
	if m.confirm == nil || !strings.Contains(m.confirm.prompt, "exists. Overwrite?") {
		t.Fatalf("confirm = %+v, want the overwrite question", m.confirm)
	}
	if len(calls) != 0 {
		t.Fatal("export must wait for the confirm")
	}
	m = key(m, "y")
	if len(calls) != 1 {
		t.Fatal("y must run the export")
	}
}

func TestExportContainerHiddenOffHoldings(t *testing.T) {
	var calls []exportCall
	m := exportModel(t, &calls)
	m = key(m, "v") // movers
	for i := range m.commands {
		if m.commands[i].id == "export.container" && m.commands[i].applies(&m) {
			t.Fatal("export.container must hide off the holdings view")
		}
	}
}
