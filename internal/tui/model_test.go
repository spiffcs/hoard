package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cphillips918/hoard/internal/scan"
	"github.com/cphillips918/hoard/internal/scryfall"
)

// fakeSearcher returns canned results for the cascade tests.
type fakeSearcher struct {
	auto   map[string][]string
	prints map[string][]scryfall.Card
	fuzzy  map[string]string // ocr text -> canonical name
}

func (f fakeSearcher) Autocomplete(_ context.Context, q string) ([]string, error) {
	return f.auto[q], nil
}
func (f fakeSearcher) SearchPrints(_ context.Context, name string) ([]scryfall.Card, error) {
	return f.prints[name], nil
}
func (f fakeSearcher) NamedFuzzy(_ context.Context, text string) (*scryfall.Card, error) {
	if name, ok := f.fuzzy[text]; ok {
		return &scryfall.Card{Name: name}, nil
	}
	return nil, nil
}

// recordingAdder captures confirmed results and can be made to fail.
type recordingAdder struct {
	got []Result
	err error
}

func (r *recordingAdder) add(res Result) error {
	if r.err != nil {
		return r.err
	}
	r.got = append(r.got, res)
	return nil
}

func noopAdder(Result) error { return nil }

// fakeScanner hands out fake camera sessions. It records the device it was asked
// to open, so camera-selection tests can assert on it.
type fakeScanner struct {
	devices []scan.Device
	devErr  error
	openErr error

	usedDevice string
	opened     int
	last       *fakeSession
}

func (f *fakeScanner) Devices(context.Context) ([]scan.Device, error) {
	return f.devices, f.devErr
}

func (f *fakeScanner) Open(_ context.Context, deviceID string) (ScanSession, error) {
	f.usedDevice = deviceID
	f.opened++
	if f.openErr != nil {
		return nil, f.openErr
	}
	f.last = &fakeSession{events: make(chan scan.Event, 8)}
	return f.last, nil
}

// fakeSession stands in for a live camera window. Tests push events onto it to
// simulate the helper reporting captures, rotations, and closure.
type fakeSession struct {
	events   chan scan.Event
	captures int
	rotates  int
	closed   bool
}

func (s *fakeSession) Capture() error {
	s.captures++
	return nil
}

func (s *fakeSession) Rotate(bool) error {
	s.rotates++
	return nil
}

func (s *fakeSession) Events() <-chan scan.Event { return s.events }

func (s *fakeSession) Close() error {
	if !s.closed {
		s.closed = true
		close(s.events)
	}
	return nil
}

// emit queues an event as the helper would.
func (s *fakeSession) emit(ev scan.Event) { s.events <- ev }

func cam(id, name, kind string) scan.Device {
	return scan.Device{ID: id, Name: name, Kind: kind}
}

func fp(v float64) *float64 { return &v }

// step executes a single (non-batched) command and feeds its message back into
// the model. The tests that use it only ever return a single lookup command.
func step(t *testing.T, m tea.Model, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return m, nil
	}
	return m.Update(cmd())
}

// isQuit reports whether running cmd yields a tea.QuitMsg.
func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

func TestExactNameSkipsNamePick(t *testing.T) {
	card := scryfall.Card{ID: "u1", Name: "Ulamog, the Infinite Gyre", Set: "uma",
		CollectorNumber: "7", Finishes: []string{"nonfoil", "foil"}}
	fs := fakeSearcher{
		prints: map[string][]scryfall.Card{"Ulamog, the Infinite Gyre": {card}},
	}
	m := newModel(context.Background(), fs, noopAdder, nil, "Ulamog, the Infinite Gyre")

	// Init fires the prints search; deliver its message.
	mm, _ := m.Update(printsMsg{name: "Ulamog, the Infinite Gyre", cards: []scryfall.Card{card}})
	got := mm.(model)
	// Single printing → auto-skip print pick; two finishes → finish picker.
	if got.state != stateFinishPick {
		t.Fatalf("state = %v, want stateFinishPick", got.state)
	}
	if got.chosen == nil || got.chosen.ID != "u1" {
		t.Fatalf("chosen printing not set: %+v", got.chosen)
	}
}

func TestAmbiguousNameShowsNamePick(t *testing.T) {
	fs := fakeSearcher{
		auto: map[string][]string{"Ulamog": {"Ulamog, the Infinite Gyre", "Ulamog, the Ceaseless Hunger"}},
	}
	m := newModel(context.Background(), fs, noopAdder, nil, "Ulamog")

	// Prints for "Ulamog" come back empty → triggers autocomplete.
	mm, cmd := m.Update(printsMsg{name: "Ulamog", cards: nil})
	// cmd is the autocomplete command; run it and feed the result.
	mm, _ = step(t, mm, cmd)
	got := mm.(model)
	if got.state != stateNamePick {
		t.Fatalf("state = %v, want stateNamePick", got.state)
	}
	if len(got.list.Items()) != 2 {
		t.Fatalf("name pick items = %d, want 2", len(got.list.Items()))
	}
}

func TestSinglePrintingSingleFinishSkipsToQty(t *testing.T) {
	card := scryfall.Card{ID: "x", Name: "Foily", Set: "sld", CollectorNumber: "1",
		Finishes: []string{"foil"}} // one printing, one finish
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "Foily")

	mm, _ := m.Update(printsMsg{name: "Foily", cards: []scryfall.Card{card}})
	got := mm.(model)
	if got.state != stateQty {
		t.Fatalf("state = %v, want stateQty (both steps auto-skipped)", got.state)
	}
	if got.finish != "foil" {
		t.Errorf("finish = %q, want foil", got.finish)
	}
}

func TestNoMatchKeepsSession(t *testing.T) {
	// Empty prints AND empty autocomplete → error banner, back on the name prompt.
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "Zzz Nonexistent")
	mm, cmd := m.Update(printsMsg{name: "Zzz Nonexistent", cards: nil})
	mm, _ = step(t, mm, cmd) // run autocomplete → namesMsg{nil}
	got := mm.(model)
	if got.state != stateName || !got.statusErr || got.status == "" {
		t.Fatalf("want stateName with an error banner, got state=%v statusErr=%v status=%q",
			got.state, got.statusErr, got.status)
	}
}

func TestConfirmAddsAndLoopsBack(t *testing.T) {
	card := scryfall.Card{ID: "u1", Name: "Ulamog", Set: "uma", CollectorNumber: "7",
		Finishes: []string{"nonfoil"}, PriceUSD: fp(37.20)}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fakeSearcher{}, ra.add, nil, "Ulamog")
	mm, _ := m.Update(printsMsg{name: "Ulamog", cards: []scryfall.Card{card}})
	got := mm.(model)
	if got.state != stateQty {
		t.Fatalf("expected stateQty, got %v", got.state)
	}
	// Enter a quantity and submit → confirm screen.
	got.qtyInput.SetValue("3")
	mm, _ = got.submitQty()
	got = mm.(model)
	if got.state != stateConfirm {
		t.Fatalf("expected stateConfirm, got %v", got.state)
	}
	// Confirm.
	mm2, cmd := got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm2.(model)

	// The adder received the right result.
	if len(ra.got) != 1 {
		t.Fatalf("adder called %d times, want 1", len(ra.got))
	}
	r := ra.got[0]
	if r.Qty != 3 || r.Finish != "normal" || r.Card.ID != "u1" {
		t.Errorf("result wrong: %+v", r)
	}
	// Session loops back to the name prompt (not quit) with a success banner.
	if got.state != stateName {
		t.Errorf("state = %v, want stateName after confirm", got.state)
	}
	if got.addedCount != 1 || got.status == "" || got.statusErr {
		t.Errorf("session state wrong: count=%d status=%q err=%v", got.addedCount, got.status, got.statusErr)
	}
	if isQuit(cmd) {
		t.Error("confirm should NOT quit in add-mode")
	}
	if got.chosen != nil || got.nameInput.Value() != "" {
		t.Error("cascade selections/name not reset for next add")
	}
}

func TestAdderErrorKeepsSession(t *testing.T) {
	card := scryfall.Card{ID: "u1", Name: "Ulamog", Set: "uma", CollectorNumber: "7",
		Finishes: []string{"nonfoil"}}
	ra := &recordingAdder{err: errors.New("disk full")}
	m := newModel(context.Background(), fakeSearcher{}, ra.add, nil, "Ulamog")
	mm, _ := m.Update(printsMsg{name: "Ulamog", cards: []scryfall.Card{card}})
	got := mm.(model) // stateQty
	mm, _ = got.submitQty()
	got = mm.(model) // stateConfirm
	mm2, cmd := got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm2.(model)

	if got.state != stateName || !got.statusErr {
		t.Fatalf("want stateName with error banner, got state=%v statusErr=%v", got.state, got.statusErr)
	}
	if got.addedCount != 0 {
		t.Errorf("addedCount = %d, want 0 on failed add", got.addedCount)
	}
	if isQuit(cmd) {
		t.Error("a failed add should not quit the session")
	}
}

func TestEscQuitsFromNameButCancelsMidCascade(t *testing.T) {
	card := scryfall.Card{ID: "a", Name: "A", Set: "x", CollectorNumber: "1", Finishes: []string{"nonfoil", "foil"}}
	// esc mid-cascade (print pick) → back to name prompt, not quit.
	fs := fakeSearcher{prints: map[string][]scryfall.Card{"A": {card,
		{ID: "b", Name: "A", Set: "y", CollectorNumber: "2", Finishes: []string{"nonfoil"}}}}}
	m := newModel(context.Background(), fs, noopAdder, nil, "A")
	mm, _ := m.Update(printsMsg{name: "A", cards: fs.prints["A"]})
	got := mm.(model)
	if got.state != statePrintPick {
		t.Fatalf("setup: want statePrintPick, got %v", got.state)
	}
	mm2, cmd := got.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	got = mm2.(model)
	if got.state != stateName {
		t.Errorf("esc mid-cascade: state = %v, want stateName", got.state)
	}
	if isQuit(cmd) {
		t.Error("esc mid-cascade should not quit")
	}

	// esc at the name prompt → quit.
	_, cmd = got.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if !isQuit(cmd) {
		t.Error("esc at name prompt should quit")
	}
}

func TestScanResolvesToPrintingPicker(t *testing.T) {
	// Two printings so we land in the printing picker after a scan.
	cards := []scryfall.Card{
		{ID: "a", Name: "Sol Ring", Set: "c21", CollectorNumber: "263", Finishes: []string{"nonfoil"}},
		{ID: "b", Name: "Sol Ring", Set: "ltc", CollectorNumber: "300", Finishes: []string{"nonfoil", "foil"}},
	}
	fs := fakeSearcher{
		fuzzy:  map[string]string{"Sol Rlng": "Sol Ring"}, // OCR noise → canonical
		prints: map[string][]scryfall.Card{"Sol Ring": cards},
	}
	sc := &fakeScanner{devices: []scan.Device{cam("c1", "iPhone", "iPhone")}}
	m := newModel(context.Background(), fs, noopAdder, sc, "")

	// ctrl+o looks for cameras; a lone camera is opened without asking.
	mm, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlO})
	if mm.(model).state != stateCameraList {
		t.Fatalf("ctrl+o should enter stateCameraList, got %v", mm.(model).state)
	}
	mm, _ = mm.(model).onCameras(camerasMsg{devices: sc.devices})
	sess := &fakeSession{events: make(chan scan.Event, 8)}
	mm, _ = mm.(model).onSession(sessionMsg{session: sess})
	if mm.(model).state != stateCapture {
		t.Fatalf("an open session should wait at stateCapture, got %v", mm.(model).state)
	}
	// A capture reads text → fuzzy → prints. Drive the messages.
	gen := mm.(model).sessionGen
	mm, _ = mm.(model).onSessionEvent(sessionEventMsg{gen: gen, ok: true,
		ev: scan.Event{Kind: scan.EventScan, Name: "Sol Rlng", Candidates: []string{"Sol Rlng"}}})
	mm, _ = mm.(model).onFuzzy(fuzzyMsg{canonical: "Sol Ring", ocr: "Sol Rlng"})
	mm, _ = mm.(model).Update(printsMsg{name: "Sol Ring", cards: cards})
	got := mm.(model)
	if got.state != statePrintPick {
		t.Fatalf("state = %v, want statePrintPick after scan→fuzzy→search", got.state)
	}
	if len(got.list.Items()) != 2 {
		t.Errorf("printing list = %d items, want 2", len(got.list.Items()))
	}
	// The scanned name stays pinned as a header through the rest of the cascade.
	if got.scanned != "Sol Ring" {
		t.Errorf("scanned header name = %q, want %q", got.scanned, "Sol Ring")
	}
	if !strings.Contains(got.View(), "Sol Ring") {
		t.Error("printing picker should show the scanned card name as a header")
	}
	_ = cmd
}

func TestScanHeaderShowsOcrTextWhenItDiffers(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "")
	m.scanned, m.scannedOCR = "Sol Ring", "Sol Rlng"
	head := m.scanHeader()
	if !strings.Contains(head, "Sol Ring") || !strings.Contains(head, "Sol Rlng") {
		t.Errorf("header should show canonical and raw OCR text, got %q", head)
	}

	// An exact read needs no "(read ...)" annotation.
	m.scannedOCR = "sol ring"
	if strings.Contains(m.scanHeader(), "read") {
		t.Errorf("header should omit OCR text when it matches, got %q", m.scanHeader())
	}

	// No scan → no header at all.
	m.scanned, m.scannedOCR = "", ""
	if m.scanHeader() != "" {
		t.Errorf("header should be empty without a scan, got %q", m.scanHeader())
	}
}

func TestScanHeaderClearedAfterAdd(t *testing.T) {
	card := scryfall.Card{ID: "s", Name: "Sol Ring", Set: "c21", CollectorNumber: "263",
		Finishes: []string{"nonfoil"}}
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "")
	m.scanned, m.scannedOCR = "Sol Ring", "Sol Rlng"
	m.chosen, m.finish = &card, "normal"
	m.qtyInput.SetValue("1")

	mm, _ := m.confirmAdd()
	if got := mm.(model); got.scanned != "" || got.scannedOCR != "" {
		t.Errorf("scan header should clear for the next card, got %q/%q", got.scanned, got.scannedOCR)
	}
}

func TestClosingCameraWindowReturnsToPrompt(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "")
	sess := &fakeSession{events: make(chan scan.Event, 8)}
	mm, _ := m.onSession(sessionMsg{session: sess})
	got := mm.(model)

	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true,
		ev: scan.Event{Kind: scan.EventClosed}})
	got = mm.(model)
	if got.state != stateName || got.statusErr {
		t.Errorf("closing the window should return to the prompt quietly: state=%v statusErr=%v",
			got.state, got.statusErr)
	}
	if got.session != nil {
		t.Error("session should be forgotten once the window closes")
	}
	if !sess.closed {
		t.Error("the session should have been closed")
	}
}

func TestScanFallsBackToLaterOcrLines(t *testing.T) {
	// The top-line guess is rules text (what a misrotated capture yields); the
	// real title is further down the list and should still resolve.
	fs := fakeSearcher{fuzzy: map[string]string{"Elspeth, Knight-Errant": "Elspeth, Knight-Errant"}}
	m := newModel(context.Background(), fs, noopAdder, nil, "")

	lines := []string{"control have indestructible.\"", "Volkan Baga", "Elspeth, Knight-Errant"}
	msg := m.namedFuzzyCmd(lines)().(fuzzyMsg)
	if msg.canonical != "Elspeth, Knight-Errant" {
		t.Errorf("canonical = %q, want the title found on a later line", msg.canonical)
	}
	if msg.ocr != "Elspeth, Knight-Errant" {
		t.Errorf("ocr = %q, want the line that actually matched", msg.ocr)
	}
}

func TestPlausibleMatch(t *testing.T) {
	cases := []struct {
		ocr, canonical string
		want           bool
		why            string
	}{
		// The real bug: the word "option" in frame (a keycap, a sleeve) resolved
		// to the card "Opt", because Scryfall fuzzy-matches anything containing a
		// name. Text that merely contains a short name is not that card.
		{"option", "Opt", false, "containing a short name is not being it"},
		{"options", "Opt", false, "same, pluralized"},
		{"adopt", "Opt", false, "short name embedded mid-word"},

		// Genuinely short cards still scan when read exactly.
		{"Opt", "Opt", true, "exact read of a short name"},
		{"opt.", "Opt", true, "punctuation is not a difference"},
		{"Fog", "Fog", true, "exact read of another short name"},

		// Normal reads, including OCR noise and dropped punctuation.
		{"Elspeth, Knight-Errant", "Elspeth, Knight-Errant", true, "clean read"},
		{"Elspeth Knight Errant", "Elspeth, Knight-Errant", true, "punctuation dropped"},
		{"Sol Rlng", "Sol Ring", true, "one-character OCR slip"},
		{"Elspeth", "Elspeth, Knight-Errant", true, "partial read of a longer name"},

		// Card text must not resolve to a card.
		{"control have indestructible", "Opt", false, "rules text"},
		{"Volkan Baga", "Elspeth, Knight-Errant", false, "artist line"},
	}
	for _, c := range cases {
		if got := plausibleMatch(c.ocr, c.canonical); got != c.want {
			t.Errorf("plausibleMatch(%q, %q) = %v, want %v — %s",
				c.ocr, c.canonical, got, c.want, c.why)
		}
	}
}

func TestScanRejectsImplausibleFuzzyMatch(t *testing.T) {
	// Scryfall resolves "option" to "Opt"; the scan must not accept it and must
	// fall through to the line that actually names a card.
	fs := fakeSearcher{fuzzy: map[string]string{
		"option":                 "Opt",
		"Elspeth, Knight-Errant": "Elspeth, Knight-Errant",
	}}
	m := newModel(context.Background(), fs, noopAdder, nil, "")

	msg := m.namedFuzzyCmd([]string{"option", "Elspeth, Knight-Errant"})().(fuzzyMsg)
	if msg.canonical != "Elspeth, Knight-Errant" {
		t.Errorf("canonical = %q, want the real card rather than the Opt false positive",
			msg.canonical)
	}
}

func TestScanFuzzyMissReportsTopLine(t *testing.T) {
	// Nothing matches → the best-guess line is what gets pre-filled for editing.
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "")
	msg := m.namedFuzzyCmd([]string{"Blrgh", "Nonsense"})().(fuzzyMsg)
	if msg.canonical != "" || msg.ocr != "Blrgh" {
		t.Errorf("miss: canonical=%q ocr=%q, want empty canonical and the top line",
			msg.canonical, msg.ocr)
	}
}

func TestScanFuzzyStopsAfterMaxTries(t *testing.T) {
	// A text-heavy capture must not turn into an unbounded burst of lookups.
	var tries int
	counting := countingSearcher{onFuzzy: func() { tries++ }}
	m := newModel(context.Background(), counting, noopAdder, nil, "")

	lines := make([]string, 20)
	for i := range lines {
		lines[i] = "line"
	}
	m.namedFuzzyCmd(lines)()
	if tries != maxFuzzyTries {
		t.Errorf("made %d lookups, want %d", tries, maxFuzzyTries)
	}
}

// countingSearcher counts fuzzy lookups and never matches.
type countingSearcher struct {
	fakeSearcher
	onFuzzy func()
}

func (c countingSearcher) NamedFuzzy(context.Context, string) (*scryfall.Card, error) {
	c.onFuzzy()
	return nil, nil
}

func TestScanMissPrefillsOcrText(t *testing.T) {
	// Fuzzy match fails → back to prompt with OCR text pre-filled for editing.
	m := newModel(context.Background(), fakeSearcher{}, noopAdder,
		&fakeScanner{}, "")
	mm, _ := m.onFuzzy(fuzzyMsg{ocr: "Blrgh Nonsense"}) // canonical empty = miss
	got := mm.(model)
	if got.state != stateName || !got.statusErr {
		t.Fatalf("miss should show error banner on prompt: state=%v statusErr=%v", got.state, got.statusErr)
	}
	if got.nameInput.Value() != "Blrgh Nonsense" {
		t.Errorf("OCR text not pre-filled: %q", got.nameInput.Value())
	}
}

func TestCameraPickerChoosesDeviceAndIsRemembered(t *testing.T) {
	devices := []scan.Device{cam("phone", "Chris's iPhone", "iPhone"), cam("spare", "Old iPhone", "iPhone")}
	sc := &fakeScanner{devices: devices}
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, sc, "")

	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlO})
	mm, _ = mm.(model).onCameras(camerasMsg{devices: devices})
	got := mm.(model)
	if got.state != stateCameraPick {
		t.Fatalf("two cameras should show a picker, got %v", got.state)
	}
	if len(got.list.Items()) != 2 {
		t.Fatalf("camera list = %d items, want 2", len(got.list.Items()))
	}

	// Select the second phone and open it.
	got.list.Select(1)
	mm, cmd := got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm.(model)
	if got.cameraID != "spare" || !got.cameraChosen {
		t.Errorf("camera not recorded: id=%q chosen=%v", got.cameraID, got.cameraChosen)
	}
	if cmd == nil {
		t.Fatal("selecting a camera should issue an open command")
	}
	mm, _ = got.Update(got.openSessionCmd()())
	got = mm.(model)
	if got.state != stateCapture {
		t.Fatalf("opening a camera should land on stateCapture, got %v", got.state)
	}
	if sc.usedDevice != "spare" {
		t.Errorf("opened device %q, want %q", sc.usedDevice, "spare")
	}

	// With the window already open, ctrl+o goes straight back to framing rather
	// than reopening the camera.
	mm, _ = got.cancelToName()
	got = mm.(model)
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyCtrlO})
	if s := mm.(model).state; s != stateCapture {
		t.Errorf("ctrl+o with a live session should return to capture, got %v", s)
	}
	if sc.opened != 1 {
		t.Errorf("opened the camera %d times, want 1", sc.opened)
	}

	// ctrl+r re-opens the picker deliberately.
	mm, _ = mm.(model).handleKey(tea.KeyMsg{Type: tea.KeyCtrlR})
	if s := mm.(model).state; s != stateCameraList {
		t.Errorf("ctrl+r should re-open the camera picker, got %v", s)
	}
}

func TestNoCamerasKeepsSession(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "")
	mm, _ := m.onCameras(camerasMsg{devices: nil})
	got := mm.(model)
	if got.state != stateName || !got.statusErr || got.status == "" {
		t.Errorf("no cameras should banner back to the prompt: state=%v err=%v status=%q",
			got.state, got.statusErr, got.status)
	}
}

// openCapture drives a model to a live capture state with a fake session.
func openCapture(t *testing.T, m model) (model, *fakeSession) {
	t.Helper()
	sess := &fakeSession{events: make(chan scan.Event, 8)}
	mm, _ := m.onSession(sessionMsg{session: sess})
	got := mm.(model)
	if got.state != stateCapture {
		t.Fatalf("setup: want stateCapture, got %v", got.state)
	}
	return got, sess
}

func TestSpaceCapturesWithoutReopeningTheCamera(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "")
	got, sess := openCapture(t, m)

	mm, _ := got.handleKey(tea.KeyMsg{Type: tea.KeySpace})
	got = mm.(model)
	if got.state != stateCapturing {
		t.Fatalf("space should start a capture, got %v", got.state)
	}
	if sess.captures != 1 {
		t.Errorf("session captures = %d, want 1", sess.captures)
	}
	if sess.closed {
		t.Error("capturing must not close the camera window")
	}
}

func TestArrowKeysRotateFromTheTerminal(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "")
	got, sess := openCapture(t, m)

	mm, _ := got.handleKey(tea.KeyMsg{Type: tea.KeyLeft})
	mm, _ = mm.(model).handleKey(tea.KeyMsg{Type: tea.KeyRight})
	if sess.rotates != 2 {
		t.Errorf("session rotates = %d, want 2", sess.rotates)
	}
	if s := mm.(model).state; s != stateCapture {
		t.Errorf("rotating should stay on the capture step, got %v", s)
	}
}

func TestAddingACardReturnsToCaptureWithTheWindowOpen(t *testing.T) {
	// The whole point of the persistent window: after a card lands in the DB the
	// user is back at framing, not at the prompt having to press ctrl+o again.
	card := scryfall.Card{ID: "s", Name: "Sol Ring", Set: "c21", CollectorNumber: "263",
		Finishes: []string{"nonfoil"}}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fakeSearcher{}, ra.add, &fakeScanner{}, "")
	got, sess := openCapture(t, m)

	got.chosen, got.finish = &card, "normal"
	got.qtyInput.SetValue("1")
	mm, _ := got.confirmAdd()
	got = mm.(model)

	if len(ra.got) != 1 {
		t.Fatalf("adder called %d times, want 1", len(ra.got))
	}
	if got.state != stateCapture {
		t.Errorf("after an add, state = %v, want stateCapture", got.state)
	}
	if got.session == nil || sess.closed {
		t.Error("the camera window should still be open for the next card")
	}
	if got.status == "" || got.statusErr {
		t.Error("the success banner should carry over to the capture step")
	}
}

func TestEscAtCaptureClosesCameraButKeepsSession(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "")
	got, sess := openCapture(t, m)

	mm, cmd := got.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	got = mm.(model)
	if got.state != stateName {
		t.Errorf("esc should return to the prompt, got %v", got.state)
	}
	if !sess.closed || got.session != nil {
		t.Error("esc should close the camera window")
	}
	if isQuit(cmd) {
		t.Error("esc at capture should not quit the add session")
	}
}

func TestStaleSessionEventsAreDropped(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "")
	got, _ := openCapture(t, m)
	stale := got.sessionGen

	// Close the camera, then deliver an event from the session that just died.
	mm, _ := got.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	got = mm.(model)

	mm, _ = got.Update(sessionEventMsg{gen: stale, ok: true,
		ev: scan.Event{Kind: scan.EventScan, Name: "Sol Ring"}})
	if s := mm.(model).state; s != stateName {
		t.Errorf("a stale scan event should be dropped, got state %v", s)
	}
}

func TestUnreadableFrameStaysOnCapture(t *testing.T) {
	// A frame Vision couldn't read shouldn't drop the user out of the camera.
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "")
	got, _ := openCapture(t, m)
	mm, _ := got.handleKey(tea.KeyMsg{Type: tea.KeySpace})
	got = mm.(model)

	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true,
		ev: scan.Event{Kind: scan.EventScan}}) // no name, no candidates
	got = mm.(model)
	if got.state != stateCapture {
		t.Errorf("state = %v, want stateCapture so the user can retry", got.state)
	}
	if got.status == "" || !got.statusErr {
		t.Error("an unreadable frame should say so")
	}
}

func TestCaptureErrorKeepsTheWindowOpen(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "")
	got, sess := openCapture(t, m)
	mm, _ := got.handleKey(tea.KeyMsg{Type: tea.KeySpace})
	got = mm.(model)

	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true,
		ev: scan.Event{Kind: scan.EventError, Message: "capture failed"}})
	got = mm.(model)
	if got.state != stateCapture {
		t.Errorf("state = %v, want stateCapture after a failed capture", got.state)
	}
	if got.session == nil || sess.closed {
		t.Error("one bad frame should not tear down the session")
	}
	if got.status != "capture failed" {
		t.Errorf("status = %q, want the helper's message", got.status)
	}
}

func TestScanDisabledWhenNil(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "")
	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlO})
	got := mm.(model)
	if got.state != stateName || !got.statusErr {
		t.Errorf("ctrl+o with no scanner should show a banner, got state=%v statusErr=%v", got.state, got.statusErr)
	}
}

func TestQtyValidation(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "x")
	m.state = stateQty
	m.qtyInput.SetValue("0")
	mm, _ := m.submitQty()
	got := mm.(model)
	if got.state != stateQty || got.qtyErr == "" {
		t.Errorf("qty 0 should stay on stateQty with an error, got state=%v err=%q", got.state, got.qtyErr)
	}
}

func TestFinishOptions(t *testing.T) {
	c := scryfall.Card{Finishes: []string{"foil", "nonfoil", "etched"}}
	got := finishOptions(c)
	want := []string{"normal", "foil", "etched"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("finishOptions = %v, want %v (stable order)", got, want)
	}
	if fo := finishOptions(scryfall.Card{}); len(fo) != 0 {
		t.Errorf("no finishes → empty, got %v", fo)
	}
}
