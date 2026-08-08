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
		// exact match without saying so twice — including the nomination
		// flag, mirroring the real searchers: a truncated title comes back
		// PrefixOnly, an identity comes back plain.
		n, c := cardname.Normalize(text), cardname.Normalize(name)
		if n != c && !cardname.Plausible(text, name) && cardname.PrefixCandidate(text, name) {
			return &scryfall.Card{Name: name},
				cardname.Match{Similarity: cardname.Similarity(n, c), PrefixOnly: true}, nil
		}
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

// fakeSession stands in for a live link to the phone. Tests push events onto it
// to simulate the phone reporting captures, trigger states, and closure.
type fakeSession struct {
	chimes   int
	results  []scan.HUDResult
	events   chan scan.Event
	captures int
	autoOn   int
	autoOff  int
	torchOn  int
	torchOff int
	rearms   int
	evBiases []float64
	closed   bool
}

func (s *fakeSession) Result(r scan.HUDResult) error {
	s.results = append(s.results, r)
	return nil
}

func (s *fakeSession) Rearm() error {
	s.rearms++
	return nil
}

func (s *fakeSession) EVBias(ev float64) error {
	s.evBiases = append(s.evBiases, ev)
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

func (s *fakeSession) Torch(on bool) error {
	if on {
		s.torchOn++
	} else {
		s.torchOff++
	}
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

// A number that names exactly one printing settles it — when a second read
// corroborates (here the set code) — and the picker is skipped.
//
// This function used to hold the opposite rule — promote the row, never select
// it, so a misread digit stays visible. A one-row list is not review: nobody
// reads it, they press enter. The keystroke it saved on every scanned card is
// what the hands-free flow exists for; a bare, uncorroborated number keeps the
// picker (see TestBareNumberNarrowingToOneRowStillShowsThePicker).
func TestScannedNumberNamingOnePrintingSkipsThePicker(t *testing.T) {
	cards := solRingPrints()
	fs := fakeSearcher{prints: map[string][]scryfall.Card{"Sol Ring": cards}}
	m := newModel(context.Background(), fs, noopAdder, nil, "", nil)
	m.scanned = "Sol Ring"
	m.scannedSet = "MH3"
	m.scannedNumber = "123"

	mm, _ := m.onPrints(printsMsg{name: "Sol Ring", cards: cards})
	got := mm.(model)

	if got.state == statePrintPick {
		t.Fatalf("still at the printing picker with one candidate")
	}
	if got.chosen == nil {
		t.Fatal("nothing chosen")
	}
	if !strings.EqualFold(got.chosen.Set, "mh3") || got.chosen.CollectorNumber != "123" {
		t.Errorf("chose %s #%s, want MH3 #123", got.chosen.Set, got.chosen.CollectorNumber)
	}
	// Nothing left hidden, so nothing offers to unhide it.
	if got.printsAll != nil {
		t.Errorf("printsAll = %v, want nil once the number settled it", got.printsAll)
	}
}

// solRingSharedNumber is the case the picker still exists for: a number that
// narrows the list without settling it, because two printings carry it.
func solRingSharedNumber() []scryfall.Card {
	return []scryfall.Card{
		{ID: "a", Name: "Sol Ring", Set: "ltc", CollectorNumber: "300", Finishes: []string{"nonfoil"}},
		{ID: "b", Name: "Sol Ring", Set: "c21", CollectorNumber: "263", Finishes: []string{"nonfoil"}},
		{ID: "c", Name: "Sol Ring", Set: "cmm", CollectorNumber: "263", Finishes: []string{"nonfoil"}},
	}
}

func TestScannedNumberNarrowsAndMarksPrintings(t *testing.T) {
	cards := solRingSharedNumber()
	fs := fakeSearcher{prints: map[string][]scryfall.Card{"Sol Ring": cards}}
	m := newModel(context.Background(), fs, noopAdder, nil, "", nil)
	m.scanned = "Sol Ring"
	m.scannedNumber = "263" // no set read, so both 263s survive

	mm, _ := m.onPrints(printsMsg{name: "Sol Ring", cards: cards})
	got := mm.(model)

	if got.state != statePrintPick {
		t.Fatalf("state = %v, want statePrintPick: 263 names two printings", got.state)
	}
	// Only the printings the number can be. Promoting the right row to the top
	// was never the same as removing the ones the card rules out — live,
	// Victimize read a clean 413 and still asked which of nineteen.
	if len(got.list.Items()) != 2 {
		t.Fatalf("list has %d items, want the two printings numbered 263",
			len(got.list.Items()))
	}
	for i, it := range got.list.Items() {
		if n := it.(printItem).card.CollectorNumber; n != "263" {
			t.Errorf("item %d is #%s, want only #263", i, n)
		}
	}
	if !got.list.Items()[0].(printItem).scanned {
		t.Error("the leading printing should be marked as scanned")
	}
	// Pre-selected, not auto-committed: two candidates is a real question.
	if got.chosen != nil {
		t.Error("a number matching two printings must not select one outright")
	}
	if got.printsAll == nil || len(got.printsAll) != len(cards) {
		t.Errorf("printsAll = %v, want all %d kept for the toggle", got.printsAll, len(cards))
	}

	// ctrl+a brings the hidden printing back. The narrowing is the scanner's
	// belief and the digits can be misread, so there has to be a way back.
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyCtrlA})
	all := mm.(model)
	if len(all.list.Items()) != len(cards) {
		t.Fatalf("after ctrl+a: %d items, want all %d", len(all.list.Items()), len(cards))
	}
	if all.printsAll != nil {
		t.Error("nothing left to restore, so nothing should offer to")
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
	canonical, ocr, idx, _, _, err := resolveName(context.Background(), fs, lines)
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
		// The prefix rule is gone: a fragment that begins a different card's
		// name ("Gliding" → Gliding Licid, live) was indistinguishable from a
		// legitimate truncation, so truncations queue for review instead.
		{"Elspeth", "Elspeth, Knight-Errant", false, "partial read must not resolve by prefix"},

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
	canonical, _, _, _, _, _ := resolveName(context.Background(), fs,
		[]string{"option", "Elspeth, Knight-Errant"})
	if canonical != "Elspeth, Knight-Errant" {
		t.Errorf("canonical = %q, want the real card rather than the Opt false positive", canonical)
	}
}

func TestScanFuzzyMissReportsTopLine(t *testing.T) {
	// Nothing matches → the best-guess line is what gets pre-filled for editing.
	canonical, ocr, _, _, _, _ := resolveName(context.Background(), fakeSearcher{},
		[]string{"Blrgh", "Nonsense"})
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
	canonical, _, _, _, _, _ := resolveName(context.Background(), fs,
		[]string{"Inspired Fire deals + tam garbage xyz", "creature.", "flavor text"})
	if canonical != "" {
		t.Errorf("canonical = %q, want no match — the type-word fallback line must be skipped", canonical)
	}
	// The same text as the primary line still resolves: that IS the card.
	canonical, _, _, _, _, _ = resolveName(context.Background(), fs, []string{"Creature Guy"})
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
	resolveName(context.Background(), counting, lines)
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
	resolveName(context.Background(), counting, []string{"Blurred Junk", "Haste", "Flying"})
	if tries != 1 {
		t.Errorf("made %d lookups, want 1 — keyword fallback lines must not reach the searcher", tries)
	}
	resolveName(context.Background(), counting, []string{"Flash"})
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
	})
	if tries != 1 {
		t.Errorf("made %d lookups, want 1 — only the primary line", tries)
	}

	// A real title on a fallback line still resolves.
	fs := fakeSearcher{fuzzy: map[string]string{"Elspeth, Knight-Errant": "Elspeth, Knight-Errant"}}
	canonical, _, idx, _, _, _ := resolveName(context.Background(), fs,
		[]string{"blurred junk", "Elspeth, Knight-Errant"})
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

	// From the live camera, ctrl+o means reselect: the window closes and the
	// picker is offered again — the recovery key when a camera drops, the
	// same "choose a source" it means at the prompt.
	mm, _ = got.cancelToName()
	got = mm.(model)
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyCtrlO})
	if s := mm.(model).state; s != stateCameraBusy {
		t.Errorf("ctrl+o at the camera should reselect, got %v", s)
	}
	if mm.(model).session != nil {
		t.Error("reselecting should close the live window")
	}

	// From the prompt with no session, ctrl+o re-offers the picker too —
	// choosing a source is what the key means everywhere.
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

// The keys that left with the Continuity Camera must stay inert.
//
// Rotation (arrows), auto-framing (z) and the Video Effects panel (v) were all
// Mac-camera concepts: a landscape frame the Mac had to turn upright, Center
// Stage, and Studio Light. The phone answers none of them. This pins that they
// do nothing rather than falling through to something else — a stray binding
// here would fire mid-pile, on a step where every other key does something.
func TestRetiredCameraKeysDoNothingOnTheCaptureStep(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	got, sess := openCapture(t, m)

	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyLeft},
		{Type: tea.KeyRight},
		{Type: tea.KeyRunes, Runes: []rune("z")},
		{Type: tea.KeyRunes, Runes: []rune("v")},
	} {
		mm, _ := got.handleKey(key)
		got = mm.(model)
		if got.state != stateCapture {
			t.Fatalf("%v left the capture step for %v", key, got.state)
		}
		if got.statusErr {
			t.Errorf("%v raised an error banner; it should be ignored outright", key)
		}
	}
	if sess.captures != 0 || sess.autoOn+sess.autoOff != 0 || sess.torchOn+sess.torchOff != 0 {
		t.Errorf("a retired key reached the phone: %+v", sess)
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

// A truncated title plus the frame's own collector number identifies the
// card: the fragment nominates, the number confirms against the nominee's
// printings. Without the number — or with one that matches nothing — the
// nomination dies, which is what keeps the "Gliding" → Gliding Licid steal
// dead while truncated reads still resolve.
func TestPrefixNominationConfirmedByCollectorNumber(t *testing.T) {
	fs := fakeSearcher{
		fuzzy: map[string]string{"Elspeth": "Elspeth, Knight-Errant"},
		prints: map[string][]scryfall.Card{"Elspeth, Knight-Errant": {
			{ID: "eke-ala", Name: "Elspeth, Knight-Errant", Set: "ala",
				CollectorNumber: "9", Finishes: []string{"nonfoil"}},
		}},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	// Truncated title + a band number that matches the nominee's printing:
	// set+number corroboration, so it commits like any verified read.
	confirmed := scan.Card{Name: "Elspeth", Candidates: []string{"Elspeth"},
		SetCode: "ALA", CollectorNumber: "9", Confidence: 0.95}
	got := resolve(t, m, confirmed)
	if len(ra.got) != 1 || ra.got[0].Card.ID != "eke-ala" {
		t.Fatalf("confirmed nomination: adder got %+v, want the ala/9 printing", ra.got)
	}
	if len(got.review) != 0 {
		t.Errorf("confirmed nomination queued %d entries, want none", len(got.review))
	}

	// Same fragment, a number that matches nothing of the nominee's: the
	// nomination must die, not resolve — this is the steal shape.
	m2 := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m2, _ = openCapture(t, m2)
	denied := scan.Card{Name: "Elspeth", Candidates: []string{"Elspeth"},
		SetCode: "XXX", CollectorNumber: "99", Confidence: 0.95}
	got = resolve(t, m2, denied)
	if len(ra.got) != 1 {
		t.Fatalf("a non-matching number confirmed a nomination: adder got %+v", ra.got)
	}
	// A denied nomination stays unidentified, and unnamed reads land in the
	// receipt rather than the queue (the review gate).
	if len(got.review) != 0 || got.summary.Count("skipped") == 0 {
		t.Errorf("denied nomination: review=%d skipped=%d, want a receipt line and no queue entry",
			len(got.review), got.summary.Count("skipped"))
	}

	// Same fragment, no digits at all — the live steal exactly. The
	// nomination dies and the junk filter eats the unidentifiable capture;
	// what matters is that nothing resolved and nothing committed.
	m3 := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m3, _ = openCapture(t, m3)
	bare := scan.Card{Name: "Elspeth", Candidates: []string{"Elspeth"}, Confidence: 0.95}
	got = resolve(t, m3, bare)
	if len(ra.got) != 1 {
		t.Fatalf("a bare fragment resolved with no confirming number: adder got %+v", ra.got)
	}
	for _, q := range got.review {
		if q.canonical != "" {
			t.Errorf("bare fragment resolved to %q with no confirming number", q.canonical)
		}
	}
}

// reviewTiers counts the review flashes the phone was actually sent.
func reviewTiers(s *fakeSession) int {
	n := 0
	for _, r := range s.results {
		if r.Tier == tierReview {
			n++
		}
	}
	return n
}

// The decision ceiling expiring while a read is in flight must hold the
// review flash, not race it: Meltdown's commit landed 16ms after its review
// tone, live. If the read answers, no flash ever; if it lands without
// answering, the flash fires on the drain.
func TestCeilingHoldsWhileReadInFlight(t *testing.T) {
	_, fs := confidentFixture()
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, sess := openCapture(t, m)
	m.hudCapable = true

	// A card queues unverified — the flash defers behind the ceiling.
	it := queueItem{id: 1, canonical: "Meltdown", match: cardname.Match{Exact: true},
		prints: []scryfall.Card{{ID: "md-a", Name: "Meltdown", Set: "usg", CollectorNumber: "1", Finishes: []string{"nonfoil"}},
			{ID: "md-b", Name: "Meltdown", Set: "mh3", CollectorNumber: "418", Finishes: []string{"nonfoil"}}},
		rank: scanMatchNone, captureSeq: 1}
	mm, _ := m.Update(resolveDoneMsg{gen: m.resolveGen, item: it})
	m = mm.(model)
	if len(m.review) != 1 || reviewTiers(sess) != 0 {
		t.Fatalf("setup: review=%d flashes=%d, want 1 queued and the flash held",
			len(m.review), reviewTiers(sess))
	}

	// The ceiling expires with the retry's read still mid-resolve: held.
	m.resolving = 1
	mm, _ = m.Update(flashDeadlineMsg{name: "Meltdown"})
	m = mm.(model)
	if reviewTiers(sess) != 0 {
		t.Fatalf("the ceiling flashed over an in-flight read")
	}

	// The read lands and answers nothing (junk capture, killed): the drain
	// owes the operator the flash it held.
	junk := queueItem{id: 2, ocrLine: "I tample", captureSeq: 2}
	mm, _ = m.Update(resolveDoneMsg{gen: m.resolveGen, item: junk})
	m = mm.(model)
	if got := reviewTiers(sess); got != 1 {
		t.Errorf("flashes after the drain = %d, want exactly 1", got)
	}
}

// The rescue committing the queued card while the ceiling is overdue means no
// review flash at all — the question answered itself.
func TestOverdueFlashDiesWhenTheRescueCommits(t *testing.T) {
	printing := scryfall.Card{ID: "md-b", Name: "Meltdown", Set: "mh3",
		CollectorNumber: "418", ReleasedAt: "2024-06-14", Finishes: []string{"nonfoil"}}
	fs := fakeSearcher{fuzzy: map[string]string{"Meltdown": "Meltdown"},
		prints: map[string][]scryfall.Card{"Meltdown": {printing}}}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, sess := openCapture(t, m)
	m.hudCapable = true

	it := queueItem{id: 1, canonical: "Meltdown", match: cardname.Match{Exact: true},
		prints: []scryfall.Card{printing, {ID: "md-a", Name: "Meltdown", Set: "usg",
			CollectorNumber: "1", Finishes: []string{"nonfoil"}}},
		rank: scanMatchNone, captureSeq: 1}
	mm, _ := m.Update(resolveDoneMsg{gen: m.resolveGen, item: it})
	m = mm.(model)

	m.resolving = 1
	mm, _ = m.Update(flashDeadlineMsg{name: "Meltdown"})
	m = mm.(model)

	rescue := queueItem{id: 2, canonical: "Meltdown", match: cardname.Match{Exact: true},
		prints: []scryfall.Card{printing}, rank: scanMatchNumberAndYear, captureSeq: 2}
	mm, _ = m.Update(resolveDoneMsg{gen: m.resolveGen, item: rescue})
	m = mm.(model)
	if len(ra.got) != 1 {
		t.Fatalf("the rescue should commit, adder=%d", len(ra.got))
	}
	if got := reviewTiers(sess); got != 0 {
		t.Errorf("review flashes = %d, want 0 — the rescue answered the question", got)
	}
}

// An unidentified capture never reaches review: with footer evidence it goes
// to the session receipt ("Creature - Eldrazi" and "(unreadable)" entries
// offered the operator nothing but a discard, live), and its card gets its
// answer from the next fire — here, No-Dachi committing 834ms later. No
// flash at any point.
func TestUnnamedCaptureSkipsToTheReceiptNotReview(t *testing.T) {
	printing := scryfall.Card{ID: "nd", Name: "No-Dachi", Set: "chk",
		CollectorNumber: "264", Finishes: []string{"nonfoil"}}
	fs := fakeSearcher{fuzzy: map[string]string{"No-Dachi": "No-Dachi"},
		prints: map[string][]scryfall.Card{"No-Dachi": {printing}}}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, sess := openCapture(t, m)
	m.hudCapable = true
	m.autoCapable = true

	// Footer evidence spares the fragment from the junk kill; the receipt
	// gate keeps it out of the queue anyway.
	frag := queueItem{id: 1, ocrLine: "a Dach", captureSeq: 1,
		raw: scan.Card{CopyrightYear: 2004}}
	mm, _ := m.Update(resolveDoneMsg{gen: m.resolveGen, item: frag})
	m = mm.(model)
	if len(m.review) != 0 {
		t.Fatalf("an unnamed capture queued: review=%d, want 0", len(m.review))
	}
	if got := m.summary.Count("skipped"); got != 1 {
		t.Fatalf("receipt skipped = %d, want the unreadable capture accounted for", got)
	}
	if got := reviewTiers(sess); got != 0 {
		t.Fatalf("an unnamed capture flashed review: %d", got)
	}

	// The next capture commits the real card, business as usual.
	real := queueItem{id: 2, canonical: "No-Dachi", match: cardname.Match{Exact: true},
		prints: []scryfall.Card{printing}, rank: scanMatchSetAndNumber, captureSeq: 2}
	mm, _ = m.Update(resolveDoneMsg{gen: m.resolveGen, item: real})
	m = mm.(model)
	if len(ra.got) != 1 || len(m.review) != 0 || reviewTiers(sess) != 0 {
		t.Errorf("after the real read: adds=%d review=%d flashes=%d, want 1/0/0",
			len(ra.got), len(m.review), reviewTiers(sess))
	}
}

// An errored lookup still queues — a timeout is not a verdict about the card
// — and its held flash dies when the next capture commits the same physical
// moment's card.
func TestErroredUnnamedQueueFlashDiesOnTheNextCommit(t *testing.T) {
	printing := scryfall.Card{ID: "nd", Name: "No-Dachi", Set: "chk",
		CollectorNumber: "264", Finishes: []string{"nonfoil"}}
	fs := fakeSearcher{fuzzy: map[string]string{"No-Dachi": "No-Dachi"},
		prints: map[string][]scryfall.Card{"No-Dachi": {printing}}}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, sess := openCapture(t, m)
	m.hudCapable = true
	m.autoCapable = true

	frag := queueItem{id: 1, ocrLine: "a Dach", captureSeq: 1,
		errText: "lookup timed out"}
	mm, _ := m.Update(resolveDoneMsg{gen: m.resolveGen, item: frag})
	m = mm.(model)
	if len(m.review) != 1 || reviewTiers(sess) != 0 {
		t.Fatalf("setup: review=%d flashes=%d, want the errored read queued silently",
			len(m.review), reviewTiers(sess))
	}

	real := queueItem{id: 2, canonical: "No-Dachi", match: cardname.Match{Exact: true},
		prints: []scryfall.Card{printing}, rank: scanMatchSetAndNumber, captureSeq: 2}
	mm, _ = m.Update(resolveDoneMsg{gen: m.resolveGen, item: real})
	m = mm.(model)
	if len(ra.got) != 1 {
		t.Fatalf("the real card should commit, adder=%d", len(ra.got))
	}
	if got := reviewTiers(sess); got != 0 {
		t.Errorf("review flashes = %d, want 0 — the commit was the answer", got)
	}
	if len(m.review) != 1 {
		t.Errorf("review = %d, want the errored entry kept for the walk", len(m.review))
	}
}

// A type-shaped line is the card's TYPE row, never its title: it must not be
// searched (a fuzzy match can only name a wrong card) and must not queue.
func TestTypeLinesAreNeverSearchedOrQueued(t *testing.T) {
	for _, line := range []string{
		"Creature - Eldrazi", "Creature — Eldrazi Drone",
		"Legendary Artifact Creature ~ Golem", "Basic Snow Land — Island",
		"Instant = Arcane",
	} {
		if !typeLineShape(line) {
			t.Errorf("typeLineShape(%q) = false, want true", line)
		}
	}
	for _, line := range []string{
		"No-Dachi", "Snow-Covered Island", "Artifact Ward", "Fear",
		"Creature Guy", "Elspeth, Knight-Errant",
	} {
		if typeLineShape(line) {
			t.Errorf("typeLineShape(%q) = true — a real name misread as a type line", line)
		}
	}
	// End to end: a frame whose only line is a type row identifies nothing,
	// even though the searcher would happily fuzzy-match it to a card.
	fs := fakeSearcher{fuzzy: map[string]string{"Creature - Eldrazi": "Creature Guy"},
		prints: map[string][]scryfall.Card{"Creature Guy": {{ID: "cg", Name: "Creature Guy"}}}}
	canonical, _, _, _, _, err := resolveName(context.Background(), fs,
		[]string{"Creature - Eldrazi"})
	if err != nil || canonical != "" {
		t.Errorf("resolveName searched a type line: canonical=%q err=%v", canonical, err)
	}
}

// The second copy of an identical card, arriving via the queue-then-rescue
// path, must commit. Live (2026-08-07, Brainsurge playset): copy 2 queued as
// a real placement 8.4s after copy 1 committed, its rescue re-read arrived
// flagged `moved` — true only relative to the queued capture, since identical
// art makes copies indistinguishable — and the fromMoved arm suppressed it
// against the 9-second-old commit. The queue entry was already replaced, so
// the card vanished entirely: no commit, no review, one Brainsurge short.
func TestRescuedSecondCopyOfIdenticalCardCommits(t *testing.T) {
	printing := scryfall.Card{ID: "bs-mh3", Name: "Brainsurge", Set: "mh3",
		CollectorNumber: "399", ReleasedAt: "2024-06-14",
		Finishes: []string{"nonfoil", "foil"}}
	fs := fakeSearcher{
		fuzzy:  map[string]string{"Brainsurge": "Brainsurge"},
		prints: map[string][]scryfall.Card{"Brainsurge": {printing, {ID: "bs-old", Name: "Brainsurge", Set: "wwk", CollectorNumber: "26", Finishes: []string{"nonfoil", "foil"}}}},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)
	current := time.Date(2026, 8, 7, 23, 31, 51, 0, time.UTC)
	m.now = func() time.Time { return current }

	// Copy 1 commits off a verified read.
	it1 := queueItem{id: m.nextResolveID, canonical: "Brainsurge",
		match: cardname.Match{Exact: true}, prints: []scryfall.Card{printing},
		rank: scanMatchSetAndNumber, captureSeq: 1}
	mm, _ := m.Update(resolveDoneMsg{gen: m.resolveGen, item: it1})
	m = mm.(model)
	if len(ra.got) != 1 {
		t.Fatalf("copy 1: adder called %d times, want 1", len(ra.got))
	}

	// 8.4s later copy 2 is placed and its first read queues unverified.
	current = current.Add(8400 * time.Millisecond)
	it2 := queueItem{id: m.nextResolveID + 1, canonical: "Brainsurge",
		match: cardname.Match{Exact: true},
		prints: []scryfall.Card{printing, {ID: "bs-old", Name: "Brainsurge",
			Set: "wwk", CollectorNumber: "26", Finishes: []string{"nonfoil", "foil"}}},
		rank: scanMatchNone, captureSeq: 2}
	mm, _ = m.Update(resolveDoneMsg{gen: m.resolveGen, item: it2})
	m = mm.(model)
	if len(m.review) != 1 {
		t.Fatalf("copy 2's weak read should queue, review=%d", len(m.review))
	}

	// The rescue lands 700ms on, flagged moved — moved relative to the queued
	// capture, not to copy 1's ancient commit. It replaces the queue entry and
	// must commit, not evaporate.
	current = current.Add(700 * time.Millisecond)
	it3 := queueItem{id: m.nextResolveID + 2, canonical: "Brainsurge",
		match: cardname.Match{Exact: true}, prints: []scryfall.Card{printing},
		rank: scanMatchNumberAndYear, captureSeq: 3, fromMoved: true}
	mm, _ = m.Update(resolveDoneMsg{gen: m.resolveGen, item: it3})
	m = mm.(model)
	if len(ra.got) != 2 {
		t.Fatalf("the rescued second copy must commit: adder called %d times, want 2 (review=%d)",
			len(ra.got), len(m.review))
	}
	if len(m.review) != 0 {
		t.Errorf("the rescue should have consumed the queue entry, review=%d", len(m.review))
	}
}

// A deliberate slow playset of one printing with alternating finishes is
// three cards: each re-scan slower than the same-card floor commits, in both
// directions (nonfoil→foil→nonfoil and foil→nonfoil→foil), with no finish
// "correction" rewriting an earlier copy — corrections belong to fast
// re-reads of the same physical card, not to paced placements.
func TestSlowAlternatingFinishPlaysetCommitsEachCopy(t *testing.T) {
	printing := scryfall.Card{ID: "hs-lgn", Name: "Hollow Specter", Set: "lgn",
		CollectorNumber: "75", Finishes: []string{"nonfoil", "foil"}}
	for _, hints := range [][]string{{"", "foil", ""}, {"foil", "", "foil"}} {
		fs := fakeSearcher{
			fuzzy:  map[string]string{"Hollow Specter": "Hollow Specter"},
			prints: map[string][]scryfall.Card{"Hollow Specter": {printing}},
		}
		ra := &recordingAdder{}
		m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
		m, _ = openCapture(t, m)
		current := time.Date(2026, 8, 7, 23, 32, 41, 0, time.UTC)
		m.now = func() time.Time { return current }

		for i, hint := range hints {
			it := queueItem{id: m.nextResolveID + i, canonical: "Hollow Specter",
				match: cardname.Match{Exact: true}, prints: []scryfall.Card{printing},
				rank: scanMatchSetAndNumber, captureSeq: i + 1, finishHint: hint}
			mm, _ := m.Update(resolveDoneMsg{gen: m.resolveGen, item: it})
			m = mm.(model)
			current = current.Add(4 * time.Second) // slower than sameCardFloor
		}
		if len(ra.got) != 3 {
			t.Fatalf("hints %v: adder called %d times, want 3 — every paced copy commits",
				hints, len(ra.got))
		}
		for i, hint := range hints {
			want := "nonfoil"
			if hint == "foil" {
				want = "foil"
			}
			if ra.got[i].Finish != want || ra.got[i].ReplacesFinish != "" {
				t.Errorf("hints %v copy %d: finish=%q replaces=%q, want %q with no correction",
					hints, i+1, ra.got[i].Finish, ra.got[i].ReplacesFinish, want)
			}
		}
	}
}

// One physical card whose two reads land on two different printings must not
// commit twice. dupCapture keys on the printing id, so the second read (a
// degraded rank picking a different id moments later) used to walk past every
// duplicate rule and write a card nobody owns.
func TestSameCardOnDifferentPrintingDoesNotDoubleCommit(t *testing.T) {
	ev, fs := confidentFixture()
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	got := resolve(t, mm.(model), ev.CardList()[0])
	if len(ra.got) != 1 {
		t.Fatalf("first read committed %d times, want 1", len(ra.got))
	}
	first := ra.got[0].Card.ID

	// The same card again, moments later, off a weaker read that ranked a
	// *different* printing — corroborated enough that verdict says commit.
	var other scryfall.Card
	for _, p := range solRingPrints() {
		if p.ID != first {
			other = p
			break
		}
	}
	it := queueItem{
		id: got.nextResolveID, canonical: "Sol Ring",
		match:      cardname.Match{Exact: true},
		prints:     []scryfall.Card{other},
		rank:       scanMatchNumberAndYear,
		captureSeq: got.captureSeq + 1,
	}
	mm, _ = got.Update(resolveDoneMsg{gen: got.resolveGen, item: it})
	got = mm.(model)
	if len(ra.got) != 1 {
		t.Fatalf("the cross-printing re-read committed: adder called %d times, want 1", len(ra.got))
	}
	if !strings.Contains(got.status, "Still seeing Sol Ring") {
		t.Errorf("status = %q, want the still-seeing suppression message", got.status)
	}
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
	if _, s := rankByScanStrength(cards, "MH3", "123", 0, "", "", "", "", nil); s != scanMatchSetAndNumber {
		t.Errorf("set+number strength = %v, want scanMatchSetAndNumber", s)
	}
	if _, s := rankByScanStrength(cards, "", "263", 0, "", "", "", "", nil); s != scanMatchNumberOnly {
		t.Errorf("unique number strength = %v, want scanMatchNumberOnly", s)
	}
	dupes := []scryfall.Card{
		{ID: "a", Name: "X", Set: "aaa", CollectorNumber: "7"},
		{ID: "b", Name: "X", Set: "bbb", CollectorNumber: "7"},
	}
	if _, s := rankByScanStrength(dupes, "", "7", 0, "", "", "", "", nil); s != scanMatchNumberAmbiguous {
		t.Errorf("shared number strength = %v, want scanMatchNumberAmbiguous", s)
	}
	if _, s := rankByScanStrength(cards[:1], "", "", 0, "", "", "", "", nil); s != scanMatchSinglePrint {
		t.Errorf("single printing strength = %v, want scanMatchSinglePrint", s)
	}
	// A number that matches nothing makes even a lone printing suspect: the
	// name match may have landed on the wrong card entirely.
	if _, s := rankByScanStrength(cards[:1], "", "999", 0, "", "", "", "", nil); s != scanMatchNone {
		t.Errorf("conflicting number strength = %v, want scanMatchNone", s)
	}
	if _, s := rankByScanStrength(cards, "", "", 0, "", "", "", "", nil); s != scanMatchNone {
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
	_, before := rankByScanStrength(prints, "", "", 1995, "", "", "", "", nil)
	reordered, _ := applyBorderEvidence(prints, "white", 1995)
	_, after := rankByScanStrength(reordered, "", "", 1995, "", "", "", "", nil)
	if before != after {
		t.Errorf("rank moved from %v to %v after a border reorder", before, after)
	}
	// 1995 alone cannot separate 4ED from 4BB, and must not pretend to.
	if before != scanMatchNone {
		t.Errorf("year-only rank = %v, want scanMatchNone: 1995 is shared by "+
			"two printings, so the year settles nothing", before)
	}
}

// A read number pins a printing, and a misread border must not replace it.
// Live on 2026-08-06: Ornithopter read set=M15 num=223 lang=en — an exact
// A re-read that replaces a queued entry but still cannot commit must land
// back in review, not vanish.
//
// upgradeQueued removes the queued entry the moment a better-ranked read of
// the same name arrives. If that read then fails verdict anyway (better rank,
// still unverified) it heads for the queue — where the same-card drop rules
// used to swallow it, because its own first read put its name in
// recentNames moments ago. The entry it replaced was already gone, so the
// card ended represented nowhere: no commit, no review row, and a "Needs
// Review" flash promised for an entry that did not exist.
func TestUpgradedButUnverifiedReReadStaysInReview(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "a68", Name: "Gravel Slinger", Set: "ons", CollectorNumber: "68",
			ReleasedAt: "2002-10-07", Finishes: []string{"nonfoil"}},
		{ID: "b68", Name: "Gravel Slinger", Set: "mmq", CollectorNumber: "68",
			ReleasedAt: "1999-10-04", Finishes: []string{"nonfoil"}},
		{ID: "c12", Name: "Gravel Slinger", Set: "8ed", CollectorNumber: "12",
			ReleasedAt: "2003-07-28", Finishes: []string{"nonfoil"}},
	}
	fs := fakeSearcher{
		fuzzy:  map[string]string{"Gravel Slinger": "Gravel Slinger"},
		prints: map[string][]scryfall.Card{"Gravel Slinger": prints},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	clock := time.Date(2026, 8, 7, 12, 20, 1, 0, time.UTC)
	m.now = func() time.Time { return clock }
	m, _ = openCapture(t, m)

	// First read: name only. Three printings, nothing pinned — queues.
	blind := scan.Card{Name: "Gravel Slinger", Candidates: []string{"Gravel Slinger"},
		Confidence: 0.95, Source: "crop"}
	mm, _ := m.Update(m.resolveCardCmd(1, blind, 1)())
	got := mm.(model)
	if len(got.review) != 1 {
		t.Fatalf("setup: blind read should queue, review = %d", len(got.review))
	}

	// 900ms later: digits that match two printings, and a year that matches
	// neither of them — a strictly better rank that still cannot commit.
	clock = clock.Add(900 * time.Millisecond)
	got.captureSeq++
	better := scan.Card{Name: "Gravel Slinger", Candidates: []string{"Gravel Slinger"},
		CollectorNumber: "68", CopyrightYear: 1997, Confidence: 0.95, Source: "crop"}
	it := got.resolveCardCmd(2, better, 1)().(resolveDoneMsg).item
	if it.rank <= scanMatchNone {
		t.Fatalf("premise broken: the second read should outrank none, got %v", it.rank)
	}
	if auto, _, _ := verdict(it); auto {
		t.Fatalf("premise broken: the second read should still fail verdict")
	}
	mm, _ = got.Update(got.resolveCardCmd(2, better, 1)())
	got = mm.(model)
	if len(got.review) != 1 {
		t.Fatalf("review = %d, want the upgraded read re-queued, not vanished",
			len(got.review))
	}
	if got.review[0].rank <= scanMatchNone {
		t.Errorf("queued rank = %v, want the better read's rank kept", got.review[0].rank)
	}
	if len(ra.got) != 0 {
		t.Errorf("adds = %d, want none — nothing verified", len(ra.got))
	}
}

// The frame stratum runs behind a near-certain fuzzy name, not only an exact
// one. Live, 18:58 pile run: Consuming Corruption read at 95% similarity on
// both its capture and its rescue, the exact-only gate kept the frame
// evidence idle, and the card took the session's only review stop. The
// operator set the floor at 0.92; below it the bet still refuses.
func TestFramePickRunsBehindANearCertainName(t *testing.T) {
	twins := []scryfall.Card{
		{ID: "reg", Name: "Consuming Corruption", Set: "mh3", CollectorNumber: "102",
			ReleasedAt: "2024-06-14", Frame: "2015", Finishes: []string{"nonfoil", "foil"}},
		{ID: "retro", Name: "Consuming Corruption", Set: "mh3", CollectorNumber: "407",
			ReleasedAt: "2024-06-14", Frame: "1997", Finishes: []string{"nonfoil", "foil"}},
	}
	fs := fakeSearcher{
		fuzzy:  map[string]string{"Consuming Corrupticr": "Consuming Corruption"},
		prints: map[string][]scryfall.Card{"Consuming Corruption": twins},
		match: map[string]cardname.Match{
			"Consuming Corrupticr": {Similarity: 0.95}},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	c := scan.Card{Name: "Consuming Corrupticr", Candidates: []string{"Consuming Corrupticr"},
		CopyrightYear: 2024, FrameStyle: "retro", Confidence: 0.95, Source: "crop"}
	mm, _ := m.Update(m.resolveCardCmd(1, c, 1)())
	got := mm.(model)
	if len(ra.got) != 1 || ra.got[0].Card.ID != "retro" {
		t.Fatalf("adds = %+v, want the retro row committed off a 95%% name", ra.got)
	}
	if !ra.got[0].PrintingGuessed {
		t.Error("a fuzzy-name frame pick must still carry the guess flag")
	}

	// Below the floor the bet refuses, whatever the frame read said.
	fs.match["Consuming Corrupticr"] = cardname.Match{Similarity: 0.90}
	m2 := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m2, _ = openCapture(t, m2)
	before := len(ra.got)
	mm, _ = m2.Update(m2.resolveCardCmd(1, c, 1)())
	got = mm.(model)
	if len(ra.got) != before {
		t.Errorf("adds grew to %d, want a 90%% name refused", len(ra.got))
	}
	if len(got.review) != 1 {
		t.Errorf("review = %d, want the sub-floor read queued", len(got.review))
	}
}

// A frame-picked printing commits, and commits flagged: no digits confirmed
// it, so the row must be auditable against the physical card later.
func TestFramePickedPrintingCommitsFlagged(t *testing.T) {
	twins := []scryfall.Card{
		{ID: "reg", Name: "Brainsurge", Set: "mh3", CollectorNumber: "106",
			ReleasedAt: "2024-06-14", Frame: "2015", Finishes: []string{"nonfoil", "foil"}},
		{ID: "retro", Name: "Brainsurge", Set: "mh3", CollectorNumber: "399",
			ReleasedAt: "2024-06-14", Frame: "1997", Finishes: []string{"nonfoil", "foil"}},
	}
	fs := fakeSearcher{
		fuzzy:  map[string]string{"Brainsurge": "Brainsurge"},
		prints: map[string][]scryfall.Card{"Brainsurge": twins},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	c := scan.Card{Name: "Brainsurge", Candidates: []string{"Brainsurge"},
		CopyrightYear: 2024, FrameStyle: "retro", Confidence: 0.95, Source: "crop"}
	mm, _ := m.Update(m.resolveCardCmd(1, c, 1)())
	got := mm.(model)
	if len(ra.got) != 1 {
		t.Fatalf("adds = %d, want the frame-picked printing committed (review=%+v)",
			len(ra.got), got.review)
	}
	if ra.got[0].Card.ID != "retro" {
		t.Errorf("committed %s, want the retro-frame row", ra.got[0].Card.ID)
	}
	if !ra.got[0].PrintingGuessed {
		t.Error("PrintingGuessed = false, want the commit flagged — no digits confirmed it")
	}
}

// An alt collector block with no year of its own still ranks with the card's
// copyright year. The year belongs to the card — there is only one copyright
// line — but the ranker used to see only the winning block's Year field, so
// an alt carrying just digits silently discarded the year the log printed
// and a read that should have corroborated ranked a stratum lower.
func TestAltBlockInheritsTheCopyrightYear(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "mh3", Name: "Lion Umbra", Set: "mh3", CollectorNumber: "426",
			ReleasedAt: "2024-06-14", Finishes: []string{"nonfoil", "foil"}},
		{ID: "roe", Name: "Lion Umbra", Set: "roe", CollectorNumber: "36",
			ReleasedAt: "2010-04-23", Finishes: []string{"nonfoil"}},
	}
	fs := fakeSearcher{
		fuzzy:  map[string]string{"Lion Umbra": "Lion Umbra"},
		prints: map[string][]scryfall.Card{"Lion Umbra": prints},
	}
	m := newModel(context.Background(), fs, noopAdder, nil, "", nil)
	c := scan.Card{
		Name: "Lion Umbra", Candidates: []string{"Lion Umbra"},
		CopyrightYear: 2024,
		CollectorAlts: []scan.CollectorAlt{{Number: "426"}},
	}
	it := m.resolveCardCmd(1, c, 1)().(resolveDoneMsg).item
	if it.rank != scanMatchNumberAndYear {
		t.Fatalf("rank = %v, want number+year — the alt's digits plus the "+
			"card's copyright year", it.rank)
	}
	if it.prints[0].ID != "mh3" {
		t.Errorf("head = %q, want mh3", it.prints[0].ID)
	}
}

// set+number+lang pin — with the border misread as white on a black-bordered
// card. The border reorder then ruled the pinned M15 row out, promoted a
// borderless SLD sibling to the head, and the head is what commits: the
// session recorded SLD/604 foil from a card the phone had read correctly,
// with the foil coming from the promoted row being foil-only.
func TestPinnedPrintingSurvivesBorderMisread(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "sld", Name: "Ornithopter", Set: "sld", CollectorNumber: "604",
			Lang: "en", BorderColor: "borderless", ReleasedAt: "2022-04-22",
			Finishes: []string{"foil"}},
		{ID: "m15", Name: "Ornithopter", Set: "m15", CollectorNumber: "223",
			Lang: "en", BorderColor: "black", ReleasedAt: "2014-07-18",
			Finishes: []string{"nonfoil", "foil"}},
	}
	// The displacement is real: left to run, the border reorder demotes the
	// pinned row. This is what the guard is holding back.
	displaced, changed := applyBorderEvidence(prints, "white", 2014)
	if !changed || displaced[0].ID == "m15" {
		t.Fatalf("premise broken: a white misread should displace the black "+
			"M15 row (changed=%v head=%s)", changed, displaced[0].ID)
	}

	fs := fakeSearcher{
		fuzzy:  map[string]string{"Ornithopter": "Ornithopter"},
		prints: map[string][]scryfall.Card{"Ornithopter": prints},
	}
	m := newModel(context.Background(), fs, noopAdder, nil, "", nil)
	c := scan.Card{
		Name: "Ornithopter", Candidates: []string{"Ornithopter"},
		SetCode: "m15", CollectorNumber: "223", Language: "en",
		BorderColor: "white", CopyrightYear: 2014, FinishHint: "nonfoil",
	}
	it := m.resolveCardCmd(1, c, 1)().(resolveDoneMsg).item
	if it.rank != scanMatchSetNumberAndLang {
		t.Fatalf("rank = %v, want set+number+lang", it.rank)
	}
	if len(it.prints) == 0 || it.prints[0].ID != "m15" {
		t.Errorf("head = %q, want the pinned m15 row to survive the border misread",
			it.prints[0].ID)
	}
	auto, finish, note := verdict(it)
	if !auto || finish != "nonfoil" {
		t.Errorf("verdict = auto=%v finish=%q note=%q, want an unattended "+
			"nonfoil commit of the pinned printing", auto, finish, note)
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
	ranked, s := rankByScanStrength(variants, "", "", 0, "", "", "", "", nil)
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
	if _, s := rankByScanStrength(stars, "", "", 0, "", "", "", "", nil); s != scanMatchSinglePrint {
		t.Errorf("star pair strength = %v, want scanMatchSinglePrint", s)
	}
	// Variants across different sets are genuinely different printings.
	spread := []scryfall.Card{
		{ID: "a", Name: "X", Set: "7ed", CollectorNumber: "95"},
		{ID: "b", Name: "X", Set: "8ed", CollectorNumber: "95★"},
	}
	if _, s := rankByScanStrength(spread, "", "", 0, "", "", "", "", nil); s != scanMatchNone {
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
	ranked, s := rankByScanStrength(prints, "", "95", 2003, "", "", "", "", nil)
	if s != scanMatchNumberAndYear {
		t.Errorf("year-pinned strength = %v, want scanMatchNumberAndYear", s)
	}
	if ranked[0].ID != "8ed" {
		t.Errorf("ranked[0] = %s, want the printing released that year", ranked[0].ID)
	}
	// A misread year matches no printing and must leave the tie as it found
	// it — ambiguous queues, never a guessed commit.
	if _, s := rankByScanStrength(prints, "", "95", 2013, "", "", "", "", nil); s != scanMatchNumberAmbiguous {
		t.Errorf("misread year strength = %v, want scanMatchNumberAmbiguous", s)
	}
	// A year shared by both matches decides nothing either.
	same := []scryfall.Card{
		{ID: "a", Name: "X", Set: "aaa", CollectorNumber: "7", ReleasedAt: "2003-01-01"},
		{ID: "b", Name: "X", Set: "bbb", CollectorNumber: "7", ReleasedAt: "2003-06-01"},
	}
	if _, s := rankByScanStrength(same, "", "7", 2003, "", "", "", "", nil); s != scanMatchNumberAmbiguous {
		t.Errorf("shared year strength = %v, want scanMatchNumberAmbiguous", s)
	}
	// The year never overrides a full set+number verification.
	if _, s := rankByScanStrength(prints, "7ed", "95", 2003, "", "", "", "", nil); s != scanMatchSetAndNumber {
		t.Errorf("set+number with year = %v, want scanMatchSetAndNumber", s)
	}
}

// A second copy of a card commits rather than stopping the session — provided
// enough time passed for a hand to have swapped it.
//
// Committing at all used to queue. It cost three stops on a playset of four,
// and it was not even consistent: a copy scanned past the window committed
// anyway, so the same physical action gave different answers depending on how
// fast you were. What bounds it now is the one thing a person cannot fake —
// nobody swaps a card in under 3856ms. See sameCardFloor.
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

	// The same card a whole swap later: a second copy, committed.
	clock = clock.Add(sameCardFloor + time.Second)
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true, ev: ev})
	got = resolve(t, mm.(model), ev.CardList()[0])
	if len(ra.got) != 2 {
		t.Fatalf("a second copy should commit, adder got %d", len(ra.got))
	}
	if len(got.review) != 0 {
		t.Fatalf("review = %+v, want nothing queued", got.review)
	}

	// And past the window, which used to be the only way it committed. Same
	// answer either side of that boundary now, which is the point.
	clock = clock.Add(dupWindow + time.Second)
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true, ev: ev})
	got = resolve(t, mm.(model), ev.CardList()[0])
	if len(ra.got) != 3 {
		t.Errorf("timing must not change the answer, adder got %d", len(ra.got))
	}
}

// The same card again a second later is the card that is already there.
//
// Replayed from the session of 2026-08-05 19:21, where Skirk Volcanist
// committed five times in six seconds while sitting still on the desk. Every
// repeat arrived tagged `replaced`, because the phone's trigger cannot tell a
// card that *moved* from one swapped in place — so the Mac stops believing that
// field on a repeat this fast and believes the clock instead.
func TestFastRepeatOfTheSameCardIsDropped(t *testing.T) {
	ev, fs := confidentFixture()
	ev.FireReason = scan.FireReplaced
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	clock := time.Date(2026, 8, 5, 19, 21, 57, 0, time.UTC)
	m.now = func() time.Time { return clock }
	m, _ = openCapture(t, m)

	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	got := resolve(t, mm.(model), ev.CardList()[0])
	if len(ra.got) != 1 {
		t.Fatalf("first scan should commit, adder got %d", len(ra.got))
	}

	// The real gaps from that burst, in order. Each one re-anchors the floor,
	// which is what stops the third and fourth slipping past a fixed three
	// seconds measured from the original commit.
	for _, gap := range []time.Duration{931, 1604, 932, 2595} {
		clock = clock.Add(gap * time.Millisecond)
		mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true, ev: ev})
		got = resolve(t, mm.(model), ev.CardList()[0])
	}
	if len(ra.got) != 1 {
		t.Errorf("adder called %d times, want 1 — four re-reads of one card", len(ra.got))
	}
	if len(got.review) != 0 {
		t.Errorf("review = %+v, want the repeats dropped rather than queued", got.review)
	}
	if !strings.Contains(got.status, "Still seeing") {
		t.Errorf("status = %q, want the still-seeing note", got.status)
	}

	// And the card genuinely swapped afterwards still lands.
	clock = clock.Add(sameCardFloor + time.Second)
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true, ev: ev})
	got = resolve(t, mm.(model), ev.CardList()[0])
	if len(ra.got) != 2 {
		t.Errorf("adder called %d times, want the real second copy to commit", len(ra.got))
	}
}

// A repeat the source itself calls a *move* is dropped however long ago the
// card was last seen.
//
// The clock floor catches a fast repeat, but the phone can do better than a
// clock: it watched a box hold the watched spot while still looking like the
// card it had already read, and said so. Two of the eight duplicates in the
// session that prompted this were 5.4s and 7.1s apart — past any defensible
// time floor, and still the same card sitting there.
func TestMovedRepeatIsDroppedWhateverTheClockSays(t *testing.T) {
	ev, fs := confidentFixture()
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	clock := time.Date(2026, 8, 5, 19, 21, 22, 0, time.UTC)
	m.now = func() time.Time { return clock }
	m, _ = openCapture(t, m)

	placed := ev
	placed.FireReason = scan.FireRemoved
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: placed})
	got := resolve(t, mm.(model), placed.CardList()[0])
	if len(ra.got) != 1 {
		t.Fatalf("first scan should commit, adder got %d", len(ra.got))
	}

	// Seven seconds later — comfortably past the floor — but the source says
	// the card only moved.
	moved := ev
	moved.FireReason = scan.FireMoved
	clock = clock.Add(7 * time.Second)
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true, ev: moved})
	got = resolve(t, mm.(model), moved.CardList()[0])
	if len(ra.got) != 1 {
		t.Errorf("adder called %d times, want 1 — the source said it only moved", len(ra.got))
	}
	if len(got.review) != 0 {
		t.Errorf("review = %+v, want the move dropped rather than queued", got.review)
	}

	// And a genuine placement at the same remove still commits, so the rule is
	// about the evidence rather than about the gap.
	clock = clock.Add(7 * time.Second)
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true, ev: placed})
	got = resolve(t, mm.(model), placed.CardList()[0])
	if len(ra.got) != 2 {
		t.Errorf("adder called %d times, want a witnessed placement to commit", len(ra.got))
	}
}

// The 19:33 pile run's two phantom reviews, pinned.
//
// "Gliding" — half of Glowrider mid-slide — fuzzy-matched a different card
// at 71% and queued "uncertain name match"; a nameless numberless read with
// only a year queued "nothing readable" 2.5s behind the same commit. A wrong
// canonical walks past every name-keyed rule, and a year alone failed the
// old digits-required footer echo.
func TestSlideDebrisAfterACommitIsDropped(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "lgn15", Name: "Glowrider", Set: "lgn", CollectorNumber: "15",
			ReleasedAt: "2003-01-31", Finishes: []string{"nonfoil", "foil"}},
	}
	fs := fakeSearcher{
		fuzzy: map[string]string{"Glowrider": "Glowrider", "Gliding": "Gliding Sprite"},
		prints: map[string][]scryfall.Card{
			"Glowrider": prints,
			"Gliding Sprite": {{ID: "x", Name: "Gliding Sprite", Set: "abc",
				CollectorNumber: "9", ReleasedAt: "1999-01-01",
				Finishes: []string{"nonfoil"}}},
		},
		match: map[string]cardname.Match{"Gliding": {Similarity: 0.71}},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	clock := time.Date(2026, 8, 7, 19, 33, 18, 0, time.UTC)
	m.now = func() time.Time { return clock }
	m, _ = openCapture(t, m)

	good := scan.Card{Name: "Glowrider", Candidates: []string{"Glowrider"},
		CollectorNumber: "15", NumberSource: "copyright", CopyrightYear: 2003,
		Confidence: 0.95, Source: "crop"}
	mm, _ := m.Update(m.resolveCardCmd(1, good, 1)())
	got := mm.(model)
	if len(ra.got) != 1 {
		t.Fatalf("setup: Glowrider should commit, adds = %d", len(ra.got))
	}

	// 1.8s later: the wrong-card mangle.
	clock = clock.Add(1798 * time.Millisecond)
	got.captureSeq++
	mangle := scan.Card{Name: "Gliding", Candidates: []string{"Gliding"},
		Confidence: 0.6, Source: "crop"}
	mm, _ = got.Update(got.resolveCardCmd(2, mangle, 1)())
	got = mm.(model)
	if len(got.review) != 0 {
		t.Errorf("review = %+v, want the wrong-card slide mangle dropped", got.review)
	}

	// 2.5s after the commit: nameless, numberless, year-only.
	clock = clock.Add(719 * time.Millisecond)
	got.captureSeq++
	ghost := scan.Card{CopyrightYear: 2003, Source: "crop"}
	mm, _ = got.Update(got.resolveCardCmd(3, ghost, 1)())
	got = mm.(model)
	if len(got.review) != 0 {
		t.Errorf("review = %+v, want the year-only ghost dropped as a footer echo", got.review)
	}
	if len(ra.got) != 1 {
		t.Errorf("adds = %d, want nothing beyond Glowrider", len(ra.got))
	}
}

// A mid-slide mangle of a card just handled is dropped, not queued.
//
// Live, 2026-08-07 pile session: sliding Hollow Specter off the pile put
// "AMN Spectes" in front of the lens — half a title plus debris. It resolved
// to nothing, carried a copyright year (so the junk filter rightly spared
// it: digits prove a card was in frame), and queued as `couldn't identify
// "AMN Spectes"` — a review stop with nothing actionable in it.
func TestMidSlideMangleIsDropped(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "lgn75", Name: "Hollow Specter", Set: "lgn", CollectorNumber: "75",
			ReleasedAt: "2003-01-31", Finishes: []string{"nonfoil", "foil"}},
	}
	fs := fakeSearcher{
		fuzzy:  map[string]string{"Hollow Specter": "Hollow Specter"},
		prints: map[string][]scryfall.Card{"Hollow Specter": prints},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	clock := time.Date(2026, 8, 7, 13, 54, 12, 0, time.UTC)
	m.now = func() time.Time { return clock }
	m, _ = openCapture(t, m)

	good := scan.Card{Name: "Hollow Specter", Candidates: []string{"Hollow Specter"},
		CollectorNumber: "75", NumberSource: "copyright", CopyrightYear: 2003,
		Confidence: 0.95, Source: "crop"}
	mm, _ := m.Update(m.resolveCardCmd(1, good, 1)())
	got := mm.(model)
	if len(ra.got) != 1 {
		t.Fatalf("setup: the clean read should commit, adds = %d", len(ra.got))
	}

	// 5.1s later, the slide: half a title, a year, no identity.
	clock = clock.Add(5111 * time.Millisecond)
	got.captureSeq++
	mangle := scan.Card{Name: "AMN Spectes", Candidates: []string{"AMN Spectes"},
		CopyrightYear: 2003, Confidence: 0.4, Source: "crop"}
	mm, _ = got.Update(got.resolveCardCmd(2, mangle, 1)())
	got = mm.(model)
	if len(got.review) != 0 {
		t.Errorf("review = %+v, want the mangle dropped", got.review)
	}
	if len(ra.got) != 1 {
		t.Errorf("adds = %d, want nothing new", len(ra.got))
	}

	// An unidentifiable line that resembles nothing recent is not an echo —
	// it lands in the receipt (unnamed reads never queue since the review
	// gate; the next fire re-reads the card) rather than being silently
	// swallowed like the mangle above.
	clock = clock.Add(2 * time.Second)
	got.captureSeq++
	skippedBefore := got.summary.Count("skipped")
	unrelated := scan.Card{Name: "Zzyzx Qwrbl", Candidates: []string{"Zzyzx Qwrbl"},
		CopyrightYear: 1997, Confidence: 0.4, Source: "crop"}
	mm, _ = got.Update(got.resolveCardCmd(3, unrelated, 1)())
	got = mm.(model)
	if len(got.review) != 0 || got.summary.Count("skipped") != skippedBefore+1 {
		t.Errorf("review=%d skipped=%d, want the unrelated unreadable read accounted in the receipt, not queued",
			len(got.review), got.summary.Count("skipped"))
	}
}

// The live pile-mode triple: one hand-held foil, three fires in 2.5s, every
// face "decisive" — one row, finish corrected, no phantom copies.
//
// Trap Digger, 2026-08-07: committed nonfoil-guessed off a footerless first
// read, then re-read twice as the hand shifted (`replaced` face=32.5, then
// 26.5, both over placementFaceFloor) and committed three rows. The wanted
// shape: the first repeat carries the foil marker the first read missed and
// re-keys the row (guess → evidence); the second repeat is a plain drop.
func TestPileRepeatCorrectsInsteadOfDuplicating(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "scg24", Name: "Trap Digger", Set: "scg", CollectorNumber: "24",
			ReleasedAt: "2003-05-26", Finishes: []string{"nonfoil", "foil"}},
	}
	fs := fakeSearcher{
		fuzzy:  map[string]string{"Trap Digger": "Trap Digger"},
		prints: map[string][]scryfall.Card{"Trap Digger": prints},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	clock := time.Date(2026, 8, 7, 13, 54, 9, 0, time.UTC)
	m.now = func() time.Time { return clock }
	m, _ = openCapture(t, m)

	// Footerless first read: single printing, no finish marker → nonfoil
	// guessed.
	first := scan.Card{Name: "Trap Digger", Candidates: []string{"Trap Digger"},
		Confidence: 0.95, Source: "crop"}
	got := resolve(t, m, first)
	if len(ra.got) != 1 || !ra.got[0].FinishGuessed || ra.got[0].Finish != "nonfoil" {
		t.Fatalf("setup: first read should commit nonfoil guessed, got %+v", ra.got)
	}

	// 1.6s later, "decisive" face on the same card — and a real foil marker.
	clock = clock.Add(1602 * time.Millisecond)
	got.captureSeq++
	got.lastScanReplaced = true
	face := 32.5
	got.lastScanFaceDelta = &face
	foilRead := scan.Card{Name: "Trap Digger", Candidates: []string{"Trap Digger"},
		FinishHint: "foil", Confidence: 0.95, Source: "crop"}
	got = resolve(t, got, foilRead)
	if len(ra.got) != 2 {
		t.Fatalf("adds = %d, want the correction and nothing else", len(ra.got))
	}
	if ra.got[1].ReplacesFinish != "nonfoil" || ra.got[1].Finish != "foil" {
		t.Errorf("second write = %+v, want a nonfoil→foil re-key, not a new row", ra.got[1])
	}

	// 0.95s later, another "decisive" face, foil again: nothing new — drop.
	clock = clock.Add(946 * time.Millisecond)
	got.captureSeq++
	face2 := 26.5
	got.lastScanFaceDelta = &face2
	got = resolve(t, got, foilRead)
	if len(ra.got) != 2 {
		t.Errorf("adds = %d, want the third sighting dropped", len(ra.got))
	}
	if len(got.review) != 0 {
		t.Errorf("review = %+v, want no stop for a card already written", got.review)
	}
}

// A same-printing repeat under the floor is suppressed and offered back on
// the recovery key — whatever the face measurement claimed.
//
// This contract has flipped once, each direction backed by a live session.
// First: two physical No-Dachi stacked 1671ms apart, the phone said
// `replaced` face=32.5, the floor threw it away, and the copy waited 73.5s —
// so a decisive face was allowed to override the floor. Then hand-held pile
// scanning showed the measurement cannot carry that weight: a foil's face
// moves with the hand — the same Trap Digger read face=30.4/32.5/26.5 across
// three fires, all "decisive", and committed three rows in 2.5s. Identity
// (the resolve naming the printing just written) now outranks the face; the
// stacked-copy case pays one keystroke on pendingDup instead of the
// collection growing phantom rows.
func TestSameCardFloorOutranksADecisiveFace(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason string
		face   *float64
		want   int // adds after the repeat, including the first copy
	}{
		// This row used to want 2: a `replaced` at face=32.5 overrode the
		// floor and committed (the No-Dachi stack). Hand-held pile scanning
		// refuted the measurement — the same card reads face=26.5-32.5 as
		// the hand shifts a foil, and one Trap Digger committed three rows
		// in 2.5s through this override. Identity now outranks the face
		// under the floor; the fast-stacked real copy is what the pendingDup
		// recovery key is for, and the assertions below insist it is offered.
		{"decisive replacement", scan.FireReplaced, fp(32.5), 1},
		// The other side of the same measurement, from the same session.
		{"moved", scan.FireMoved, fp(15.8), 1},
		// Over movedFaceMax but under our margin: the source's claim is real
		// and weak, so the clock keeps it.
		{"marginal replacement", scan.FireReplaced, fp(20.1), 1},
		// A claim with nothing behind it is a boolean, and a boolean from an
		// interpolated threshold is what the floor exists to distrust.
		{"replacement with no measurement", scan.FireReplaced, nil, 1},
		// `removed` says the captured card left the watched rect — equally
		// what picking it up and setting it down does. Never a placement
		// claim, whatever number rides along.
		{"removed", scan.FireRemoved, fp(40.0), 1},
		// A helper too old to send any of this falls back to the clock, which
		// is the compatibility path the floor must keep serving.
		{"old helper", "", nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ev, fs := confidentFixture()
			ra := &recordingAdder{}
			m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
			clock := time.Date(2026, 8, 5, 23, 54, 51, 0, time.UTC)
			m.now = func() time.Time { return clock }
			m, _ = openCapture(t, m)

			first := ev
			first.FireReason = scan.FireRemoved
			mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: first})
			got := resolve(t, mm.(model), first.CardList()[0])
			if len(ra.got) != 1 {
				t.Fatalf("setup: first copy should commit, adds = %d", len(ra.got))
			}

			// The gap from the live session, comfortably under the floor.
			clock = clock.Add(1671 * time.Millisecond)
			repeat := ev
			repeat.FireReason = tc.reason
			repeat.FaceDelta = tc.face
			mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true, ev: repeat})
			got = resolve(t, mm.(model), repeat.CardList()[0])

			if len(ra.got) != tc.want {
				t.Errorf("adds = %d, want %d", len(ra.got), tc.want)
			}
			if len(got.review) != 0 {
				t.Errorf("review = %+v, want the repeat decided without a stop", got.review)
			}
			// Whatever the verdict, a suppressed copy must be recoverable and
			// must say so — the silence is what made the live drop cost a card.
			if tc.want == 1 {
				if got.pending == nil {
					t.Error("pending = nil, want the suppressed copy held for +")
				}
				if !strings.Contains(got.status, "press +") {
					t.Errorf("status = %q, want it to offer the recovery key", got.status)
				}
			}
		})
	}
}

// The floor still governs everything the source cannot answer for, including a
// gap wide enough to have been a real swap.
func TestSameCardFloorStillCommitsADeliberateReScan(t *testing.T) {
	ev, fs := confidentFixture()
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	clock := time.Date(2026, 8, 5, 23, 54, 51, 0, time.UTC)
	m.now = func() time.Time { return clock }
	m, _ = openCapture(t, m)

	first := ev
	first.FireReason = scan.FireRemoved
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: first})
	got := resolve(t, mm.(model), first.CardList()[0])

	// Past the floor, and the source offers no measurement at all.
	clock = clock.Add(sameCardFloor + time.Second)
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true, ev: first})
	got = resolve(t, mm.(model), first.CardList()[0])
	if len(ra.got) != 2 {
		t.Errorf("adds = %d, want a deliberate re-scan to commit", len(ra.got))
	}
}

// A suppressed copy is written when the operator says it was real.
//
// The point of the key is the asymmetry it removes. Every rule above judges a
// physical act nobody in this process witnessed, and placementFaceFloor is
// fitted on a single negative sample; being wrong used to cost a card with no
// trace, and now costs one keystroke.
func TestPlusPromotesTheSuppressedCopy(t *testing.T) {
	ev, fs := confidentFixture()
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	clock := time.Date(2026, 8, 5, 23, 54, 51, 0, time.UTC)
	m.now = func() time.Time { return clock }
	m, _ = openCapture(t, m)

	first := ev
	first.FireReason = scan.FireRemoved
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: first})
	got := resolve(t, mm.(model), first.CardList()[0])

	// A repeat the rules suppress: too fast, and no measurement behind it.
	clock = clock.Add(900 * time.Millisecond)
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true, ev: first})
	got = resolve(t, mm.(model), first.CardList()[0])
	if len(ra.got) != 1 || got.pending == nil {
		t.Fatalf("setup: adds = %d, pending = %v, want one add and a held copy",
			len(ra.got), got.pending != nil)
	}

	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})
	got = mm.(model)
	if len(ra.got) != 2 {
		t.Fatalf("adds = %d, want + to write the second copy", len(ra.got))
	}
	if got.pending != nil {
		t.Error("pending survived the promotion — + would write a third copy")
	}
	if got.summary.Count("duplicate-confirmed") != 1 {
		t.Errorf("summary = %+v, want the promotion on the receipt", got.summary.Entries)
	}

	// Pressing it again answers rather than silently doing nothing: the status
	// line invited the key, so a no-op would read as a missed keystroke.
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})
	got = mm.(model)
	if len(ra.got) != 2 {
		t.Errorf("adds = %d, want a second press to add nothing", len(ra.got))
	}
	if !strings.Contains(got.status, "Nothing to add") {
		t.Errorf("status = %q, want the empty-slot answer", got.status)
	}
}

// A promotion offered past its window is refused rather than written.
func TestStalePendingCopyIsNotPromoted(t *testing.T) {
	ev, fs := confidentFixture()
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	clock := time.Date(2026, 8, 5, 23, 54, 51, 0, time.UTC)
	m.now = func() time.Time { return clock }
	m, _ = openCapture(t, m)

	first := ev
	first.FireReason = scan.FireRemoved
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: first})
	got := resolve(t, mm.(model), first.CardList()[0])
	clock = clock.Add(900 * time.Millisecond)
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true, ev: first})
	got = resolve(t, mm.(model), first.CardList()[0])

	clock = clock.Add(pendingDupWindow + time.Second)
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("+")})
	got = mm.(model)
	if len(ra.got) != 1 {
		t.Errorf("adds = %d, want a stale copy left unwritten", len(ra.got))
	}
}

func TestResolveResultsLandOutOfOrderAndAfterTabbingAway(t *testing.T) {
	// Two captures in flight; the user tabs into the review list before either
	// resolves. Both land regardless of UI state, out of order.
	//
	// Two distinct names that resolve but cannot verify a printing, so both
	// genuinely queue. (This test once used unnamed footer-evidence reads,
	// but those go to the receipt now, not the queue.)
	twoPrints := func(name string) []scryfall.Card {
		return []scryfall.Card{
			{ID: name + "-a", Name: name, Set: "aaa", CollectorNumber: "1", Finishes: []string{"nonfoil"}},
			{ID: name + "-b", Name: name, Set: "bbb", CollectorNumber: "2", Finishes: []string{"nonfoil"}},
		}
	}
	fs := fakeSearcher{
		fuzzy: map[string]string{"Meltdown": "Meltdown", "Brainsurge": "Brainsurge"},
		prints: map[string][]scryfall.Card{
			"Meltdown": twoPrints("Meltdown"), "Brainsurge": twoPrints("Brainsurge")},
	}
	m := newModel(context.Background(), fs, noopAdder, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	ev := func(name string) scan.Event {
		return scan.Event{Kind: scan.EventScan, Name: name, Candidates: []string{name},
			Cards: []scan.Card{{Name: name, Candidates: []string{name}}}}
	}
	unknown := ev("Meltdown")
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: unknown})
	got := mm.(model)
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true, ev: ev("Brainsurge")})
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
	second := got.resolveCardCmd(2, ev("Brainsurge").CardList()[0], 1)().(resolveDoneMsg)
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

// A swallowed echo re-arms the recheck instead of ending it.
//
// This branch returned nil, which stopped the loop after a single echo. The
// card then sat unprocessed until the phone re-fired on its own — 73.5s in the
// session that surfaced it, against a suppression window of ten. Every other
// drop in onResolveDone reschedules; this was the outlier.
func TestNudgeEchoReschedulesTheRecheck(t *testing.T) {
	ev, fs := confidentFixture()
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	clock := time.Date(2026, 8, 5, 23, 54, 51, 0, time.UTC)
	m.now = func() time.Time { return clock }
	m, _ = openCapture(t, m)

	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
	got := resolve(t, mm.(model), ev.CardList()[0])

	got.nudgeSentAt = got.now()
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true, ev: ev})
	got = mm.(model)
	mm, cmd := got.Update(got.resolveCardCmd(got.nextResolveID, ev.CardList()[0], 1)())
	got = mm.(model)
	if got.nudgeDrops != 1 {
		t.Fatalf("setup: nudgeDrops = %d, want the echo swallowed", got.nudgeDrops)
	}
	if cmd == nil {
		t.Error("the echo returned no command, so the recheck loop ends here " +
			"and the card waits on the phone")
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
	clock := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return clock }
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

	// The same card again, fired by real disruption rather than the nudge, and
	// a whole swap later: a placement the phone watched happen, at a speed a
	// person could actually have managed, so a second copy commits.
	got.nudgeSentAt = time.Time{}
	clock = clock.Add(sameCardFloor + time.Second)
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

	// The same unresolvable read from a single-card capture dies too. It used
	// to queue, on the reasoning that the only card of a shot must never
	// vanish silently — but a capture that read no name and nothing off a
	// footer held no card to vanish, and the queue entry offered one action:
	// discard. A live session of 25 left three of these in review.
	mm, _ = got.Update(got.resolveCardCmd(3, ev.Cards[1], 1)())
	got = mm.(model)
	if len(got.review) != 0 {
		t.Fatalf("a single-card miss should be ignored, review = %+v", got.review)
	}
	if got.ignored != 2 {
		t.Errorf("ignored = %d, want 2 — both misses counted for the receipt", got.ignored)
	}

	// Printing evidence spares it from the junk kill — a footer read is proof
	// a card was in frame — but proof of a card is not a review question, and
	// an unnamed entry offers only a discard. It lands in the receipt; the
	// next fire re-reads whatever is actually there.
	withFooter := scan.Card{Name: "Survey the Realm", Candidates: []string{"Survey the Realm"},
		CollectorNumber: "412", NumberSource: "copyright"}
	skippedBefore := got.summary.Count("skipped")
	mm, _ = got.Update(got.resolveCardCmd(4, withFooter, 1)())
	got = mm.(model)
	if len(got.review) != 0 || got.summary.Count("skipped") != skippedBefore+1 {
		t.Errorf("review=%d skipped=%d, want the footer read in the receipt, not the queue",
			len(got.review), got.summary.Count("skipped"))
	}
}

// The three shapes of junk a live session actually produced, none of which
// should reach review.
//
// From the session of 2026-08-05 20:00, 25 outcomes: a capture that read
// literally nothing, a fragment of rules copy, and a mangled word off a card
// whose crop was never located. Each queued, and each offered the user exactly
// one action — discard.
func TestUnidentifiableCapturesNeverReachReview(t *testing.T) {
	for _, tc := range []struct {
		name string
		card scan.Card
	}{
		{"a capture that read nothing at all",
			scan.Card{Source: "crop"}},
		{"a fragment of rules text",
			scan.Card{Name: "creature, then put a +1/+1 counter",
				Candidates: []string{"creature, then put a +1/+1 counter",
					"on each creature you control with a"},
				Source: "crop"}},
		{"a mangled word with no card located",
			scan.Card{Name: "I tample", Candidates: []string{"I tample"},
				Source: "frame"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
			m, _ = openCapture(t, m)
			mm, _ := m.Update(m.resolveCardCmd(1, tc.card, 1)())
			got := mm.(model)
			if len(got.review) != 0 {
				t.Errorf("review = %+v, want nothing queued", got.review)
			}
			if got.ignored != 1 {
				t.Errorf("ignored = %d, want it counted for the receipt", got.ignored)
			}
			if got.statusErr {
				t.Error("ignoring a non-card is not an error")
			}
		})
	}
}

// A worse re-read of a card just committed is dropped, not queued.
//
// Live: Ancient Silverback committed on M15/168, and 918ms later the same card
// came back with no collector number and a set code of "TAP" scavenged out of
// its rules text. It ranked nothing, so it could not pin a printing among
// seven, and it queued as "printing unverified" — asking the user a question
// the capture before it had already answered.
//
// The same-card floor guarded only the commit path, so a repeat whose read
// degraded slipped straight past it.
func TestWorseReReadOfACommittedCardIsDropped(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "m15", Name: "Ancient Silverback", Set: "m15", CollectorNumber: "168",
			ReleasedAt: "2014-07-18", Finishes: []string{"nonfoil"}},
		{ID: "8ed", Name: "Ancient Silverback", Set: "8ed", CollectorNumber: "236",
			ReleasedAt: "2003-07-28", Finishes: []string{"nonfoil"}},
	}
	fs := fakeSearcher{
		fuzzy:  map[string]string{"Ancient Silverback": "Ancient Silverback"},
		prints: map[string][]scryfall.Card{"Ancient Silverback": prints},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	clock := time.Date(2026, 8, 5, 20, 1, 13, 0, time.UTC)
	m.now = func() time.Time { return clock }
	m, _ = openCapture(t, m)

	// The good read: set and number, which pins the printing.
	good := scan.Card{Name: "Ancient Silverback", Candidates: []string{"Ancient Silverback"},
		SetCode: "M15", CollectorNumber: "168", Confidence: 0.95, Source: "crop"}
	mm, _ := m.Update(m.resolveCardCmd(1, good, 1)())
	got := mm.(model)
	if len(ra.got) != 1 {
		t.Fatalf("setup: the good read should commit, adds = %d", len(ra.got))
	}

	// 918ms later: no number, and a set code scraped out of the rules text.
	clock = clock.Add(918 * time.Millisecond)
	worse := scan.Card{Name: "Ancient Silverback", Candidates: []string{"Ancient Silverback", "4 c"},
		SetCode: "TAP", Confidence: 0.95, Source: "crop"}
	mm, _ = got.Update(got.resolveCardCmd(2, worse, 1)())
	got = mm.(model)
	if len(got.review) != 0 {
		t.Errorf("review = %+v, want the worse re-read dropped", got.review)
	}
	if len(ra.got) != 1 {
		t.Errorf("adds = %d, want no second copy either", len(ra.got))
	}
	if !strings.Contains(got.status, "Still seeing Ancient Silverback") {
		t.Errorf("status = %q, want the still-seeing note", got.status)
	}

	// A genuine second copy, read just as poorly but a swap later, still
	// queues: the rule is about how recently the card was seen, not about the
	// quality of the read.
	clock = clock.Add(sameCardFloor + time.Second)
	mm, _ = got.Update(got.resolveCardCmd(3, worse, 1)())
	got = mm.(model)
	if len(got.review) != 1 {
		t.Errorf("review = %d, want a later poor read to reach review", len(got.review))
	}
}

// A nameless footer echo of a card just committed is dropped, not queued.
//
// Live, 2026-08-07: Trap Digger committed on SCG/24, and 896ms later the
// trigger fired `replaced` on the card sliding out under a hand. The capture
// read no title at all — just the footer: number 24, copyright year 2003 —
// so every named echo rule skipped it, and it queued as "nothing readable".
// A review entry that names nothing and describes the card already written
// is a stop with no question in it.
//
// The fire was also placedDecisively (face=25.0, at the floor), which must
// not save it: with no readable name there is nothing an operator could
// confirm from the entry anyway.
func TestFooterEchoOfACommittedCardIsDropped(t *testing.T) {
	prints := []scryfall.Card{
		{ID: "scg24", Name: "Trap Digger", Set: "scg", CollectorNumber: "24",
			ReleasedAt: "2003-05-26", Finishes: []string{"nonfoil", "foil"}},
	}
	fs := fakeSearcher{
		fuzzy:  map[string]string{"Trap Digger": "Trap Digger"},
		prints: map[string][]scryfall.Card{"Trap Digger": prints},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	clock := time.Date(2026, 8, 7, 11, 53, 43, 0, time.UTC)
	m.now = func() time.Time { return clock }
	m, _ = openCapture(t, m)

	good := scan.Card{Name: "Trap Digger", Candidates: []string{"Trap Digger"},
		CollectorNumber: "24", NumberSource: "copyright", CopyrightYear: 2003,
		Confidence: 0.95, Source: "crop"}
	mm, _ := m.Update(m.resolveCardCmd(1, good, 1)())
	got := mm.(model)
	if len(ra.got) != 1 {
		t.Fatalf("setup: the good read should commit, adds = %d", len(ra.got))
	}

	// 896ms later: the same footer with no title anchor, on a fire the
	// trigger called a decisive placement.
	clock = clock.Add(896 * time.Millisecond)
	got.captureSeq++
	got.lastScanReplaced = true
	face := 25.0
	got.lastScanFaceDelta = &face
	echo := scan.Card{CollectorNumber: "24", NumberSource: "copyright",
		CopyrightYear: 2003, Source: "crop"}
	mm, _ = got.Update(got.resolveCardCmd(2, echo, 1)())
	got = mm.(model)
	if len(got.review) != 0 {
		t.Errorf("review = %+v, want the footer echo dropped", got.review)
	}
	if len(ra.got) != 1 {
		t.Errorf("adds = %d, want no second copy either", len(ra.got))
	}
	if !strings.Contains(got.status, "Still seeing Trap Digger") {
		t.Errorf("status = %q, want the still-seeing note", got.status)
	}

	// The same nameless footer a comfortable swap later is not an echo — it
	// must not vanish silently like the echo above. Unnamed reads no longer
	// queue (the review gate), so "not vanishing" means a receipt line.
	clock = clock.Add(sameCardFloor + time.Second)
	got.captureSeq++
	mm, _ = got.Update(got.resolveCardCmd(3, echo, 1)())
	got = mm.(model)
	if len(got.review) != 0 || got.summary.Count("skipped") != 1 {
		t.Errorf("review=%d skipped=%d, want the later nameless read in the receipt",
			len(got.review), got.summary.Count("skipped"))
	}

	// And a nameless footer naming a *different* printing inside the window
	// is not an echo either: same treatment, distinct from the silent drop.
	clock = clock.Add(time.Second)
	got.captureSeq++
	other := scan.Card{SetCode: "MSH", CollectorNumber: "412", Source: "crop"}
	mm, _ = got.Update(got.resolveCardCmd(4, other, 1)())
	got = mm.(model)
	if len(got.review) != 0 || got.summary.Count("skipped") != 2 {
		t.Errorf("review=%d skipped=%d, want the unrelated footer in the receipt",
			len(got.review), got.summary.Count("skipped"))
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

func TestDropKeyRemovesQueuedCard(t *testing.T) {
	// d on the review list drops the card under the cursor — the browser's
	// removal key — and the drop shows up in the receipt as a skip.
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	m, sess := openCapture(t, m)
	m.review = []queueItem{
		{id: 1, canonical: "Sol Ring", note: "printing unverified"},
		{id: 2, canonical: "Serra Angel", note: "printing unverified"},
	}

	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	got := mm.(model)
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	got = mm.(model)
	if got.state != stateQueueReview || len(got.review) != 1 || got.review[0].id != 2 {
		t.Fatalf("d should drop the selected card and keep the list up: state=%v review=%+v",
			got.state, got.review)
	}
	if got.summary.Count("skipped") != 1 {
		t.Errorf("summary skipped = %d, want 1", got.summary.Count("skipped"))
	}
	if sess.closed {
		t.Error("dropping a queued card must not close the camera")
	}

	// Emptying the queue hands the screen back to the camera, which is still
	// live — the drop is a queue edit, not an exit.
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	got = mm.(model)
	if got.state != stateCapture || len(got.review) != 0 {
		t.Fatalf("last drop should return to capture: state=%v review=%d", got.state, len(got.review))
	}
	if got.summary.Count("skipped") != 2 {
		t.Errorf("summary skipped = %d, want 2", got.summary.Count("skipped"))
	}
}

func TestDropKeyIsTypedWhileFiltering(t *testing.T) {
	// The queue's filter has first claim on printable keys: someone typing
	// "dragon" must not lose the card they were narrowing toward.
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)
	m.review = []queueItem{{id: 1, canonical: "Shivan Dragon", note: "printing unverified"}}

	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	got := mm.(model)
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	got = mm.(model)
	if !got.list.SettingFilter() {
		t.Fatal("/ should open the queue filter")
	}
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	got = mm.(model)
	if len(got.review) != 1 {
		t.Fatalf("d while filtering dropped the card: review=%d", len(got.review))
	}
	if v := got.list.FilterValue(); v != "d" {
		t.Errorf("filter = %q, want the keystroke typed into it", v)
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

	ranked, rank := rankByScanStrength(solRingPrints(), "MH3", "123", 0, "", "", "", "", nil)
	m.review = []queueItem{{id: 1, canonical: "Sol Ring", ocrLine: "Sol Ring",
		raw:    scan.Card{SetCode: "MH3", CollectorNumber: "123", Confidence: 0.5},
		match:  cardname.Match{Exact: true, Similarity: 1},
		prints: ranked, rank: rank, note: "low OCR confidence (50%)"}}
	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyTab})
	got := mm.(model)
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm.(model)
	// MH3/123 names exactly one Sol Ring printing, so the picker has nothing to
	// ask and is skipped — the prints were already fetched by the background
	// resolve, so re-entry lands straight on the quantity prompt.
	if got.state != stateQty {
		t.Fatalf("state = %v, want stateQty — one printing and one finish leave nothing to pick", got.state)
	}
	if got.chosen == nil || !strings.EqualFold(got.chosen.Set, "mh3") {
		t.Errorf("chosen = %+v, want the scanned MH3 printing", got.chosen)
	}
	if len(got.review) != 0 {
		t.Errorf("the item under review should leave the queue, %d remain", len(got.review))
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

	ranked, rank := rankByScanStrength(solRingPrints(), "MH3", "123", 0, "", "", "", "", nil)
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

// The close-time walk parks on the spinner while resolves are in flight, and
// only a resolve that appends to the queue used to wake it — one that
// auto-commits (or drops) left the spinner up forever in a state with no
// exit key. The last in-flight resolve finishing, whatever its outcome, must
// end the wait.
func TestWalkSpinnerEndsWhenLastResolveCommits(t *testing.T) {
	_, fs := confidentFixture()
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)
	m.resolving = 1 // one card still looking itself up

	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	got := mm.(model)
	if got.state != stateClosePrompt {
		t.Fatalf("c with a resolve in flight should prompt, got %v", got.state)
	}
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	got = mm.(model)
	if got.state != stateLoading || !got.walking {
		t.Fatalf("state=%v walking=%v, want the spinner wait", got.state, got.walking)
	}

	// The straggler resolves confidently and auto-commits — no queue append.
	it := queueItem{
		id: got.nextResolveID, canonical: "Sol Ring",
		match:  cardname.Match{Exact: true},
		prints: []scryfall.Card{solRingPrints()[0]},
		rank:   scanMatchSetAndNumber,
	}
	mm, _ = got.Update(resolveDoneMsg{gen: got.resolveGen, item: it})
	got = mm.(model)
	if len(ra.got) != 1 {
		t.Fatalf("adder called %d times, want 1", len(ra.got))
	}
	if got.state == stateLoading || got.walking {
		t.Errorf("state=%v walking=%v after the last resolve committed — the spinner never ends",
			got.state, got.walking)
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

// A banner does not outlive the capture it describes. The "Ignored" note for
// a junk read used to sit above the "✓ Auto-added" row of the card scanned
// after it, still claiming to be current (observed live: "O L" stranded over
// an Ancient Silverback commit).
func TestStaleStatusClearedByNextCapture(t *testing.T) {
	ra := &recordingAdder{}
	m := newModel(context.Background(), multiFixture(), ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	// A capture holding nothing the catalog would accept and no collector block.
	m = sendScan(t, m, scan.Card{Name: "O L", Candidates: []string{"O L"}, Confidence: 0.95})
	if !strings.Contains(m.status, "not a card") {
		t.Fatalf("setup: status = %q, want the ignored note", m.status)
	}
	if len(ra.got) != 0 {
		t.Fatalf("setup: adds = %d, want the junk read ignored", len(ra.got))
	}

	// The next capture commits a real card, and the stale note goes with it.
	m = sendScan(t, m, scanCard("Ancient Tomb", "UMA", "236"))
	if len(ra.got) != 1 {
		t.Fatalf("adds = %d, want the card committed", len(ra.got))
	}
	if m.status != "" {
		t.Errorf("status = %q, want it cleared by the newer capture", m.status)
	}
	if len(m.tally) != 1 {
		t.Errorf("tally = %v, want the commit alone on the receipt", m.tally)
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
	clock := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return clock }
	m, _ = openCapture(t, m)

	m = sendScan(t, m, scanCard("Sol Ring", "MH3", "123"))
	// Swapping one card for another is a physical act with a measured floor
	// under it; a "re-scan" faster than that is the same card, not a new one.
	clock = clock.Add(sameCardFloor + time.Second)
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

	name, _, idx, _, _, err := resolveName(context.Background(), s, lines)
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

	name, _, idx, _, _, err := resolveName(context.Background(), s, lines)
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
	ranked, rank := rankByScanStrength(prints, "", "", 2003, "", "", "", "", nil)
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
	if _, rank := rankByScanStrength(keeperPrints(), "", "", 2003, "", "", "", "", nil); rank != scanMatchNone {
		t.Errorf("rank = %v, want scanMatchNone — both printings are 2003", rank)
	}
	// A year matching no printing is the misread case: unchanged behavior.
	if _, rank := rankByScanStrength(keeperPrints(), "", "", 1997, "", "", "", "", nil); rank != scanMatchNone {
		t.Errorf("rank = %v, want scanMatchNone for a year no printing shares", rank)
	}
	// And with no year read at all, nothing changes either.
	if _, rank := rankByScanStrength(keeperPrints(), "", "", 0, "", "", "", "", nil); rank != scanMatchNone {
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

// A nonfoil and then a foil of one card, swapped at human speed, are two
// cards.
//
// This used to *correct* the first row instead of adding a second, which made
// scanning a card and its foil impossible to express. The removed function's
// own comment had spotted the conflict: two copies scanned back to back look
// exactly like one misread copy, and rewriting the first row is as wrong as
// dropping the second.
//
// "At human speed" is what changed, and it is measured, not guessed: the
// fastest observed card swap is 3856ms (see sameCardFloor). A finish flip
// inside that floor used to bypass every duplicate rule — the dup window was
// keyed on printing *and* finish — and committed a phantom second row.
// Observed live twice in one session: Brainsurge, nonfoil then foil, 800ms
// apart, off one physical card. Now the flip goes through the same duplicate
// judgement as everything else, and a sub-floor re-read carrying real finish
// evidence corrects the guessed row instead — see
// TestFastFinishReReadCorrectsTheGuessedRow.
func TestNonfoilThenFoilAreTwoCards(t *testing.T) {
	fs := fakeSearcher{
		fuzzy:  map[string]string{"Inspired Fire": "Inspired Fire"},
		prints: map[string][]scryfall.Card{"Inspired Fire": inspiredFirePrints()},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	clock := time.Date(2026, 8, 6, 23, 50, 0, 0, time.UTC)
	m.now = func() time.Time { return clock }
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

	// The foil is laid down where the nonfoil was, five seconds later — a real
	// swap's timing. The phone watched it happen and says so, which is what
	// makes it a placement rather than another look — a nudge echo would be
	// swallowed, and should be.
	clock = clock.Add(5 * time.Second)
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

// A fast re-read carrying real finish evidence corrects the guessed row.
//
// Live on 2026-08-06: Brainsurge's first look read no marker and committed the
// nonfoil default; 800ms later the source fired again on the same card — its
// own reason said "moved", not "placed" — and the marker read foil. The finish
// difference used to make the two looks different keys in the duplicate
// window, so the duplicate rules never ran and a phantom second row committed.
// The right outcome is one row, foil: the physical-identity rules decide it is
// the same card, and evidence beats a guess.
func TestFastFinishReReadCorrectsTheGuessedRow(t *testing.T) {
	fs := fakeSearcher{
		fuzzy: map[string]string{"Brainsurge": "Brainsurge"},
		prints: map[string][]scryfall.Card{"Brainsurge": {{ID: "mh3399",
			Name: "Brainsurge", Set: "mh3", CollectorNumber: "399",
			Finishes: []string{"nonfoil", "foil"}}}},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	clock := time.Date(2026, 8, 6, 23, 50, 58, 0, time.UTC)
	m.now = func() time.Time { return clock }
	m, _ = openCapture(t, m)

	blind := scan.Event{Kind: scan.EventScan, Name: "Brainsurge",
		Cards: []scan.Card{{Name: "Brainsurge", Candidates: []string{"Brainsurge"},
			Confidence: 0.95, Source: "crop"}}}
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: blind})
	m = resolve(t, mm.(model), blind.CardList()[0])
	if len(ra.got) != 1 || ra.got[0].Finish != "nonfoil" {
		t.Fatalf("first look = %+v, want the nonfoil default committed", ra.got)
	}

	clock = clock.Add(800 * time.Millisecond)
	marked := scan.Event{Kind: scan.EventScan, Name: "Brainsurge",
		FireReason: scan.FireMoved,
		Cards: []scan.Card{{Name: "Brainsurge", Candidates: []string{"Brainsurge"},
			FinishHint: "foil", Confidence: 0.95, Source: "crop"}}}
	mm, _ = m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: marked})
	m = resolve(t, mm.(model), marked.CardList()[0])

	if len(ra.got) != 2 {
		t.Fatalf("adder called %d times, want 2 — the commit and its correction", len(ra.got))
	}
	fix := ra.got[1]
	if fix.Finish != "foil" || fix.ReplacesFinish != "nonfoil" {
		t.Errorf("correction = %+v, want the nonfoil row re-keyed to foil", fix)
	}
	if m.addedCount != 1 {
		t.Errorf("addedCount = %d, want 1 — a correction is not a card", m.addedCount)
	}
	if len(m.review) != 0 {
		t.Errorf("review = %+v, want nothing queued", m.review)
	}

	// A third sighting agreeing with the corrected row is back to being noise.
	clock = clock.Add(800 * time.Millisecond)
	mm, _ = m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: marked})
	m = resolve(t, mm.(model), marked.CardList()[0])
	if len(ra.got) != 2 {
		t.Errorf("a repeat of the corrected row must drop, adder got %d", len(ra.got))
	}
}

// One review entry per card, however many times it re-announces itself.
//
// Live, 2026-08-07: Charitable Levy queued "printing unverified: 2 printings"
// three times off one physical card — the re-reads taught nothing new (equal
// rank), so upgradeQueued left the old entry standing, and each read then
// appended another. Three stops for one decision.
func TestRepeatQueueingIsOneReviewEntry(t *testing.T) {
	fs := fakeSearcher{
		fuzzy: map[string]string{"Charitable Levy": "Charitable Levy"},
		prints: map[string][]scryfall.Card{"Charitable Levy": {
			{ID: "a", Name: "Charitable Levy", Set: "mh3", CollectorNumber: "390",
				Finishes: []string{"nonfoil", "foil"}},
			{ID: "b", Name: "Charitable Levy", Set: "mh3", CollectorNumber: "90",
				Finishes: []string{"nonfoil", "foil"}},
		}},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	ev := scan.Event{Kind: scan.EventScan, Name: "Charitable Levy",
		FireReason: scan.FireReplaced,
		Cards: []scan.Card{{Name: "Charitable Levy",
			Candidates: []string{"Charitable Levy"}, Confidence: 0.95, Source: "crop"}}}
	for range 3 {
		mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: ev})
		m = resolve(t, mm.(model), ev.CardList()[0])
	}
	if len(m.review) != 1 {
		t.Errorf("review holds %d entries, want 1 — the card only has one problem", len(m.review))
	}
	if len(ra.got) != 0 {
		t.Errorf("adder called %d times, want 0 — nothing verified", len(ra.got))
	}
}

// A better printing read must not discard the finish the read it replaces
// already carried.
//
// Live, 2026-08-07: Glowrider's first look read the marker at 0.814 — foil —
// but its name matched at 88%, so it queued. The retry read the collector
// number, replaced the queued entry, and committed nonfoil off its own flat
// patch: a true foil written wrong while the evidence sat in the discarded
// entry. Printing evidence and finish evidence come off different pixels and
// degrade independently; silence in one read must not overwrite the other
// read's answer.
func TestUpgradeCarriesTheQueuedFinishEvidence(t *testing.T) {
	fs := fakeSearcher{
		fuzzy: map[string]string{"Glowrider": "Glowrider"},
		prints: map[string][]scryfall.Card{"Glowrider": {
			{ID: "lgn15", Name: "Glowrider", Set: "lgn", CollectorNumber: "15",
				ReleasedAt: "2003-02-03", Finishes: []string{"nonfoil", "foil"}},
			{ID: "promo", Name: "Glowrider", Set: "plgn", CollectorNumber: "15p",
				ReleasedAt: "2003-06-01", Finishes: []string{"foil"}},
		}},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	// First look: the marker read foil, but with no number and two printings
	// the ranking settles nothing — it queues exactly as the live read did
	// (rank=none, prints=2, finish=foil).
	first := scan.Event{Kind: scan.EventScan, Name: "Glowrider",
		Cards: []scan.Card{{Name: "Glowrider", Candidates: []string{"Glowrider"},
			FinishHint: "foil", Confidence: 0.95, Source: "crop"}}}
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: first})
	m = resolve(t, mm.(model), first.CardList()[0])
	if len(m.review) != 1 {
		t.Fatalf("setup: want the mangled read queued, got review=%d adds=%d",
			len(m.review), len(ra.got))
	}

	// The retry reads the number cleanly but its own marker patch is flat.
	second := scan.Event{Kind: scan.EventScan, Name: "Glowrider",
		FireReason: scan.FireReplaced,
		Cards: []scan.Card{{Name: "Glowrider", Candidates: []string{"Glowrider"},
			CollectorNumber: "15", NumberSource: "copyright", CopyrightYear: 2003,
			Confidence: 0.95, Source: "crop"}}}
	mm, _ = m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: second})
	m = resolve(t, mm.(model), second.CardList()[0])

	if len(ra.got) != 1 {
		t.Fatalf("adder called %d times, want the upgraded read committed once", len(ra.got))
	}
	if got := ra.got[0]; got.Finish != "foil" || got.FinishGuessed {
		t.Errorf("committed %s guessed=%v, want the queued look's foil carried "+
			"through the upgrade", got.Finish, got.FinishGuessed)
	}
	if len(m.review) != 0 {
		t.Errorf("review = %d entries, want the queued entry spent by the upgrade", len(m.review))
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

func (s *blockFakeSearcher) PrintBySetNumberLang(_ context.Context, set, number, lang string) (*scryfall.Card, error) {
	key := strings.ToLower(set) + "/" + number
	if lang != "" && !strings.EqualFold(lang, "en") {
		key += "/" + strings.ToLower(lang)
	}
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
	if len(got.review) != 0 || got.summary.Count("skipped") != 1 {
		t.Errorf("review=%d skipped=%d, want the unidentified card in the receipt (unnamed reads never queue)",
			len(got.review), got.summary.Count("skipped"))
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
	// A block is evidence a card is in frame, so the read is not killed as a
	// phantom — but unnamed reads never queue either (the review gate); the
	// distinction the kill preserves now shows up as a receipt line.
	if len(got.review) != 0 || got.summary.Count("skipped") != 1 {
		t.Fatalf("review=%d skipped=%d, want the blocked entry accounted in the receipt",
			len(got.review), got.summary.Count("skipped"))
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
	ranked, rank := rankByScanStrength(prints, "", "12", 2003, "", "", "", "", nil)
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
	ranked, rank := rankByScanStrength(eternalDragonPrints(), "", "12", 0, "", "", "", "", nil)
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
	if _, rank := rankByScanStrength(eternalDragonPrints(), "", "12", 1999, "", "", "", "", nil); rank != scanMatchNumberOnly {
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
//
// The session's own timings are replayed rather than treated as instantaneous,
// because they are what makes the third scan legitimate: seven seconds separate
// the two foils, comfortably past the floor under which a repeat is the card
// that never left. Run with no clock at all this used to pass by accident.
func TestLiveFoilNonfoilFoilSequenceCommitsThree(t *testing.T) {
	fs := fakeSearcher{
		fuzzy:  map[string]string{"Inspired Fire": "Inspired Fire"},
		prints: map[string][]scryfall.Card{"Inspired Fire": inspiredFirePrints()},
	}
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	clock := time.Date(2026, 7, 30, 13, 12, 26, 0, time.UTC)
	m.now = func() time.Time { return clock }
	m, _ = openCapture(t, m)

	look := func(finish string) scan.Event {
		return scan.Event{Kind: scan.EventScan, Name: "Inspired Fire",
			FireReason: scan.FireRemoved,
			Cards: []scan.Card{{Name: "Inspired Fire",
				Candidates: []string{"Inspired Fire"},
				FinishHint: finish, Confidence: 0.95, Source: "crop"}}}
	}
	// 13:12:26, 13:12:30, 13:12:33 — the gaps as they were recorded.
	for i, finish := range []string{"foil", "nonfoil", "foil"} {
		if i > 0 {
			clock = clock.Add([]time.Duration{0, 4, 3}[i] * time.Second)
		}
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

func TestErrorBannerClearsOnAnyKey(t *testing.T) {
	// The helper's "no phone found" guidance is three lines long and used to
	// outlive its usefulness: sticky across states, still above the prompt long
	// after its reader gave up and went back to typing names. Any key at all is
	// proof it has been read.
	const guidance = "no iPhone running Hoardling was found on this network"

	for _, tc := range []struct {
		name string
		key  tea.KeyMsg
	}{
		{"a typed character", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")}},
		{"esc", tea.KeyMsg{Type: tea.KeyEsc}},
		{"ctrl+o", tea.KeyMsg{Type: tea.KeyCtrlO}},
		{"ctrl+p", tea.KeyMsg{Type: tea.KeyCtrlP}},
		{"the command drawer", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := &fakeScanner{devices: []scan.Device{cam("c1", "iPhone", "iPhone")}}
			m := newModel(context.Background(), fakeSearcher{}, noopAdder, sc, "", nil)

			// The helper's own wording reaches the prompt unprefixed.
			mm, _ := m.onCameras(camerasMsg{err: errors.New(guidance)})
			got := mm.(model)
			if got.status != guidance || !got.statusErr {
				t.Fatalf("setup: banner = %q (err=%v), want the guidance", got.status, got.statusErr)
			}

			mm, _ = got.handleKey(tc.key)
			if s := mm.(model).status; s == guidance {
				t.Errorf("%s left the stale error banner up", tc.name)
			}
			if mm.(model).statusErr && mm.(model).status == "" {
				t.Error("an empty banner should not still be flagged as an error")
			}
		})
	}
}

func TestSuccessBannerSurvivesTyping(t *testing.T) {
	// The other half of the rule: a receipt is a record of what happened, not a
	// complaint about it, and typing the next card's name is no reason to take
	// it away.
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	m.status, m.statusErr = "Added Sol Ring ×1", false

	mm, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if got := mm.(model).status; got != "Added Sol Ring ×1" {
		t.Errorf("receipt = %q, want it kept while the next name is typed", got)
	}
}

// The retry state threads through the real queue path, and a commit clears it.
//
// Sol Ring with no collector info is the headline never-rule case: several
// printings, nothing verified, so it queues — and queueing for that reason is
// exactly what earns a second look.
func TestUnverifiedPrintingArmsASecondLook(t *testing.T) {
	ev, fs := confidentFixture()
	ra := &recordingAdder{}
	m := newModel(context.Background(), fs, ra.add, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)

	bare := scan.Event{Kind: scan.EventScan, Name: "Sol Ring",
		Candidates: []string{"Sol Ring"}, Confidence: 0.99}
	mm, _ := m.onSessionEvent(sessionEventMsg{gen: m.sessionGen, ok: true, ev: bare})
	got := resolve(t, mm.(model), bare.CardList()[0])

	if len(got.review) != 1 {
		t.Fatalf("review = %d, want the card queued", len(got.review))
	}
	if got.secondLookFor != "Sol Ring" {
		t.Fatalf("secondLookFor = %q, want the queued card's name", got.secondLookFor)
	}

	// A commit ends that card's run of bad reads, so a later copy reading badly
	// is owed its own look rather than inheriting this one's spent attempt.
	mm, _ = got.onSessionEvent(sessionEventMsg{gen: got.sessionGen, ok: true, ev: ev})
	got = resolve(t, mm.(model), ev.CardList()[0])
	if len(ra.got) != 1 {
		t.Fatalf("adder called %d times, want the confident scan committed", len(ra.got))
	}
	if got.secondLookFor != "" {
		t.Errorf("secondLookFor = %q, want cleared by the commit", got.secondLookFor)
	}
}

// An art-channel result is supplementary: no resolveCardCmd increment ever
// paid for it, so it must not spend the counter a real resolve owns. It used
// to route through onResolveDone's decrement like any resolution, and the
// under-count ended the close-time walk early and shrank the close prompt's
// "N unsaved scans" warning.
func TestArtMatchDoesNotSpendTheResolveCounter(t *testing.T) {
	sol := scryfall.Card{ID: "art", Name: "Sol Ring", Set: "ltc",
		CollectorNumber: "284", Finishes: []string{"nonfoil"}}
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	m, _ = openCapture(t, m)
	m.resolving = 2 // two real lookups in flight

	it := queueItem{id: 7, canonical: "Sol Ring", rank: scanMatchArt,
		prints: []scryfall.Card{sol}, match: cardname.Match{Exact: true}}
	mm, _ := m.Update(artMatchMsg{gen: m.resolveGen, item: it})
	got := mm.(model)

	if got.resolving != 2 {
		t.Errorf("resolving = %d after the art message, want 2 — the real lookups are still out",
			got.resolving)
	}
}

// The queued look's finish marker only carries onto a read of the same
// physical sighting. A same-name better read landing past the second-look
// window may be a second copy — one foil, one not, in one session — and it
// used to inherit the first copy's marker on the name match alone.
func TestUpgradeFinishCarryStopsAtTheSecondLookWindow(t *testing.T) {
	sol := scryfall.Card{ID: "s", Name: "Sol Ring", Set: "mh3",
		CollectorNumber: "123", Finishes: []string{"nonfoil", "foil"}}
	t0 := time.Now()
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	m.review = []queueItem{{id: 1, canonical: "Sol Ring", rank: scanMatchNone,
		finishHint: "foil", captureSeq: 1, queuedAt: t0,
		prints: []scryfall.Card{sol}}}

	// Inside the window: the rescue's re-read, whatever reason it fired for.
	m.now = func() time.Time { return t0.Add(600 * time.Millisecond) }
	in := queueItem{id: 2, canonical: "Sol Ring", rank: scanMatchSetAndNumber,
		captureSeq: 2, prints: []scryfall.Card{sol}}
	if _, ok := m.upgradeQueued(&in); !ok {
		t.Fatal("setup: the better read should replace the queued entry")
	}
	if in.finishHint != "foil" {
		t.Errorf("finishHint = %q inside the window, want the queued foil carried", in.finishHint)
	}

	// Past the window, off a claimed placement: possibly a second copy.
	m.review = []queueItem{{id: 3, canonical: "Sol Ring", rank: scanMatchNone,
		finishHint: "foil", captureSeq: 1, queuedAt: t0,
		prints: []scryfall.Card{sol}}}
	m.now = func() time.Time { return t0.Add(3 * time.Second) }
	out := queueItem{id: 4, canonical: "Sol Ring", rank: scanMatchSetAndNumber,
		captureSeq: 2, fromReplaced: true, prints: []scryfall.Card{sol}}
	if _, ok := m.upgradeQueued(&out); !ok {
		t.Fatal("setup: the better read should still replace the queued entry")
	}
	if out.finishHint != "" {
		t.Errorf("finishHint = %q past the window, want the marker left with the sighting that read it",
			out.finishHint)
	}
}

// ctrl+d runs ahead of the drawer intercept, so it must dismiss the drawer
// itself: finishAdding left it mounted, and the leave gate's y went to the
// invisible palette query instead of answering the confirm.
func TestCtrlDUnderTheDrawerReachesTheLeaveGate(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, &fakeScanner{}, "", nil)
	m.review = []queueItem{{id: 1, canonical: "Sol Ring"}} // pending work arms the gate
	mm, _ := m.openAddPalette()
	m = mm.(model)
	if m.addPalette == nil {
		t.Fatal("setup: the drawer should be open")
	}

	mm, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyCtrlD})
	got := mm.(model)
	if got.state != stateLeaveConfirm {
		t.Fatalf("state = %v, want the leave gate", got.state)
	}
	if got.addPalette != nil {
		t.Fatal("the drawer is still mounted under the gate")
	}

	// And the y actually answers the gate now.
	mm, _ = got.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	if !mm.(model).done {
		t.Error("y did not answer the leave confirm")
	}
}

// A number that narrows to one row commits only with a second signal
// agreeing (set code or copyright year). Bare digits keep their one-row
// picker: a misread digit naming a different real printing of the same card
// used to commit silently, with the ctrl+a hatch gone because no picker
// ever showed. Scanner behavior: awaits live validation.
func TestBareNumberNarrowingToOneRowStillShowsThePicker(t *testing.T) {
	cards := solRingPrints()
	fs := fakeSearcher{prints: map[string][]scryfall.Card{"Sol Ring": cards}}
	m := newModel(context.Background(), fs, noopAdder, nil, "", nil)
	m.scanned = "Sol Ring"
	m.scannedNumber = "123" // one row answers, but nothing corroborates it

	mm, _ := m.onPrints(printsMsg{name: "Sol Ring", cards: cards})
	got := mm.(model)

	if got.state != statePrintPick {
		t.Fatalf("state = %v, want the one-row picker for an uncorroborated number", got.state)
	}
	if got.chosen != nil {
		t.Error("an uncorroborated number must not select a printing outright")
	}
	if len(got.list.Items()) != 1 {
		t.Fatalf("list has %d items, want the narrowed single row", len(got.list.Items()))
	}
	if got.printsAll == nil {
		t.Error("printsAll = nil, want the full list kept behind ctrl+a")
	}
}

// The live receipt is a window, not an archive: the session Summary keeps
// every row, so the tally can stop growing once its history is deeper than
// anyone scrolls mid-session.
func TestTallyIsCapped(t *testing.T) {
	m := newModel(context.Background(), fakeSearcher{}, noopAdder, nil, "", nil)
	for i := range tallyCap + 50 {
		m.recordTally(fmt.Sprintf("row %d", i))
	}
	if len(m.tally) != tallyCap {
		t.Fatalf("tally holds %d rows, want capped at %d", len(m.tally), tallyCap)
	}
	if got := m.tally[len(m.tally)-1]; got != fmt.Sprintf("row %d", tallyCap+49) {
		t.Errorf("newest row = %q, want the latest commit kept", got)
	}
}
