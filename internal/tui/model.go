package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/spiffcs/hoard/internal/cardname"
	"github.com/spiffcs/hoard/internal/scan"
	"github.com/spiffcs/hoard/internal/scryfall"
)

type state int

const (
	stateName state = iota
	stateCameraList
	stateCameraPick
	stateCapture   // camera window live, waiting for the user to frame and capture
	stateCapturing // shutter pressed, waiting on the OCR result
	stateLoading
	stateNamePick
	statePrintPick
	stateFinishPick
	stateQty
	stateConfirm
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true)
	helpStyle   = lipgloss.NewStyle().Faint(true)
	errStyle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("9"))
	scanStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	promptStyle = lipgloss.NewStyle().Bold(true)
)

// --- messages ---

type printsMsg struct {
	name  string
	cards []scryfall.Card
}
type namesMsg struct{ names []string }
type errMsg struct{ err error }

// camerasMsg carries the list of cameras available to scan with.
type camerasMsg struct {
	devices []scan.Device
	err     error
}

// sessionMsg carries the result of opening a camera session.
type sessionMsg struct {
	session ScanSession
	err     error
}

// sessionEventMsg carries one event from the live camera session. gen tags the
// session it came from, so events from a session the user has already closed are
// discarded rather than yanking them back into a scan. ok is false when the
// event channel closed, meaning the window is gone.
type sessionEventMsg struct {
	gen int
	ev  scan.Event
	ok  bool
}

// fuzzyMsg carries the canonical card name resolved from OCR text via Scryfall's
// fuzzy match; ocr is the original scanned text, kept so it can pre-fill the
// prompt on a miss.
type fuzzyMsg struct {
	canonical string
	ocr       string
	// set and number are the collector info read off the card's bottom border,
	// carried through untouched so the printing picker can rank by them. Both are
	// empty for cards that don't print them.
	set    string
	number string
	err    error
}

// --- list item types ---

type nameItem string

func (n nameItem) Title() string       { return string(n) }
func (n nameItem) Description() string { return "" }
func (n nameItem) FilterValue() string { return string(n) }

// printItem is one printing in the picker. scanned marks the printing whose
// collector number matched the one read off the card, so the user can see what
// the camera picked before committing to it.
type printItem struct {
	card    scryfall.Card
	scanned bool
}

func (p printItem) Title() string {
	t := fmt.Sprintf("%s #%s · %s",
		strings.ToUpper(p.card.Set), p.card.CollectorNumber, p.card.SetName)
	if p.scanned {
		t += "  ← scanned"
	}
	return t
}
func (p printItem) Description() string {
	parts := []string{}
	if p.card.ReleasedAt != "" {
		parts = append(parts, p.card.ReleasedAt)
	}
	if mk := printMarkers(p.card); mk != "" {
		parts = append(parts, mk)
	}
	parts = append(parts, priceLabel(p.card))
	return strings.Join(parts, " · ")
}
func (p printItem) FilterValue() string { return p.Title() + " " + p.Description() }

type finishItem string

func (f finishItem) Title() string       { return string(f) }
func (f finishItem) Description() string { return "" }
func (f finishItem) FilterValue() string { return string(f) }

type cameraItem struct{ dev scan.Device }

func (c cameraItem) Title() string       { return c.dev.Name }
func (c cameraItem) Description() string { return c.dev.Kind }
func (c cameraItem) FilterValue() string { return c.dev.Name + " " + c.dev.Kind }

// --- model ---

type model struct {
	ctx      context.Context
	searcher Searcher
	adder    Adder
	scanner  Scanner

	state state

	nameInput textinput.Model
	qtyInput  textinput.Model
	list      list.Model
	spinner   spinner.Model

	width, height int

	// cascade selections
	prints []scryfall.Card
	chosen *scryfall.Card
	finish string

	qtyErr string

	// scan feedback: the canonical name a camera scan resolved to (and the raw
	// OCR text it came from), shown as a fixed header for the rest of the
	// cascade so the user can see the capture was read correctly.
	scanned    string
	scannedOCR string
	// collector info read off the card, used to rank and pre-select the printing.
	scannedSet    string
	scannedNumber string

	// camera choice, remembered for the session so bulk scanning doesn't ask
	// every time. cameraChosen distinguishes "no camera picked yet" from a
	// deliberate empty ID.
	cameraID     string
	cameraName   string
	cameraChosen bool

	// the live camera session. It outlives individual cards: the window stays up
	// while the cascade runs, and the flow returns here after each add. gen tags
	// events so a closed session's stragglers are ignored.
	session    ScanSession
	sessionGen int

	// session state
	status     string // banner shown on the name prompt (last add / error / cancel)
	statusErr  bool   // style the banner as an error
	addedCount int
	err        error // fatal program error (rare)
}

func newModel(ctx context.Context, s Searcher, add Adder, sc Scanner, initialName string) model {
	ni := textinput.New()
	ni.Placeholder = "Card name, e.g. Ulamog, the Infinite Gyre"
	ni.Focus()
	ni.CharLimit = 200
	ni.Width = 50

	qi := textinput.New()
	qi.Placeholder = "1"
	qi.CharLimit = 6
	qi.Width = 10

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	l := list.New(nil, list.NewDefaultDelegate(), 0, 0)
	l.SetShowStatusBar(false)
	l.SetShowTitle(false)

	m := model{
		ctx:       ctx,
		searcher:  s,
		adder:     add,
		scanner:   sc,
		nameInput: ni,
		qtyInput:  qi,
		spinner:   sp,
		list:      l,
		width:     80,
		height:    22,
	}
	if strings.TrimSpace(initialName) != "" {
		m.nameInput.SetValue(strings.TrimSpace(initialName))
		m.state = stateLoading
	} else {
		m.state = stateName
	}
	return m
}

func (m model) Init() tea.Cmd {
	if m.state == stateLoading {
		return tea.Batch(m.spinner.Tick, m.searchPrintsCmd(m.nameInput.Value()))
	}
	return textinput.Blink
}

// --- commands ---

func (m model) searchPrintsCmd(name string) tea.Cmd {
	return func() tea.Msg {
		cards, err := m.searcher.SearchPrints(m.ctx, name)
		if err != nil {
			return errMsg{err}
		}
		return printsMsg{name: name, cards: cards}
	}
}

func (m model) autocompleteCmd(q string) tea.Cmd {
	return func() tea.Msg {
		names, err := m.searcher.Autocomplete(m.ctx, q)
		if err != nil {
			return errMsg{err}
		}
		return namesMsg{names: names}
	}
}

// listCamerasCmd asks the scanner which cameras are attached.
func (m model) listCamerasCmd() tea.Cmd {
	scanner := m.scanner
	return func() tea.Msg {
		devices, err := scanner.Devices(m.ctx)
		return camerasMsg{devices: devices, err: err}
	}
}

// openSessionCmd opens the camera window on the chosen device.
func (m model) openSessionCmd() tea.Cmd {
	scanner, deviceID := m.scanner, m.cameraID
	return func() tea.Msg {
		s, err := scanner.Open(m.ctx, deviceID)
		return sessionMsg{session: s, err: err}
	}
}

// nextEventCmd waits for one event from the live session. Each handler re-issues
// it, which is how a channel becomes a stream of messages in Bubble Tea.
func nextEventCmd(s ScanSession, gen int) tea.Cmd {
	events := s.Events()
	return func() tea.Msg {
		ev, ok := <-events
		return sessionEventMsg{gen: gen, ev: ev, ok: ok}
	}
}

// beginScan opens the camera, first choosing one if this run hasn't yet. force
// re-opens the picker even when a camera is already chosen. An already-open
// session just gets focus back rather than being reopened.
func (m *model) beginScan(force bool) tea.Cmd {
	if m.session != nil && !force {
		m.status = ""
		m.state = stateCapture
		return nil
	}
	if m.cameraChosen && !force {
		m.status = ""
		m.state = stateCameraList
		return tea.Batch(m.spinner.Tick, m.openSessionCmd())
	}
	m.status = ""
	m.state = stateCameraList
	return tea.Batch(m.spinner.Tick, m.listCamerasCmd())
}

// closeSession shuts the camera window and forgets it. Bumping the generation
// makes any in-flight event from the dying session stale.
func (m *model) closeSession() {
	if m.session == nil {
		return
	}
	_ = m.session.Close()
	m.session = nil
	m.sessionGen++
}

// maxFuzzyTries bounds how many OCR lines are tried against Scryfall, so a
// text-heavy capture can't turn into a burst of lookups.
const maxFuzzyTries = 5

const ()

// namedFuzzyCmd resolves noisy OCR text to a canonical card name, trying each
// recognized line in order until one matches. Only the first line is a real
// guess at the title — the rest are the fallback for when that guess is wrong,
// which is what a skewed or misrotated capture produces.
// set and number are the collector info read off the same capture; they take no
// part in resolving the name and are simply carried through to the printing step.
func (m model) namedFuzzyCmd(lines []string, set, number string) tea.Cmd {
	return func() tea.Msg {
		var firstErr error
		for i, line := range lines {
			if i >= maxFuzzyTries {
				break
			}
			if strings.TrimSpace(line) == "" {
				continue
			}
			card, err := m.searcher.NamedFuzzy(m.ctx, line)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			if card != nil && cardname.Plausible(line, card.Name) {
				return fuzzyMsg{canonical: card.Name, ocr: line, set: set, number: number}
			}
		}
		// Report the best-guess line: it's what gets pre-filled for editing.
		var top string
		if len(lines) > 0 {
			top = lines[0]
		}
		return fuzzyMsg{ocr: top, set: set, number: number, err: firstErr}
	}
}

// --- update ---

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.list.SetSize(msg.Width, m.listHeight())
		return m, nil

	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
		return m.handleKey(msg)

	case printsMsg:
		return m.onPrints(msg)

	case namesMsg:
		return m.onNames(msg)

	case camerasMsg:
		return m.onCameras(msg)

	case sessionMsg:
		return m.onSession(msg)

	case sessionEventMsg:
		return m.onSessionEvent(msg)

	case fuzzyMsg:
		return m.onFuzzy(msg)

	case errMsg:
		return m.failToName(msg.err.Error())

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	// Delegate to the active component for non-key messages.
	return m.updateActive(msg)
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.state {
	case stateName:
		switch msg.Type {
		case tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyCtrlO:
			if m.scanner == nil {
				return m.failToName("card scanning isn't available in this build")
			}
			cmd := m.beginScan(false)
			return m, cmd
		case tea.KeyCtrlR:
			// Re-open the camera picker even if this session already chose one.
			if m.scanner == nil {
				return m.failToName("card scanning isn't available in this build")
			}
			cmd := m.beginScan(true)
			return m, cmd
		case tea.KeyEnter:
			name := strings.TrimSpace(m.nameInput.Value())
			if name == "" {
				return m, nil
			}
			m.status = ""
			m.state = stateLoading
			return m, tea.Batch(m.spinner.Tick, m.searchPrintsCmd(name))
		}
	case stateCameraList:
		if msg.Type == tea.KeyEsc {
			return m.cancelToName()
		}
	case stateCameraPick:
		if msg.Type == tea.KeyEsc && !m.list.SettingFilter() {
			return m.cancelToName()
		}
		if msg.Type == tea.KeyEnter && !m.list.SettingFilter() {
			if it, ok := m.list.SelectedItem().(cameraItem); ok {
				m.cameraID = it.dev.ID
				m.cameraName = it.dev.Name
				m.cameraChosen = true
				m.state = stateCameraList
				return m, tea.Batch(m.spinner.Tick, m.openSessionCmd())
			}
		}
	case stateCapture:
		switch msg.Type {
		case tea.KeySpace, tea.KeyEnter:
			return m.requestCapture()
		case tea.KeyLeft:
			return m.rotatePreview(true)
		case tea.KeyRight:
			return m.rotatePreview(false)
		case tea.KeyCtrlR:
			// Switch phones without leaving the camera step.
			m.closeSession()
			cmd := m.beginScan(true)
			return m, cmd
		case tea.KeyEsc:
			// Close the camera without ending the add session.
			m.closeSession()
			return m.cancelToName()
		}
	case stateCapturing:
		// The shutter is already pressed; esc abandons the whole camera rather
		// than leaving the user staring at a spinner.
		if msg.Type == tea.KeyEsc {
			m.closeSession()
			return m.cancelToName()
		}
	case stateNamePick:
		if msg.Type == tea.KeyEsc && !m.list.SettingFilter() {
			return m.cancelToName()
		}
		if msg.Type == tea.KeyEnter && !m.list.SettingFilter() {
			if it, ok := m.list.SelectedItem().(nameItem); ok {
				m.state = stateLoading
				return m, tea.Batch(m.spinner.Tick, m.searchPrintsCmd(string(it)))
			}
		}
	case statePrintPick:
		if msg.Type == tea.KeyEsc && !m.list.SettingFilter() {
			return m.cancelToName()
		}
		if msg.Type == tea.KeyEnter && !m.list.SettingFilter() {
			if it, ok := m.list.SelectedItem().(printItem); ok {
				card := it.card
				m.chosen = &card
				return m.advanceAfterPrint()
			}
		}
	case stateFinishPick:
		if msg.Type == tea.KeyEsc && !m.list.SettingFilter() {
			return m.cancelToName()
		}
		if msg.Type == tea.KeyEnter && !m.list.SettingFilter() {
			if it, ok := m.list.SelectedItem().(finishItem); ok {
				m.finish = string(it)
				return m.toQty()
			}
		}
	case stateQty:
		if msg.Type == tea.KeyEsc {
			return m.cancelToName()
		}
		if msg.Type == tea.KeyEnter {
			return m.submitQty()
		}
	case stateConfirm:
		switch msg.Type {
		case tea.KeyEnter:
			return m.confirmAdd()
		case tea.KeyEsc:
			return m.cancelToName()
		}
	}
	return m.updateActive(msg)
}

// updateActive forwards a message to whichever interactive component is active.
func (m model) updateActive(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.state {
	case stateName:
		m.nameInput, cmd = m.nameInput.Update(msg)
	case stateQty:
		m.qtyInput, cmd = m.qtyInput.Update(msg)
	case stateNamePick, statePrintPick, stateFinishPick, stateCameraPick:
		m.list, cmd = m.list.Update(msg)
	case stateLoading, stateCapturing, stateCameraList:
		m.spinner, cmd = m.spinner.Update(msg)
	}
	return m, cmd
}

// --- transitions ---

func (m model) onPrints(msg printsMsg) (tea.Model, tea.Cmd) {
	if len(msg.cards) == 0 {
		// The name wasn't an exact match; offer autocomplete suggestions.
		return m, m.autocompleteCmd(msg.name)
	}
	m.prints = msg.cards
	if len(msg.cards) == 1 {
		card := msg.cards[0]
		m.chosen = &card
		return m.advanceAfterPrint()
	}

	// A collector number read off the card promotes its printing to the top and
	// marks it, but never selects it outright: a misread digit has to be visible
	// before it is committed. Heavily reprinted cards make this worth doing —
	// Sol Ring has well over a hundred printings.
	cards, matched := rankByScan(msg.cards, m.scannedSet, m.scannedNumber)
	m.prints = cards

	items := make([]list.Item, len(cards))
	for i, c := range cards {
		items[i] = printItem{card: c, scanned: matched && i == 0}
	}
	m.setListItems("Select a printing", items)
	if m.scannedNumber != "" && !matched {
		// Either the digits were misread or the name match is wrong. Saying so
		// beats silently showing an unranked list as though nothing was scanned.
		m.status = fmt.Sprintf("card #%s isn't among these printings — pick manually", m.scannedNumber)
		m.statusErr = true
	}
	m.state = statePrintPick
	return m, nil
}

// rankByScan moves the printing matching a scanned collector number to the front,
// leaving every other printing in Scryfall's order (newest first). A set code
// makes the match exact; without one, the number alone is enough as long as it
// picks out a single printing.
//
// It reports whether anything matched, so the caller can both mark the row and
// tell the user when a scanned number found nothing.
func rankByScan(cards []scryfall.Card, set, number string) ([]scryfall.Card, bool) {
	if number == "" || len(cards) == 0 {
		return cards, false
	}
	best := -1
	for i, c := range cards {
		if !strings.EqualFold(c.CollectorNumber, number) {
			continue
		}
		if set != "" && strings.EqualFold(c.Set, set) {
			best = i // exact set+number: stop looking
			break
		}
		if best < 0 {
			best = i // number-only match: keep, but a set match would beat it
		}
	}
	if best < 0 {
		return cards, false
	}
	ranked := make([]scryfall.Card, 0, len(cards))
	ranked = append(ranked, cards[best])
	ranked = append(ranked, cards[:best]...)
	ranked = append(ranked, cards[best+1:]...)
	return ranked, true
}

func (m model) onNames(msg namesMsg) (tea.Model, tea.Cmd) {
	switch len(msg.names) {
	case 0:
		return m.failToName(fmt.Sprintf("no cards found matching %q", m.nameInput.Value()))
	case 1:
		m.state = stateLoading
		return m, tea.Batch(m.spinner.Tick, m.searchPrintsCmd(msg.names[0]))
	default:
		items := make([]list.Item, len(msg.names))
		for i, n := range msg.names {
			items[i] = nameItem(n)
		}
		m.setListItems("Select a card", items)
		m.state = stateNamePick
		return m, nil
	}
}

// onCameras handles the camera list. A single camera is used without asking;
// several are offered as a picker; none is an error the user can act on.
func (m model) onCameras(msg camerasMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		// The helper's own "no iPhone found" guidance is the useful message here,
		// so pass it through unprefixed.
		return m.failToName(msg.err.Error())
	}
	switch len(msg.devices) {
	case 0:
		return m.failToName("no iPhone found — scanning uses Continuity Camera; connect an iPhone and try again")
	case 1:
		m.cameraID = msg.devices[0].ID
		m.cameraName = msg.devices[0].Name
		m.cameraChosen = true
		return m, tea.Batch(m.spinner.Tick, m.openSessionCmd())
	default:
		items := make([]list.Item, len(msg.devices))
		for i, d := range msg.devices {
			items[i] = cameraItem{dev: d}
		}
		m.setListItems("Scan with which iPhone?", items)
		// Pre-select the session's current camera so re-picking is a single enter.
		for i, d := range msg.devices {
			if d.ID == m.cameraID {
				m.list.Select(i)
				break
			}
		}
		m.state = stateCameraPick
		return m, nil
	}
}

// requestCapture asks the live session for a photo. The window stays up; only
// the TUI moves to a waiting state.
func (m model) requestCapture() (tea.Model, tea.Cmd) {
	if m.session == nil {
		return m.failToName("the camera isn't open")
	}
	if err := m.session.Capture(); err != nil {
		m.closeSession()
		return m.failToName(err.Error())
	}
	m.status = ""
	m.state = stateCapturing
	return m, m.spinner.Tick
}

// rotatePreview turns the camera preview a quarter-turn from the terminal, so the
// user doesn't have to focus the camera window to fix the framing.
func (m model) rotatePreview(left bool) (tea.Model, tea.Cmd) {
	if m.session == nil {
		return m, nil
	}
	if err := m.session.Rotate(left); err != nil {
		m.closeSession()
		return m.failToName(err.Error())
	}
	return m, nil
}

// onSession handles the camera window opening: on success the event pump starts
// and the user can frame their first card.
func (m model) onSession(msg sessionMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.failToName(msg.err.Error())
	}
	m.session = msg.session
	m.sessionGen++
	m.state = stateCapture
	return m, nextEventCmd(m.session, m.sessionGen)
}

// onSessionEvent handles one message from the camera window and re-arms the pump.
func (m model) onSessionEvent(msg sessionEventMsg) (tea.Model, tea.Cmd) {
	// A straggler from a session the user already closed.
	if msg.gen != m.sessionGen || m.session == nil {
		return m, nil
	}
	// The channel closed: the window is gone, however that happened.
	if !msg.ok {
		m.session = nil
		m.sessionGen++
		if m.state == stateCapture || m.state == stateCapturing {
			return m.cancelToName()
		}
		return m, nil
	}

	again := nextEventCmd(m.session, m.sessionGen)
	switch msg.ev.Kind {
	case scan.EventScan:
		lines := msg.ev.Lines()
		if len(lines) == 0 {
			m.status = "nothing readable in that frame — reframe and capture again"
			m.statusErr = true
			m.state = stateCapture
			return m, again
		}
		m.state = stateLoading
		return m, tea.Batch(m.spinner.Tick,
			m.namedFuzzyCmd(lines, msg.ev.SetCode, msg.ev.CollectorNumber), again)

	case scan.EventError:
		// The window is still up, so stay on the capture step and let them retry.
		m.status = msg.ev.Message
		m.statusErr = true
		if m.state == stateCapturing {
			m.state = stateCapture
		}
		return m, again

	case scan.EventClosed:
		m.closeSession()
		return m.cancelToName()

	case scan.EventReady:
		if msg.ev.Device != "" {
			m.cameraName = msg.ev.Device
		}
		return m, again
	}
	// EventRotation and anything unrecognized: nothing to do but keep listening.
	return m, again
}

// onFuzzy handles the fuzzy-name resolution of scanned text. On a match it looks
// up printings (reusing the normal cascade); on a miss it drops back to the
// prompt with the OCR text pre-filled so the user can correct it.
func (m model) onFuzzy(msg fuzzyMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		return m.scanMissToName(msg.ocr, "scan: "+msg.err.Error())
	}
	if msg.canonical == "" {
		return m.scanMissToName(msg.ocr, fmt.Sprintf("couldn't identify %q — edit and press enter", msg.ocr))
	}
	m.scanned = msg.canonical
	m.scannedOCR = msg.ocr
	m.scannedSet = msg.set
	m.scannedNumber = msg.number
	m.state = stateLoading
	return m, tea.Batch(m.spinner.Tick, m.searchPrintsCmd(msg.canonical))
}

// scanMissToName returns to the name prompt with the OCR text pre-filled and an
// error banner, so a failed scan is never a dead end.
func (m model) scanMissToName(ocr, msg string) (tea.Model, tea.Cmd) {
	m.prints = nil
	m.chosen = nil
	m.finish = ""
	m.qtyErr = ""
	m.scanned = ""
	m.scannedOCR = ""
	m.scannedSet = ""
	m.scannedNumber = ""
	m.nameInput.SetValue(ocr)
	m.nameInput.CursorEnd()
	m.nameInput.Focus()
	m.status = msg
	m.statusErr = true
	m.state = stateName
	return m, textinput.Blink
}

// advanceAfterPrint moves past printing selection: it auto-skips the finish step
// when the printing has a single finish, otherwise shows the finish picker.
func (m model) advanceAfterPrint() (tea.Model, tea.Cmd) {
	finishes := finishOptions(*m.chosen)
	if len(finishes) <= 1 {
		if len(finishes) == 1 {
			m.finish = finishes[0]
		} else {
			m.finish = "normal"
		}
		return m.toQty()
	}
	items := make([]list.Item, len(finishes))
	for i, f := range finishes {
		items[i] = finishItem(f)
	}
	m.setListItems("Select a finish", items)
	m.state = stateFinishPick
	return m, nil
}

// confirmAdd persists the pinpointed selection via the adder, records a banner,
// and loops back to the name prompt for the next card.
func (m model) confirmAdd() (tea.Model, tea.Cmd) {
	res := Result{Card: *m.chosen, Finish: m.finish, Qty: m.qtyValue()}
	if err := m.adder(res); err != nil {
		return m.failToName(err.Error())
	}
	m.addedCount++
	m.status = fmt.Sprintf("✓ Added %d× %s (%s/%s) %s — %s",
		res.Qty, res.Card.Name, res.Card.Set, res.Card.CollectorNumber,
		res.Finish, priceForFinish(res.Card, res.Finish))
	m.statusErr = false
	return m.resetForNext()
}

// cancelToName abandons the in-progress add and returns to the name prompt.
func (m model) cancelToName() (tea.Model, tea.Cmd) {
	m.status = ""
	return m.resetForNext()
}

// failToName shows an error banner and returns to the name prompt, keeping the
// session alive.
func (m model) failToName(msg string) (tea.Model, tea.Cmd) {
	m.status = msg
	m.statusErr = true
	return m.resetForNext()
}

// resetForNext clears the cascade selections and refocuses the name input.
func (m model) resetForNext() (tea.Model, tea.Cmd) {
	m.prints = nil
	m.chosen = nil
	m.finish = ""
	m.qtyErr = ""
	m.scanned = ""
	m.scannedOCR = ""
	m.scannedSet = ""
	m.scannedNumber = ""
	m.nameInput.SetValue("")
	// With the camera still open, go back to framing the next card rather than to
	// the prompt — that's the whole point of holding the window open.
	if m.session != nil {
		m.state = stateCapture
		return m, nil
	}
	m.nameInput.Focus()
	m.state = stateName
	return m, textinput.Blink
}

func (m model) toQty() (tea.Model, tea.Cmd) {
	m.qtyInput.SetValue("1")
	m.qtyInput.Focus()
	m.state = stateQty
	return m, textinput.Blink
}

func (m model) submitQty() (tea.Model, tea.Cmd) {
	v := strings.TrimSpace(m.qtyInput.Value())
	if v == "" {
		v = "1"
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		m.qtyErr = "enter a whole number ≥ 1"
		return m, nil
	}
	m.qtyErr = ""
	m.qtyInput.SetValue(strconv.Itoa(n))
	m.state = stateConfirm
	return m, nil
}

func (m model) qtyValue() int {
	n, err := strconv.Atoi(strings.TrimSpace(m.qtyInput.Value()))
	if err != nil || n < 1 {
		return 1
	}
	return n
}

func (m *model) setListItems(title string, items []list.Item) {
	m.list.Title = title
	m.list.SetItems(items)
	m.list.ResetFilter()
	m.list.Select(0)
	m.list.SetSize(m.width, m.listHeight())
}

// cameraLabel names the camera in use, falling back to a generic word before the
// session has reported its device.
func (m model) cameraLabel() string {
	if m.cameraName != "" {
		return m.cameraName
	}
	return "camera"
}

// listHeight is the space left for the list once the surrounding chrome (help
// line, plus the scan header when one is showing) is accounted for.
func (m model) listHeight() int {
	chrome := 4
	if m.scanned != "" {
		chrome += 2
	}
	return maxInt(m.height-chrome, 4)
}

// --- view ---

// scanHeader renders the fixed banner shown for the rest of the cascade after a
// camera scan, so the user can confirm the OCR landed on the right card. When
// the raw OCR text differs from the canonical name, it's shown alongside so a
// wrong-but-plausible match is obvious.
func (m model) scanHeader() string {
	if m.scanned == "" {
		return ""
	}
	line := scanStyle.Render("📷 Scanned: " + m.scanned)
	if m.scannedOCR != "" && !strings.EqualFold(m.scannedOCR, m.scanned) {
		line += helpStyle.Render(fmt.Sprintf("  (read %q)", m.scannedOCR))
	}
	return line + "\n\n"
}

func (m model) View() string {
	switch m.state {
	case stateName:
		var b strings.Builder
		b.WriteString(titleStyle.Render("Add cards to your collection") + "\n\n")
		if m.status != "" {
			style := okStyle
			if m.statusErr {
				style = errStyle
			}
			b.WriteString(style.Render(m.status) + "\n\n")
		}
		b.WriteString(m.nameInput.View() + "\n\n")
		scanHint := ""
		if m.scanner != nil {
			scanHint = "ctrl+o scan card · ctrl+r change camera · "
			if m.session != nil {
				scanHint = "ctrl+o back to camera · ctrl+r change camera · "
			}
		}
		help := scanHint + "enter search · esc quit · ctrl+c quit"
		if m.addedCount > 0 {
			help = fmt.Sprintf("%d added this session · %s", m.addedCount, help)
		}
		b.WriteString(helpStyle.Render(help))
		return b.String()
	case stateCameraList:
		return fmt.Sprintf("%s looking for a connected iPhone…\n\n%s",
			m.spinner.View(), helpStyle.Render("esc cancel · ctrl+c quit"))
	case stateCameraPick:
		return m.list.View() + "\n" +
			helpStyle.Render("↑/↓ move · enter scan with this camera · esc back · ctrl+c quit")
	case stateCapture:
		var b strings.Builder
		b.WriteString(titleStyle.Render("Scanning with "+m.cameraLabel()) + "\n\n")
		if m.status != "" {
			style := okStyle
			if m.statusErr {
				style = errStyle
			}
			b.WriteString(style.Render(m.status) + "\n\n")
		}
		b.WriteString("Frame the next card, then press space.\n\n")
		help := "space capture · ←/→ rotate · esc close camera · ctrl+c quit"
		if m.addedCount > 0 {
			help = fmt.Sprintf("%d added this session · %s", m.addedCount, help)
		}
		b.WriteString(helpStyle.Render(help + "\n(space and ←/→ also work in the camera window)"))
		return b.String()
	case stateCapturing:
		return fmt.Sprintf("%s reading the card…\n\n%s",
			m.spinner.View(), helpStyle.Render("esc close camera · ctrl+c quit"))
	case stateLoading:
		return m.scanHeader() + fmt.Sprintf("%s searching Scryfall…\n\n%s",
			m.spinner.View(), helpStyle.Render("ctrl+c to quit"))
	case stateNamePick, statePrintPick, stateFinishPick:
		return m.scanHeader() + m.list.View() + "\n" +
			helpStyle.Render("↑/↓ move · / filter · enter select · esc cancel · ctrl+c quit")
	case stateQty:
		out := m.scanHeader() + promptStyle.Render("Quantity for "+m.chosen.Name) + "\n\n" + m.qtyInput.View()
		if m.qtyErr != "" {
			out += "\n" + errStyle.Render(m.qtyErr)
		}
		return out + "\n\n" + helpStyle.Render("enter to continue · esc cancel · ctrl+c quit")
	case stateConfirm:
		return m.scanHeader() + titleStyle.Render("Confirm") + "\n\n" + m.confirmSummary() + "\n\n" +
			helpStyle.Render("enter to add · esc cancel · ctrl+c quit")
	}
	return ""
}

func (m model) confirmSummary() string {
	c := m.chosen
	finish := m.finish
	price := priceForFinish(*c, finish)
	return fmt.Sprintf("%d× %s\n%s #%s · %s\nfinish: %s   price: %s",
		m.qtyValue(), c.Name, strings.ToUpper(c.Set), c.CollectorNumber, c.SetName,
		finish, price)
}

// --- pure helpers (unit-tested) ---

// finishOptions maps a card's Scryfall finishes to the tool's finish names,
// preserving a stable normal→foil→etched order.
func finishOptions(c scryfall.Card) []string {
	has := map[string]bool{}
	for _, f := range c.Finishes {
		switch f {
		case "nonfoil":
			has["normal"] = true
		case "foil":
			has["foil"] = true
		case "etched":
			has["etched"] = true
		}
	}
	var out []string
	for _, f := range []string{"normal", "foil", "etched"} {
		if has[f] {
			out = append(out, f)
		}
	}
	return out
}

// printMarkers summarizes distinguishing traits of a printing for display.
func printMarkers(c scryfall.Card) string {
	var parts []string
	if c.BorderColor == "borderless" {
		parts = append(parts, "borderless")
	}
	parts = append(parts, c.FrameEffects...)
	parts = append(parts, c.PromoTypes...)
	return strings.Join(parts, "/")
}

func priceLabel(c scryfall.Card) string {
	return "$" + priceStr(c.PriceUSD) + " / $" + priceStr(c.PriceUSDFoil) + "f"
}

func priceForFinish(c scryfall.Card, finish string) string {
	p := c.PriceUSD
	if scryfall.PricedAsFoil(finish) {
		p = c.PriceUSDFoil
	}
	if p == nil {
		return "—"
	}
	return "$" + priceStr(p)
}

func priceStr(p *float64) string {
	if p == nil {
		return "—"
	}
	return strconv.FormatFloat(*p, 'f', 2, 64)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
