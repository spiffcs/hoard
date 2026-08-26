package tui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/spiffcs/hoard/internal/cardname"
	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scan"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/ui"
)

type state int

const (
	stateName state = iota

	stateCameraBusy
	stateCameraPick

	statePairIntro

	statePairCode

	statePairBusy
	stateCapture
	stateCapturing
	stateLoading
	stateNamePick
	statePrintPick
	stateFinishPick
	stateDestPick
	stateQty
	stateConfirm

	stateQueueReview
	stateClosePrompt

	stateLeaveConfirm

	stateAbandonConfirm
)

type printsMsg struct {
	name  string
	cards []scryfall.Card
}
type namesMsg struct{ names []string }
type errMsg struct{ err error }

type camerasMsg struct {
	devices []scan.Device
	err     error
}

type sessionMsg struct {
	session ScanSession
	err     error
}

type sessionEventMsg struct {
	gen int
	ev  scan.Event
	ok  bool
}

type nameItem string

func (n nameItem) Title() string       { return string(n) }
func (n nameItem) Description() string { return "" }
func (n nameItem) FilterValue() string { return string(n) }

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

type finishItem finish.Finish

func (f finishItem) Title() string       { return finish.Finish(f).String() }
func (f finishItem) Description() string { return "" }
func (f finishItem) FilterValue() string { return finish.Finish(f).String() }

type cameraItem struct{ dev scan.Device }

func (c cameraItem) Title() string       { return c.dev.Name }
func (c cameraItem) Description() string { return c.dev.Kind }
func (c cameraItem) FilterValue() string { return c.dev.Name + " " + c.dev.Kind }

type destItem struct{ d Destination }

func (d destItem) Title() string { return d.d.Name }
func (d destItem) Description() string {
	if d.d.Kind == "deck" {
		return "deck · adds to the mainboard"
	}
	return d.d.Kind
}
func (d destItem) FilterValue() string { return d.d.Name + " " + d.d.Kind }

type model struct {
	ctx      context.Context
	searcher Searcher
	adder    Adder
	scanner  Scanner

	completer  Completer
	completing int

	leaving bool

	theme ui.Theme

	state state

	done bool

	nameInput textinput.Model
	qtyInput  textinput.Model
	codeInput textinput.Model

	addPalette *addPalette
	list       list.Model
	spinner    spinner.Model

	width, height int

	prints []scryfall.Card
	chosen *scryfall.Card
	finish finish.Finish

	printsAll []scryfall.Card

	dests []Destination
	dest  Destination

	qtyErr string

	scanned    string
	scannedOCR string

	scannedSet    string
	scannedNumber string

	scannedPromoted bool

	review        []queueItem
	nextResolveID int
	resolveGen    int
	resolving     int

	current  *queueItem
	walking  bool
	walkDone int

	recent []recentCommit
	now    func() time.Time

	tally []string

	tallyOffset int
	summary     Summary

	autoState string

	autoCapable bool

	torchCapable bool
	torchOn      bool

	hudCapable     bool
	nudgeGen       int
	nudgeSentAt    time.Time
	lastScanNudged bool

	lastScanMoved bool

	lastScanReplaced bool

	lastScanFaceDelta *float64

	pending    *pendingDup
	nudgeDrops int

	secondLookFor string

	deferredFlashFor string

	deferredFlashArmed bool

	flashOverdue bool

	ignored int

	recentNames []recentName

	captureSeq int

	destPicked     bool
	destForSession bool

	cameraID     string
	cameraName   string
	cameraChosen bool

	justPairedID string

	pairing bool

	session    ScanSession
	sessionGen int

	status    string
	statusErr bool

	statusSeq  int
	addedCount int

	embedded bool

	leaveFrom state

	addedValue float64
	err        error
}

func newModel(ctx context.Context, s Searcher, add Adder, sc Scanner, initialName string,
	dests []Destination, opts ...Option) model {
	ni := textinput.New()
	ni.Placeholder = "Card name, e.g. Ulamog, the Infinite Gyre"
	ni.Focus()
	ni.CharLimit = 200
	ni.Width = 50

	qi := textinput.New()
	qi.Placeholder = "1"
	qi.CharLimit = 6
	qi.Width = 10

	ci := textinput.New()
	ci.Placeholder = "123456"
	ci.CharLimit = 7
	ci.Width = 12

	th := ui.DefaultTheme()
	for _, in := range []*textinput.Model{&ni, &qi} {
		in.PromptStyle = th.Prompt
		in.Cursor.Style = th.Accent
	}

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	l := list.New(nil, cascadeDelegate{theme: th}, 0, 0)
	l.SetShowStatusBar(false)
	l.SetShowTitle(false)

	l.DisableQuitKeybindings()

	m := model{
		ctx:       ctx,
		searcher:  s,
		adder:     add,
		scanner:   sc,
		theme:     th,
		dests:     dests,
		nameInput: ni,
		qtyInput:  qi,
		codeInput: ci,
		spinner:   sp,
		list:      l,
		width:     80,
		height:    22,
		now:       time.Now,
	}
	if len(dests) > 0 {
		m.dest = dests[0]
	}
	if strings.TrimSpace(initialName) != "" {
		m.nameInput.SetValue(strings.TrimSpace(initialName))
		m.state = stateLoading
	} else {
		m.state = stateName
	}
	for _, opt := range opts {
		opt(&m)
	}
	return m
}

func (m model) Init() tea.Cmd {
	if m.state == stateLoading {
		return tea.Batch(m.spinner.Tick, m.searchPrintsCmd(m.nameInput.Value()))
	}
	return textinput.Blink
}

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

func (m model) listCamerasCmd() tea.Cmd {
	scanner := m.scanner
	return func() tea.Msg {
		devices, err := scanner.Devices(m.ctx)
		return camerasMsg{devices: devices, err: err}
	}
}

func (m model) openSessionCmd() tea.Cmd {
	scanner, deviceID := m.scanner, m.cameraID
	return func() tea.Msg {
		s, err := scanner.Open(m.ctx, deviceID)
		return sessionMsg{session: s, err: err}
	}
}

func nextEventCmd(s ScanSession, gen int) tea.Cmd {
	events := s.Events()
	return func() tea.Msg {
		ev, ok := <-events
		return sessionEventMsg{gen: gen, ev: ev, ok: ok}
	}
}

func (m *model) beginScan() tea.Cmd {
	if len(m.dests) > 1 && !m.destPicked {
		m.status = ""
		m.destForSession = true
		showPicker(m, "Scanned cards go to", m.dests, stateDestPick, func(_ int, d Destination) list.Item {
			return destItem{d}
		})
		for i, d := range m.dests {
			if d.ID == m.dest.ID {
				m.list.Select(i)
				break
			}
		}
		return nil
	}
	if m.session != nil {

		m.status = ""
		m.state = stateCapture
		return nil
	}
	m.status = ""
	m.state = stateCameraBusy

	if id := m.justPairedID; id != "" {
		m.justPairedID = ""
		m.cameraID = id
		m.cameraChosen = true
		return tea.Batch(m.spinner.Tick, m.openSessionCmd())
	}
	return tea.Batch(m.spinner.Tick, m.listCamerasCmd())
}

func (m *model) closeSession() {
	if m.session == nil {
		return
	}
	_ = m.session.Close()
	m.session = nil
	m.sessionGen++
}

const maxFuzzyTries = 5

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.list.SetSize(msg.Width, m.listHeight())

		m.nameInput.Width = max(min(50, msg.Width-4), 10)
		return m, nil

	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {

			return m, m.leaveNow()
		}
		return m.handleKey(msg)

	case printsMsg:
		return m.onPrints(msg)

	case namesMsg:
		return m.onNames(msg)

	case pairedMsg:
		return m.onPaired(msg)
	case camerasMsg:
		return m.onCameras(msg)

	case sessionMsg:
		return m.onSession(msg)

	case sessionEventMsg:
		return m.onSessionEvent(msg)

	case completedMsg:
		return m.onCompleted(msg)

	case resolveDoneMsg:
		return settleAfterResolve(m.onResolveDone(msg))

	case nudgeMsg:
		return m.onNudge(msg)

	case flashDeadlineMsg:

		if m.deferredFlashArmed && m.deferredFlashFor == msg.name {

			if m.resolving > 0 {
				m.flashOverdue = true
				m.note("outcome %q: ceiling hit with a read in flight, holding the flash",
					orDash(msg.name))
				return m, nil
			}
			m.flushDeferredFlash()
			m.note("outcome %q: no better read within %dms, review it",
				orDash(msg.name), decisionCeiling.Milliseconds())
		}
		return m, nil

	case errMsg:
		return m.failToName(msg.err.Error())

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}

	return m.updateActive(msg)
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {

	if m.statusErr {
		m.status, m.statusErr = "", false
	}

	if msg.Type == tea.KeyCtrlS && m.reviewing() && !m.list.SettingFilter() {
		m.addPalette = nil
		m.recordSkip(*m.current)
		m.status, m.statusErr = "skipped", false
		return m.afterCard()
	}

	if msg.Type == tea.KeyCtrlD {
		m.addPalette = nil
		return m.finishAdding()
	}

	if m.addPalette != nil {
		return m.handleAddPaletteKey(msg)
	}
	switch m.state {
	case stateName:
		switch msg.Type {
		case tea.KeyEsc:

			if m.reviewing() {
				return m.cancelToName()
			}

			m.leaveFrom = stateName
			m.state = stateLeaveConfirm
			return m, nil
		case tea.KeyTab:

			if len(m.review) > 0 {
				return m.showReviewList()
			}
			return m, nil
		case tea.KeyCtrlP:

			if m.scanner == nil {
				return m.failToName("Card scanning isn't available in this build")
			}
			m.pairing = true
			m.state = statePairIntro
			return m, nil
		case tea.KeyCtrlO:
			if m.scanner == nil {
				return m.failToName("Card scanning isn't available in this build")
			}

			cmd := m.beginScan()
			return m, cmd
		case tea.KeyRunes:

			if string(msg.Runes) == ":" && strings.TrimSpace(m.nameInput.Value()) == "" {
				return m.openAddPalette()
			}
		case tea.KeyEnter:
			name := strings.TrimSpace(m.nameInput.Value())
			if name == "" {
				return m, nil
			}
			m.status = ""
			m.state = stateLoading
			return m, tea.Batch(m.spinner.Tick, m.searchPrintsCmd(name))
		}
	case stateCameraBusy:
		if msg.Type == tea.KeyEsc {
			return m.cancelToName()
		}
	case stateCameraPick:
		if msg.Type == tea.KeyEsc {
			m.pairing = false
		}
		if next, cmd, ok := m.pickerKey(msg, func(it list.Item) (tea.Model, tea.Cmd, bool) {
			ci, ok := it.(cameraItem)
			if !ok {
				return nil, nil, false
			}
			m.cameraID = ci.dev.ID
			m.cameraName = ci.dev.Name
			m.cameraChosen = true

			if m.pairing {
				m.codeInput.SetValue("")
				m.codeInput.Focus()

				m.status, m.statusErr = "", false
				m.state = statePairCode
				return m, nil, true
			}
			m.state = stateCameraBusy
			return m, tea.Batch(m.spinner.Tick, m.openSessionCmd()), true
		}); ok {
			return next, cmd
		}
	case stateCapture:
		switch msg.Type {
		case tea.KeyCtrlP:

			m.closeSession()
			m.pairing = true
			m.state = statePairIntro
			return m, nil
		case tea.KeyCtrlO:

			m.closeSession()
			cmd := m.beginScan()
			return m, cmd
		case tea.KeySpace:

			return m.requestCapture()
		case tea.KeyTab:
			if len(m.review) > 0 {
				return m.showReviewList()
			}
			return m, nil
		case tea.KeyUp:

			return m.scrollTally(1)
		case tea.KeyDown:
			return m.scrollTally(-1)
		case tea.KeyPgUp:
			return m.scrollTally(tallyShown)
		case tea.KeyPgDown:
			return m.scrollTally(-tallyShown)
		case tea.KeyHome:
			return m.scrollTally(len(m.tally))
		case tea.KeyEnd:
			return m.scrollTally(-len(m.tally))
		case tea.KeyRunes:
			switch {
			case strings.EqualFold(string(msg.Runes), "t"):
				return m.toggleTorch()
			case string(msg.Runes) == "+" || string(msg.Runes) == "=":

				return m.promotePending()
			case strings.EqualFold(string(msg.Runes), "c"):

				if len(m.review) > 0 || m.resolving > 0 {
					m.state = stateClosePrompt
					return m, nil
				}
				m.closeSession()
				return m.cancelToName()
			}
		case tea.KeyEsc:

			m.leaveFrom = stateCapture
			m.state = stateLeaveConfirm
			return m, nil
		}
	case stateCapturing:

		if msg.Type == tea.KeyEsc {
			if len(m.review) > 0 || m.resolving > 0 {
				m.state = stateClosePrompt
				return m, nil
			}
			m.closeSession()
			return m.cancelToName()
		}
	case stateQueueReview:
		if m.list.SettingFilter() {
			break
		}
		switch msg.Type {
		case tea.KeyTab, tea.KeyEsc:

			if m.session == nil {
				return m.resetForNext()
			}
			m.state = stateCapture
			return m, nil
		case tea.KeyEnter:
			ri, ok := m.list.SelectedItem().(reviewItem)
			if !ok {
				return m, nil
			}
			m.takeFromReview(ri.it.id)
			return m.startReview(ri.it)
		case tea.KeyRunes:

			if string(msg.Runes) != "d" {
				break
			}
			ri, ok := m.list.SelectedItem().(reviewItem)
			if !ok {
				return m, nil
			}
			m.takeFromReview(ri.it.id)
			m.recordSkip(ri.it)
			if len(m.review) == 0 {
				m.state = stateCapture
				return m, nil
			}
			return m.showReviewList()
		}
	case stateClosePrompt:
		switch {
		case msg.Type == tea.KeyEnter || msg.String() == "r":

			m.closeSession()
			m.walking = true
			m.walkDone = 0
			if len(m.review) == 0 {

				m.state = stateLoading
				return m, m.spinner.Tick
			}
			next := m.review[0]
			m.review = m.review[1:]
			return m.startReview(next)
		case msg.String() == "d":

			m.closeSession()
			m.resolveGen++
			dropped := len(m.review) + m.resolving
			m.resolving = 0
			m.review = nil
			if dropped > 0 {
				m.summary.add("discarded", fmt.Sprintf("%d scanned cards discarded unprocessed", dropped))
			}
			m.status = fmt.Sprintf("%d scanned cards discarded", dropped)
			m.statusErr = false
			return m.resetForNext()
		case msg.Type == tea.KeyEsc:
			m.state = stateCapture
			return m, nil
		}
	case stateLoading:

		if msg.Type == tea.KeyEsc && m.walking {
			m.walking = false
			dropped := m.resolving
			m.resolveGen++
			m.resolving = 0
			if dropped > 0 {
				m.summary.add("discarded", fmt.Sprintf("%d scanned cards discarded unprocessed", dropped))
			}
			m.status = fmt.Sprintf("stopped waiting; %d unresolved scans discarded", dropped)
			m.statusErr = false
			return m.resetForNext()
		}
	case stateAbandonConfirm:
		if msg.Type == tea.KeyRunes && strings.EqualFold(string(msg.Runes), "y") {
			return m.abandonReviewWalk()
		}

		if m.current == nil {
			return m.afterCard()
		}
		return m.startReview(*m.current)
	case stateLeaveConfirm:
		if msg.Type == tea.KeyRunes && strings.EqualFold(string(msg.Runes), "y") {
			return m, m.leaveNow()
		}

		m.state = m.leaveFrom
		if m.leaveFrom == stateName {
			return m, textinput.Blink
		}
		return m, nil
	case stateNamePick:
		if next, cmd, ok := m.pickerKey(msg, func(it list.Item) (tea.Model, tea.Cmd, bool) {
			ni, ok := it.(nameItem)
			if !ok {
				return nil, nil, false
			}
			m.state = stateLoading
			return m, tea.Batch(m.spinner.Tick, m.searchPrintsCmd(string(ni))), true
		}); ok {
			return next, cmd
		}
	case statePrintPick:

		if msg.Type == tea.KeyCtrlA && m.printsAll != nil {
			all := m.printsAll
			m.printsAll, m.prints = nil, all

			showPicker(&m, "Select a printing", all, statePrintPick,
				func(i int, c scryfall.Card) list.Item {
					return printItem{card: c, scanned: i == 0}
				})
			m.status = fmt.Sprintf("showing all %d printings", len(all))
			m.statusErr = false
			return m, nil
		}
		if next, cmd, ok := m.pickerKey(msg, func(it list.Item) (tea.Model, tea.Cmd, bool) {
			pi, ok := it.(printItem)
			if !ok {
				return nil, nil, false
			}
			card := pi.card
			m.chosen = &card
			next, cmd := m.advanceAfterPrint()
			return next, cmd, true
		}); ok {
			return next, cmd
		}
	case stateFinishPick:
		if next, cmd, ok := m.pickerKey(msg, func(it list.Item) (tea.Model, tea.Cmd, bool) {
			fi, ok := it.(finishItem)
			if !ok {
				return nil, nil, false
			}
			m.finish = finish.Finish(fi)
			next, cmd := m.toDest()
			return next, cmd, true
		}); ok {
			return next, cmd
		}
	case stateDestPick:
		if next, cmd, ok := m.pickerKey(msg, func(it list.Item) (tea.Model, tea.Cmd, bool) {
			di, ok := it.(destItem)
			if !ok {
				return nil, nil, false
			}
			m.dest = di.d

			if m.destForSession {
				m.destForSession = false
				m.destPicked = true
				cmd := m.beginScan()
				return m, cmd, true
			}
			next, cmd := m.toQty()
			return next, cmd, true
		}); ok {
			return next, cmd
		}
	case statePairIntro:
		if msg.Type == tea.KeyEsc {
			m.pairing = false
			return m.cancelToName()
		}
		if msg.Type == tea.KeyEnter {
			m.state = stateCameraBusy
			return m, tea.Batch(m.spinner.Tick, m.listCamerasCmd())
		}
	case statePairCode:
		if msg.Type == tea.KeyEsc {
			m.pairing = false
			return m.cancelToName()
		}
		if msg.Type == tea.KeyEnter {
			return m.submitPairCode()
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

func (m model) updateActive(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.state {
	case stateName:
		m.nameInput, cmd = m.nameInput.Update(msg)
	case stateQty:
		m.qtyInput, cmd = m.qtyInput.Update(msg)
	case statePairCode:
		m.codeInput, cmd = m.codeInput.Update(msg)
	case stateNamePick, statePrintPick, stateFinishPick, stateDestPick, stateCameraPick, stateQueueReview:
		m.list, cmd = m.list.Update(msg)
	case stateLoading, stateCapturing, stateCameraBusy:
		m.spinner, cmd = m.spinner.Update(msg)
	}
	return m, cmd
}

func (m model) onPrints(msg printsMsg) (tea.Model, tea.Cmd) {
	if len(msg.cards) == 0 {

		return m, m.autocompleteCmd(msg.name)
	}
	m.prints = msg.cards
	if len(msg.cards) == 1 {
		card := msg.cards[0]
		m.chosen = &card
		return m.advanceAfterPrint()
	}

	cards, matched := rankByScan(msg.cards, m.scannedSet, m.scannedNumber)

	if !matched && m.scannedPromoted {
		matched = true
	}

	m.printsAll = nil
	if idxs := numberMatches(cards, m.scannedSet, m.scannedNumber, ""); len(idxs) > 0 &&
		len(idxs) < len(cards) {
		kept := make([]scryfall.Card, 0, len(idxs))
		for _, i := range idxs {
			kept = append(kept, cards[i])
		}
		m.printsAll, cards, matched = cards, kept, true
		if len(cards) == 1 && m.narrowCorroborated(cards[0]) {
			m.printsAll, m.prints = nil, cards
			card := cards[0]
			m.chosen = &card
			return m.advanceAfterPrint()
		}
	}
	m.prints = cards

	showPicker(&m, "Select a printing", cards, statePrintPick, func(i int, c scryfall.Card) list.Item {
		return printItem{card: c, scanned: matched && i == 0}
	})
	if m.printsAll != nil {
		m.status = fmt.Sprintf("showing the %d printings matching #%s · ctrl+a for all %d",
			len(cards), m.scannedNumber, len(m.printsAll))
		m.statusErr = false
	}
	if m.scannedNumber != "" && !matched {

		m.status = fmt.Sprintf("Card #%s isn't among these printings · pick manually", m.scannedNumber)
		m.statusErr = true
	}
	return m, nil
}

func (m model) narrowCorroborated(c scryfall.Card) bool {
	if m.scannedSet != "" && strings.EqualFold(c.Set, m.scannedSet) {
		return true
	}
	if m.reviewing() {
		if y := m.current.raw.CopyrightYear; y > 0 &&
			strings.HasPrefix(c.ReleasedAt, fmt.Sprintf("%d", y)) {
			return true
		}
	}
	return false
}

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
			best = i
			break
		}
		if best < 0 {
			best = i
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
		return m.failToName(fmt.Sprintf("No cards found matching %q", m.nameInput.Value()))
	case 1:
		m.state = stateLoading
		return m, tea.Batch(m.spinner.Tick, m.searchPrintsCmd(msg.names[0]))
	default:
		showPicker(&m, "Select a card", msg.names, stateNamePick, func(_ int, n string) list.Item {
			return nameItem(n)
		})
		return m, nil
	}
}

func (m model) onCameras(msg camerasMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {

		return m.failToName(msg.err.Error())
	}
	if m.pairing {
		phones := make([]scan.Device, 0, len(msg.devices))
		for _, d := range msg.devices {
			if d.Kind == scan.KindRemote {
				phones = append(phones, d)
			}
		}
		if len(phones) == 0 {

			m.state = statePairIntro
			m.status = "No phone found. Is Hoardling open and on screen?"
			m.statusErr = true
			return m, nil
		}
		if len(phones) == 1 {

			m.cameraID = phones[0].ID
			m.cameraName = phones[0].Name
			m.codeInput.SetValue("")
			m.codeInput.Focus()

			m.status, m.statusErr = "", false
			m.state = statePairCode
			return m, nil
		}
		showPicker(&m, "Pair with which phone?", phones, stateCameraPick,
			func(_ int, d scan.Device) list.Item { return cameraItem{dev: d} })
		return m, nil
	}
	switch len(msg.devices) {
	case 0:
		return m.failToName("No phone found: open Hoardling on your iPhone and keep it on screen, on the same Wi-Fi as this Mac")
	case 1:
		m.cameraID = msg.devices[0].ID
		m.cameraName = msg.devices[0].Name
		m.cameraChosen = true
		return m, tea.Batch(m.spinner.Tick, m.openSessionCmd())
	default:

		showPicker(&m, "Scan with which phone?", msg.devices, stateCameraPick, func(_ int, d scan.Device) list.Item {
			return cameraItem{dev: d}
		})

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

func (m model) requestCapture() (tea.Model, tea.Cmd) {
	if m.session == nil {
		return m.failToName("The camera isn't open")
	}
	if err := m.session.Capture(); err != nil {
		m.closeSession()
		return m.failToName(err.Error())
	}
	m.status = ""
	m.state = stateCapturing
	return m, m.spinner.Tick
}

func (m model) toggleTorch() (tea.Model, tea.Cmd) {
	if m.session == nil {
		return m, nil
	}
	if !m.torchCapable {
		m.status, m.statusErr = "this camera has no torch", true
		return m, nil
	}
	if err := m.session.Torch(!m.torchOn); err != nil {
		m.closeSession()
		return m.failToName(err.Error())
	}
	return m, nil
}

func (m model) onSession(msg sessionMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {

		if errors.Is(msg.err, scan.ErrNotPaired) {
			m.codeInput.SetValue("")
			m.codeInput.Focus()

			m.status = "Not paired with this Mac yet."
			m.statusErr = false
			m.state = statePairCode
			return m, nil
		}
		return m.failToName(msg.err.Error())
	}
	m.session = msg.session
	m.sessionGen++
	m.state = stateCapture
	return m, nextEventCmd(m.session, m.sessionGen)
}

func (m model) onSessionEvent(msg sessionEventMsg) (tea.Model, tea.Cmd) {

	if msg.gen != m.sessionGen || m.session == nil {
		return m, nil
	}

	if !msg.ok {
		m.session = nil
		m.sessionGen++
		if len(m.review) > 0 || m.resolving > 0 {

			m.state = stateClosePrompt
			return m, nil
		}
		if m.state == stateCapture || m.state == stateCapturing {
			return m.cancelToName()
		}
		return m, nil
	}

	again := nextEventCmd(m.session, m.sessionGen)
	switch msg.ev.Kind {
	case scan.EventScan:
		cards := msg.ev.CardList()
		if len(cards) == 0 {

			if !msg.ev.Auto {
				m.status = "Nothing readable in that frame · reframe and capture again"
				m.statusErr = true
			}
			if m.state == stateCapturing {
				m.state = stateCapture
			}
			return m, again
		}

		m.lastScanMoved = msg.ev.FireReason == scan.FireMoved

		m.lastScanReplaced = msg.ev.FireReason == scan.FireReplaced
		m.lastScanFaceDelta = msg.ev.FaceDelta
		switch msg.ev.FireReason {
		case scan.FireRemoved, scan.FireReplaced, scan.FireMoved:
			m.lastScanNudged = false
		default:

			m.lastScanNudged = !m.nudgeSentAt.IsZero() &&
				m.now().Sub(m.nudgeSentAt) < nudgeEchoWindow
		}
		m.nudgeGen++
		m.captureSeq++
		cmds := []tea.Cmd{again}
		for _, c := range cards {
			m.nextResolveID++
			m.resolving++
			cmds = append(cmds, m.resolveCardCmd(m.nextResolveID, c, len(cards)))
		}
		if m.state == stateCapturing {
			m.state = stateCapture
		}
		return m, tea.Batch(cmds...)

	case scan.EventError:

		m.status = msg.ev.Message
		m.statusErr = true
		if m.state == stateCapturing {
			m.state = stateCapture
		}
		return m, again

	case scan.EventClosed:
		m.closeSession()
		if len(m.review) > 0 || m.resolving > 0 {
			m.state = stateClosePrompt
			return m, nil
		}
		return m.cancelToName()

	case scan.EventReady:
		if msg.ev.Device != "" {
			m.cameraName = msg.ev.Device
		}

		m.note("versions phone=%s hoard=%s",
			cmp.Or(msg.ev.AppVersion, "unstamped"), hoardBuildVersion())

		m.autoCapable = slices.Contains(msg.ev.Features, "auto")
		if m.autoCapable {
			_ = m.session.Auto(true)
			m.autoState = "armed"
		} else {
			m.autoState = ""
		}
		m.hudCapable = slices.Contains(msg.ev.Features, "hud")

		m.torchCapable = slices.Contains(msg.ev.Features, "torch")
		m.torchOn = false
		return m, again

	case scan.EventAuto:
		m.autoState = msg.ev.State

		return m, again

	case scan.EventTorch:
		m.torchOn = msg.ev.State == "on"
		if m.torchOn {
			m.status, m.statusErr = "torch on · watch for glare on foils", false
		} else {
			m.status, m.statusErr = "torch off", false
		}
		return m, again
	case scan.EventPromote:

		next, cmd := m.promotePending()
		return next, tea.Batch(cmd, again)
	}

	return m, again
}

func (m model) note(format string, args ...any) {
	if m.session != nil {
		m.session.Note(fmt.Sprintf(format, args...))
	}
}

func (m model) onResolveDone(msg resolveDoneMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.resolveGen {
		return m, nil
	}
	if !msg.supplementary && m.resolving > 0 {
		m.resolving--
	}
	it := msg.item
	now := m.now()

	if !m.reviewing() && it.captureSeq != m.statusSeq {
		m.status, m.statusErr = "", false
		m.statusSeq = it.captureSeq
	}

	resolved := it.canonical
	if resolved == "" {
		resolved = "miss:" + it.ocrLine
	}
	m.note("resolve %q line=%d name=%dms prints=%dms rank=%s match=%s set=%s num=%s%s prints=%d%s%s%s",
		resolved, it.lineIdx, msg.nameDur.Milliseconds(), msg.printsDur.Milliseconds(),
		it.rank, matchDesc(it.match), orDash(it.raw.SetCode), orDash(it.raw.CollectorNumber),
		numberSourceSuffix(it.raw.NumberSource), len(it.prints), borderSuffix(it),
		finishSuffix(it), siblingSuffix(it))

	upgraded, replacedQueued := m.upgradeQueued(&it)
	if replacedQueued {
		m.note("outcome %q re-read: %s beats the queued %s, replacing it",
			it.canonical, it.rank, upgraded)
	}

	if it.fromNudge && it.canonical != "" && seenRecently(m.recentNames, it.canonical, now) {

		if !replacedQueued {
			m.note("outcome %q dropped: nudge echo of a card already handled", it.canonical)
			m.nudgeDrops++
			m.recentNames = recordName(m.recentNames, it.canonical, now)
			m.status = fmt.Sprintf("Still seeing %s, waiting for the next card", it.canonical)
			m.statusErr = false

			return m, m.scheduleNudge()
		}

	}

	if it.canonical == "" && it.errText == "" && !hasPrintingEvidence(it.raw) {
		m.note("outcome %q killed: nothing identifiable in the capture", it.ocrLine)
		m.ignored++
		if it.ocrLine == "" {
			m.status = "Ignored a capture with nothing readable in it"
		} else {
			m.status = fmt.Sprintf("Ignored %q: not a card", it.ocrLine)
		}
		m.statusErr = false
		return m, m.scheduleNudge()
	}

	auto, finish, note := verdict(it)
	if auto {
		if prior, since, dup := dupCapture(m.recent, it.prints[0].ID, now); dup {

			switch {
			case prior.captureSeq == it.captureSeq:

				it.dup = true
			case replacedQueued:

				it.dup = true
			case it.siblings > 1 || it.fromNudge:

				return m.suppressRepeat(it, finish, prior, now,
					"lingering neighbour of a just-added card", true)
			case it.fromMoved:

				return m.suppressRepeat(it, finish, prior, now,
					"same card, the source says it only moved", false)
			case since < sameCardFloor:

				return m.suppressRepeat(it, finish, prior, now,
					fmt.Sprintf("same card, re-read %dms after the last sighting",
						since.Milliseconds()), false)
			default:

				it.dup = true
			}
		}
	}

	if auto && !it.dup && !replacedQueued {
		if prior, since, dup := dupCaptureByName(m.recent, it.canonical, now); dup &&
			prior.scryfallID != it.prints[0].ID && prior.captureSeq != it.captureSeq &&
			(it.fromMoved || since < sameCardFloor) {
			why := fmt.Sprintf("re-read %dms later", since.Milliseconds())
			if it.fromMoved {
				why = "the source says it only moved"
			}
			m.note("outcome %q dropped: same card re-read onto a different printing, %s",
				it.canonical, why)
			m.recent = touchCommit(m.recent, prior.scryfallID, now)
			m.recentNames = recordName(m.recentNames, it.canonical, now)
			m.status = fmt.Sprintf("Still seeing %s, waiting for the next card", it.canonical)
			m.statusErr = false
			return m, m.scheduleNudge()
		}
	}

	if auto {
		card := it.prints[0]
		_, evidenced := finishFromEvidence(card, it.finishHint)
		printingGuessed := it.rank == scanMatchYearAndFrame
		res := Result{Card: card, Finish: finish, Qty: 1, ContainerID: m.dest.ID,
			FinishGuessed: !evidenced, PrintingGuessed: printingGuessed}
		if err := m.adder(res); err != nil {

			m.note("outcome %q queued: add failed: %v", it.canonical, err)
			m.reviewFlash()
			nudge := m.scheduleNudge()
			it.note = "add failed: " + err.Error()
			it.queuedAt = now
			m.review = append(m.review, it)
			next, cmd := m.reviewChanged()
			return next, tea.Batch(cmd, nudge)
		}
		complete := m.dispatchCompletion(res)

		chosen := ""
		if read := it.raw.SetCode; read != "" && !strings.EqualFold(read, card.Set) {
			chosen = fmt.Sprintf(" (read %s/%s)", strings.ToUpper(it.raw.SetCode),
				orDash(it.raw.CollectorNumber))
		}
		guessed := ""
		if !evidenced {
			guessed = " (finish guessed)"
		}
		if printingGuessed {
			guessed += " (printing guessed)"
		}
		m.note("outcome %q committed: %s/%s %s%s%s", card.Name,
			strings.ToUpper(card.Set), card.CollectorNumber, finish, chosen, guessed)
		m.recent = recordCommit(m.recent, card, finish, it.captureSeq, now, !evidenced,
			printingGuessed)
		m.recentNames = recordName(m.recentNames, it.canonical, now)
		m.nudgeDrops = 0

		m.secondLookFor = ""

		m.clearDeferredFlash(it.canonical)

		if m.deferredFlashArmed && m.deferredFlashFor == "" {
			m.deferredFlashArmed = false
			m.flashOverdue = false
			m.note("outcome %q: commit answered an unidentified capture's held flash", it.canonical)
		}

		m.pending = clearUnofferedPending(m.pending)
		m.addedCount++
		m.addedValue += priceValue(card, finish)

		m.celebrate(card.Name, priceValuePtr(card, finish), finish)
		line := fmt.Sprintf("%s (%s/%s) %s · %s", card.Name,
			strings.ToUpper(card.Set), card.CollectorNumber, finish, priceForFinish(card, finish))
		m.recordTally(line)
		m.summary.add("auto", line)

		return m, tea.Batch(m.scheduleNudge(), complete)
	}

	if !it.dup && !replacedQueued && it.canonical != "" {
		if since, seen := seenWithin(m.recentNames, it.canonical, now); seen &&
			(it.fromMoved || since < sameCardFloor) {
			why := fmt.Sprintf("re-read %dms later", since.Milliseconds())
			if it.fromMoved {
				why = "the source says it only moved"
			}
			m.note("outcome %q dropped: worse re-read of a card already handled, %s",
				it.canonical, why)
			m.recentNames = recordName(m.recentNames, it.canonical, now)
			m.status = fmt.Sprintf("Still seeing %s, waiting for the next card", it.canonical)
			m.statusErr = false
			return m, m.scheduleNudge()
		}
	}

	if it.canonical == "" && it.errText == "" {
		if prior, ok := footerEcho(m.recent, it.raw, now); ok {
			m.note("outcome %q dropped: footer echo of %q, committed moments ago",
				orDash(it.ocrLine), prior.name)

			m.recent = touchCommit(m.recent, prior.scryfallID, now)
			m.status = fmt.Sprintf("Still seeing %s, waiting for the next card", prior.name)
			m.statusErr = false
			return m, m.scheduleNudge()
		}
	}

	if !replacedQueued && it.canonical != "" && !it.match.Exact &&
		it.match.Similarity < cardname.AutoCommitSimilarity &&
		it.raw.CollectorNumber == "" {
		if len(m.recent) > 0 && now.Sub(m.recent[len(m.recent)-1].at) < sameCardFloor {
			m.note("outcome %q dropped: sub-floor name in %q's slide window",
				it.canonical, m.recent[len(m.recent)-1].name)
			m.status = fmt.Sprintf("Still seeing %s, waiting for the next card",
				m.recent[len(m.recent)-1].name)
			m.statusErr = false
			return m, m.scheduleNudge()
		}
	}

	if it.canonical == "" && it.errText == "" && len(it.prints) == 0 && it.ocrLine != "" {
		if name, ok := mangledEcho(m.recentNames, it.ocrLine, now); ok {
			m.note("outcome %q dropped: mid-slide mangle of %q", it.ocrLine, name)
			m.status = fmt.Sprintf("Still seeing %s, waiting for the next card", name)
			m.statusErr = false
			return m, m.scheduleNudge()
		}
	}

	if !it.dup && !replacedQueued && (it.fromNudge || it.siblings > 1) {
		probe := it.canonical
		if probe == "" {
			probe = it.ocrLine
		}
		if name, ok := similarRecent(m.recentNames, probe, now); ok {
			m.note("outcome %q dropped: OCR variant of %q, seen moments ago", probe, name)
			m.recentNames = recordName(m.recentNames, name, now)
			m.status = fmt.Sprintf("Still seeing %s, waiting for the next card", name)
			m.statusErr = false
			return m, m.scheduleNudge()
		}
	}

	if it.canonical == "" && it.errText == "" {
		m.note("outcome %q skipped: card in frame but unreadable, not queued", orDash(it.ocrLine))
		m.summary.add("skipped", fmt.Sprintf("unreadable capture (%s)", orDash(it.ocrLine)))
		m.status = "Card seen but unreadable — waiting for the next look"
		m.statusErr = false
		return m, m.scheduleNudge()
	}

	if it.canonical != "" {
		for _, q := range m.review {
			if q.canonical == it.canonical {
				m.note("outcome %q dropped: already awaiting review", it.canonical)

				m.summary.add("dropped-repeat",
					fmt.Sprintf("%s — repeat sighting while queued; if it was a second copy, add it by hand", it.canonical))
				m.recentNames = recordName(m.recentNames, it.canonical, now)
				m.status = fmt.Sprintf("%s is already in the review queue", it.canonical)
				m.statusErr = false
				return m, m.scheduleNudge()
			}
		}
	}
	if it.canonical != "" {
		m.recentNames = recordName(m.recentNames, it.canonical, now)
	}
	m.note("outcome %q queued: %s", orDash(it.canonical), note)
	m.nudgeDrops = 0

	m.pending = clearUnofferedPending(m.pending)

	secondLook := m.wantsSecondLook(it)

	deferrable := secondLook || (m.session != nil && m.autoCapable)
	if !deferrable {
		m.reviewFlash()
		m.clearDeferredFlash(it.canonical)
	}
	it.note = note
	it.queuedAt = now
	m.review = append(m.review, it)

	nudge := m.scheduleNudge()
	var deadline tea.Cmd
	if deferrable {
		m.deferredFlashFor = it.canonical
		m.deferredFlashArmed = true

		deadline = delayedMsg(m.ctx, decisionCeiling, flashDeadlineMsg{name: it.canonical})
	}
	if secondLook {
		m.secondLookFor = it.canonical
		m.note("outcome %q: looking again", it.canonical)

		if m.session != nil && m.autoCapable && m.autoState == "held" {
			_ = m.session.Rearm()
			m.nudgeSentAt = m.now()
			m.note("rescue %q: rearm sent into held", it.canonical)
		}
	}
	next, cmd := m.reviewChanged()
	return next, tea.Batch(cmd, nudge, deadline)
}

func (m model) suppressRepeat(it queueItem, fin finish.Finish, prior recentCommit,
	now time.Time, why string, besideNew bool) (tea.Model, tea.Cmd) {

	if card := it.prints[0]; fin != prior.finish && prior.finishGuessed {
		if _, evidenced := finishFromEvidence(card, it.finishHint); evidenced {
			res := Result{Card: card, Finish: fin, Qty: 1,
				ContainerID: m.dest.ID, ReplacesFinish: prior.finish}
			if err := m.adder(res); err != nil {
				m.note("outcome %q dropped: %s (fin correction failed: %v)",
					it.canonical, why, err)
			} else {
				correction := m.dispatchCompletion(res)
				m.note("outcome %q corrected: %s/%s %s → %s, %s", it.canonical,
					strings.ToUpper(card.Set), card.CollectorNumber,
					prior.finish, fin, why)
				m.recent = rekeyCommit(m.recent, card.ID, prior.finish, fin, now)
				m.recentNames = recordName(m.recentNames, it.canonical, now)
				m.addedValue += priceValue(card, fin) - priceValue(card, prior.finish)
				line := fmt.Sprintf("%s (%s/%s) %s · corrected from %s", card.Name,
					strings.ToUpper(card.Set), card.CollectorNumber, fin, prior.finish)
				m.recordTally(line)
				m.summary.add("auto", line)
				m.status = fmt.Sprintf("Corrected %s to %s", it.canonical, fin)
				m.statusErr = false
				return m, tea.Batch(m.scheduleNudge(), correction)
			}
		}
	}
	m.note("outcome %q dropped: %s", it.canonical, why)
	m.recent = touchCommit(m.recent, it.prints[0].ID, now)
	m.recentNames = recordName(m.recentNames, it.canonical, now)
	m.pending = &pendingDup{it: it, finish: fin, at: now}
	seeing := fmt.Sprintf("Still seeing %s", it.canonical)
	if besideNew {
		seeing += " beside the new card"
	}

	m.status = seeing + " — press + if that's a second copy"
	m.statusErr = false

	if m.session != nil && m.hudCapable && it.fromReplaced {
		if err := m.session.Result(scan.HUDResult{
			Note:    seeing + " — another copy?",
			Promote: true,
		}); err == nil {

			m.pending.offered = true
		}
	}
	return m, m.scheduleNudge()
}

func (m model) promotePending() (tea.Model, tea.Cmd) {
	p := m.pending
	if p == nil || m.now().Sub(p.at) > pendingDupWindow {

		m.pending = nil
		m.status = "Nothing to add a second copy of"
		m.statusErr = false
		return m, nil
	}
	m.pending = nil
	now := m.now()
	card := p.it.prints[0]
	_, evidenced := finishFromEvidence(card, p.it.finishHint)
	res := Result{Card: card, Finish: p.finish, Qty: 1, ContainerID: m.dest.ID,
		FinishGuessed: !evidenced}
	if err := m.adder(res); err != nil {
		m.note("outcome %q queued: promote failed: %v", p.it.canonical, err)
		m.reviewFlash()
		it := p.it
		it.note = "add failed: " + err.Error()
		it.queuedAt = now
		m.review = append(m.review, it)
		next, cmd := m.reviewChanged()
		return next, cmd
	}
	complete := m.dispatchCompletion(res)
	m.note("outcome %q committed: %s/%s %s (promoted by hand)", card.Name,
		strings.ToUpper(card.Set), card.CollectorNumber, p.finish)

	m.captureSeq++

	m.recent = recordCommit(m.recent, card, p.finish, m.captureSeq, now, !evidenced, false)
	m.recentNames = recordName(m.recentNames, p.it.canonical, now)
	m.addedCount++
	m.addedValue += priceValue(card, p.finish)
	m.celebrate(card.Name, priceValuePtr(card, p.finish), p.finish)
	line := fmt.Sprintf("%s (%s/%s) %s · %s", card.Name,
		strings.ToUpper(card.Set), card.CollectorNumber, p.finish,
		priceForFinish(card, p.finish))
	m.recordTally(line)
	m.summary.add("duplicate-confirmed", line)
	m.status = "✓ Added a second copy of " + card.Name
	m.statusErr = false
	return m, complete
}

func (m model) chime() {
	if m.session != nil {
		_ = m.session.Chime()
	}
}

func (m model) celebrate(name string, price *float64, finish finish.Finish) {
	if m.session == nil {
		return
	}
	if !m.hudCapable {
		m.chime()
		return
	}
	r := scan.HUDResult{
		Amount: price, Tier: tierFor(price), Finish: finish.String(),
		Name: name,
	}

	if price != nil {
		t := m.addedValue
		r.Total = &t
	}
	_ = m.session.Result(r)
}

func (m model) reviewFlash() {
	if m.session == nil {
		return
	}
	if !m.hudCapable {
		m.chime()
		return
	}
	_ = m.session.Result(scan.HUDResult{Tier: tierReview})
}

func (m *model) clearDeferredFlash(name string) {
	if name != "" && m.deferredFlashFor == name {
		m.deferredFlashFor = ""
		m.deferredFlashArmed = false
		m.flashOverdue = false
	}
}

func (m *model) flushDeferredFlash() bool {
	if !m.deferredFlashArmed {
		return false
	}
	m.deferredFlashArmed = false
	m.deferredFlashFor = ""
	m.flashOverdue = false
	m.reviewFlash()
	return true
}

func (m model) hudTotal() {
	if m.session == nil || !m.hudCapable {
		return
	}
	t := m.addedValue
	_ = m.session.Result(scan.HUDResult{Total: &t})
}

func (m model) reviewChanged() (tea.Model, tea.Cmd) {
	if m.walking && m.current == nil && len(m.review) > 0 {
		next := m.review[0]
		m.review = m.review[1:]
		return m.startReview(next)
	}
	if m.state == stateQueueReview {
		return m.showReviewList()
	}
	return m, nil
}

func (m model) reviewing() bool { return m.current != nil }

func (m *model) upgradeQueued(it *queueItem) (scanMatch, bool) {
	if it.canonical == "" {
		return 0, false
	}
	for i, q := range m.review {
		if q.canonical == it.canonical && it.rank > q.rank {

			if it.finishHint == "" && q.finishHint != "" &&
				(it.captureSeq == q.captureSeq || it.fromNudge || it.fromMoved ||
					m.now().Sub(q.queuedAt) <= decisionCeiling) {
				it.finishHint = q.finishHint
			}
			m.review = append(m.review[:i], m.review[i+1:]...)
			return q.rank, true
		}
	}
	return 0, false
}

const tallyShown = 10

const tallyCap = 300

func (m *model) recordTally(line string) {
	m.tally = append(m.tally, line)
	if m.tallyOffset > 0 {
		m.tallyOffset++
	}
	if len(m.tally) > tallyCap {

		m.tally = append([]string(nil), m.tally[len(m.tally)-tallyCap:]...)
	}
	m.clampTally()
}

func (m model) tallyMaxOffset() int {
	return max(0, len(m.tally)-tallyShown)
}

func (m *model) clampTally() {
	m.tallyOffset = min(max(m.tallyOffset, 0), m.tallyMaxOffset())
}

func (m model) scrollTally(by int) (tea.Model, tea.Cmd) {
	m.tallyOffset += by
	m.clampTally()
	return m, nil
}

func (m *model) takeFromReview(id int) {
	for i, it := range m.review {
		if it.id == id {
			m.review = append(m.review[:i], m.review[i+1:]...)
			return
		}
	}
}

func (m *model) recordSkip(it queueItem) {
	m.summary.add("skipped", reviewItem{it}.Title())
}

func (m model) showReviewList() (tea.Model, tea.Cmd) {
	showPicker(&m, fmt.Sprintf("Review queue (%d)", len(m.review)), m.review, stateQueueReview,
		func(_ int, it queueItem) list.Item { return reviewItem{it} })
	return m, nil
}

func (m model) startReview(it queueItem) (tea.Model, tea.Cmd) {
	m.current = &it
	m.scanned = it.canonical
	m.scannedOCR = it.ocrLine
	m.scannedSet = it.raw.SetCode
	m.scannedNumber = it.raw.CollectorNumber
	m.scannedPromoted = it.rank == scanMatchYearOnly
	m.status, m.statusErr = "", false
	if it.note != "" {
		m.status, m.statusErr = it.note, true
	}
	if it.canonical == "" {
		m.prints = nil
		m.chosen = nil
		m.finish = finish.Finish{}
		m.qtyErr = ""
		m.nameInput.SetValue(it.ocrLine)
		m.nameInput.CursorEnd()
		m.nameInput.Focus()
		m.state = stateName
		return m, textinput.Blink
	}
	if len(it.prints) == 0 {
		m.state = stateLoading
		return m, tea.Batch(m.spinner.Tick, m.searchPrintsCmd(it.canonical))
	}

	return m.onPrints(printsMsg{name: it.canonical, cards: it.prints})
}

func settleAfterResolve(mm tea.Model, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	next, ok := mm.(model)
	if !ok {
		return mm, cmd
	}

	if next.resolving == 0 && next.flashOverdue {
		name := orDash(next.deferredFlashFor)
		if next.flushDeferredFlash() {
			next.note("outcome %q: decisioning drained without an answer, review it", name)
		}
		next.flashOverdue = false
	}
	if next.state == stateLoading && next.walking && next.current == nil &&
		len(next.review) == 0 && next.resolving == 0 {
		next.walking = false
		done, doneCmd := next.resetForNext()
		return done, tea.Batch(cmd, doneCmd)
	}
	return next, cmd
}

func (m model) afterCard() (tea.Model, tea.Cmd) {
	if !m.reviewing() {
		return m.resetForNext()
	}
	m.current = nil
	if m.walking {
		m.walkDone++
		if len(m.review) > 0 {
			next := m.review[0]
			m.review = m.review[1:]
			return m.startReview(next)
		}
		if m.resolving > 0 {

			m.scanned = ""
			m.scannedOCR = ""
			m.state = stateLoading
			return m, m.spinner.Tick
		}
		m.walking = false
		return m.resetForNext()
	}
	if len(m.review) > 0 {
		return m.showReviewList()
	}
	return m.resetForNext()
}

func (m model) advanceAfterPrint() (tea.Model, tea.Cmd) {
	finishes := finishOptions(*m.chosen)
	if len(finishes) <= 1 {
		if len(finishes) == 1 {
			m.finish = finishes[0]
		} else {
			m.finish = finish.Nonfoil
		}
		return m.toDest()
	}
	showPicker(&m, "Select a finish", finishes, stateFinishPick, func(_ int, f finish.Finish) list.Item {
		return finishItem(f)
	})

	if m.reviewing() && m.current.finishHint != "" {
		for i, f := range finishes {
			if f.String() == m.current.finishHint {
				m.list.Select(i)
				break
			}
		}
	}
	return m, nil
}

func (m model) toDest() (tea.Model, tea.Cmd) {
	if len(m.dests) <= 1 {
		return m.toQty()
	}
	showPicker(&m, "Add to", m.dests, stateDestPick, func(_ int, d Destination) list.Item {
		return destItem{d}
	})
	for i, d := range m.dests {
		if d.ID == m.dest.ID {
			m.list.Select(i)
			break
		}
	}
	return m, nil
}

func (m model) confirmAdd() (tea.Model, tea.Cmd) {
	res := Result{Card: *m.chosen, Finish: m.finish, Qty: m.qtyValue(), ContainerID: m.dest.ID}
	if err := m.adder(res); err != nil {
		return m.failToName(err.Error())
	}
	complete := m.dispatchCompletion(res)
	m.addedCount++
	m.addedValue += float64(res.Qty) * priceValue(res.Card, res.Finish)
	if m.reviewing() {

		var amt *float64
		if p := priceValuePtr(res.Card, res.Finish); p != nil {
			v := *p * float64(res.Qty)
			amt = &v
		}
		m.celebrate(res.Card.Name, amt, res.Finish)
	} else {

		m.hudTotal()
	}
	m.status = fmt.Sprintf("✓ Added %d× %s (%s/%s) %s · %s",
		res.Qty, res.Card.Name, res.Card.Set, res.Card.CollectorNumber,
		res.Finish, priceForFinish(res.Card, res.Finish))

	if len(m.dests) > 1 {
		m.status += " → " + m.dest.Name
	}
	m.statusErr = false
	if m.reviewing() {
		kind := "reviewed"
		if m.current.dup {
			kind = "duplicate-confirmed"
		}
		m.summary.add(kind, fmt.Sprintf("%d× %s (%s/%s) %s",
			res.Qty, res.Card.Name, strings.ToUpper(res.Card.Set),
			res.Card.CollectorNumber, res.Finish))
	}
	next, cmd := m.afterCard()
	return next, tea.Batch(cmd, complete)
}

func (m model) cancelToName() (tea.Model, tea.Cmd) {
	if m.reviewing() {
		return m.cancelReview()
	}
	m.status = ""
	m.destForSession = false
	return m.resetForNext()
}

func (m model) cancelReview() (tea.Model, tea.Cmd) {
	if m.walking {

		m.state = stateAbandonConfirm
		return m, nil
	}
	it := *m.current
	m.current = nil
	m.review = append([]queueItem{it}, m.review...)
	m.status, m.statusErr = "", false
	return m.showReviewList()
}

func (m model) abandonReviewWalk() (tea.Model, tea.Cmd) {
	m.current = nil
	m.walking = false
	m.resolveGen++
	dropped := len(m.review) + m.resolving + 1
	m.resolving = 0
	m.review = nil
	m.summary.add("discarded", fmt.Sprintf("%d scanned cards discarded unprocessed", dropped))
	m.status = fmt.Sprintf("Review abandoned · %d cards not added", dropped)
	m.statusErr = false
	return m.resetForNext()
}

func (m model) failToName(msg string) (tea.Model, tea.Cmd) {
	m.status = msg
	m.statusErr = true
	if m.reviewing() {
		m.summary.add("skipped", reviewItem{*m.current}.Title()+" · "+msg)
		return m.afterCard()
	}
	return m.resetForNext()
}

func (m model) resetForNext() (tea.Model, tea.Cmd) {
	m.prints = nil
	m.chosen = nil
	m.finish = finish.Finish{}
	m.qtyErr = ""
	m.scanned = ""
	m.scannedOCR = ""
	m.scannedSet = ""
	m.scannedNumber = ""
	m.scannedPromoted = false
	m.nameInput.SetValue("")

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

type pairedMsg struct{ err error }

func (m model) pairCmd(deviceID, code string) tea.Cmd {
	sc := m.scanner
	return func() tea.Msg {
		return pairedMsg{err: sc.Pair(deviceID, code)}
	}
}

func (m model) onPaired(msg pairedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {

		m.state = statePairCode
		m.status = "Pairing failed: " + msg.err.Error()
		m.statusErr = true
		m.codeInput.Focus()
		return m, nil
	}

	m.pairing = false
	m.cameraChosen = true

	m.justPairedID = m.cameraID
	m.status = fmt.Sprintf("Paired with %s", m.cameraName)

	m.statusErr = false
	cmd := m.beginScan()
	return m, cmd
}

func (m model) submitPairCode() (tea.Model, tea.Cmd) {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, m.codeInput.Value())
	if len(digits) != 6 {
		m.status = "The pairing code is six digits, shown on the phone"
		m.statusErr = true
		return m, nil
	}

	m.status = ""
	m.statusErr = false
	m.state = statePairBusy
	return m, tea.Batch(m.spinner.Tick, m.pairCmd(m.cameraID, digits))
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

func (m model) pickerKey(msg tea.KeyMsg, onEnter func(list.Item) (tea.Model, tea.Cmd, bool)) (tea.Model, tea.Cmd, bool) {
	if m.list.SettingFilter() {
		return nil, nil, false
	}
	switch msg.Type {
	case tea.KeyEsc:
		next, cmd := m.cancelToName()
		return next, cmd, true
	case tea.KeyEnter:
		return onEnter(m.list.SelectedItem())
	}
	return nil, nil, false
}

func showPicker[T any](m *model, title string, xs []T, st state, item func(int, T) list.Item) {
	items := make([]list.Item, len(xs))
	for i, x := range xs {
		items[i] = item(i, x)
	}
	m.setListItems(title, items)
	m.state = st
}

func (m *model) setListItems(title string, items []list.Item) {
	m.list.Title = title
	m.list.SetItems(items)
	m.list.ResetFilter()
	m.list.Select(0)
	m.list.SetSize(m.width, m.listHeight())
}

func (m model) cameraLabel() string {
	if m.cameraName != "" {
		return m.cameraName
	}
	return "camera"
}

func (m model) listHeight() int {
	chrome := 4
	if m.scanned != "" {
		chrome += 2
	}
	return max(m.height-chrome, 4)
}

func (m model) help(s string) string {
	return m.theme.Help.Render(strings.Join(ui.WrapHelp(s, m.width), "\n"))
}

func (m model) escWord() string {
	if m.embedded {
		return "back"
	}
	return "quit"
}

func (m model) batchHelp(base string) string {
	if m.reviewing() {
		return base + " · ctrl+s skip card"
	}
	return base
}

func (m model) scanHeader() string {
	var badge string
	if m.reviewing() {
		switch {
		case m.walking:

			badge = fmt.Sprintf("reviewing %d of %d",
				m.walkDone+1, m.walkDone+1+len(m.review))
		case len(m.review) > 0:
			badge = fmt.Sprintf("reviewing · %d more queued", len(m.review))
		default:
			badge = "reviewing"
		}
	}
	if m.scanned == "" {
		if badge == "" {
			return ""
		}
		return m.theme.Accent.Render("📷 "+badge) + "\n\n"
	}
	if badge != "" {
		badge += " · "
	}
	line := m.theme.Accent.Render("📷 " + badge + "Scanned: " + m.scanned)
	if m.scannedOCR != "" && !strings.EqualFold(m.scannedOCR, m.scanned) {
		line += m.theme.Help.Render(fmt.Sprintf("  (read %q)", m.scannedOCR))
	}
	return line + "\n\n"
}

func (m model) View() string {
	if m.width <= 0 {
		return m.viewContent()
	}
	return ansi.Wrap(m.viewContent(), m.width, "")
}

func (m model) viewContent() string {
	switch m.state {
	case stateName:
		var b strings.Builder
		b.WriteString(m.theme.Title.Render("Add cards to your collection") + "\n\n")
		if m.status != "" {
			style := m.theme.OK
			if m.statusErr {
				style = m.theme.Err
			}
			b.WriteString(style.Render(m.status) + "\n\n")
		}
		b.WriteString(m.nameInput.View() + "\n\n")

		if m.addPalette != nil {
			b.WriteString(m.addPaletteView())
			return b.String()
		}

		e := []ui.HelpEntry{ui.HelpCommands}
		if m.scanner != nil {

			open := "scan"
			if m.session != nil {
				open = "camera"
			}
			e = append(e, ui.K("ctrl+p", "pair"), ui.K("ctrl+o", open))
		}

		if n := len(m.review); n > 0 {
			e = append(e, ui.K("tab", fmt.Sprintf("review queue (%d)", n)))
		}
		e = append(e, ui.K("enter", "search"), ui.K("ctrl+d", "done"),
			ui.K("esc", m.escWord()), ui.K("ctrl+c", "force quit"))
		help := ui.Help(e...)
		if m.addedCount > 0 {
			help = m.sessionTally() + " · " + help
		}
		b.WriteString(m.help(help))
		return b.String()
	case stateCameraBusy:
		if m.pairing {
			return fmt.Sprintf("%s looking for a phone running Hoardling…\n\n%s",
				m.spinner.View(), m.help("esc cancel · ctrl+c force quit"))
		}

		if m.cameraChosen && m.cameraName != "" {
			return fmt.Sprintf("%s connecting to %s…\n\n%s",
				m.spinner.View(), m.cameraName, m.help("esc cancel · ctrl+c force quit"))
		}
		return fmt.Sprintf("%s looking for a camera or a paired phone…\n\n%s",
			m.spinner.View(), m.help("esc cancel · ctrl+c force quit"))
	case statePairBusy:
		return fmt.Sprintf("%s asking %s to accept that code…\n\n%s",
			m.spinner.View(), m.cameraName, m.help("ctrl+c force quit"))
	case statePairIntro:
		banner := ""
		if m.status != "" && m.statusErr {
			banner = m.theme.Err.Render(m.status) + "\n\n"
		}
		return banner + fmt.Sprintf(
			"Pair a phone\n\n%s\n\n%s",
			m.help("1. Open Hoardling on your iPhone.\n"+
				"2. Select Pair. It will pull up a six-digit code.\n"+
				"3. Press enter to search for the phone."),
			m.help("esc back · ctrl+c force quit"))
	case statePairCode:

		banner := ""
		if m.status != "" {
			style := m.theme.Warn
			if m.statusErr {
				style = m.theme.Err
			}
			banner = style.Render(m.status) + "\n\n"
		}
		return banner + fmt.Sprintf(
			"Pair with %s\n\n%s\n\n%s\n\n%s",
			m.cameraName,
			m.help("Enter your six digit code"),
			m.codeInput.View(),
			m.help("press enter to pair · esc back · ctrl+c force quit"))
	case stateCameraPick:
		return m.list.View() + "\n" +
			m.help("↑/↓ move · enter scan with this camera · esc back · ctrl+c force quit")
	case stateCapture:
		var b strings.Builder
		b.WriteString(m.theme.Title.Render("Scanning with "+m.cameraLabel()) + "\n\n")
		if m.status != "" {
			style := m.theme.OK
			if m.statusErr {
				style = m.theme.Err
			}
			b.WriteString(style.Render(m.status) + "\n\n")
		}

		if len(m.tally) > 0 {
			end := len(m.tally) - min(m.tallyOffset, m.tallyMaxOffset())
			start := max(0, end-tallyShown)
			for _, line := range m.tally[start:end] {
				b.WriteString(m.theme.OK.Render("✓ Auto-added: "+line) + "\n")
			}

			if len(m.tally) > tallyShown {
				b.WriteString(m.help(fmt.Sprintf("showing %d-%d of %d · ↑/↓ history",
					start+1, end, len(m.tally))) + "\n")
			}
		}
		if len(m.tally) > 0 || len(m.review) > 0 || m.resolving > 0 {
			counter := fmt.Sprintf("%d auto-added", len(m.tally))

			if m.addedValue > 0 {
				counter += fmt.Sprintf(" ($%.2f)", m.addedValue)
			}
			counter += fmt.Sprintf(" · %d need review", len(m.review))
			if m.resolving > 0 {
				counter += fmt.Sprintf(" · %d resolving", m.resolving)
			}
			b.WriteString(m.help(counter) + "\n")
		}
		if len(m.tally) > 0 || len(m.review) > 0 || m.resolving > 0 {
			b.WriteString("\n")
		}
		switch m.autoState {
		case "armed":
			b.WriteString("Set a card down and the app will run auto capture. Press spacebar to manually trigger a scan.\n\n")
		case "held":
			b.WriteString("Captured. Swap in the next card.\n\n")
		default:
			b.WriteString("Frame the next card, then press space.\n\n")
		}

		e := []ui.HelpEntry{
			ui.K("ctrl+p", "pair"), ui.K("ctrl+o", "camera"),
			ui.K("space", "capture")}
		if len(m.tally) > tallyShown {
			e = append(e, ui.K("↑/↓", "history"))
		}

		e = append(e, ui.K("c", "close camera"), ui.K("ctrl+d", "done"),
			ui.K("esc", m.escWord()), ui.K("ctrl+c", "force quit"))
		b.WriteString(m.help(ui.Help(e...)))
		return b.String()
	case stateQueueReview:
		return m.list.View() + "\n" +
			m.help("↑/↓ move · enter fix this card · d drop it · tab/esc back to camera · ctrl+c force quit")
	case stateClosePrompt:
		var b strings.Builder
		b.WriteString(m.theme.Title.Render("Close the camera?") + "\n\n")
		n := len(m.review)
		line := fmt.Sprintf("%d scanned cards are waiting for review", n)
		if m.resolving > 0 {
			line += fmt.Sprintf(" (%d still resolving)", m.resolving)
		}
		b.WriteString(line + ".\n\n")
		b.WriteString(m.help("enter review them now · d discard them · esc back to camera"))
		return b.String()
	case stateLeaveConfirm:

		noun := "cards"
		if m.addedCount == 1 {
			noun = "card"
		}
		saved := m.theme.OK.Render(fmt.Sprintf(
			"%d %s automatically saved to the database", m.addedCount, noun))

		prompt := "quit add session?"
		if n := len(m.review) + m.resolving; n > 0 {
			prompt = fmt.Sprintf("quit add session? %d unsaved scans will be dropped", n)
		}
		return saved + "\n" + m.theme.Err.Render(prompt) + m.theme.Help.Render("  y/n")
	case stateAbandonConfirm:
		n := len(m.review) + m.resolving + 1
		return m.theme.Err.Render(fmt.Sprintf(
			"abandon review? %d scanned cards will be dropped unsaved", n)) +
			m.theme.Help.Render("  y/n")
	case stateCapturing:
		return fmt.Sprintf("%s reading the card…\n\n%s",
			m.spinner.View(), m.help("esc close camera · ctrl+c force quit"))
	case stateLoading:
		keys := "ctrl+c to force quit"
		if m.walking {
			keys = "esc stop waiting (drops unresolved) · ctrl+c force quit"
		}
		return m.scanHeader() + fmt.Sprintf("%s searching Scryfall…\n\n%s",
			m.spinner.View(), m.help(keys))
	case stateNamePick, statePrintPick, stateFinishPick, stateDestPick:
		keys := "↑/↓ move · / filter · enter select · esc cancel · ctrl+c force quit"
		if m.state == statePrintPick && m.printsAll != nil {
			keys = "↑/↓ move · / filter · enter select · ctrl+a all printings · esc cancel"
		}
		return m.scanHeader() + m.list.View() + "\n" + m.help(m.batchHelp(keys))
	case stateQty:
		out := m.scanHeader() + m.theme.Prompt.Render("Quantity for "+m.chosen.Name) + "\n\n" + m.qtyInput.View()
		if m.qtyErr != "" {
			out += "\n" + m.theme.Err.Render(m.qtyErr)
		}
		return out + "\n\n" + m.help(m.batchHelp("enter to continue · esc cancel · ctrl+c force quit"))
	case stateConfirm:
		return m.scanHeader() + m.theme.Title.Render("Confirm") + "\n\n" + m.confirmSummary() + "\n\n" +
			m.help(m.batchHelp("enter to add · esc cancel · ctrl+c force quit"))
	}
	return ""
}

func (m model) confirmSummary() string {
	c := m.chosen
	finish := m.finish
	price := priceForFinish(*c, finish)
	out := fmt.Sprintf("%d× %s\n%s #%s · %s\nfinish: %s   price: %s",
		m.qtyValue(), c.Name, strings.ToUpper(c.Set), c.CollectorNumber, c.SetName,
		finish, price)

	if m.dest.Name != "" {
		out += "\nadd to: " + m.dest.Name
	}
	return out
}

func finishOptions(c scryfall.Card) []finish.Finish {
	return scryfall.Finishes(c)
}

func printMarkers(c scryfall.Card) string {
	var parts []string

	switch c.BorderColor {
	case "borderless":
		parts = append(parts, "borderless")
	case "white":
		parts = append(parts, "white border")
	case "silver", "gold":
		parts = append(parts, c.BorderColor+" border")
	}
	parts = append(parts, c.FrameEffects...)
	parts = append(parts, c.PromoTypes...)
	return strings.Join(parts, "/")
}

func priceLabel(c scryfall.Card) string {
	return "$" + priceStr(c.PriceUSD) + " / $" + priceStr(c.PriceUSDFoil) + "f"
}

func (m model) sessionTally() string {
	t := fmt.Sprintf("%d added this session", m.addedCount)
	if m.addedValue > 0 {
		t += fmt.Sprintf(" ($%.2f)", m.addedValue)
	}
	return t
}

func priceValue(c scryfall.Card, finish finish.Finish) float64 {
	p := finishPrice(c, finish)
	if p == nil {
		return 0
	}
	return *p
}

func priceForFinish(c scryfall.Card, finish finish.Finish) string {
	p := finishPrice(c, finish)
	if p == nil {
		return "—"
	}
	return "$" + priceStr(p)
}

func finishPrice(c scryfall.Card, fin finish.Finish) *float64 {
	return fin.EffectivePrice(c.PriceUSD, c.PriceUSDFoil, c.PriceUSDEtched)
}

func priceStr(p *float64) string {
	if p == nil {
		return "—"
	}
	return strconv.FormatFloat(*p, 'f', 2, 64)
}

type completedMsg struct {
	card scryfall.Card
	err  error
}

func (m *model) dispatchCompletion(res Result) tea.Cmd {
	if m.completer == nil {
		return nil
	}
	complete := m.completer
	m.completing++
	return func() tea.Msg {
		return completedMsg{card: res.Card, err: complete(res)}
	}
}

func (m model) onCompleted(msg completedMsg) (tea.Model, tea.Cmd) {
	if m.completing > 0 {
		m.completing--
	}
	if msg.err != nil {
		m.summary.add("incomplete", fmt.Sprintf("%s (%s/%s) · %v", msg.card.Name,
			strings.ToUpper(msg.card.Set), msg.card.CollectorNumber, msg.err))
		m.note("completion for %q failed: %v", msg.card.Name, msg.err)
	}
	if m.leaving && m.completing == 0 {
		m.done = true
		return m, tea.Quit
	}
	if m.leaving {
		m.status = m.drainingStatus()
	}
	return m, nil
}

func (m model) drainingStatus() string {
	return fmt.Sprintf("Finishing %s · ctrl-c leaves without waiting",
		ui.Plural(m.completing, "card", "cards"))
}

func (m *model) leaveNow() tea.Cmd {
	m.closeSession()
	if m.completing > 0 && !m.leaving {
		m.leaving = true
		m.status, m.statusErr = m.drainingStatus(), false
		return nil
	}
	m.done = true
	return tea.Quit
}
