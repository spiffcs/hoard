package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/cardname"
	"github.com/spiffcs/hoard/internal/scan"
	"github.com/spiffcs/hoard/internal/scryfall"
)

// fakeSearcher returns canned results for the cascade tests.
type fakeSearcher struct {
	auto   map[string][]string
	prints map[string][]scryfall.Card
	fuzzy  map[string]string         // ocr text -> canonical name
	match  map[string]cardname.Match // optional override; else computed for real
}

func (f fakeSearcher) Autocomplete(_ context.Context, q string) ([]string, error) {
	return f.auto[q], nil
}
func (f fakeSearcher) SearchPrints(_ context.Context, name string) ([]scryfall.Card, error) {
	return f.prints[name], nil
}
func (f fakeSearcher) NamedFuzzy(_ context.Context, text string) (*scryfall.Card, cardname.Match, error) {
	if name, ok := f.fuzzy[text]; ok {
		if m, ok := f.match[text]; ok {
			return &scryfall.Card{Name: name}, m, nil
		}
		// Compute the match for real, so tests mapping an exact name get an
		// exact match without saying so twice.
		n, c := cardname.Normalize(text), cardname.Normalize(name)
		return &scryfall.Card{Name: name},
			cardname.Match{Exact: n == c, Similarity: cardname.Similarity(n, c)}, nil
	}
	return nil, cardname.Match{}, nil
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
	pairs   map[string]string
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

// paired records what Pair was asked to remember, so a test can assert the
// prompt handed the digits over rather than merely accepting them.
func (f *fakeScanner) Pair(deviceID, code string) error {
	if f.pairs == nil {
		f.pairs = map[string]string{}
	}
	f.pairs[deviceID] = code
	return nil
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
	chimes     int
	results    []scan.HUDResult
	events     chan scan.Event
	captures   int
	rotates    int
	autoOn     int
	autoOff    int
	framingOn  int
	framingOff int
	torchOn    int
	torchOff   int
	effects    int
	rearms     int
	closed     bool
}

func (s *fakeSession) Result(r scan.HUDResult) error {
	s.results = append(s.results, r)
	return nil
}

func (s *fakeSession) Rearm() error {
	s.rearms++
	return nil
}

func (s *fakeSession) Chime() error {
	s.chimes++
	return nil
}

func (s *fakeSession) Capture() error {
	s.captures++
	return nil
}

func (s *fakeSession) Auto(on bool) error {
	if on {
		s.autoOn++
	} else {
		s.autoOff++
	}
	return nil
}

func (s *fakeSession) Rotate(bool) error {
	s.rotates++
	return nil
}

func (s *fakeSession) AutoFraming(on bool) error {
	if on {
		s.framingOn++
	} else {
		s.framingOff++
	}
	return nil
}

func (s *fakeSession) Torch(on bool) error {
	if on {
		s.torchOn++
	} else {
		s.torchOff++
	}
	return nil
}

func (s *fakeSession) VideoEffects() error {
	s.effects++
	return nil
}

func (s *fakeSession) Note(string) {}

func (s *fakeSession) Events() <-chan scan.Event { return s.events }

func (s *fakeSession) Close() error {
	if !s.closed {
		s.closed = true
		close(s.events)
	}
	return nil
}

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
	m := newModel(context.Background(), fs, noopAdder, nil, "Ulamog, the Infinite Gyre", nil)

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
	m := newModel(context.Background(), fs, noopAdder, nil, "Ulamog", nil)

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
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "Foily", nil)

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
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "Zzz Nonexistent", nil)
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
	m := newModel(context.Background(), fakeSearcher{}, ra.add, nil, "Ulamog", nil)
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
	if r.Qty != 3 || r.Finish != "nonfoil" || r.Card.ID != "u1" {
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
	m := newModel(context.Background(), fakeSearcher{}, ra.add, nil, "Ulamog", nil)
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
	m := newModel(context.Background(), fs, noopAdder, nil, "A", nil)
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

	// esc at the name prompt → the leave gate, and a single y quits.
	mm2, cmd = got.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	got = mm2.(model)
	if got.state != stateLeaveConfirm || isQuit(cmd) {
		t.Fatalf("esc at name: state = %v (cmd quit %v), want the leave gate", got.state, isQuit(cmd))
	}
	// Enter is not part of the confirm: it stays, like any other key.
	mm2, cmd = got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm2.(model)
	if got.state != stateName || isQuit(cmd) {
		t.Fatalf("enter on the gate: state = %v (cmd quit %v), want a return to stateName", got.state, isQuit(cmd))
	}
	mm2, cmd = got.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	got = mm2.(model)
	_, cmd = got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if !isQuit(cmd) {
		t.Error("y on the gate should quit")
	}
}

func TestScanResolvesInBackgroundAndQueues(t *testing.T) {
	// Two printings and no collector info: the card resolves but cannot commit
	// itself, so it queues while the camera stays interactive — and reviewing
	// it re-enters the printing picker with the scan header pinned.
	cards := []scryfall.Card{
		{ID: "a", Name: "Sol Ring", Set: "c21", CollectorNumber: "263", Finishes: []string{"nonfoil"}},
		{ID: "b", Name: "Sol Ring", Set: "ltc", CollectorNumber: "300", Finishes: []string{"nonfoil", "foil"}},
	}
	fs := fakeSearcher{
		fuzzy:  map[string]string{"Sol Rlng": "Sol Ring"}, // OCR noise → canonical
		prints: map[string][]scryfall.Card{"Sol Ring": cards},
	}
	sc := &fakeScanner{devices: []scan.Device{cam("c1", "iPhone", "iPhone")}}
	m := newModel(context.Background(), fs, noopAdder, sc, "", nil)

	// ctrl+o looks for cameras; a lone camera is opened without asking.
	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlO})
	if mm.(model).state != stateCameraBusy {
		t.Fatalf("ctrl+o should enter stateCameraBusy, got %v", mm.(model).state)
	}
	mm, _ = mm.(model).onCameras(camerasMsg{devices: sc.devices})
	sess := &fakeSession{events: make(chan scan.Event, 8)}
	mm, _ = mm.(model).onSession(sessionMsg{session: sess})
	if mm.(model).state != stateCapture {
		t.Fatalf("an open session should wait at stateCapture, got %v", mm.(model).state)
	}
	got := mm.(model)
	ev := scan.Event{Kind: scan.EventScan, Name: "Sol Rlng", Candidates: []string{"Sol Rlng"}}
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true, ev: ev})
	got = mm.(model)
	if got.state != stateCapture {
		t.Fatalf("resolution must run behind the camera, got state %v", got.state)
	}
	// Run the background resolve as the command would, and land its result.
	mm, _ = got.Update(got.resolveCardCmd(1, ev.CardList()[0], 1)())
	got = mm.(model)
	if len(got.review) != 1 {
		t.Fatalf("review queue = %d, want the unverified card queued", len(got.review))
	}
	if got.state != stateCapture {
		t.Errorf("queueing must not change state, got %v", got.state)
	}

	// Review it: the cascade re-enters at the printing picker, no refetch.
	mm, _ = got.startReview(got.review[0])
	got = mm.(model)
	if got.state != statePrintPick {
		t.Fatalf("state = %v, want statePrintPick on review", got.state)
	}
	if len(got.list.Items()) != 2 {
		t.Errorf("printing list = %d items, want 2", len(got.list.Items()))
	}
	if got.scanned != "Sol Ring" {
		t.Errorf("scanned header name = %q, want %q", got.scanned, "Sol Ring")
	}
	if !strings.Contains(got.View(), "Sol Ring") {
		t.Error("printing picker should show the scanned card name as a header")
	}
}

// solRingPrints is a stand-in for a heavily reprinted card, in Scryfall's
// newest-first order. The wanted printing is deliberately not first.
func solRingPrints() []scryfall.Card {
	return []scryfall.Card{
		{ID: "a", Name: "Sol Ring", Set: "ltc", CollectorNumber: "300", Finishes: []string{"nonfoil"}},
		{ID: "b", Name: "Sol Ring", Set: "c21", CollectorNumber: "263", Finishes: []string{"nonfoil"}},
		{ID: "c", Name: "Sol Ring", Set: "mh3", CollectorNumber: "123", Finishes: []string{"nonfoil"}},
	}
}

func TestScannedNumberRanksAndMarksPrinting(t *testing.T) {
	cards := solRingPrints()
	fs := fakeSearcher{prints: map[string][]scryfall.Card{"Sol Ring": cards}}
	m := newModel(context.Background(), fs, noopAdder, nil, "", nil)
	m.scanned = "Sol Ring"
	m.scannedSet = "MH3"
	m.scannedNumber = "123"

	mm, _ := m.onPrints(printsMsg{name: "Sol Ring", cards: cards})
	got := mm.(model)

	if got.state != statePrintPick {
		t.Fatalf("state = %v, want statePrintPick", got.state)
	}
	// Every printing is still offered; only the order changed.
	if len(got.list.Items()) != len(cards) {
		t.Fatalf("list has %d items, want all %d printings", len(got.list.Items()), len(cards))
	}
	first, ok := got.list.Items()[0].(printItem)
	if !ok {
		t.Fatalf("first item is %T, want printItem", got.list.Items()[0])
	}
	if first.card.CollectorNumber != "123" || !strings.EqualFold(first.card.Set, "mh3") {
		t.Errorf("first printing = %s #%s, want MH3 #123",
			first.card.Set, first.card.CollectorNumber)
	}
	if !first.scanned {
		t.Error("the matched printing should be marked as scanned")
	}
	if !strings.Contains(first.Title(), "scanned") {
		t.Errorf("Title() = %q, want a scanned marker", first.Title())
	}
	// Pre-selected, not auto-committed: the cursor is on it but nothing advanced.
	if got.list.Index() != 0 {
		t.Errorf("cursor at %d, want the scanned printing at 0", got.list.Index())
	}
	if got.chosen != nil {
		t.Error("a scanned number must not select a printing outright")
	}
	// The rest keep Scryfall's newest-first order.
	rest := got.list.Items()[1:]
	if rest[0].(printItem).card.Set != "ltc" || rest[1].(printItem).card.Set != "c21" {
		t.Errorf("remaining order = %v, want ltc then c21", rest)
	}
}

func TestScannedNumberMatchingNothingSaysSo(t *testing.T) {
	cards := solRingPrints()
	fs := fakeSearcher{prints: map[string][]scryfall.Card{"Sol Ring": cards}}
	m := newModel(context.Background(), fs, noopAdder, nil, "", nil)
	m.scanned = "Sol Ring"
	m.scannedNumber = "999" // misread, or the name match was wrong

	mm, _ := m.onPrints(printsMsg{name: "Sol Ring", cards: cards})
	got := mm.(model)

	if got.list.Items()[0].(printItem).card.Set != "ltc" {
		t.Error("an unmatched number must leave Scryfall's order alone")
	}
	for i, it := range got.list.Items() {
		if it.(printItem).scanned {
			t.Errorf("item %d marked scanned when nothing matched", i)
		}
	}
	if !strings.Contains(got.status, "999") {
		t.Errorf("status = %q, want it to name the number that found nothing", got.status)
	}
}

func TestRankByScan(t *testing.T) {
	cards := solRingPrints()

	// No number read: nothing moves.
	if _, matched := rankByScan(cards, "", ""); matched {
		t.Error("no scanned number should not match")
	}

	// Number alone is enough when it picks out one printing.
	got, matched := rankByScan(cards, "", "263")
	if !matched || got[0].Set != "c21" {
		t.Errorf("number-only match = %v (matched=%v), want c21 first", got, matched)
	}

	// A set code disambiguates when two printings share a collector number.
	dupes := []scryfall.Card{
		{ID: "a", Name: "X", Set: "aaa", CollectorNumber: "7"},
		{ID: "b", Name: "X", Set: "bbb", CollectorNumber: "7"},
	}
	got, matched = rankByScan(dupes, "BBB", "7")
	if !matched || got[0].Set != "bbb" {
		t.Errorf("set+number match = %v, want bbb first", got)
	}

	// Original slice must be left alone; the caller still holds it.
	if cards[0].Set != "ltc" {
		t.Error("rankByScan mutated its input")
	}
}

func TestScanHeaderShowsOcrTextWhenItDiffers(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
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
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	m.scanned, m.scannedOCR = "Sol Ring", "Sol Rlng"
	m.chosen, m.finish = &card, "nonfoil"
	m.qtyInput.SetValue("1")

	mm, _ := m.confirmAdd()
	if got := mm.(model); got.scanned != "" || got.scannedOCR != "" {
		t.Errorf("scan header should clear for the next card, got %q/%q", got.scanned, got.scannedOCR)
	}
}

func TestClosingCameraWindowReturnsToPrompt(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
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
	// real title is further down the list and should still resolve — and the
	// line index says it was a fallback, which the auto-commit bar refuses.
	fs := fakeSearcher{fuzzy: map[string]string{"Elspeth, Knight-Errant": "Elspeth, Knight-Errant"}}

	lines := []string{"control have indestructible.\"", "Volkan Baga", "Elspeth, Knight-Errant"}
	canonical, ocr, idx, _, err := resolveName(context.Background(), fs, lines, tuningFor(""))
	if err != nil {
		t.Fatalf("resolveName: %v", err)
	}
	if canonical != "Elspeth, Knight-Errant" {
		t.Errorf("canonical = %q, want the title found on a later line", canonical)
	}
	if ocr != "Elspeth, Knight-Errant" {
		t.Errorf("ocr = %q, want the line that actually matched", ocr)
	}
	if idx != 2 {
		t.Errorf("line index = %d, want 2 — the verdict needs to know it was a fallback", idx)
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

		// OCR glues the title to the line below it; the read starting with
		// the whole (long enough) name is that name.
		{"Inspired Fire deals + tam", "Inspired Fire", true, "title glued to rules text"},
		{"option please", "Opt", false, "the prefix mirror must not revive the Opt bug"},
	}
	for _, c := range cases {
		if got := cardname.Plausible(c.ocr, c.canonical); got != c.want {
			t.Errorf("cardname.Plausible(%q, %q) = %v, want %v — %s",
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
	canonical, _, _, _, _ := resolveName(context.Background(), fs,
		[]string{"option", "Elspeth, Knight-Errant"}, tuningFor(""))
	if canonical != "Elspeth, Knight-Errant" {
		t.Errorf("canonical = %q, want the real card rather than the Opt false positive", canonical)
	}
}

func TestScanFuzzyMissReportsTopLine(t *testing.T) {
	// Nothing matches → the best-guess line is what gets pre-filled for editing.
	canonical, ocr, _, _, _ := resolveName(context.Background(), fakeSearcher{},
		[]string{"Blrgh", "Nonsense"}, tuningFor(""))
	if canonical != "" || ocr != "Blrgh" {
		t.Errorf("miss: canonical=%q ocr=%q, want empty canonical and the top line", canonical, ocr)
	}
}

func TestFallbackLinesWithTypeWordsNeverResolve(t *testing.T) {
	// "creature." as a fallback line fuzzy-resolved to the real card Creature
	// Guy (observed live). Type-line vocabulary on a non-primary line is frame
	// furniture; on the primary line it may be the actual title.
	fs := fakeSearcher{fuzzy: map[string]string{
		"creature.":    "Creature Guy",
		"Creature Guy": "Creature Guy",
	}}
	canonical, _, _, _, _ := resolveName(context.Background(), fs,
		[]string{"Inspired Fire deals + tam garbage xyz", "creature.", "flavor text"}, tuningFor(""))
	if canonical != "" {
		t.Errorf("canonical = %q, want no match — the type-word fallback line must be skipped", canonical)
	}
	// The same text as the primary line still resolves: that IS the card.
	canonical, _, _, _, _ = resolveName(context.Background(), fs, []string{"Creature Guy"}, tuningFor(""))
	if canonical != "Creature Guy" {
		t.Errorf("canonical = %q, want the primary line to stay eligible", canonical)
	}
}

func TestScanFuzzyStopsAfterMaxTries(t *testing.T) {
	// A text-heavy capture must not turn into an unbounded burst of lookups.
	var tries int
	counting := countingSearcher{onFuzzy: func() { tries++ }}

	lines := make([]string, 20)
	for i := range lines {
		// Title-like on purpose: lines the junk gate skips don't cost a
		// lookup, and this test is about the cap on lines that do.
		lines[i] = fmt.Sprintf("Cardish Name %c", 'A'+rune(i))
	}
	resolveName(context.Background(), counting, lines, tuningFor(""))
	if tries != maxFuzzyTries {
		t.Errorf("made %d lookups, want %d", tries, maxFuzzyTries)
	}
}

func TestTitleLikelyGate(t *testing.T) {
	cases := []struct {
		line string
		want bool
		why  string
	}{
		{"Elspeth, Knight-Errant", true, "a clean title"},
		{"Volkan Baga", true, "artist lines stay eligible — coarse by design"},
		{"Borrowing 100,000 Arrows", true, "digits in a real name are fine"},
		{"Sol Rlng", true, "OCR slips must still resolve"},

		{"counters on one or more other Heroes you", false, "rules text leads lowercase"},
		{"\"I'll hold down the fort.\"", false, "flavor text leads with a quote"},
		{"—Doctor Doom", false, "attribution leads with a dash"},
		{"2/5", false, "the P/T box"},
		{"U 0017", false, "a collector line is mostly digits"},
		{"1M & : 2026 Wizards of the Coast", false, "copyright line is digit-heavy"},
		{"I™ & € 2026 Wizards of the Coast", false, "trademark glyphs"},
		{"MSH", false, "a bare set code is too short"},
		{"", false, "empty"},
	}
	for _, c := range cases {
		if got := titleLikely(c.line); got != c.want {
			t.Errorf("titleLikely(%q) = %v, want %v — %s", c.line, got, c.want, c.why)
		}
	}
}

func TestKeywordFallbackLinesNeverResolve(t *testing.T) {
	// "Haste" as a fallback line fuzzy-resolved to the real card Haste Magic
	// (observed live, a phantom queue entry). Keyword-only lines are frame
	// text; the primary line stays eligible for the rare title that IS one.
	for _, line := range []string{"Haste", "Flying", "Double Strike", "Lifelink."} {
		if !keywordLine(line) {
			t.Errorf("keywordLine(%q) = false, want true", line)
		}
	}
	for _, line := range []string{"Haste Magic", "Flash of Insight", "Reach of Branches"} {
		if keywordLine(line) {
			t.Errorf("keywordLine(%q) = true, want false — real names must stay eligible", line)
		}
	}

	var tries int
	counting := countingSearcher{onFuzzy: func() { tries++ }}
	resolveName(context.Background(), counting, []string{"Blurred Junk", "Haste", "Flying"}, tuningFor(""))
	if tries != 1 {
		t.Errorf("made %d lookups, want 1 — keyword fallback lines must not reach the searcher", tries)
	}
	resolveName(context.Background(), counting, []string{"Flash"}, tuningFor(""))
	if tries != 2 {
		t.Errorf("made %d lookups, want 2 — a keyword as the primary line is still tried", tries)
	}
}

func TestJunkFallbackLinesNeverReachTheSearcher(t *testing.T) {
	// Every fallback line here is junk (rules text, flavor, collector), so
	// only line 0 — the helper's actual title guess, never gated — may cost
	// a lookup. Junk lines were guaranteed catalog misses, and each one paid
	// a sequential Scryfall round trip and risked fuzzy-resolving into a
	// real-but-unscanned card (the shadow-card channel).
	var tries int
	counting := countingSearcher{onFuzzy: func() { tries++ }}
	resolveName(context.Background(), counting, []string{
		"blurred junk title", // line 0: junk, but always eligible
		"counters on one or more other Heroes you",
		"\"Y'll hold down the fort while you guys bicker.\"",
		"2/5",
		"1M & : 2026 Wizards of the Coast",
	}, tuningFor(""))
	if tries != 1 {
		t.Errorf("made %d lookups, want 1 — only the primary line", tries)
	}

	// A real title on a fallback line still resolves.
	fs := fakeSearcher{fuzzy: map[string]string{"Elspeth, Knight-Errant": "Elspeth, Knight-Errant"}}
	canonical, _, idx, _, _ := resolveName(context.Background(), fs,
		[]string{"blurred junk", "Elspeth, Knight-Errant"}, tuningFor(""))
	if canonical != "Elspeth, Knight-Errant" || idx != 1 {
		t.Errorf("canonical=%q idx=%d, want the fallback title to survive the gate", canonical, idx)
	}
}

// countingSearcher counts fuzzy lookups and never matches.
type countingSearcher struct {
	fakeSearcher
	onFuzzy func()
}

func (c countingSearcher) NamedFuzzy(context.Context, string) (*scryfall.Card, cardname.Match, error) {
	c.onFuzzy()
	return nil, cardname.Match{}, nil
}

func TestReviewItemWithFuzzyMissPrefillsName(t *testing.T) {
	// A card that never resolved a name reviews at the prompt, its OCR text
	// pre-filled for editing and the queue reason as the banner.
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	it := queueItem{id: 1, ocrLine: "Blrgh Nonsense", note: `couldn't identify "Blrgh Nonsense"`}
	mm, _ := m.startReview(it)
	got := mm.(model)
	if got.state != stateName || !got.statusErr {
		t.Fatalf("miss should review at the prompt with a banner: state=%v statusErr=%v",
			got.state, got.statusErr)
	}
	if got.nameInput.Value() != "Blrgh Nonsense" {
		t.Errorf("OCR text not pre-filled: %q", got.nameInput.Value())
	}
	if !got.reviewing() {
		t.Error("the cascade should know it is reviewing a queued card")
	}
	// esc there abandons the item, not the program.
	mm, cmd := got.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if isQuit(cmd) {
		t.Error("esc while reviewing must not quit the program")
	}
	if s := mm.(model).state; s != stateQueueReview {
		t.Errorf("esc from a tab review should return to the queue list, got %v", s)
	}
	if n := len(mm.(model).review); n != 1 {
		t.Errorf("the abandoned item should go back to the queue, have %d", n)
	}
}

func TestCameraPickerChoosesDeviceAndIsRemembered(t *testing.T) {
	devices := []scan.Device{cam("phone", "Chris's iPhone", "iPhone"), cam("spare", "Old iPhone", "iPhone")}
	sc := &fakeScanner{devices: devices}
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, sc, "", nil)

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

	// Closing the camera and pressing ctrl+o again re-offers the picker. There
	// is no separate "change camera" key: with a Continuity camera and a phone
	// app both on the list, choosing is what ctrl+o is for.
	// Close first, then return to the prompt: with a live session
	// resetForNext deliberately keeps you at the camera step, which is where
	// ctrl+o means "back to framing" rather than "choose a source".
	back := mm.(model)
	back.closeSession()
	mm, _ = back.cancelToName()
	mm, _ = mm.(model).handleKey(tea.KeyMsg{Type: tea.KeyCtrlO})
	if s := mm.(model).state; s != stateCameraBusy {
		t.Errorf("ctrl+o with no session should offer the picker, got %v", s)
	}
}

func TestNoCamerasKeepsSession(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
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
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
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
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
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

func TestZTogglesAutoFramingWhenTheHelperOffersIt(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	got, sess := openCapture(t, m)

	// Before the ready event advertises the feature, z refuses with a banner
	// rather than sending a command an old helper would error on.
	mm, _ := got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	got = mm.(model)
	if sess.framingOn+sess.framingOff != 0 {
		t.Fatal("z before the feature is advertised must not reach the helper")
	}
	if !got.statusErr {
		t.Error("z without the feature should explain itself in the status")
	}

	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true,
		ev: scan.Event{Kind: scan.EventReady, Features: []string{"framing", "hud"}}})
	got = mm.(model)
	if !got.framingCapable || got.framingOn {
		t.Fatalf("ready: framingCapable=%v framingOn=%v, want capable and off", got.framingCapable, got.framingOn)
	}

	// The session starts with framing forced off, so the first z asks for on;
	// the state only flips when the helper's framing event confirms it.
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	got = mm.(model)
	if sess.framingOn != 1 {
		t.Fatalf("session frame-on commands = %d, want 1", sess.framingOn)
	}
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true,
		ev: scan.Event{Kind: scan.EventFraming, State: "auto"}})
	got = mm.(model)
	if !got.framingOn {
		t.Error("the framing event should flip the mirrored state on")
	}
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("z")})
	got = mm.(model)
	if sess.framingOff != 1 {
		t.Errorf("session frame-off commands = %d, want 1", sess.framingOff)
	}
	if got.state != stateCapture {
		t.Errorf("toggling framing should stay on the capture step, got %v", got.state)
	}
}

func TestTTogglesTheTorchWhenTheHelperOffersIt(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	got, sess := openCapture(t, m)

	// Without the feature, t refuses with a banner rather than sending a
	// command the helper would error on.
	mm, _ := got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	got = mm.(model)
	if sess.torchOn+sess.torchOff != 0 {
		t.Fatal("t before the feature is advertised must not reach the helper")
	}
	if !got.statusErr {
		t.Error("t without the feature should explain itself in the status")
	}

	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true,
		ev: scan.Event{Kind: scan.EventReady, Features: []string{"torch", "hud"}}})
	got = mm.(model)
	if !got.torchCapable || got.torchOn {
		t.Fatalf("ready: torchCapable=%v torchOn=%v, want capable and dark", got.torchCapable, got.torchOn)
	}

	// The torch starts dark, so the first t asks for on; the mirror only
	// flips when the helper's torch event confirms the light actually took.
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	got = mm.(model)
	if sess.torchOn != 1 {
		t.Fatalf("session torch-on commands = %d, want 1", sess.torchOn)
	}
	if got.torchOn {
		t.Error("the mirror must wait for the torch event, not flip on the keypress")
	}
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true,
		ev: scan.Event{Kind: scan.EventTorch, State: "on"}})
	got = mm.(model)
	if !got.torchOn {
		t.Error("the torch event should flip the mirrored state on")
	}
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("t")})
	got = mm.(model)
	if sess.torchOff != 1 {
		t.Errorf("session torch-off commands = %d, want 1", sess.torchOff)
	}
	if got.state != stateCapture {
		t.Errorf("toggling the torch should stay on the capture step, got %v", got.state)
	}
}

func TestPaletteMovesTheSoundTierLines(t *testing.T) {
	// Deterministic thresholds regardless of the developer's environment.
	t.Setenv("HOARD_SCAN_WIN", "")
	t.Setenv("HOARD_SCAN_JACKPOT", "")
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	got, _ := openCapture(t, m)

	mm, _ := got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":")})
	got = mm.(model)
	if got.state != statePalette {
		t.Fatalf(": should open the command line, got state %v", got.state)
	}

	// Garbage stays on the line with the error shown, thresholds untouched.
	got.paletteInput.SetValue("win lots")
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm.(model)
	if got.state != statePalette || got.paletteErr == "" {
		t.Fatalf("a bad amount should stay on the line with an error, got state %v err %q", got.state, got.paletteErr)
	}
	if got.hudWin != 1 {
		t.Fatalf("hudWin = %v, want the untouched default 1", got.hudWin)
	}

	// A win above the jackpot line is refused — the tiers must stay ordered.
	got.paletteInput.SetValue("win 25")
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm.(model)
	if got.state != statePalette || got.hudWin != 1 {
		t.Fatalf("win above jackpot must be refused, got state %v hudWin %v", got.state, got.hudWin)
	}

	got.paletteInput.SetValue("win $5")
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm.(model)
	if got.state != stateCapture || got.hudWin != 5 {
		t.Fatalf("win $5: state %v hudWin %v, want capture and 5", got.state, got.hudWin)
	}

	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":")})
	got = mm.(model)
	got.paletteInput.SetValue("jackpot 50")
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm.(model)
	if got.state != stateCapture || got.hudJackpot != 50 {
		t.Fatalf("jackpot 50: state %v hudJackpot %v, want capture and 50", got.state, got.hudJackpot)
	}

	// esc abandons the line without touching anything.
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":")})
	got = mm.(model)
	got.paletteInput.SetValue("jackpot 9999")
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	got = mm.(model)
	if got.state != stateCapture || got.hudJackpot != 50 {
		t.Fatalf("esc: state %v hudJackpot %v, want capture and the unchanged 50", got.state, got.hudJackpot)
	}
}

func TestAddingACardReturnsToCaptureWithTheWindowOpen(t *testing.T) {
	// The whole point of the persistent window: after a card lands in the DB the
	// user is back at framing, not at the prompt having to press ctrl+o again.
	card := scryfall.Card{ID: "s", Name: "Sol Ring", Set: "c21", CollectorNumber: "263",
		Finishes: []string{"nonfoil"}}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fakeSearcher{}, ra.add, &fakeScanner{}, "", nil)
	got, sess := openCapture(t, m)

	got.chosen, got.finish = &card, "nonfoil"
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

func TestEscAtCaptureOpensLeaveGateAndCCloses(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	got, sess := openCapture(t, m)

	// esc keeps its session-wide meaning: the gated quit, not the close.
	mm, cmd := got.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	got = mm.(model)
	if got.state != stateLeaveConfirm || isQuit(cmd) {
		t.Fatalf("esc should open the leave gate without quitting, got %v", got.state)
	}
	if sess.closed {
		t.Error("the leave gate must not close the camera — declining is free")
	}

	// A stray key declines and lands back on the live camera.
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	got = mm.(model)
	if got.state != stateCapture || sess.closed {
		t.Fatalf("declining should return to capture with the camera open, got %v", got.state)
	}

	// c is the close key: back to the prompt, window closed, program alive.
	mm, cmd = got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	got = mm.(model)
	if got.state != stateName {
		t.Errorf("c should return to the prompt, got %v", got.state)
	}
	if !sess.closed || got.session != nil {
		t.Error("c should close the camera window")
	}
	if isQuit(cmd) {
		t.Error("c at capture should not quit the add session")
	}
}

func TestStaleSessionEventsAreDropped(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	got, _ := openCapture(t, m)
	stale := got.sessionGen

	// Close the camera, then deliver an event from the session that just died.
	mm, _ := got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	got = mm.(model)

	mm, _ = got.Update(sessionEventMsg{gen: stale, ok: true,
		ev: scan.Event{Kind: scan.EventScan, Name: "Sol Ring"}})
	if s := mm.(model).state; s != stateName {
		t.Errorf("a stale scan event should be dropped, got state %v", s)
	}
}

func TestUnreadableFrameStaysOnCapture(t *testing.T) {
	// A frame Vision couldn't read shouldn't drop the user out of the camera.
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
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
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
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
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlO})
	got := mm.(model)
	if got.state != stateName || !got.statusErr {
		t.Errorf("ctrl+o with no scanner should show a banner, got state=%v statusErr=%v", got.state, got.statusErr)
	}
}

func TestQtyValidation(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "x", nil)
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
	want := []string{"nonfoil", "foil", "etched"}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Errorf("finishOptions = %v, want %v (stable order)", got, want)
	}
	if fo := finishOptions(scryfall.Card{}); len(fo) != 0 {
		t.Errorf("no finishes → empty, got %v", fo)
	}
}

// destFixtures is a hoard with somewhere to choose: the default binder, a
// named binder, and a deck.
func destFixtures() []Destination {
	return []Destination{
		{ID: 1, Name: "Binder", Kind: "binder"},
		{ID: 2, Name: "Trade", Kind: "binder"},
		{ID: 7, Name: "Fish Deck", Kind: "deck"},
	}
}

// With more than one destination the cascade asks where the card goes after
// the finish step, hands the choice to the adder, and remembers it — the next
// card's picker opens on the last answer.
func TestDestinationPickerAsksHandsOffAndRemembers(t *testing.T) {
	card := scryfall.Card{ID: "x", Name: "Foily", Set: "sld", CollectorNumber: "1",
		Finishes: []string{"foil"}} // single finish: the next question is the destination
	ra := &recordingAdder{}
	m := newModel(context.Background(), fakeSearcher{}, ra.add, nil, "Foily", destFixtures())

	mm, _ := m.Update(printsMsg{name: "Foily", cards: []scryfall.Card{card}})
	got := mm.(model)
	if got.state != stateDestPick {
		t.Fatalf("state = %v, want stateDestPick after the finish auto-skip", got.state)
	}
	if len(got.list.Items()) != 3 {
		t.Fatalf("picker items = %d, want 3", len(got.list.Items()))
	}
	if got.list.Index() != 0 {
		t.Fatalf("first pick opens at %d, want the default binder at 0", got.list.Index())
	}

	// Choose the Trade binder and finish the add.
	got.list.Select(1)
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm.(model)
	if got.state != stateQty || got.dest.ID != 2 {
		t.Fatalf("state=%v dest=%+v, want stateQty with Trade chosen", got.state, got.dest)
	}
	mm, _ = got.submitQty()
	got = mm.(model)
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyEnter}) // confirm
	got = mm.(model)
	if len(ra.got) != 1 || ra.got[0].ContainerID != 2 {
		t.Fatalf("adder got %+v, want ContainerID 2", ra.got)
	}
	if !strings.Contains(got.status, "→ Trade") {
		t.Errorf("banner %q should name the destination", got.status)
	}

	// The next card's picker opens on the remembered pick.
	mm, _ = got.Update(printsMsg{name: "Foily", cards: []scryfall.Card{card}})
	got = mm.(model)
	if got.state != stateDestPick || got.list.Index() != 1 {
		t.Fatalf("second card: state=%v index=%d, want the picker preselecting Trade", got.state, got.list.Index())
	}
}

// One destination is no choice at all: the cascade never asks, the flow is the
// one it always was, and the adder still learns which container that was.
func TestSingleDestinationSkipsThePicker(t *testing.T) {
	card := scryfall.Card{ID: "x", Name: "Foily", Set: "sld", CollectorNumber: "1",
		Finishes: []string{"foil"}}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fakeSearcher{}, ra.add, nil, "Foily",
		[]Destination{{ID: 1, Name: "Binder", Kind: "binder"}})

	mm, _ := m.Update(printsMsg{name: "Foily", cards: []scryfall.Card{card}})
	got := mm.(model)
	if got.state != stateQty {
		t.Fatalf("state = %v, want stateQty — one destination must not ask", got.state)
	}
	mm, _ = got.submitQty()
	got = mm.(model)
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm.(model)
	if len(ra.got) != 1 || ra.got[0].ContainerID != 1 {
		t.Fatalf("adder got %+v, want ContainerID 1", ra.got)
	}
	if strings.Contains(got.status, "→") {
		t.Errorf("banner %q names a destination nobody chose", got.status)
	}
}

// confidentEvent is a scan whose card clears every auto-commit gate against
// solRingPrints: exact name, set+number pinning MH3 #123, high confidence.
func confidentEvent() scan.Event {
	return scan.Event{Kind: scan.EventScan, Name: "Sol Ring",
		Candidates: []string{"Sol Ring"}, SetCode: "MH3", CollectorNumber: "123",
		Confidence: 0.95, BandAnchored: true}
}

func confidentFixture() (scan.Event, fakeSearcher) {
	return confidentEvent(), fakeSearcher{
		fuzzy:  map[string]string{"Sol Ring": "Sol Ring"},
		prints: map[string][]scryfall.Card{"Sol Ring": solRingPrints()},
	}
}

// resolve runs one card's background resolution synchronously and lands it.
func resolve(t *testing.T, m model, c scan.Card) model {
	t.Helper()
	mm, _ := m.Update(m.resolveCardCmd(m.nextResolveID, c, 1)())
	return mm.(model)
}

func TestConfidentScanAutoCommitsWithoutKeys(t *testing.T) {
	ev, fs := confidentFixture()
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, sess := openCapture(t, m)

	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	got := mm.(model)
	if got.state != stateCapture || got.resolving != 1 {
		t.Fatalf("scan should resolve behind the camera: state=%v resolving=%d",
			got.state, got.resolving)
	}
	got = resolve(t, got, ev.CardList()[0])

	// No keys pressed, and the card is in the collection.
	if len(ra.got) != 1 {
		t.Fatalf("adder called %d times, want 1", len(ra.got))
	}
	r := ra.got[0]
	if r.Qty != 1 || r.Finish != "nonfoil" || !strings.EqualFold(r.Card.Set, "mh3") {
		t.Errorf("result = %+v, want 1× the MH3 printing, nonfoil", r)
	}
	if got.state != stateCapture {
		t.Errorf("state = %v, want the camera still interactive", got.state)
	}
	if len(got.tally) != 1 || !strings.Contains(got.View(), "Auto-added") {
		t.Errorf("the tally should show the commit: tally=%v", got.tally)
	}
	// The commit chimes, always — the shutter pop is quiet on nudge-armed
	// captures, so this is the one guaranteed audible receipt.
	if sess.chimes != 1 {
		t.Errorf("chimes = %d after an auto-add, want exactly 1", sess.chimes)
	}
	if got.summary.Count("auto") != 1 || len(got.review) != 0 || got.resolving != 0 {
		t.Errorf("summary auto=%d review=%d resolving=%d, want 1/0/0",
			got.summary.Count("auto"), len(got.review), got.resolving)
	}
}

func TestMultiPrintNoCollectorNeverAutoCommits(t *testing.T) {
	// The headline never-rule: a clean name match with several printings and no
	// collector verification queues — newest-first would guess the wrong set.
	_, fs := confidentFixture()
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	ev := scan.Event{Kind: scan.EventScan, Name: "Sol Ring",
		Candidates: []string{"Sol Ring"}, Confidence: 0.99}
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	got := resolve(t, mm.(model), ev.CardList()[0])

	if len(ra.got) != 0 {
		t.Fatalf("adder called %d times, want 0 — printing was never verified", len(ra.got))
	}
	if len(got.review) != 1 || !strings.Contains(got.review[0].note, "printing unverified") {
		t.Fatalf("review = %+v, want the card queued with a printing note", got.review)
	}
	if !strings.Contains(got.View(), "review") {
		t.Error("the capture view should show the queue count")
	}
}

func TestUncertainScanQueues(t *testing.T) {
	// A shaky fuzzy score queues when the printing evidence is short of a
	// full set+number verification — a bare number match is not enough to
	// carry a name this weak. (With set+number the same score commits: see
	// TestVerdict's carry rows.)
	_, fs := confidentFixture()
	fs.fuzzy["Sol Rmg"] = "Sol Ring"
	fs.match = map[string]cardname.Match{"Sol Rmg": {Similarity: 0.75}}
	ev := scan.Event{Kind: scan.EventScan, Name: "Sol Rmg", Candidates: []string{"Sol Rmg"},
		CollectorNumber: "263"} // number-only: unique among the printings, no set read
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, sess := openCapture(t, m)

	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	got := resolve(t, mm.(model), ev.CardList()[0])

	if len(ra.got) != 0 {
		t.Fatalf("adder called %d times, want 0", len(ra.got))
	}
	if len(got.review) != 1 || !strings.Contains(got.review[0].note, "uncertain name") {
		t.Fatalf("review = %+v, want an uncertain-name note", got.review)
	}
	// Queuing chimes too: either resolution means "place the next card",
	// and the sound is the prompt.
	if sess.chimes != 1 {
		t.Errorf("chimes = %d after a queue outcome, want 1", sess.chimes)
	}
}

func TestCopyrightNumberNeverVetoesAutoCommit(t *testing.T) {
	// The old-frame copyright line misreads digits (observed live: Aven
	// Envoy's "30/145" read as "80/145"). A copyright-sourced number is
	// upgrade-only evidence: on a single-printing card the mismatch must not
	// demote the auto-commit — and the printed finish hint must survive the
	// sentinel candidate winning the rank.
	fs := fakeSearcher{
		fuzzy: map[string]string{"Aven Envoy": "Aven Envoy"},
		prints: map[string][]scryfall.Card{"Aven Envoy": {
			{ID: "lgn", Name: "Aven Envoy", Set: "lgn", CollectorNumber: "30",
				Finishes: []string{"nonfoil", "foil"}}}},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	ev := scan.Event{Kind: scan.EventScan, Name: "Aven Envoy",
		Cards: []scan.Card{{Name: "Aven Envoy", Candidates: []string{"Aven Envoy"},
			CollectorNumber: "80", NumberSource: "copyright", Confidence: 0.95,
			FinishHint: "foil", Source: "crop"}}}
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	got := resolve(t, mm.(model), ev.CardList()[0])

	if len(ra.got) != 1 {
		t.Fatalf("adder called %d times, want 1 — a copyright misread must not veto", len(ra.got))
	}
	if r := ra.got[0]; !strings.EqualFold(r.Card.Set, "lgn") || r.Finish != "foil" {
		t.Errorf("result = %+v, want the lone printing with the finish hint kept", r)
	}
	if len(got.review) != 0 {
		t.Errorf("review = %+v, want empty", got.review)
	}
}

func TestBandNumberMismatchStillVetoes(t *testing.T) {
	// A band number that matches nothing keeps its veto when the name was
	// only a fuzzy match — that is what the veto was always for: the band is
	// reliable, so the mismatch means the name may have landed on the wrong
	// card. ("Aven Envo" is the glare-truncated read, not an exact hit.)
	fs := fakeSearcher{
		fuzzy: map[string]string{"Aven Envo": "Aven Envoy"},
		prints: map[string][]scryfall.Card{"Aven Envoy": {
			{ID: "lgn", Name: "Aven Envoy", Set: "lgn", CollectorNumber: "30",
				Finishes: []string{"nonfoil"}}}},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	ev := scan.Event{Kind: scan.EventScan, Name: "Aven Envo",
		Cards: []scan.Card{{Name: "Aven Envo", Candidates: []string{"Aven Envo"},
			CollectorNumber: "80", Confidence: 0.95, Source: "crop"}}}
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	got := resolve(t, mm.(model), ev.CardList()[0])

	if len(ra.got) != 0 {
		t.Fatalf("adder called %d times, want 0 — a fuzzy name plus a bad number queues", len(ra.got))
	}
	if len(got.review) != 1 || !strings.Contains(got.review[0].note, "printing unverified") {
		t.Fatalf("review = %+v, want the card queued unverified", got.review)
	}
}

// An exact name on a card with one printing outlives a misread number. The
// digits are what this glyph size gets wrong — Aven Envoy's "30" arrived as
// "80", live — and refusing the card left it worse off than if the band had
// been unreadable, which is the outcome an exact name already commits on.
func TestExactNameSinglePrintSurvivesBadNumber(t *testing.T) {
	fs := fakeSearcher{
		fuzzy: map[string]string{"Aven Envoy": "Aven Envoy"},
		prints: map[string][]scryfall.Card{"Aven Envoy": {
			{ID: "lgn", Name: "Aven Envoy", Set: "lgn", CollectorNumber: "30",
				Finishes: []string{"nonfoil"}}}},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	ev := scan.Event{Kind: scan.EventScan, Name: "Aven Envoy",
		Cards: []scan.Card{{Name: "Aven Envoy", Candidates: []string{"Aven Envoy"},
			CollectorNumber: "80", Confidence: 0.95, Source: "crop"}}}
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	got := resolve(t, mm.(model), ev.CardList()[0])

	if len(ra.got) != 1 {
		t.Fatalf("adder called %d times, want 1 — an exact name with one printing commits", len(ra.got))
	}
	if ra.got[0].Card.ID != "lgn" {
		t.Errorf("committed %q, want the card's only printing", ra.got[0].Card.ID)
	}
	if len(got.review) != 0 {
		t.Errorf("review = %+v, want nothing queued", got.review)
	}
}

// The rescue is a floor, not a licence: with several printings there is still
// nothing to pick between them, so a bad number queues exactly as before.
func TestExactNameManyPrintsStillQueuesOnBadNumber(t *testing.T) {
	fs := fakeSearcher{
		fuzzy: map[string]string{"Brain Freeze": "Brain Freeze"},
		prints: map[string][]scryfall.Card{"Brain Freeze": {
			{ID: "scg", Name: "Brain Freeze", Set: "scg", CollectorNumber: "29",
				Finishes: []string{"nonfoil"}},
			{ID: "vma", Name: "Brain Freeze", Set: "vma", CollectorNumber: "60",
				Finishes: []string{"nonfoil"}}}},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	ev := scan.Event{Kind: scan.EventScan, Name: "Brain Freeze",
		Cards: []scan.Card{{Name: "Brain Freeze", Candidates: []string{"Brain Freeze"},
			CollectorNumber: "999", Confidence: 0.95, Source: "crop"}}}
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	got := resolve(t, mm.(model), ev.CardList()[0])

	if len(ra.got) != 0 {
		t.Fatalf("adder called %d times, want 0 — two printings and no usable number", len(ra.got))
	}
	if len(got.review) != 1 || !strings.Contains(got.review[0].note, "printing unverified") {
		t.Fatalf("review = %+v, want the card queued unverified", got.review)
	}
}

func TestCopyrightYearBreaksTieToAutoCommit(t *testing.T) {
	// Remove Soul, observed live: the band read "95/350", which is both 7th
	// and 8th Edition — ambiguous, queued. The copyright range on the same
	// line ends in the release year; carried as evidence it pins 8th Edition
	// and the scan commits.
	fs := fakeSearcher{
		fuzzy: map[string]string{"Remove Soul": "Remove Soul"},
		prints: map[string][]scryfall.Card{"Remove Soul": {
			{ID: "7ed", Name: "Remove Soul", Set: "7ed", CollectorNumber: "95",
				ReleasedAt: "2001-04-11", Finishes: []string{"nonfoil"}},
			{ID: "8ed", Name: "Remove Soul", Set: "8ed", CollectorNumber: "95",
				ReleasedAt: "2003-07-28", Finishes: []string{"nonfoil"}}}},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	ev := scan.Event{Kind: scan.EventScan, Name: "Remove Soul",
		Cards: []scan.Card{{Name: "Remove Soul", Candidates: []string{"Remove Soul"},
			CollectorNumber: "95", CopyrightYear: 2003, Confidence: 0.95, Source: "crop"}}}
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	got := resolve(t, mm.(model), ev.CardList()[0])

	if len(ra.got) != 1 {
		t.Fatalf("adder called %d times, want 1 — the year breaks the tie", len(ra.got))
	}
	if r := ra.got[0]; !strings.EqualFold(r.Card.Set, "8ed") {
		t.Errorf("committed set = %q, want the printing released that year", r.Card.Set)
	}
	if len(got.review) != 0 {
		t.Errorf("review = %+v, want empty", got.review)
	}
}

func TestVerdict(t *testing.T) {
	verified := solRingPrints()[2:] // the MH3 printing first
	multi := solRingPrints()
	foilOnly := []scryfall.Card{{ID: "f", Name: "Foily", Set: "sld",
		CollectorNumber: "1", Finishes: []string{"foil"}}}
	exact := cardname.Match{Exact: true, Similarity: 1}

	multiFinish := []scryfall.Card{{ID: "mf", Name: "Sol Ring", Set: "mh3",
		CollectorNumber: "123", Finishes: []string{"nonfoil", "foil"}}}

	cases := []struct {
		name     string
		it       queueItem
		wantAuto bool
		finish   string
		note     string
	}{
		{"exact set+number commits nonfoil",
			queueItem{canonical: "Sol Ring", match: exact, prints: verified,
				rank: scanMatchSetAndNumber, raw: scan.Card{Confidence: 0.95}},
			true, "nonfoil", ""},
		{"printed star commits as foil",
			queueItem{canonical: "Sol Ring", match: exact, prints: multiFinish,
				rank: scanMatchSetAndNumber, finishHint: "foil"},
			true, "foil", ""},
		{"printed bullet commits as nonfoil",
			queueItem{canonical: "Sol Ring", match: exact, prints: multiFinish,
				rank: scanMatchSetAndNumber, finishHint: "nonfoil"},
			true, "nonfoil", ""},
		{"foil marker on a nonfoil-only printing is ignored",
			queueItem{canonical: "Sol Ring", match: exact, prints: verified,
				rank: scanMatchSetAndNumber, finishHint: "foil"},
			true, "nonfoil", ""},
		{"single foil-only printing commits as foil",
			queueItem{canonical: "Foily", match: exact, prints: foilOnly,
				rank: scanMatchSinglePrint},
			true, "foil", ""},
		{"unknown confidence decides by name and printing alone",
			queueItem{canonical: "Sol Ring", match: exact, prints: verified,
				rank: scanMatchNumberOnly},
			true, "nonfoil", ""},
		{"lookup error queues",
			queueItem{errText: "api down"}, false, "", "lookup failed"},
		{"no name queues",
			queueItem{ocrLine: "Blrgh"}, false, "", "couldn't identify"},
		// A fallback line is a weak place to find a name, so it queues on its
		// own — but a number that matches a printing of the card that line
		// resolved to could not have agreed with a wrong name (live: a Forest
		// with an exact name and a full MSH/286 match sat in review).
		{"fallback-line match queues without printing evidence",
			queueItem{canonical: "Sol Ring", lineIdx: 2, match: exact,
				prints: verified[:1], rank: scanMatchSinglePrint},
			false, "", "fallback"},
		{"fallback-line match commits when the number verifies",
			queueItem{canonical: "Sol Ring", lineIdx: 2, match: exact,
				prints: verified, rank: scanMatchSetAndNumber},
			true, "nonfoil", ""},
		{"set+number verification carries a shaky name",
			// Glare truncated the title (observed live: "Danther Wakandan
			// King"), but the collector block pinned the printing — and a
			// wrong-card resolution could not have matched these printings.
			queueItem{canonical: "Sol Ring", match: cardname.Match{Similarity: 0.79},
				prints: verified, rank: scanMatchSetAndNumber},
			true, "nonfoil", ""},
		{"set+number verification carries low OCR confidence",
			// A trailing glare glyph drags the line confidence to 0.5 while
			// the normalized name matches exactly (observed live every run on
			// one foil).
			queueItem{canonical: "Sol Ring", match: exact, prints: verified,
				rank: scanMatchSetAndNumber, raw: scan.Card{Confidence: 0.5}},
			true, "nonfoil", ""},
		{"shaky similarity without strong printing queues",
			queueItem{canonical: "Sol Ring", match: cardname.Match{Similarity: 0.8},
				prints: verified[:1], rank: scanMatchNumberOnly},
			false, "", "uncertain name"},
		// Vision's confidence describes the glyphs; a matched number answers it,
		// because the digits and the name agree on a real printing. With no
		// number to check, the floor still stands.
		{"low OCR confidence on a fuzzy name without a number queues",
			queueItem{canonical: "Sol Ring", match: cardname.Match{Similarity: 0.95},
				prints: verified[:1], rank: scanMatchSinglePrint,
				raw: scan.Card{Confidence: 0.5}},
			false, "", "low OCR confidence"},
		{"low OCR confidence commits when the number verifies",
			queueItem{canonical: "Sol Ring", match: cardname.Match{Similarity: 0.95},
				prints: verified[:1], rank: scanMatchNumberOnly,
				raw: scan.Card{Confidence: 0.5}},
			true, "nonfoil", ""},
		{"exact name with low confidence and weak printing still commits",
			// Exact normalized equality IS the confidence check: the text
			// matched a real card name letter for letter.
			queueItem{canonical: "Sol Ring", match: exact, prints: verified[:1],
				rank: scanMatchNumberOnly, raw: scan.Card{Confidence: 0.5}},
			true, "nonfoil", ""},
		// A number matching several printings now commits the one the ranking
		// put in front, rather than queuing. Deliberate: a wrong printing is
		// one row to correct, a queued card is a stop in the session.
		{"ambiguous number commits the front printing",
			queueItem{canonical: "Sol Ring", match: exact, prints: multi,
				rank: scanMatchNumberAmbiguous},
			true, "nonfoil", ""},
		// Unless the card said otherwise. Absence of a year is why the pick is
		// uncertain; a year that disagrees is evidence against it.
		{"ambiguous number queues when the year contradicts the front printing",
			queueItem{canonical: "Sol Ring", match: exact, prints: multi,
				rank: scanMatchNumberAmbiguous,
				raw:  scan.Card{CopyrightYear: 1999}},
			false, "", "not from 1999"},
		{"multi-print no collector queues",
			queueItem{canonical: "Sol Ring", match: exact, prints: multi,
				rank: scanMatchNone},
			false, "", "printing unverified"},
		{"no printings queues",
			queueItem{canonical: "Sol Ring", match: exact}, false, "", "no printings"},
	}
	for _, c := range cases {
		auto, finish, note := verdict(c.it)
		if auto != c.wantAuto {
			t.Errorf("%s: auto = %v, want %v", c.name, auto, c.wantAuto)
			continue
		}
		if auto && finish != c.finish {
			t.Errorf("%s: finish = %q, want %q", c.name, finish, c.finish)
		}
		if !auto && !strings.Contains(note, c.note) {
			t.Errorf("%s: note = %q, want it to mention %q", c.name, note, c.note)
		}
	}
}

func TestRankByScanStrength(t *testing.T) {
	cards := solRingPrints()
	if _, s := rankByScanStrength(cards, "MH3", "123", 0, "", ""); s != scanMatchSetAndNumber {
		t.Errorf("set+number strength = %v, want scanMatchSetAndNumber", s)
	}
	if _, s := rankByScanStrength(cards, "", "263", 0, "", ""); s != scanMatchNumberOnly {
		t.Errorf("unique number strength = %v, want scanMatchNumberOnly", s)
	}
	dupes := []scryfall.Card{
		{ID: "a", Name: "X", Set: "aaa", CollectorNumber: "7"},
		{ID: "b", Name: "X", Set: "bbb", CollectorNumber: "7"},
	}
	if _, s := rankByScanStrength(dupes, "", "7", 0, "", ""); s != scanMatchNumberAmbiguous {
		t.Errorf("shared number strength = %v, want scanMatchNumberAmbiguous", s)
	}
	if _, s := rankByScanStrength(cards[:1], "", "", 0, "", ""); s != scanMatchSinglePrint {
		t.Errorf("single printing strength = %v, want scanMatchSinglePrint", s)
	}
	// A number that matches nothing makes even a lone printing suspect: the
	// name match may have landed on the wrong card entirely.
	if _, s := rankByScanStrength(cards[:1], "", "999", 0, "", ""); s != scanMatchNone {
		t.Errorf("conflicting number strength = %v, want scanMatchNone", s)
	}
	if _, s := rankByScanStrength(cards, "", "", 0, "", ""); s != scanMatchNone {
		t.Errorf("no signal strength = %v, want scanMatchNone", s)
	}
}

// Control Magic is the case border exists for: 4ED (white, 1995) and 4BB
// (black, 1995) share their year, their art and their artist, so the copyright
// year — the only other evidence a pre-1998 card carries — settles nothing.
func controlMagicPrints() []scryfall.Card {
	return []scryfall.Card{
		{ID: "a", Name: "Control Magic", Set: "4bb", CollectorNumber: "48",
			ReleasedAt: "1995-04-01", BorderColor: "black"},
		{ID: "b", Name: "Control Magic", Set: "4ed", CollectorNumber: "48",
			ReleasedAt: "1995-04-01", BorderColor: "white"},
		{ID: "c", Name: "Control Magic", Set: "3ed", CollectorNumber: "48",
			ReleasedAt: "1994-04-11", BorderColor: "white"},
	}
}

func TestApplyBorderEvidence(t *testing.T) {
	prints := controlMagicPrints()

	// Border alone cannot pick 4ED: 3ED is white too, and newer-first puts it
	// first. Only the pairing with 1995 names one printing.
	got, changed := applyBorderEvidence(prints, "white", 0)
	if !changed {
		t.Fatal("a white read over a mixed list should reorder")
	}
	if got[0].Set != "4ed" || got[1].Set != "3ed" || got[2].Set != "4bb" {
		t.Errorf("order = %s/%s/%s, want 4ed/3ed/4bb — matching borders first, "+
			"each group otherwise undisturbed", got[0].Set, got[1].Set, got[2].Set)
	}

	// With the year the pairing leads, which is the row a rank trusting both
	// fields would commit — and 4BB, sharing the year but not the border, must
	// not be it.
	got, changed = applyBorderEvidence(prints, "white", 1995)
	if !changed {
		t.Fatal("border plus year should reorder")
	}
	if got[0].Set != "4ed" {
		t.Errorf("top = %s, want 4ed: 1995 is shared by 4ED and 4BB and white "+
			"by 4ED and 3ED, so only the pairing names one", got[0].Set)
	}
	if len(got) != len(prints) {
		t.Errorf("kept %d printings, want %d — border orders, it never removes, "+
			"or the picker could not reach the row it demoted", len(got), len(prints))
	}

	// A border matching nothing is ignored outright rather than treated as a
	// contradiction. This is what keeps a wrong read on a card the reader
	// should have abstained on — gold, silver, foreign-language — free.
	if _, changed := applyBorderEvidence(prints, "gold", 0); changed {
		t.Error("a border no printing has must change nothing")
	}
	// And a border every printing shares separates nothing, so it is not a
	// reorder either — the distinction the resolve line reports as "unused".
	allWhite := []scryfall.Card{
		{ID: "a", Set: "3ed", BorderColor: "white"},
		{ID: "b", Set: "4ed", BorderColor: "white"},
	}
	if _, changed := applyBorderEvidence(allWhite, "white", 0); changed {
		t.Error("a border every printing shares must change nothing")
	}
	if _, changed := applyBorderEvidence(prints, "", 0); changed {
		t.Error("no border read must change nothing")
	}
	// Gold and silver read as white often enough to matter, so such a printing
	// is never ruled out — but it is not promoted over a genuine match either,
	// and it must still outrank the printings this read really does exclude.
	withGold := append(controlMagicPrints(), scryfall.Card{
		ID: "d", Name: "Control Magic", Set: "ptc", CollectorNumber: "jn12",
		ReleasedAt: "1996-05-01", BorderColor: "gold"})
	got, changed = applyBorderEvidence(withGold, "white", 1995)
	if !changed {
		t.Fatal("a gold sibling must not suppress the border wholesale: 22% of " +
			"pre-1998 multi-printing cards have one, Control Magic included")
	}
	if got[0].Set != "4ed" {
		t.Errorf("top = %s, want 4ed", got[0].Set)
	}
	posOf := func(set string) int {
		for i, c := range got {
			if c.Set == set {
				return i
			}
		}
		return -1
	}
	if posOf("ptc") > posOf("4bb") {
		t.Errorf("gold ptc at %d sits below black 4bb at %d — a white read "+
			"cannot rule out a colour the reader never recognises, so it must "+
			"not sink below one it genuinely excludes", posOf("ptc"), posOf("4bb"))
	}
	if _, changed := applyBorderEvidence(prints[:1], "white", 0); changed {
		t.Error("a lone printing has no ordering to change")
	}
}

// Reordering never moves the rank. The *ranking* may now use a border, but
// only as its own input and only after the year has narrowed the field — a
// list that has merely been re-sorted must rank exactly as it did before.
//
// The original form of this test asserted the rank was border-blind outright,
// on the reasoning that one bit which always matches something cannot justify
// an unattended write. That reasoning was right about a border used *alone* and
// is preserved as such: with no year, or with a year that narrows nothing, the
// border still settles nothing. What changed is that a border asked to choose
// between two printings the year has already isolated is not one bit against
// the whole catalog — it is the single remaining difference between them.
func TestApplyBorderEvidenceDoesNotChangeRank(t *testing.T) {
	prints := controlMagicPrints()
	_, before := rankByScanStrength(prints, "", "", 1995, "", "")
	reordered, _ := applyBorderEvidence(prints, "white", 1995)
	_, after := rankByScanStrength(reordered, "", "", 1995, "", "")
	if before != after {
		t.Errorf("rank moved from %v to %v after a border reorder", before, after)
	}
	// 1995 alone cannot separate 4ED from 4BB, and must not pretend to.
	if before != scanMatchNone {
		t.Errorf("year-only rank = %v, want scanMatchNone: 1995 is shared by "+
			"two printings, so the year settles nothing", before)
	}
}

func TestRankByScanStrengthCollapsesVariants(t *testing.T) {
	// A card whose only "reprint" is its own theme-deck alternate — one set,
	// one base number, rows differing only by the variation marker — is a
	// single logical printing, and the unmarked row must lead so it is the one
	// verdict prices and the commit writes. Observed live: Cephalid Looter
	// (ody 72 beside ody 72†) queued "printing unverified: 2 printings" on a
	// perfect read.
	variants := []scryfall.Card{
		{ID: "alt", Name: "X", Set: "ody", CollectorNumber: "72†"},
		{ID: "plain", Name: "X", Set: "ody", CollectorNumber: "72"},
	}
	ranked, s := rankByScanStrength(variants, "", "", 0, "", "")
	if s != scanMatchSinglePrint {
		t.Errorf("variant pair strength = %v, want scanMatchSinglePrint", s)
	}
	if ranked[0].ID != "plain" {
		t.Errorf("ranked[0] = %s, want the unmarked row first", ranked[0].ID)
	}
	// ★ rows collapse the same way (TrimRight's cutset is runes, not bytes).
	stars := []scryfall.Card{
		{ID: "plain", Name: "X", Set: "8ed", CollectorNumber: "95"},
		{ID: "star", Name: "X", Set: "8ed", CollectorNumber: "95★"},
	}
	if _, s := rankByScanStrength(stars, "", "", 0, "", ""); s != scanMatchSinglePrint {
		t.Errorf("star pair strength = %v, want scanMatchSinglePrint", s)
	}
	// Variants across different sets are genuinely different printings.
	spread := []scryfall.Card{
		{ID: "a", Name: "X", Set: "7ed", CollectorNumber: "95"},
		{ID: "b", Name: "X", Set: "8ed", CollectorNumber: "95★"},
	}
	if _, s := rankByScanStrength(spread, "", "", 0, "", ""); s != scanMatchNone {
		t.Errorf("cross-set variants strength = %v, want scanMatchNone", s)
	}
}

func TestRankByScanStrengthYearBreaksNumberTie(t *testing.T) {
	// "95" is Remove Soul in both 7th and 8th Edition; the copyright range's
	// end year ("1993-2003") equals the release year and picks one (observed
	// live — the read queued as ambiguous without it).
	prints := []scryfall.Card{
		{ID: "7ed", Name: "Remove Soul", Set: "7ed", CollectorNumber: "95", ReleasedAt: "2001-04-11"},
		{ID: "8ed", Name: "Remove Soul", Set: "8ed", CollectorNumber: "95", ReleasedAt: "2003-07-28"},
	}
	// The year did not merely break the tie, it agreed with the winner — two
	// signals, so the name gate is waived downstream.
	ranked, s := rankByScanStrength(prints, "", "95", 2003, "", "")
	if s != scanMatchNumberAndYear {
		t.Errorf("year-pinned strength = %v, want scanMatchNumberAndYear", s)
	}
	if ranked[0].ID != "8ed" {
		t.Errorf("ranked[0] = %s, want the printing released that year", ranked[0].ID)
	}
	// A misread year matches no printing and must leave the tie as it found
	// it — ambiguous queues, never a guessed commit.
	if _, s := rankByScanStrength(prints, "", "95", 2013, "", ""); s != scanMatchNumberAmbiguous {
		t.Errorf("misread year strength = %v, want scanMatchNumberAmbiguous", s)
	}
	// A year shared by both matches decides nothing either.
	same := []scryfall.Card{
		{ID: "a", Name: "X", Set: "aaa", CollectorNumber: "7", ReleasedAt: "2003-01-01"},
		{ID: "b", Name: "X", Set: "bbb", CollectorNumber: "7", ReleasedAt: "2003-06-01"},
	}
	if _, s := rankByScanStrength(same, "", "7", 2003, "", ""); s != scanMatchNumberAmbiguous {
		t.Errorf("shared year strength = %v, want scanMatchNumberAmbiguous", s)
	}
	// The year never overrides a full set+number verification.
	if _, s := rankByScanStrength(prints, "7ed", "95", 2003, "", ""); s != scanMatchSetAndNumber {
		t.Errorf("set+number with year = %v, want scanMatchSetAndNumber", s)
	}
}

// A second copy of a card commits rather than stopping the session.
//
// This used to queue. It cost three stops on a playset of four, and it was not
// even consistent — a copy scanned past the window committed anyway, so the
// same physical action gave different answers depending on how fast you were.
func TestRefireCommitsAsASecondCopy(t *testing.T) {
	ev, fs := confidentFixture()
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	clock := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return clock }
	m, _ = openCapture(t, m)

	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	got := resolve(t, mm.(model), ev.CardList()[0])
	if len(ra.got) != 1 {
		t.Fatalf("first scan should commit, adder got %d", len(ra.got))
	}

	// The same card again seconds later: a second copy, committed.
	clock = clock.Add(2 * time.Second)
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true, ev: ev})
	got = resolve(t, mm.(model), ev.CardList()[0])
	if len(ra.got) != 2 {
		t.Fatalf("a second copy should commit, adder got %d", len(ra.got))
	}
	if len(got.review) != 0 {
		t.Fatalf("review = %+v, want nothing queued", got.review)
	}

	// And past the window, which used to be the only way it committed. Same
	// answer either side of the boundary now, which is the point.
	clock = clock.Add(dupWindow + time.Second)
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true, ev: ev})
	got = resolve(t, mm.(model), ev.CardList()[0])
	if len(ra.got) != 3 {
		t.Errorf("timing must not change the answer, adder got %d", len(ra.got))
	}
}

func TestResolveResultsLandOutOfOrderAndAfterTabbingAway(t *testing.T) {
	// Two captures in flight; the user tabs into the review list before either
	// resolves. Both land regardless of UI state, out of order.
	_, fs := confidentFixture()
	m := newModel(context.Background(), fs, noopAdder, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	unknown := scan.Event{Kind: scan.EventScan, Name: "Blrgh", Candidates: []string{"Blrgh"}}
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: unknown})
	got := mm.(model)
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true, ev: unknown})
	got = mm.(model)
	if got.resolving != 2 {
		t.Fatalf("resolving = %d, want 2 in flight", got.resolving)
	}

	// Seed one queued item so tab has something to show, then tab away.
	got.review = append(got.review, queueItem{id: 99, ocrLine: "seed", note: "seeded"})
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	got = mm.(model)
	if got.state != stateQueueReview {
		t.Fatalf("tab should open the review list, got %v", got.state)
	}

	// The second capture's resolution lands first, then the first's.
	second := got.resolveCardCmd(2, unknown.CardList()[0], 1)().(resolveDoneMsg)
	first := got.resolveCardCmd(1, unknown.CardList()[0], 1)().(resolveDoneMsg)
	mm, _ = got.Update(second)
	mm, _ = mm.(model).Update(first)
	got = mm.(model)
	if len(got.review) != 3 || got.resolving != 0 {
		t.Errorf("review = %d resolving = %d, want 3/0 — arrivals land in any order, any state",
			len(got.review), got.resolving)
	}
	if got.state != stateQueueReview {
		t.Errorf("landing resolutions must not yank the user out of the list, got %v", got.state)
	}
	if len(got.list.Items()) != 3 {
		t.Errorf("open review list should refresh in place: %d items", len(got.list.Items()))
	}
}

func TestFoilMarkerCommitsAsFoil(t *testing.T) {
	// The printed star beside the language code is the foil marker; a starred
	// card whose printing offers both finishes commits as foil with no keys.
	prints := []scryfall.Card{{ID: "f1", Name: "Sol Ring", Set: "mh3",
		CollectorNumber: "123", Finishes: []string{"nonfoil", "foil"}}}
	fs := fakeSearcher{
		fuzzy:  map[string]string{"Sol Ring": "Sol Ring"},
		prints: map[string][]scryfall.Card{"Sol Ring": prints},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	card := scan.Card{Name: "Sol Ring", Candidates: []string{"Sol Ring"},
		SetCode: "MH3", CollectorNumber: "123", FinishHint: "foil", Confidence: 0.95}
	ev := scan.Event{Kind: scan.EventScan, Name: "Sol Ring", Cards: []scan.Card{card}}
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	got := resolve(t, mm.(model), card)

	if len(ra.got) != 1 || ra.got[0].Finish != "foil" {
		t.Fatalf("adder got %+v, want the starred card committed as foil", ra.got)
	}
	if len(got.review) != 0 {
		t.Errorf("review = %d, want none", len(got.review))
	}
}

func TestAltCollectorCandidateVerifiesStackScan(t *testing.T) {
	// A card scanned off the top of a stack: the primary collector read is the
	// neighbour card's sliver (which matches no printing of the resolved
	// name), and the true block rides in the alternates. The alternate that
	// verifies wins, and the card auto-commits. Observed live: Green Goblin
	// MSC #657 with a stacked card's MSH #286 parsed first.
	prints := solRingPrints() // has MH3 #123
	fs := fakeSearcher{
		fuzzy:  map[string]string{"Sol Ring": "Sol Ring"},
		prints: map[string][]scryfall.Card{"Sol Ring": prints},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	card := scan.Card{Name: "Sol Ring", Candidates: []string{"Sol Ring"},
		SetCode: "MSH", CollectorNumber: "286", // the neighbour's border
		CollectorAlts: []scan.CollectorAlt{{Number: "123", Set: "MH3"}},
		Confidence:    0.95}
	ev := scan.Event{Kind: scan.EventScan, Name: "Sol Ring", Cards: []scan.Card{card}}
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	got := resolve(t, mm.(model), card)

	if len(ra.got) != 1 || !strings.EqualFold(ra.got[0].Card.Set, "mh3") {
		t.Fatalf("adder got %+v, want the alternate-verified MH3 printing committed", ra.got)
	}
	if len(got.review) != 0 {
		t.Errorf("review = %d, want none — the alternate verified", len(got.review))
	}
}

func TestNudgeEchoIsSilentlyDropped(t *testing.T) {
	// After a commit, the model nudges the parked trigger; the nudge re-reads
	// the same sitting card. That echo must neither re-commit nor dup-queue —
	// but the same read arriving WITHOUT a nudge (a real disruption fired it)
	// still dup-queues, which is what keeps stacked playset copies alive.
	ev, fs := confidentFixture()
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	got := resolve(t, mm.(model), ev.CardList()[0])
	if len(ra.got) != 1 {
		t.Fatalf("setup: first scan should commit, got %d", len(ra.got))
	}

	// The nudged re-read of the same card: dropped. The tag is a time window
	// rather than a consumed flag, so it survives a real scan racing the
	// nudge onto the wire.
	got.nudgeSentAt = got.now()
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true, ev: ev})
	got = mm.(model)
	if !got.lastScanNudged {
		t.Fatal("a scan inside the nudge window should be tagged as nudged")
	}
	got = resolve(t, got, ev.CardList()[0])
	if len(ra.got) != 1 || len(got.review) != 0 {
		t.Fatalf("echo leaked: adds=%d review=%d, want 1/0", len(ra.got), len(got.review))
	}
	if got.nudgeDrops != 1 {
		t.Errorf("nudgeDrops = %d, want 1", got.nudgeDrops)
	}

	// The same card again, fired by real disruption rather than the nudge:
	// a placement the phone watched happen, so a second copy commits.
	got.nudgeSentAt = time.Time{}
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true, ev: ev})
	got = resolve(t, mm.(model), ev.CardList()[0])
	if len(ra.got) != 2 {
		t.Errorf("a disruption-fired repeat is a second copy, adds=%d", len(ra.got))
	}
	if len(got.review) != 0 {
		t.Errorf("nothing should queue, review=%+v", got.review)
	}
}

func TestMultiCardPhantomsDieQuietly(t *testing.T) {
	// A licensed frame's ability names and brand lines read title-like and
	// become entries; when the capture yielded several cards, the ones that
	// resolve to nothing are dropped with a note, not queued. Observed live:
	// "Survey the Realm" and "C MARVEL" beside a real Black Panther.
	_, fs := confidentFixture()
	m := newModel(context.Background(), fs, noopAdder, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	ev := scan.Event{Kind: scan.EventScan, Name: "Sol Ring", Cards: []scan.Card{
		{Name: "Sol Ring", Candidates: []string{"Sol Ring"}, SetCode: "MH3", CollectorNumber: "123"},
		{Name: "Survey the Realm", Candidates: []string{"Survey the Realm"}},
	}}
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	got := mm.(model)

	mm, _ = got.Update(got.resolveCardCmd(2, ev.Cards[1], 2)())
	got = mm.(model)
	if len(got.review) != 0 {
		t.Fatalf("a multi-card phantom joined the queue: %+v", got.review)
	}
	if !strings.Contains(got.status, "Survey the Realm") || got.statusErr {
		t.Errorf("status = %q, want a quiet ignored note", got.status)
	}

	// The same unresolvable read from a single-card capture still queues —
	// the only card of a shot must never vanish silently.
	mm, _ = got.Update(got.resolveCardCmd(3, ev.Cards[1], 1)())
	got = mm.(model)
	if len(got.review) != 1 {
		t.Fatalf("a single-card miss should queue, review = %d", len(got.review))
	}
}

func TestTabTogglesQueueReview(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	// Tab with an empty queue is a no-op.
	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	if s := mm.(model).state; s != stateCapture {
		t.Fatalf("tab with nothing queued should stay at capture, got %v", s)
	}

	m.review = []queueItem{{id: 1, canonical: "Sol Ring", note: "printing unverified"}}
	mm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	got := mm.(model)
	if got.state != stateQueueReview || len(got.list.Items()) != 1 {
		t.Fatalf("tab should list the queue: state=%v items=%d", got.state, len(got.list.Items()))
	}
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	got = mm.(model)
	if got.state != stateCapture || len(got.review) != 1 {
		t.Errorf("tab back keeps the queue: state=%v review=%d", got.state, len(got.review))
	}
	if got.session == nil {
		t.Error("visiting the queue must not close the camera")
	}
}

func TestReviewItemReentersCascadeFromPrints(t *testing.T) {
	// enter on a queued card re-enters the cascade at the printing picker with
	// the ← scanned marker; confirming removes it from the queue and returns to
	// the list-or-camera.
	_, fs := confidentFixture()
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	ranked, rank := rankByScanStrength(solRingPrints(), "MH3", "123", 0, "", "")
	m.review = []queueItem{{id: 1, canonical: "Sol Ring", ocrLine: "Sol Ring",
		raw:    scan.Card{SetCode: "MH3", CollectorNumber: "123", Confidence: 0.5},
		match:  cardname.Match{Exact: true, Similarity: 1},
		prints: ranked, rank: rank, note: "low OCR confidence (50%)"}}
	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	got := mm.(model)
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm.(model)
	if got.state != statePrintPick {
		t.Fatalf("state = %v, want statePrintPick — prints were already fetched", got.state)
	}
	first := got.list.Items()[0].(printItem)
	if !first.scanned || !strings.EqualFold(first.card.Set, "mh3") {
		t.Errorf("first item = %+v, want the scanned MH3 printing marked", first)
	}
	if len(got.review) != 0 {
		t.Errorf("the item under review should leave the queue, %d remain", len(got.review))
	}

	// Walk it through: printing → qty → confirm.
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm.(model)
	if got.state != stateQty {
		t.Fatalf("state = %v, want stateQty (single finish auto-skips)", got.state)
	}
	mm, _ = got.submitQty()
	mm, _ = mm.(model).handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm.(model)
	if len(ra.got) != 1 {
		t.Fatalf("adder called %d times, want 1", len(ra.got))
	}
	if got.summary.Count("reviewed") != 1 {
		t.Errorf("summary reviewed = %d, want 1", got.summary.Count("reviewed"))
	}
	if got.state != stateCapture {
		t.Errorf("with the queue empty, confirming returns to the camera, got %v", got.state)
	}
}

func TestCloseKeyWithQueuePrompts(t *testing.T) {
	// c with queued cards prompts instead of dropping them silently.
	_, fs := confidentFixture()
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, sess := openCapture(t, m)

	ranked, rank := rankByScanStrength(solRingPrints(), "MH3", "123", 0, "", "")
	item := queueItem{id: 1, canonical: "Sol Ring", ocrLine: "Sol Ring",
		match:  cardname.Match{Exact: true, Similarity: 1},
		prints: ranked, rank: rank, note: "queued"}
	m.review = []queueItem{item}

	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	got := mm.(model)
	if got.state != stateClosePrompt {
		t.Fatalf("c with a queue should prompt, got %v", got.state)
	}
	if sess.closed {
		t.Fatal("the prompt must not have closed the camera yet — c-again is free")
	}

	// esc again: changed my mind, back to the camera.
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if s := mm.(model).state; s != stateCapture {
		t.Fatalf("esc from the prompt should return to capture, got %v", s)
	}

	// enter: walk the queue through the cascade, camera closed.
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm.(model)
	if !sess.closed {
		t.Error("choosing to review should close the camera")
	}
	if got.state != statePrintPick || !got.walking {
		t.Fatalf("state = %v walking=%v, want the first item's cascade", got.state, got.walking)
	}
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyEnter}) // printing
	got = mm.(model)
	mm, _ = got.submitQty()
	mm, _ = mm.(model).handleKey(tea.KeyMsg{Type: tea.KeyEnter}) // confirm
	got = mm.(model)
	if len(ra.got) != 1 {
		t.Fatalf("adder called %d times, want 1", len(ra.got))
	}
	if got.state != stateName || got.walking {
		t.Errorf("after the walk: state=%v walking=%v, want the prompt", got.state, got.walking)
	}
}

func TestEscWithQueueWarnsBeforeDropping(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	m, sess := openCapture(t, m)
	m.review = []queueItem{{id: 1, ocrLine: "x", note: "queued"}}
	m.resolving = 1 // one lookup still in flight

	// esc goes straight to the leave gate — no close-prompt detour — and
	// the gate states the cost.
	mm, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	got := mm.(model)
	if got.state != stateLeaveConfirm || isQuit(cmd) {
		t.Fatalf("esc with a queue should open the leave gate, got %v", got.state)
	}
	if v := got.View(); !strings.Contains(v, "2 unsaved scans will be dropped") {
		t.Fatalf("the gate should state what quitting drops:\n%s", v)
	}
	if sess.closed {
		t.Fatal("the gate must not have closed the camera — declining is free")
	}

	// Declining returns to the live camera with the queue intact.
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	got = mm.(model)
	if got.state != stateCapture || len(got.review) != 1 || got.resolving != 1 {
		t.Fatalf("declining should keep the session whole: state=%v review=%d resolving=%d",
			got.state, len(got.review), got.resolving)
	}

	// y quits, closing the camera and dropping the unsaved scans with it.
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	mm, cmd = mm.(model).handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if !isQuit(cmd) || !sess.closed {
		t.Fatalf("y should quit and close the camera: quit=%v closed=%v", isQuit(cmd), sess.closed)
	}
}

func TestCloseKeyWithQueueDiscards(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	m, sess := openCapture(t, m)
	m.review = []queueItem{{id: 1, ocrLine: "x", note: "queued"}}
	m.resolving = 1 // one lookup still in flight
	stale := m.resolveGen

	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	got := mm.(model)
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	got = mm.(model)
	if !sess.closed || got.state != stateName {
		t.Fatalf("discard should close the camera and return to the prompt: closed=%v state=%v",
			sess.closed, got.state)
	}
	if len(got.review) != 0 || got.resolving != 0 {
		t.Errorf("discard left review=%d resolving=%d", len(got.review), got.resolving)
	}
	if got.summary.Count("discarded") != 1 {
		t.Errorf("summary discarded = %d, want 1", got.summary.Count("discarded"))
	}
	// The in-flight straggler lands dead.
	msg := resolveDoneMsg{gen: stale, item: queueItem{id: 2, canonical: "Sol Ring"}}
	mm, _ = got.Update(msg)
	if n := len(mm.(model).review); n != 0 {
		t.Errorf("a discarded generation's straggler joined the queue: %d", n)
	}
}

func TestSessionDestPickedOnceAndStampsAutoCommits(t *testing.T) {
	ev, fs := confidentFixture()
	ra := &recordingAdder{}
	sc := &fakeScanner{devices: []scan.Device{cam("c1", "iPhone", "iPhone")}}
	m := newModel(context.Background(), fs, ra.add, sc, "", destFixtures())

	// ctrl+o asks where scanned cards land before opening the camera.
	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlO})
	got := mm.(model)
	if got.state != stateDestPick || !got.destForSession {
		t.Fatalf("first scan should ask the session destination, got state=%v", got.state)
	}
	got.list.Select(1) // Trade binder
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm.(model)
	if got.state != stateCameraBusy || !got.destPicked || got.dest.ID != 2 {
		t.Fatalf("after the pick: state=%v destPicked=%v dest=%+v, want the camera opening with Trade",
			got.state, got.destPicked, got.dest)
	}
	mm, _ = got.onCameras(camerasMsg{devices: sc.devices})
	sess := &fakeSession{events: make(chan scan.Event, 8)}
	mm, _ = mm.(model).onSession(sessionMsg{session: sess})
	got = mm.(model)

	// An auto-commit lands in the session destination.
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true, ev: ev})
	got = resolve(t, mm.(model), ev.CardList()[0])
	if len(ra.got) != 1 || ra.got[0].ContainerID != 2 {
		t.Fatalf("auto-commit went to container %v, want the session pick 2", ra.got)
	}

	// Closing and reopening the camera does not re-ask.
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	got = mm.(model)
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyCtrlO})
	if s := mm.(model).state; s == stateDestPick {
		t.Error("the session destination should be asked once, not per camera open")
	}
}

func TestReadyEventEnablesHelperAutoCapture(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	got, sess := openCapture(t, m)

	// An old helper advertises nothing: no auto command is ever sent.
	mm, _ := got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true,
		ev: scan.Event{Kind: scan.EventReady, Device: "iPhone"}})
	got = mm.(model)
	if sess.autoOn != 0 {
		t.Fatalf("auto-on sent to a helper that never advertised it (%d times)", sess.autoOn)
	}

	// A new helper advertises auto and gets turned on.
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true,
		ev: scan.Event{Kind: scan.EventReady, Device: "iPhone", Features: []string{"auto"}}})
	got = mm.(model)
	if sess.autoOn != 1 {
		t.Errorf("auto-on sent %d times, want 1", sess.autoOn)
	}
	if got.autoState != "armed" {
		t.Errorf("autoState = %q, want armed", got.autoState)
	}

	// Trigger phase changes update the capture view's guidance.
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true,
		ev: scan.Event{Kind: scan.EventAuto, State: "held"}})
	got = mm.(model)
	if !strings.Contains(got.View(), "Swap in the next card") {
		t.Error("the capture view should follow the trigger phase")
	}
}

// q inside a picker used to quit the whole program via the list widget's own
// keymap — with a batch queued, silent data loss. It must not quit anymore.
func TestQInPickerNoLongerQuits(t *testing.T) {
	card := scryfall.Card{ID: "u1", Name: "Ulamog", Set: "uma", CollectorNumber: "7",
		Finishes: []string{"nonfoil", "foil"}}
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "Ulamog", nil)
	mm, _ := m.Update(printsMsg{name: "Ulamog", cards: []scryfall.Card{card, card}})
	got := mm.(model)
	if got.state != stateFinishPick && got.state != statePrintPick {
		t.Fatalf("setup: state = %v, want a picker", got.state)
	}
	mm2, cmd := got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if isQuit(cmd) {
		t.Fatal("q quit the program from inside a picker")
	}
	if mm2.(model).state != got.state {
		t.Errorf("q changed state to %v", mm2.(model).state)
	}
}

// multiFixture is confidentFixture plus a second auto-committable card, for
// the multi-card duplicate scenarios.
func multiFixture() fakeSearcher {
	return fakeSearcher{
		fuzzy: map[string]string{"Sol Ring": "Sol Ring", "Ancient Tomb": "Ancient Tomb"},
		prints: map[string][]scryfall.Card{
			"Sol Ring": solRingPrints(),
			"Ancient Tomb": {{ID: "t", Name: "Ancient Tomb", Set: "uma",
				CollectorNumber: "236", Finishes: []string{"nonfoil"}}},
		},
	}
}

func scanCard(name, set, number string) scan.Card {
	return scan.Card{Name: name, Candidates: []string{name}, SetCode: set,
		CollectorNumber: number, Confidence: 0.95}
}

// sendScan feeds a multi-card scan event through intake and resolves every
// card synchronously, in order.
func sendScan(t *testing.T, m model, cards ...scan.Card) model {
	t.Helper()
	ev := scan.Event{Kind: scan.EventScan, Cards: cards}
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	m = mm.(model)
	for i, c := range cards {
		mm, _ := m.Update(m.resolveCardCmd(m.nextResolveID-len(cards)+1+i, c, len(cards))())
		m = mm.(model)
	}
	return m
}

// A nudge recheck of a whole multi-card scene swallows every card of it —
// the single-last-name memory used to dup-queue all but one (observed live:
// a re-shot pair queued both).
func TestNudgeEchoSwallowsWholeCapture(t *testing.T) {
	ra := &recordingAdder{}
	m := newModel(context.Background(), multiFixture(), ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	m = sendScan(t, m, scanCard("Sol Ring", "MH3", "123"), scanCard("Ancient Tomb", "UMA", "236"))
	if len(ra.got) != 2 || len(m.review) != 0 {
		t.Fatalf("setup: adds=%d review=%d, want both auto", len(ra.got), len(m.review))
	}

	// The nudge fires, and the recheck re-reads the unchanged scene.
	m.nudgeSentAt = m.now()
	m = sendScan(t, m, scanCard("Sol Ring", "MH3", "123"), scanCard("Ancient Tomb", "UMA", "236"))
	if len(ra.got) != 2 {
		t.Errorf("adds = %d after echo, want still 2", len(ra.got))
	}
	if len(m.review) != 0 {
		t.Errorf("review = %+v after echo, want empty — both echoes swallowed", m.review)
	}
}

// A card lingering in frame beside a new one is dropped, not queued: an
// un-swapped pile is not a playset signal.
func TestLingeringNeighborDropped(t *testing.T) {
	ra := &recordingAdder{}
	m := newModel(context.Background(), multiFixture(), ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	m = sendScan(t, m, scanCard("Sol Ring", "MH3", "123"))
	if len(ra.got) != 1 {
		t.Fatalf("setup: adds=%d", len(ra.got))
	}
	// The next capture (not a nudge) sees the new card and the old one still
	// on the pile.
	m = sendScan(t, m, scanCard("Sol Ring", "MH3", "123"), scanCard("Ancient Tomb", "UMA", "236"))
	if len(ra.got) != 2 {
		t.Errorf("adds = %d, want the new card added once", len(ra.got))
	}
	if len(m.review) != 0 {
		t.Errorf("review = %+v, want the lingering Sol Ring dropped, not queued", m.review)
	}
	if !strings.Contains(m.status, "Still seeing Sol Ring") {
		t.Errorf("status = %q, want the still-seeing note", m.status)
	}
}

// Two copies fanned in one frame are two cards, and both commit.
//
// This used to queue the second for a deliberate confirm. Two copies visible in
// one photograph is not ambiguous — it is a playset, which is exactly the pile
// a hands-free session exists to get through without stopping.
func TestFannedPlaysetCommitsBothCopies(t *testing.T) {
	ra := &recordingAdder{}
	m := newModel(context.Background(), multiFixture(), ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	m = sendScan(t, m, scanCard("Sol Ring", "MH3", "123"), scanCard("Sol Ring", "MH3", "123"))
	if len(ra.got) != 2 {
		t.Errorf("adds = %d, want both copies committed", len(ra.got))
	}
	if len(m.review) != 0 {
		t.Fatalf("review = %+v, want nothing queued", m.review)
	}
}

// A deliberate solo re-scan is a second card and commits.
//
// The phone has said a card was placed — it watched the last one leave, or
// watched this one laid over it — so there is nothing left to confirm.
func TestSoloRescanCommitsAsASecondCopy(t *testing.T) {
	ra := &recordingAdder{}
	m := newModel(context.Background(), multiFixture(), ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	m = sendScan(t, m, scanCard("Sol Ring", "MH3", "123"))
	m = sendScan(t, m, scanCard("Sol Ring", "MH3", "123"))
	if len(ra.got) != 2 {
		t.Errorf("adds = %d, want both scans committed", len(ra.got))
	}
	if len(m.review) != 0 {
		t.Fatalf("review = %+v, want nothing queued", m.review)
	}
}

// An OCR-mangled re-read of a just-processed card, in a multi-card frame,
// drops instead of queueing as uncertain.
func TestOCRVariantOfRecentDropped(t *testing.T) {
	ra := &recordingAdder{}
	fs := multiFixture()
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	m = sendScan(t, m, scanCard("Ancient Tomb", "UMA", "236"))
	if len(ra.got) != 1 {
		t.Fatalf("setup: adds=%d", len(ra.got))
	}
	// The next frame reads the lingering card's title with a mangle that
	// still fuzzy-resolves, but weakly — plus a genuinely new card.
	fs.fuzzy["Ancjent Tomb"] = "Ancient Tomb"
	m.searcher = fs
	mangled := scan.Card{Name: "Ancjent Tomb", Candidates: []string{"Ancjent Tomb"}, Confidence: 0.6}
	m = sendScan(t, m, mangled, scanCard("Sol Ring", "MH3", "123"))
	if len(ra.got) != 2 {
		t.Errorf("adds = %d, want the new card added", len(ra.got))
	}
	if len(m.review) != 0 {
		t.Errorf("review = %+v, want the mangled re-read dropped", m.review)
	}
}

// Enter is not a shutter: only space captures, so the confirm reflex from
// every other step never fires the camera by accident.
func TestEnterDoesNotCapture(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	got, sess := openCapture(t, m)
	mm, _ := got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm.(model)
	if sess.captures != 0 {
		t.Fatalf("enter fired the shutter: captures = %d", sess.captures)
	}
	if got.state != stateCapture {
		t.Fatalf("state = %v, want stateCapture unchanged", got.state)
	}
}

// layeredFakeSearcher records which lines were resolved against the catalog
// alone and which were allowed off-machine, so the fallback-line policy can be
// asserted rather than inferred from timing.
type layeredFakeSearcher struct {
	fakeSearcher
	remote []string
	local  []string
}

func (s *layeredFakeSearcher) NamedFuzzy(ctx context.Context, text string) (*scryfall.Card, cardname.Match, error) {
	s.remote = append(s.remote, text)
	return s.fakeSearcher.NamedFuzzy(ctx, text)
}

func (s *layeredFakeSearcher) NamedFuzzyLocal(ctx context.Context, text string) (*scryfall.Card, cardname.Match, error) {
	s.local = append(s.local, text)
	return s.fakeSearcher.NamedFuzzy(ctx, text)
}

// Only the helper's own title guess may reach the network. verdict refuses to
// auto-commit a fallback-line match, so a round trip spent on one buys nothing
// but latency and a chance to ghost a real card into the queue — one live
// session lost 19s across 15 failed resolutions this way.
func TestResolveNameKeepsFallbackLinesLocal(t *testing.T) {
	s := &layeredFakeSearcher{fakeSearcher: fakeSearcher{
		fuzzy: map[string]string{"Dwarven Ruins": "Dwarven Ruins"},
	}}
	lines := []string{"Tins. Liz Danforth", "Dwarven Ruins", "Sacrifice Dwarven Ruins"}

	name, _, idx, _, err := resolveName(context.Background(), s, lines, tuningFor(""))
	if err != nil {
		t.Fatalf("resolveName: %v", err)
	}
	if name != "Dwarven Ruins" || idx != 1 {
		t.Fatalf("resolved %q at line %d, want \"Dwarven Ruins\" at line 1", name, idx)
	}
	if want := []string{"Tins. Liz Danforth"}; !slices.Equal(s.remote, want) {
		t.Errorf("lines allowed off-machine = %v, want only the title guess %v", s.remote, want)
	}
	// Every line is offered to the catalog first, including line 0 — that is
	// free, and it is what keeps a card genuinely named with a keyword or a
	// type word scannable.
	if want := []string{"Tins. Liz Danforth", "Dwarven Ruins"}; !slices.Equal(s.local, want) {
		t.Errorf("catalog lines = %v, want %v", s.local, want)
	}
}

// A title guess that is not title-shaped stays on the machine too. On an
// unreadable frame the catalog rightly refuses it and the escalation then asks
// Scryfall about a string that was never a name — six such lookups in one live
// session returned nothing after ~600ms each, the worst loop costing 3.9s.
func TestImplausibleTitleGuessNeverLeavesTheMachine(t *testing.T) {
	s := &layeredFakeSearcher{fakeSearcher: fakeSearcher{
		fuzzy: map[string]string{"Dwarven Ruins": "Dwarven Ruins"},
	}}
	// Leads lowercase, so it cannot be a title.
	lines := []string{"count on it. Then for each nor", "Dwarven Ruins"}

	name, _, idx, _, err := resolveName(context.Background(), s, lines, tuningFor(""))
	if err != nil {
		t.Fatalf("resolveName: %v", err)
	}
	if name != "Dwarven Ruins" || idx != 1 {
		t.Fatalf("resolved %q at line %d, want the real name at line 1", name, idx)
	}
	if len(s.remote) != 0 {
		t.Errorf("lines sent off-machine = %v, want none", s.remote)
	}
	if want := []string{"count on it. Then for each nor", "Dwarven Ruins"}; !slices.Equal(s.local, want) {
		t.Errorf("catalog lines = %v, want both tried locally %v", s.local, want)
	}
}

// The empty sentinel exists to re-derive the no-number outcome as a floor, so
// it must never displace evidence that a set and number both matched — that
// would clear the winning collector context and drop the card back under the
// strict name gates verdict exempts a set+number match from.
func TestSetAndNumberOutranksSinglePrint(t *testing.T) {
	if !(scanMatchSetAndNumber > scanMatchSinglePrint) {
		t.Errorf("scanMatchSetAndNumber (%d) must outrank scanMatchSinglePrint (%d)",
			scanMatchSetAndNumber, scanMatchSinglePrint)
	}
	if !(scanMatchSinglePrint > scanMatchNumberOnly) {
		t.Errorf("scanMatchSinglePrint (%d) must outrank scanMatchNumberOnly (%d)",
			scanMatchSinglePrint, scanMatchNumberOnly)
	}
	if got := scanMatchSetAndNumber.String(); got != "set+number" {
		t.Errorf("scanMatchSetAndNumber.String() = %q, want %q", got, "set+number")
	}
}

// keeperPrints is the case the year cannot settle: two printings that shipped
// in the *same* year, so a copyright read naming that year still leaves the
// card ambiguous. The fixture exists to pin the failing-closed direction.
func keeperPrints() []scryfall.Card {
	return []scryfall.Card{
		{ID: "lgn", Name: "Keeper of the Nine Gales", Set: "lgn", CollectorNumber: "42",
			ReleasedAt: "2003-02-03", Finishes: []string{"nonfoil"}},
		{ID: "8ed", Name: "Keeper of the Nine Gales", Set: "8ed", CollectorNumber: "88",
			ReleasedAt: "2003-07-28", Finishes: []string{"nonfoil"}},
	}
}

func TestCopyrightYearPinsPrintingWithoutNumber(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "lgn", Name: "Keeper of the Nine Gales", Set: "lgn", CollectorNumber: "42",
			ReleasedAt: "2003-02-03", Finishes: []string{"nonfoil"}},
		{ID: "10e", Name: "Keeper of the Nine Gales", Set: "10e", CollectorNumber: "88",
			ReleasedAt: "2007-07-13", Finishes: []string{"nonfoil"}},
	}
	ranked, rank := rankByScanStrength(prints, "", "", 2003, "", "")
	if rank != scanMatchYearOnly {
		t.Fatalf("rank = %v, want scanMatchYearOnly", rank)
	}
	if ranked[0].ID != "lgn" {
		t.Errorf("ranked[0] = %q, want the 2003 printing to lead", ranked[0].ID)
	}
	if rank.String() != "year-only" {
		t.Errorf("String() = %q, want %q", rank.String(), "year-only")
	}
}

// Two printings from the same year settle nothing, so the card must queue
// rather than pick whichever the catalog happened to list first.
func TestCopyrightYearAmbiguousLeavesUnverified(t *testing.T) {
	if _, rank := rankByScanStrength(keeperPrints(), "", "", 2003, "", ""); rank != scanMatchNone {
		t.Errorf("rank = %v, want scanMatchNone — both printings are 2003", rank)
	}
	// A year matching no printing is the misread case: unchanged behavior.
	if _, rank := rankByScanStrength(keeperPrints(), "", "", 1997, "", ""); rank != scanMatchNone {
		t.Errorf("rank = %v, want scanMatchNone for a year no printing shares", rank)
	}
	// And with no year read at all, nothing changes either.
	if _, rank := rankByScanStrength(keeperPrints(), "", "", 0, "", ""); rank != scanMatchNone {
		t.Errorf("rank = %v, want scanMatchNone without a year", rank)
	}
}

// A year is weaker evidence than a collector number and must rank below one,
// so a candidate that actually verified by number always wins the selection.
func TestYearOnlyRanksBelowNumber(t *testing.T) {
	if !(scanMatchYearOnly < scanMatchNumberOnly) {
		t.Errorf("year-only (%d) must rank below number-only (%d)",
			scanMatchYearOnly, scanMatchNumberOnly)
	}
	if !(scanMatchYearOnly > scanMatchNumberAmbiguous) {
		t.Errorf("year-only (%d) must rank above number-ambiguous (%d)",
			scanMatchYearOnly, scanMatchNumberAmbiguous)
	}
}

// End to end: the old frame gave a name and a copyright year and nothing else,
// and the card commits to the printing that year names.
func TestYearOnlyAutoCommits(t *testing.T) {
	fs := fakeSearcher{
		fuzzy: map[string]string{"Keeper of the Nine Gales": "Keeper of the Nine Gales"},
		prints: map[string][]scryfall.Card{"Keeper of the Nine Gales": {
			{ID: "lgn", Name: "Keeper of the Nine Gales", Set: "lgn", CollectorNumber: "42",
				ReleasedAt: "2003-02-03", Finishes: []string{"nonfoil"}},
			{ID: "10e", Name: "Keeper of the Nine Gales", Set: "10e", CollectorNumber: "88",
				ReleasedAt: "2007-07-13", Finishes: []string{"nonfoil"}}}},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	ev := scan.Event{Kind: scan.EventScan, Name: "Keeper of the Nine Gales",
		Cards: []scan.Card{{Name: "Keeper of the Nine Gales",
			Candidates:    []string{"Keeper of the Nine Gales"},
			CopyrightYear: 2003, Confidence: 0.95, Source: "crop"}}}
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	got := resolve(t, mm.(model), ev.CardList()[0])

	if len(ra.got) != 1 {
		t.Fatalf("adder called %d times, want 1 — the year names one printing", len(ra.got))
	}
	if ra.got[0].Card.ID != "lgn" {
		t.Errorf("committed %q, want the 2003 printing", ra.got[0].Card.ID)
	}
	if len(got.review) != 0 {
		t.Errorf("review = %+v, want nothing queued", got.review)
	}
}

// inspiredFire is the live case: one printing, both finishes available, and a
// frame whose foil marker only reads on some captures.
func inspiredFirePrints() []scryfall.Card {
	return []scryfall.Card{{ID: "msc690", Name: "Inspired Fire", Set: "msc",
		CollectorNumber: "690", Finishes: []string{"nonfoil", "foil"}}}
}

// A nonfoil and then a foil of one card are two cards.
//
// This used to *correct* the first row instead of adding a second, which made
// scanning a card and its foil impossible to express. The removed function's
// own comment had spotted the conflict: two copies scanned back to back look
// exactly like one misread copy, and rewriting the first row is as wrong as
// dropping the second.
//
// The cost of choosing this way is real: on the case it was built for — a foil
// Inspired Fire recorded nonfoil, its marker legible on the very next capture —
// the collection now keeps one wrong row *and* one right one.
func TestNonfoilThenFoilAreTwoCards(t *testing.T) {
	fs := fakeSearcher{
		fuzzy:  map[string]string{"Inspired Fire": "Inspired Fire"},
		prints: map[string][]scryfall.Card{"Inspired Fire": inspiredFirePrints()},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	// First look: no marker anywhere, so the default is written.
	blind := scan.Event{Kind: scan.EventScan, Name: "Inspired Fire",
		Cards: []scan.Card{{Name: "Inspired Fire", Candidates: []string{"Inspired Fire"},
			Confidence: 0.95, Source: "crop"}}}
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: blind})
	m = resolve(t, mm.(model), blind.CardList()[0])

	if len(ra.got) != 1 || ra.got[0].Finish != "nonfoil" {
		t.Fatalf("first look = %+v, want one nonfoil commit", ra.got)
	}

	// The foil is laid down where the nonfoil was. The phone watched that
	// happen and says so, which is what makes it a placement rather than
	// another look — a nudge echo would be swallowed, and should be.
	marked := scan.Event{Kind: scan.EventScan, Name: "Inspired Fire",
		FireReason: scan.FireReplaced,
		Cards: []scan.Card{{Name: "Inspired Fire", Candidates: []string{"Inspired Fire"},
			FinishHint: "foil", Confidence: 0.95, Source: "crop"}}}
	mm, _ = m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: marked})
	m = resolve(t, mm.(model), marked.CardList()[0])

	if len(ra.got) != 2 {
		t.Fatalf("adder called %d times, want 2 — two cards", len(ra.got))
	}
	second := ra.got[1]
	if second.Finish != "foil" {
		t.Errorf("second = %+v, want a foil added", second)
	}
	if second.ReplacesFinish != "" {
		t.Errorf("second replaced %q; it should replace nothing", second.ReplacesFinish)
	}
	if len(m.review) != 0 {
		t.Errorf("review = %+v, want nothing queued", m.review)
	}
	// Two cards, so two in the count.
	if m.addedCount != 2 {
		t.Errorf("addedCount = %d, want 2", m.addedCount)
	}
	// And the swallow still works: the same card looked at again because the
	// parent nudged, with no placement behind it, adds nothing.
	echo := marked
	echo.FireReason = scan.FireNudge
	mm, _ = m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: echo})
	m = resolve(t, mm.(model), echo.CardList()[0])
	if len(ra.got) != 2 {
		t.Errorf("a nudge echo must add nothing, adder got %d", len(ra.got))
	}
}

// A nudge echo of a committed foil is still an echo.
//
// The finish correction is gone, so the risk this pins has changed shape: a
// second look that misreads the marker must not become a second card.
//
// Note what does *not* rescue it. The swallow keys on the name alone, so the
// contradicting hint below is simply ignored — which is the point. A nudge echo
// is a second look at a scene nobody touched, and a card actually swapped for
// its foil arrives as `replaced` and never reaches this gate at all. Keying the
// gate on the finish as well would let this misread through as a phantom card.
func TestEvidencedFinishIsNotReopenedByEcho(t *testing.T) {
	fs := fakeSearcher{
		fuzzy:  map[string]string{"Inspired Fire": "Inspired Fire"},
		prints: map[string][]scryfall.Card{"Inspired Fire": inspiredFirePrints()},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	foil := scan.Event{Kind: scan.EventScan, Name: "Inspired Fire",
		Cards: []scan.Card{{Name: "Inspired Fire", Candidates: []string{"Inspired Fire"},
			FinishHint: "foil", Confidence: 0.95, Source: "crop"}}}
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: foil})
	m = resolve(t, mm.(model), foil.CardList()[0])
	if len(ra.got) != 1 || ra.got[0].Finish != "foil" {
		t.Fatalf("first look = %+v, want one foil commit", ra.got)
	}

	// The echo reads nonfoil. The commit was evidence-backed, so it stands and
	// the echo is swallowed as usual.
	m.nudgeSentAt = m.now()
	plain := scan.Event{Kind: scan.EventScan, Name: "Inspired Fire",
		Cards: []scan.Card{{Name: "Inspired Fire", Candidates: []string{"Inspired Fire"},
			FinishHint: "nonfoil", Confidence: 0.95, Source: "crop"}}}
	mm, _ = m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: plain})
	m = resolve(t, mm.(model), plain.CardList()[0])

	if len(m.review) != 0 {
		t.Fatalf("review = %+v, want an evidenced finish left alone", m.review)
	}
	if len(ra.got) != 1 {
		t.Errorf("adder called %d times, want 1", len(ra.got))
	}
}

// blockFakeSearcher adds collector-block lookup to the cascade fake.
type blockFakeSearcher struct {
	fakeSearcher
	byBlock map[string]scryfall.Card // "set/number" (lowercased) -> card
	asked   []string
}

func (s *blockFakeSearcher) PrintBySetNumber(_ context.Context, set, number string) (*scryfall.Card, error) {
	key := strings.ToLower(set) + "/" + number
	s.asked = append(s.asked, key)
	if c, ok := s.byBlock[key]; ok {
		return &c, nil
	}
	return nil, nil
}

func quicksilverEvent(name string, alts []scan.CollectorAlt) scan.Event {
	return scan.Event{Kind: scan.EventScan, Name: name,
		Cards: []scan.Card{{Name: name, Candidates: []string{name},
			SetCode: "MSH", CollectorNumber: "412", Confidence: 0.95, Source: "crop",
			CollectorAlts: alts}}}
}

// A title that reads as rules text does not make the card unidentifiable: the
// block names it exactly. Live, "If Quicksilver, Brash Blur is in your" came
// with a clean MSH/412 and the card went unrecorded for the whole session.
func TestUnreadableTitleResolvesFromItsCollectorBlock(t *testing.T) {
	card := scryfall.Card{ID: "msh412", Name: "Quicksilver, Brash Blur", Set: "msh",
		CollectorNumber: "412", Finishes: []string{"nonfoil"}}
	fs := &blockFakeSearcher{byBlock: map[string]scryfall.Card{"msh/412": card}}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	ev := quicksilverEvent("If Quicksilver, Brash Blur is in your", nil)
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	got := resolve(t, mm.(model), ev.CardList()[0])

	if len(ra.got) != 1 {
		t.Fatalf("adder called %d times, want 1 — the block identifies the card", len(ra.got))
	}
	if ra.got[0].Card.ID != "msh412" {
		t.Errorf("committed %q, want msh412", ra.got[0].Card.ID)
	}
	if len(got.review) != 0 {
		t.Errorf("review = %+v, want nothing queued", got.review)
	}
}

// A copyright-line number misreads digits, so it may rank a card but never
// conjure one: resolving a card that was never identified from a number we
// distrust would invent the card outright.
func TestBlockResolutionRefusesACopyrightNumber(t *testing.T) {
	card := scryfall.Card{ID: "msh412", Name: "Quicksilver, Brash Blur", Set: "msh",
		CollectorNumber: "412", Finishes: []string{"nonfoil"}}
	fs := &blockFakeSearcher{byBlock: map[string]scryfall.Card{"msh/412": card}}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	ev := quicksilverEvent("If Quicksilver, Brash Blur is in your", nil)
	ev.Cards[0].NumberSource = "copyright"
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	got := resolve(t, mm.(model), ev.CardList()[0])

	if len(ra.got) != 0 {
		t.Fatalf("adder called %d times, want 0 — a copyright number must not name a card", len(ra.got))
	}
	if len(got.review) != 1 {
		t.Errorf("review = %+v, want the card queued unidentified", got.review)
	}
}

// The phantom kill clears the noise a nudge re-look leaves behind, but a block
// is evidence a real card is in frame — killing that would delete the only
// trace of it, which is exactly what block resolution exists to rescue.
func TestNudgePhantomKillSparesAnEntryWithACollectorBlock(t *testing.T) {
	// No block lookup available, so the card stays unidentified either way;
	// what is under test is whether it survives to the queue.
	fs := &blockFakeSearcher{}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)
	m.nudgeSentAt = m.now()

	withBlock := quicksilverEvent("If Quicksilver, Brash Blur is in your", nil)
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: withBlock})
	got := resolve(t, mm.(model), withBlock.CardList()[0])
	if len(got.review) != 1 {
		t.Fatalf("review = %+v, want an entry with a block kept", got.review)
	}

	// The same nudge re-look with nothing but junk text is noise and dies.
	m2 := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m2, _ = openCapture(t, m2)
	m2.nudgeSentAt = m2.now()
	junk := scan.Event{Kind: scan.EventScan, Name: "G: Add *",
		Cards: []scan.Card{{Name: "G: Add *", Candidates: []string{"G: Add *"},
			Confidence: 0.9, Source: "crop"}}}
	mm2, _ := m2.onSessionEvent(sessionEventMsg{gen: m2.sessionGen, ok: true, ev: junk})
	got2 := resolve(t, mm2.(model), junk.CardList()[0])
	if len(got2.review) != 0 {
		t.Errorf("review = %+v, want the blockless nudge phantom killed", got2.review)
	}
}

// eternalDragonPrints is the live case: seven printings, exactly one numbered
// 12, released the year the copyright line names.
func eternalDragonPrints() []scryfall.Card {
	return []scryfall.Card{
		{ID: "scg", Name: "Eternal Dragon", Set: "scg", CollectorNumber: "12",
			ReleasedAt: "2003-05-26", Finishes: []string{"nonfoil"}},
		{ID: "c13", Name: "Eternal Dragon", Set: "c13", CollectorNumber: "10",
			ReleasedAt: "2013-11-01", Finishes: []string{"nonfoil"}},
		{ID: "c20", Name: "Eternal Dragon", Set: "c20", CollectorNumber: "88",
			ReleasedAt: "2020-04-17", Finishes: []string{"nonfoil"}},
	}
}

// A number naming one printing is one signal; that printing's release year
// agreeing with the copyright line is a second. Two independent agreements
// outrank a mangled title — live, "Stemal Dragon" resolved to Eternal Dragon
// at 76% and queued while the band read a clean 12/143 and "1993-2003".
func TestNumberAndYearRankIsEarnedAndWaivesTheNameGate(t *testing.T) {
	prints := eternalDragonPrints()
	ranked, rank := rankByScanStrength(prints, "", "12", 2003, "", "")
	if rank != scanMatchNumberAndYear {
		t.Fatalf("rank = %v, want scanMatchNumberAndYear", rank)
	}
	if ranked[0].ID != "scg" {
		t.Errorf("ranked[0] = %q, want the 2003 printing", ranked[0].ID)
	}
	if rank.String() != "number+year" {
		t.Errorf("String() = %q, want %q", rank.String(), "number+year")
	}

	auto, _, note := verdict(queueItem{canonical: "Eternal Dragon", prints: ranked,
		rank: rank, match: cardname.Match{Similarity: 0.76},
		raw: scan.Card{Confidence: 0.5}})
	if !auto {
		t.Errorf("verdict queued a doubly-corroborated card: %q", note)
	}
}

// Without the year it stays number-only, and a title that mangled still queues:
// collector number 12 is common enough that a fuzzy match onto the wrong card
// could collide with it, which is the luck the second signal removes.
func TestNumberAloneStillQueuesAMangledName(t *testing.T) {
	ranked, rank := rankByScanStrength(eternalDragonPrints(), "", "12", 0, "", "")
	if rank != scanMatchNumberOnly {
		t.Fatalf("rank = %v, want scanMatchNumberOnly without a year", rank)
	}
	auto, _, note := verdict(queueItem{canonical: "Eternal Dragon", prints: ranked,
		rank: rank, match: cardname.Match{Similarity: 0.76}})
	if auto {
		t.Errorf("verdict committed on one signal and a 76%% name, want queued")
	}
	if !strings.Contains(note, "uncertain name") {
		t.Errorf("note = %q, want the name gate to be the reason", note)
	}
}

// A year that agrees with no printing is a misread and must add nothing.
func TestDisagreeingYearDoesNotEarnTheRank(t *testing.T) {
	if _, rank := rankByScanStrength(eternalDragonPrints(), "", "12", 1999, "", ""); rank != scanMatchNumberOnly {
		t.Errorf("rank = %v, want scanMatchNumberOnly when the year matches nothing", rank)
	}
}

// Esc during the close-time review walk used to drop every remaining card on
// the spot. That walk is where a session's unsaved scans live, and nothing
// else in the app destroys that much on one keystroke without asking.
func TestEscDuringReviewWalkAsksBeforeDropping(t *testing.T) {
	fs := fakeSearcher{
		fuzzy:  map[string]string{"Sol Ring": "Sol Ring"},
		prints: map[string][]scryfall.Card{"Sol Ring": solRingPrints()},
	}
	m := newModel(context.Background(), fs, noopAdder, &fakeScanner{}, "", nil)
	m.walking = true
	m.review = []queueItem{
		{id: 2, canonical: "Sol Ring", prints: solRingPrints()},
		{id: 3, canonical: "Sol Ring", prints: solRingPrints()},
	}
	cur := queueItem{id: 1, canonical: "Sol Ring", prints: solRingPrints()}
	m.current = &cur

	got, _ := m.cancelReview()
	after := got.(model)
	if after.state != stateAbandonConfirm {
		t.Fatalf("state = %v, want the abandon gate", after.state)
	}
	if len(after.review) != 2 || after.current == nil {
		t.Fatalf("esc dropped work before the gate was answered: review=%d current=%v",
			len(after.review), after.current)
	}

	// Anything but y resumes the walk with everything intact.
	mm, _ := after.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	kept := mm.(model)
	if len(kept.review) != 2 || !kept.walking {
		t.Errorf("declining the gate lost work: review=%d walking=%v", len(kept.review), kept.walking)
	}

	// y is the deliberate answer, and only then is the queue dropped.
	mm, _ = after.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	gone := mm.(model)
	if len(gone.review) != 0 || gone.walking {
		t.Errorf("confirming did not abandon: review=%d walking=%v", len(gone.review), gone.walking)
	}
}

// The live foil / nonfoil / foil sequence, replayed from the wire.
//
// Copied from a real session (13:12:26, 13:12:30, 13:12:33 in
// scan-telemetry.log): a foil Rampaging Ferocidon, its nonfoil, then the foil
// again. The first two committed and the third queued as *"possible duplicate:
// same card auto-added just now"* — the second foil is the same printing and
// the same finish as the first, seven seconds apart, so the duplicate window
// hit legitimately.
//
// It is the third scan that this pins, not the second. Nonfoil-after-foil was
// never in danger: the window keys on printing *and* finish, so a different
// finish does not reach it. The repeat does, and the phone said `removed` for
// all three — it watched each card leave and the next go down.
func TestLiveFoilNonfoilFoilSequenceCommitsThree(t *testing.T) {
	fs := fakeSearcher{
		fuzzy:  map[string]string{"Inspired Fire": "Inspired Fire"},
		prints: map[string][]scryfall.Card{"Inspired Fire": inspiredFirePrints()},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	look := func(finish string) scan.Event {
		return scan.Event{Kind: scan.EventScan, Name: "Inspired Fire",
			FireReason: scan.FireRemoved,
			Cards: []scan.Card{{Name: "Inspired Fire",
				Candidates: []string{"Inspired Fire"},
				FinishHint: finish, Confidence: 0.95, Source: "crop"}}}
	}
	for _, finish := range []string{"foil", "nonfoil", "foil"} {
		ev := look(finish)
		mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
		m = resolve(t, mm.(model), ev.CardList()[0])
	}

	if len(ra.got) != 3 {
		t.Fatalf("adder called %d times, want 3 — three cards were placed", len(ra.got))
	}
	for i, want := range []string{"foil", "nonfoil", "foil"} {
		if ra.got[i].Finish != want {
			t.Errorf("add %d = %q, want %q", i, ra.got[i].Finish, want)
		}
	}
	if len(m.review) != 0 {
		t.Errorf("review = %+v, want nothing queued", m.review)
	}
}

// The settled card replaces the phantom the shutter caught on its way down.
//
// Replayed from a stacking run (13:33:00.742 / 13:33:01.789 and two more pairs
// like it): the shutter fires while the card is still being lowered, so the
// name reads but the footer does not and it queues as "printing unverified".
// One second later the settled card is captured again and reads perfectly.
//
// The second capture arrives as `replaced` — the held window really did change
// while the hand withdrew — which is exactly why this needs testing. The
// upgrade used to run only for nudge echoes, so a placement-fired re-read left
// the phantom sitting in review beside its own correct commit.
func TestSettledReadReplacesTheQueuedPhantom(t *testing.T) {
	// Three printings, so a name with no footer behind it cannot pick one.
	fs := fakeSearcher{
		fuzzy:  map[string]string{"Sol Ring": "Sol Ring"},
		prints: map[string][]scryfall.Card{"Sol Ring": solRingPrints()},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	// The name off the top of the card, no footer: several printings, nothing
	// to choose between them.
	early := scan.Event{Kind: scan.EventScan, Name: "Sol Ring",
		Cards: []scan.Card{{Name: "Sol Ring", Candidates: []string{"Sol Ring"},
			Confidence: 0.62, Source: "line"}}}
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: early})
	m = resolve(t, mm.(model), early.CardList()[0])
	if len(m.review) != 1 {
		t.Fatalf("review = %d, want the early read queued", len(m.review))
	}

	// The settled card, footer and all, fired by a placement.
	settled := scan.Event{Kind: scan.EventScan, Name: "Sol Ring",
		FireReason: scan.FireReplaced,
		Cards: []scan.Card{{Name: "Sol Ring", Candidates: []string{"Sol Ring"},
			SetCode: "MH3", CollectorNumber: "123",
			Confidence: 0.95, Source: "crop"}}}
	mm, _ = m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: settled})
	m = resolve(t, mm.(model), settled.CardList()[0])

	if len(ra.got) != 1 {
		t.Fatalf("adder called %d times, want 1 — one card was on the desk", len(ra.got))
	}
	if len(m.review) != 0 {
		t.Errorf("review = %+v, want the phantom replaced by the settled read", m.review)
	}
}

// The receipt shows a window onto the history, not all of it.
func TestTallyWindowsAndScrolls(t *testing.T) {
	m := model{}
	for i := 1; i <= 25; i++ {
		m.recordTally(fmt.Sprintf("card %d", i))
	}

	// Pinned to the newest while nobody has scrolled: the row that just landed
	// is the one being watched for.
	if m.tallyOffset != 0 {
		t.Fatalf("tallyOffset = %d, want 0 — the resting state is the newest",
			m.tallyOffset)
	}
	if got := m.tallyMaxOffset(); got != 15 {
		t.Errorf("tallyMaxOffset = %d, want 15 (25 rows, 10 shown)", got)
	}

	// Scrolling back stops at the oldest row rather than running off the end.
	mm, _ := m.scrollTally(999)
	m = mm.(model)
	if m.tallyOffset != 15 {
		t.Errorf("tallyOffset = %d, want it clamped to 15", m.tallyOffset)
	}

	// A card arriving while scrolled back must not slide the rows along.
	m.recordTally("card 26")
	if m.tallyOffset != 16 {
		t.Errorf("tallyOffset = %d, want 16 — the visible rows should not move",
			m.tallyOffset)
	}

	// And forward returns to the newest, never past it.
	mm, _ = m.scrollTally(-999)
	m = mm.(model)
	if m.tallyOffset != 0 {
		t.Errorf("tallyOffset = %d, want 0 back at the newest", m.tallyOffset)
	}
}

// A short session has nothing to scroll and says nothing about scrolling.
func TestShortTallyNeedsNoScroll(t *testing.T) {
	m := model{}
	for i := 0; i < tallyShown; i++ {
		m.recordTally(fmt.Sprintf("card %d", i))
	}
	if got := m.tallyMaxOffset(); got != 0 {
		t.Errorf("tallyMaxOffset = %d, want 0 — everything already fits", got)
	}
	mm, _ := m.scrollTally(5)
	if got := mm.(model).tallyOffset; got != 0 {
		t.Errorf("tallyOffset = %d, want 0 — there is nothing to scroll to", got)
	}
}

// The rendered view shows exactly the window, and slicing it cannot panic.
func TestCaptureViewRendersTenRows(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)
	for i := 1; i <= 25; i++ {
		m.recordTally(fmt.Sprintf("card %d", i))
	}
	m.state = stateCapture

	if got := strings.Count(m.View(), "Auto-added"); got != tallyShown {
		t.Errorf("rendered %d rows, want %d", got, tallyShown)
	}
	if !strings.Contains(m.View(), "showing 16-25 of 25") {
		t.Error("the newest window should say where it is in the history")
	}

	// Scrolled fully back: the oldest rows, still exactly ten of them.
	mm, _ := m.scrollTally(999)
	m = mm.(model)
	if got := strings.Count(m.View(), "Auto-added"); got != tallyShown {
		t.Errorf("scrolled back rendered %d rows, want %d", got, tallyShown)
	}
	if !strings.Contains(m.View(), "showing 1-10 of 25") {
		t.Errorf("want the oldest window; got:\n%s", m.View())
	}
}
