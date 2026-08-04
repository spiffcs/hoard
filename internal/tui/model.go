package tui

import (
	"context"
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

	"github.com/spiffcs/hoard/internal/scan"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/ui"
)

type state int

const (
	stateName state = iota
	// stateCameraBusy is any wait on the camera subsystem — listing devices or
	// opening the chosen one — so the name does not promise which.
	stateCameraBusy
	stateCameraPick
	stateCapture   // camera window live, waiting for the user to frame and capture
	stateCapturing // shutter pressed, waiting on the OCR result
	stateLoading
	stateNamePick
	statePrintPick
	stateFinishPick
	stateDestPick
	stateQty
	stateConfirm
	// stateQueueReview lists the cards that scanned but didn't auto-commit;
	// stateClosePrompt is the "you still have queued cards" gate on closing
	// the camera.
	stateQueueReview
	stateClosePrompt
	// stateLeaveConfirm gates esc out of the session: users read a bare esc
	// as "did that even save?", so leaving states what is and isn't kept
	// and asks for a confirming y. ctrl+d is the clean finish.
	stateLeaveConfirm
	// stateAbandonConfirm gates esc out of the close-time review walk. Esc
	// there used to drop every remaining card on the spot: the walk is where
	// a session's unsaved scans live, and nothing else in the app destroys
	// that much on a single keystroke without asking (discarding from the
	// close prompt is its own deliberate `d`).
	stateAbandonConfirm
	// statePalette is the capture step's command line (:), for the scanner
	// knobs that take a value — currently the sound tiers' dollar lines.
	statePalette
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

// destItem is one destination in the picker. A deck row says where in the
// deck the card lands, since "add to a deck" could plausibly mean any board.
type destItem struct{ d Destination }

func (d destItem) Title() string { return d.d.Name }
func (d destItem) Description() string {
	if d.d.Kind == "deck" {
		return "deck · adds to the mainboard"
	}
	return d.d.Kind
}
func (d destItem) FilterValue() string { return d.d.Name + " " + d.d.Kind }

// --- model ---

type model struct {
	ctx      context.Context
	searcher Searcher
	adder    Adder
	scanner  Scanner

	// theme is the shared ui palette — no styles are defined in this package.
	theme ui.Theme

	state state

	// done marks the cascade finished. INVARIANT: every tea.Quit this model
	// returns must set done first — Child.Update swallows the quit and an
	// embedding parent reads Done() instead, so a quit without done would
	// tear down the parent program. Standalone (tui.Run) the flag is inert.
	done bool

	nameInput textinput.Model
	qtyInput  textinput.Model
	// The capture command line (:): its input and the last parse error.
	paletteInput textinput.Model
	paletteErr   string
	list         list.Model
	spinner      spinner.Model

	width, height int

	// cascade selections
	prints []scryfall.Card
	chosen *scryfall.Card
	finish string

	// destinations the caller offered, and the current pick. dest doubles as
	// the session memory: it survives resetForNext, so the picker opens on
	// wherever the last card went and a bulk add answers with one enter.
	dests []Destination
	dest  Destination

	qtyErr string

	// scan feedback: the canonical name a camera scan resolved to (and the raw
	// OCR text it came from), shown as a fixed header for the rest of the
	// cascade so the user can see the capture was read correctly.
	scanned    string
	scannedOCR string
	// collector info read off the card, used to rank and pre-select the printing.
	scannedSet    string
	scannedNumber string
	// scannedPromoted marks that the background resolve already moved a
	// printing to the front on evidence rankByScan cannot see — the copyright
	// year, for cards printed before collector numbers existed. Without it the
	// winner sits silently at index 0 with nothing saying why.
	scannedPromoted bool

	// The hands-free scan flow (see autoscan.go). Every capture resolves in
	// the background under a generation number — bumping resolveGen lands any
	// in-flight straggler dead. Confident cards commit themselves; the rest
	// accumulate in review, walked mid-session (tab) or at camera close.
	review        []queueItem
	nextResolveID int
	resolveGen    int
	resolving     int // in-flight background resolutions
	// current is the review item whose cascade is running; walking means the
	// close-time sequential walk rather than a tab visit. walkDone counts
	// the cards the walk has finished — the walk is the one phase with a
	// true fraction to show, since the camera is closed and the queue has
	// stopped growing (late in-flight resolutions can still nudge it).
	current  *queueItem
	walking  bool
	walkDone int
	// recent auto-commits, for the duplicate window; now is injectable so the
	// window is testable.
	recent []recentCommit
	now    func() time.Time
	// tally is the capture view's live receipt of auto-commits; summary is the
	// whole session's, returned to the caller when the program exits.
	tally   []string
	summary Summary
	// autoState mirrors the helper's trigger phase ("armed", "held", …) for
	// the capture view; empty when the helper has no auto capture.
	autoState string
	// The rearm nudge (see autoscan.go): autoCapable marks a helper that
	// understands it, nudgeGen voids stale timers, nudgeSentAt stamps the
	// last sent nudge (a time window, not a consumed flag — a real scan can
	// race the nudge onto the wire, and the flag was observed being spent on
	// the racing scan while the true echo slipped through), lastScanNudged
	// tags scans inside that window, and nudgeDrops counts echoes.
	autoCapable bool
	// framingCapable marks a helper that can toggle the camera's auto-framing
	// (Center Stage, the "framing" feature); framingOn mirrors the helper's
	// current state, which every session starts with off — the helper forces
	// that, because the subject-tracking crop frames cards too close.
	framingCapable bool
	framingOn      bool
	// torchCapable marks a helper whose camera has a torch (the "torch"
	// feature); torchOn mirrors the light's state, session-scoped like the
	// torch itself.
	torchCapable bool
	torchOn      bool
	// effectsCapable marks a helper that can open the system Video Effects
	// panel (the "effects" feature) — Studio Light and friends.
	effectsCapable bool
	// hudCapable marks a helper whose camera window renders price results
	// (the "hud" feature); hudWin/hudJackpot are the celebration-tier
	// thresholds, read from the environment once at construction.
	hudCapable     bool
	hudWin         float64
	hudJackpot     float64
	nudgeGen       int
	nudgeSentAt    time.Time
	lastScanNudged bool
	nudgeDrops     int
	// recentNames is the recently-processed set (see autoscan.go): every
	// resolved card lands here, and every re-sighting refreshes it, so a
	// card parked in frame keeps being recognized however long it lingers.
	recentNames []recentName
	// captureSeq numbers captures, stamped onto each card's resolution so a
	// same-frame duplicate can be told from a lingering cross-frame one.
	captureSeq int
	// The session destination is asked once, when the camera opens;
	// destForSession marks the picker as that ask rather than the per-card one.
	destPicked     bool
	destForSession bool

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
	// embedded marks a cascade running as a child inside the browser, where
	// esc at the name prompt returns there rather than ending a program —
	// the help wording is the only behavioral difference.
	embedded bool
	// leaveFrom is where esc opened the leave gate, so a declined quit
	// returns exactly there — the name prompt or the live camera.
	leaveFrom state
	// addedValue is the running market value of those adds (qty-weighted,
	// unpriced printings contribute nothing) — the money beside the count.
	addedValue float64
	err        error // fatal program error (rare)
}

func newModel(ctx context.Context, s Searcher, add Adder, sc Scanner, initialName string, dests []Destination) model {
	ni := textinput.New()
	ni.Placeholder = "Card name, e.g. Ulamog, the Infinite Gyre"
	ni.Focus()
	ni.CharLimit = 200
	ni.Width = 50

	qi := textinput.New()
	qi.Placeholder = "1"
	qi.CharLimit = 6
	qi.Width = 10

	pi := textinput.New()
	pi.Placeholder = "win 5"
	pi.CharLimit = 40
	pi.Width = 30

	// One theme for the model, its inputs and its list delegate.
	th := ui.DefaultTheme()
	for _, in := range []*textinput.Model{&ni, &qi, &pi} {
		in.PromptStyle = th.Prompt
		in.Cursor.Style = th.Accent
	}

	sp := spinner.New()
	sp.Spinner = spinner.Dot

	l := list.New(nil, cascadeDelegate{theme: th}, 0, 0)
	l.SetShowStatusBar(false)
	l.SetShowTitle(false)
	// The list widget's own quit keys (q, esc as quit) would end the whole
	// program from inside a picker — with a batch queued, that silently drops
	// cards. esc keeps its cancel meaning via pickerKey; ctrl+c still quits.
	l.DisableQuitKeybindings()

	m := model{
		ctx:          ctx,
		searcher:     s,
		adder:        add,
		scanner:      sc,
		theme:        th,
		dests:        dests,
		nameInput:    ni,
		qtyInput:     qi,
		paletteInput: pi,
		spinner:      sp,
		list:         l,
		hudWin:       envFloat("HOARD_SCAN_WIN", defaultWinThreshold),
		hudJackpot:   envFloat("HOARD_SCAN_JACKPOT", defaultJackpotThreshold),
		width:        80,
		height:       22,
		now:          time.Now,
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
//
// With several destinations, the first scan of the session asks where cards
// land — once. Every auto-commit and every queued card defaults there; the
// per-card cascade can still override.
func (m *model) beginScan(force bool) tea.Cmd {
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
	if m.session != nil && !force {
		m.status = ""
		m.state = stateCapture
		return nil
	}
	if m.cameraChosen && !force {
		m.status = ""
		m.state = stateCameraBusy
		return tea.Batch(m.spinner.Tick, m.openSessionCmd())
	}
	m.status = ""
	m.state = stateCameraBusy
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

// --- update ---

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.list.SetSize(msg.Width, m.listHeight())
		// The name input tracks the window so its placeholder shrinks rather
		// than spilling; the "> " prompt and cursor account for the margin.
		m.nameInput.Width = max(min(50, msg.Width-4), 10)
		return m, nil

	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			// Close the camera here rather than leaning on Run's post-exit
			// safety net: embedded there is no Run, and the helper window
			// must not outlive the cascade.
			m.done = true
			m.closeSession()
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

	case resolveDoneMsg:
		return m.onResolveDone(msg)

	case nudgeMsg:
		return m.onNudge(msg)

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
	// ctrl+s skips the review item whose cascade is running, wherever it is:
	// one unwanted card should cost a keystroke, not the session. Gated on the
	// list's filter, which must keep every printable.
	if msg.Type == tea.KeyCtrlS && m.reviewing() && !m.list.SettingFilter() {
		m.recordSkip(*m.current)
		m.status, m.statusErr = "skipped", false
		return m.afterCard()
	}
	// ctrl+d finishes the add session cleanly from anywhere: everything
	// confirmed so far is already saved, and the receipt says so. Pending
	// review work blocks it — those cards deserve the queue's own exits
	// rather than silent loss.
	if msg.Type == tea.KeyCtrlD {
		if m.reviewing() || len(m.review) > 0 || m.resolving > 0 {
			m.status, m.statusErr = "cards are still queued for review · finish or ctrl+s them first", true
			return m, nil
		}
		m.done = true
		m.closeSession()
		return m, tea.Quit
	}
	switch m.state {
	case stateName:
		switch msg.Type {
		case tea.KeyEsc:
			// Reviewing a card that never resolved a name puts the cascade on
			// this prompt; esc there abandons the item, not the program.
			if m.reviewing() {
				return m.cancelToName()
			}
			// Leaving is gated: the prompt states what is saved and what
			// leaving drops, then a single y confirms — the same shape as
			// the browser's quit confirm.
			m.leaveFrom = stateName
			m.state = stateLeaveConfirm
			return m, nil
		case tea.KeyCtrlO, tea.KeyCtrlR:
			// Ctrl-R re-opens the camera picker even when this session already
			// chose one.
			if m.scanner == nil {
				return m.failToName("card scanning isn't available in this build")
			}
			return m, m.beginScan(msg.Type == tea.KeyCtrlR)
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
		if next, cmd, ok := m.pickerKey(msg, func(it list.Item) (tea.Model, tea.Cmd, bool) {
			ci, ok := it.(cameraItem)
			if !ok {
				return nil, nil, false
			}
			m.cameraID = ci.dev.ID
			m.cameraName = ci.dev.Name
			m.cameraChosen = true
			m.state = stateCameraBusy
			return m, tea.Batch(m.spinner.Tick, m.openSessionCmd()), true
		}); ok {
			return next, cmd
		}
	case stateCapture:
		switch msg.Type {
		case tea.KeySpace:
			// Space is the shutter, and only space: an undocumented enter
			// alias here would train a habit that misfires on every other
			// step, where enter means confirm.
			return m.requestCapture()
		case tea.KeyTab:
			if len(m.review) > 0 {
				return m.showReviewList()
			}
			return m, nil
		case tea.KeyLeft:
			return m.rotatePreview(true)
		case tea.KeyRight:
			return m.rotatePreview(false)
		case tea.KeyRunes:
			switch {
			case strings.EqualFold(string(msg.Runes), "z"):
				return m.toggleFraming()
			case strings.EqualFold(string(msg.Runes), "t"):
				return m.toggleTorch()
			case strings.EqualFold(string(msg.Runes), "v"):
				return m.openVideoEffects()
			case string(msg.Runes) == ":":
				return m.openPalette()
			case strings.EqualFold(string(msg.Runes), "c"):
				// Closing the camera with unprocessed cards — queued or still
				// resolving — deserves a decision, not a silent drop. The
				// session stays open until it's made, so c-again costs nothing.
				if len(m.review) > 0 || m.resolving > 0 {
					m.state = stateClosePrompt
					return m, nil
				}
				m.closeSession()
				return m.cancelToName()
			}
		case tea.KeyCtrlR:
			// Switch phones without leaving the camera step.
			m.closeSession()
			cmd := m.beginScan(true)
			return m, cmd
		case tea.KeyEsc:
			// Esc keeps its session-wide meaning — the gated quit — rather
			// than doubling as the close key (that's c). The gate itself
			// warns when unprocessed cards would be dropped; the camera
			// stays live under it either way.
			m.leaveFrom = stateCapture
			m.state = stateLeaveConfirm
			return m, nil
		}
	case stateCapturing:
		// The shutter is already pressed; esc abandons the whole camera rather
		// than leaving the user staring at a spinner.
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
			// Back to the camera; the queue keeps whatever it holds.
			m.state = stateCapture
			return m, nil
		case tea.KeyEnter:
			ri, ok := m.list.SelectedItem().(reviewItem)
			if !ok {
				return m, nil
			}
			m.takeFromReview(ri.it.id)
			return m.startReview(ri.it)
		case tea.KeyCtrlS:
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
			// Walk what's queued through the cascade, camera closed.
			m.closeSession()
			m.walking = true
			m.walkDone = 0
			if len(m.review) == 0 {
				// Everything still resolving; late arrivals join the walk as
				// they land (onResolveDone sees walking with no cascade).
				m.state = stateLoading
				return m, m.spinner.Tick
			}
			next := m.review[0]
			m.review = m.review[1:]
			return m.startReview(next)
		case msg.String() == "d":
			// Discard: kill in-flight resolves and drop the queue.
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
	case stateAbandonConfirm:
		if msg.Type == tea.KeyRunes && strings.EqualFold(string(msg.Runes), "y") {
			return m.abandonReviewWalk()
		}
		// Anything else resumes the walk on the card still in hand.
		return m.startReview(*m.current)
	case stateLeaveConfirm:
		if msg.Type == tea.KeyRunes && strings.EqualFold(string(msg.Runes), "y") {
			m.done = true
			m.closeSession()
			return m, tea.Quit
		}
		// Anything else stays: the safe reading of a stray keystroke on a
		// leave gate is "don't" — back to wherever esc was pressed.
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
			m.finish = string(fi)
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
			// The session-start ask: remember the answer for every auto-commit
			// and continue into the camera rather than a per-card cascade.
			if m.destForSession {
				m.destForSession = false
				m.destPicked = true
				cmd := m.beginScan(false)
				return m, cmd, true
			}
			next, cmd := m.toQty()
			return next, cmd, true
		}); ok {
			return next, cmd
		}
	case stateQty:
		if msg.Type == tea.KeyEsc {
			return m.cancelToName()
		}
		if msg.Type == tea.KeyEnter {
			return m.submitQty()
		}
	case statePalette:
		if msg.Type == tea.KeyEsc {
			m.state = stateCapture
			return m, nil
		}
		if msg.Type == tea.KeyEnter {
			return m.runPaletteCommand()
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
	case statePalette:
		m.paletteInput, cmd = m.paletteInput.Update(msg)
	case stateNamePick, statePrintPick, stateFinishPick, stateDestPick, stateCameraPick, stateQueueReview:
		m.list, cmd = m.list.Update(msg)
	case stateLoading, stateCapturing, stateCameraBusy:
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
	// rankByScan only knows collector numbers. A card from before they were
	// printed has none, so it returns "no match" even when the background
	// resolve had already promoted a printing on the copyright year — leaving
	// the row it chose unmarked and the reason invisible.
	if !matched && m.scannedPromoted {
		matched = true
	}
	m.prints = cards

	showPicker(&m, "Select a printing", cards, statePrintPick, func(i int, c scryfall.Card) list.Item {
		return printItem{card: c, scanned: matched && i == 0}
	})
	if m.scannedNumber != "" && !matched {
		// Either the digits were misread or the name match is wrong. Saying so
		// beats silently showing an unranked list as though nothing was scanned.
		m.status = fmt.Sprintf("card #%s isn't among these printings · pick manually", m.scannedNumber)
		m.statusErr = true
	}
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
		showPicker(&m, "Select a card", msg.names, stateNamePick, func(_ int, n string) list.Item {
			return nameItem(n)
		})
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
		return m.failToName("no iPhone found: scanning uses Continuity Camera; connect an iPhone and try again")
	case 1:
		m.cameraID = msg.devices[0].ID
		m.cameraName = msg.devices[0].Name
		m.cameraChosen = true
		return m, tea.Batch(m.spinner.Tick, m.openSessionCmd())
	default:
		showPicker(&m, "Scan with which iPhone?", msg.devices, stateCameraPick, func(_ int, d scan.Device) list.Item {
			return cameraItem{dev: d}
		})
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

// toggleFraming flips the camera's auto-framing (Center Stage) from the
// terminal — the one framing adjustment macOS offers. Every session starts
// with it off (the helper forces that), so the first press turns the
// subject-tracking crop on, for the setups it happens to suit. The state
// change lands as an EventFraming, which is what updates framingOn.
func (m model) toggleFraming() (tea.Model, tea.Cmd) {
	if m.session == nil {
		return m, nil
	}
	if !m.framingCapable {
		m.status, m.statusErr = "auto-framing isn't adjustable on this camera", true
		return m, nil
	}
	if err := m.session.AutoFraming(!m.framingOn); err != nil {
		m.closeSession()
		return m.failToName(err.Error())
	}
	return m, nil
}

// openVideoEffects pops the system Video Effects panel from the terminal —
// Studio Light is the one software lighting macOS offers (the phone's torch
// isn't bridged over Continuity Camera), and it lives there alongside the
// system's Center Stage and Desk View toggles.
func (m model) openVideoEffects() (tea.Model, tea.Cmd) {
	if m.session == nil {
		return m, nil
	}
	if !m.effectsCapable {
		m.status, m.statusErr = "this helper can't open the video effects panel", true
		return m, nil
	}
	if err := m.session.VideoEffects(); err != nil {
		m.closeSession()
		return m.failToName(err.Error())
	}
	m.status, m.statusErr = "video effects panel opened · Studio Light lives there", false
	return m, nil
}

// openPalette opens the capture step's command line — the add view's palette
// for the scanner knobs that take a value. Capture-only, because everywhere
// else the keyboard already belongs to a text input or a list.
func (m model) openPalette() (tea.Model, tea.Cmd) {
	m.paletteInput.SetValue("")
	m.paletteErr = ""
	m.paletteInput.Focus()
	m.state = statePalette
	return m, textinput.Blink
}

// runPaletteCommand parses and applies one command line. The commands move
// the sound tiers' dollar lines for this session; the HOARD_SCAN_WIN /
// HOARD_SCAN_JACKPOT environment variables stay the persistent knobs, so a
// bad live tweak never outlives the run. A parse problem stays on the line
// with the error shown; an empty line just closes it.
func (m model) runPaletteCommand() (tea.Model, tea.Cmd) {
	fields := strings.Fields(strings.ToLower(m.paletteInput.Value()))
	if len(fields) == 0 {
		m.state = stateCapture
		return m, nil
	}
	if len(fields) != 2 {
		m.paletteErr = "commands take one dollar amount, like: win 5"
		return m, nil
	}
	amount, err := strconv.ParseFloat(strings.TrimPrefix(fields[1], "$"), 64)
	if err != nil || amount <= 0 {
		m.paletteErr = fmt.Sprintf("%q isn't a dollar amount", fields[1])
		return m, nil
	}
	switch fields[0] {
	case "win":
		if amount >= m.hudJackpot {
			m.paletteErr = fmt.Sprintf("the win line must sit below the jackpot line ($%.2f)", m.hudJackpot)
			return m, nil
		}
		m.hudWin = amount
	case "jackpot":
		if amount <= m.hudWin {
			m.paletteErr = fmt.Sprintf("the jackpot line must sit above the win line ($%.2f)", m.hudWin)
			return m, nil
		}
		m.hudJackpot = amount
	default:
		m.paletteErr = fmt.Sprintf("unknown command %q · win <dollars> or jackpot <dollars>", fields[0])
		return m, nil
	}
	m.status, m.statusErr = fmt.Sprintf(
		"sound tiers this session · win at $%.2f · jackpot at $%.2f", m.hudWin, m.hudJackpot), false
	m.state = stateCapture
	return m, nil
}

// toggleTorch flips the phone's flashlight from the terminal, for the desk
// whose room light isn't enough for a clean read. The state that actually
// took lands as an EventTorch, which is what updates torchOn — a thermally
// refused torch never flips the mirror.
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
		if len(m.review) > 0 || m.resolving > 0 {
			// The window died with cards still queued — same decision as esc.
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
			m.status = "nothing readable in that frame · reframe and capture again"
			m.statusErr = true
			if m.state == stateCapturing {
				m.state = stateCapture
			}
			return m, again
		}
		// Every card resolves in the background — the camera stays interactive
		// the whole time. Confident cards commit themselves as their lookups
		// land; the rest queue. Each card's collector info travels with its own
		// lines — pooling them per frame is how one card's name once got
		// another card's printing.
		m.lastScanNudged = !m.nudgeSentAt.IsZero() &&
			m.now().Sub(m.nudgeSentAt) < nudgeEchoWindow
		m.nudgeGen++ // a real capture voids any armed quiet-period timer
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
		// The window is still up, so stay on the capture step and let them retry.
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
		// A helper that can fire itself is asked to: hands-free is the point.
		// Feature-gated, so an old helper is never sent a command it would
		// answer with an error event.
		if slices.Contains(msg.ev.Features, "auto") {
			_ = m.session.Auto(true)
			m.autoState = "armed"
			m.autoCapable = true
		}
		m.hudCapable = slices.Contains(msg.ev.Features, "hud")
		// The helper forces auto-framing off at startup, and the torch
		// always starts dark, so both mirrors start false regardless of
		// the last session.
		m.framingCapable = slices.Contains(msg.ev.Features, "framing")
		m.framingOn = false
		m.torchCapable = slices.Contains(msg.ev.Features, "torch")
		m.torchOn = false
		m.effectsCapable = slices.Contains(msg.ev.Features, "effects")
		return m, again

	case scan.EventAuto:
		m.autoState = msg.ev.State
		return m, again

	case scan.EventFraming:
		m.framingOn = msg.ev.State == "auto"
		if m.framingOn {
			m.status, m.statusErr = "auto-framing on · the camera crops to what it tracks", false
		} else {
			m.status, m.statusErr = "auto-framing off · full frame", false
		}
		return m, again

	case scan.EventTorch:
		m.torchOn = msg.ev.State == "on"
		if m.torchOn {
			m.status, m.statusErr = "torch on · watch for glare on foils", false
		} else {
			m.status, m.statusErr = "torch off", false
		}
		return m, again
	}
	// EventRotation and anything unrecognized: nothing to do but keep listening.
	return m, again
}

// note writes one line to the session telemetry log. Best effort: no session,
// no log, no matter.
func (m model) note(format string, args ...any) {
	if m.session != nil {
		m.session.Note(fmt.Sprintf(format, args...))
	}
}

// onResolveDone lands one background resolution: a confident card writes
// itself and shows up on the tally; anything else joins the review queue. The
// UI state is deliberately untouched — captures keep flowing while this fires.
func (m model) onResolveDone(msg resolveDoneMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.resolveGen {
		return m, nil // a discarded session's straggler
	}
	if m.resolving > 0 {
		m.resolving--
	}
	it := msg.item
	now := m.now()

	// The Go half of the per-card latency line, next to the helper's timing
	// lines in the telemetry log, plus the evidence verdict is about to weigh.
	// Without the evidence a log can say what the helper saw but never why Go
	// decided what it did, and re-deriving it offline means refetching every
	// printing.
	// On a miss the canonical is empty, which used to make the expensive
	// lookups the least attributable lines in the log — the text that cost the
	// round trip was exactly what went unrecorded.
	resolved := it.canonical
	if resolved == "" {
		resolved = "miss:" + it.ocrLine
	}
	m.note("resolve %q line=%d name=%dms prints=%dms rank=%s match=%s set=%s num=%s%s prints=%d%s%s",
		resolved, it.lineIdx, msg.nameDur.Milliseconds(), msg.printsDur.Milliseconds(),
		it.rank, matchDesc(it.match), orDash(it.raw.SetCode), orDash(it.raw.CollectorNumber),
		numberSourceSuffix(it.raw.NumberSource), len(it.prints), borderSuffix(it),
		siblingSuffix(it))

	// A nudge-fired re-read of any recently processed card is the trigger
	// seeing what we already know — swallow it, and stop nudging: one echo is
	// confirmation enough that the card is parked, and further nudges would
	// photograph the same card forever (observed live at a slow start).
	// Nudging resumes with the next real scan. The check is against the whole
	// recent set, not a single last name: a multi-card recheck echoes every
	// card of the previous capture, and remembering only the last one let the
	// rest dup-queue (observed live: a re-shot pair queued both).
	// …unless this look brought finish evidence the commit never had. The
	// echo is normally the same card telling us nothing new, but a marker
	// read on the second look and not the first is strictly better than the
	// nonfoil default already written, and swallowing it loses the only
	// chance to notice (observed live: a foil Inspired Fire recorded nonfoil,
	// its foil marker legible on the very next capture).
	if was, ok := finishConflict(m.recent, it, now); ok {
		card := it.prints[0]
		res := Result{Card: card, Finish: it.finishHint, Qty: 1,
			ContainerID: m.dest.ID, ReplacesFinish: was}
		if err := m.adder(res); err != nil {
			// The correction failed, so the guessed row stands and the user
			// has to know: this is the one place a silent failure would leave
			// a wrong price in the collection.
			m.note("outcome %q correction failed: %v", it.canonical, err)
			m.reviewFlash()
			it.note = fmt.Sprintf("reads %s but was added as %s, and the correction failed: %v",
				it.finishHint, was, err)
			m.review = append(m.review, it)
			nudge := m.scheduleNudge()
			next, cmd := m.reviewChanged()
			return next, tea.Batch(cmd, nudge)
		}
		m.note("outcome %q corrected: %s → %s", card.Name, was, it.finishHint)
		m.recent = correctRecentFinish(m.recent, card.ID, it.finishHint)
		m.recentNames = recordName(m.recentNames, it.canonical, now)
		// The count is unchanged — no new card arrived — but the value is not:
		// the two finishes price differently, which is the whole point.
		m.addedValue += priceValue(card, it.finishHint) - priceValue(card, was)
		line := fmt.Sprintf("%s (%s/%s) %s → %s", card.Name,
			strings.ToUpper(card.Set), card.CollectorNumber, was, it.finishHint)
		m.tally = append(m.tally, line)
		m.summary.add("auto", line)
		m.status = fmt.Sprintf("%s is %s — corrected", card.Name, it.finishHint)
		m.statusErr = false
		return m, m.scheduleNudge()
	}
	if it.fromNudge && it.canonical != "" && seenRecently(m.recentNames, it.canonical, now) {
		m.note("outcome %q dropped: nudge echo of a card already handled", it.canonical)
		m.nudgeDrops++
		m.recentNames = recordName(m.recentNames, it.canonical, now)
		m.status = fmt.Sprintf("still seeing %s, waiting for the next card", it.canonical)
		m.statusErr = false
		return m, nil
	}

	// A multi-card capture's unresolvable entries are phantoms — ability names,
	// brand lines, stray prose that read title-like — and they die here with a
	// note rather than queueing, exactly as the batch flow always skipped them.
	// A single-card capture keeps queueing: the only card of a shot must never
	// vanish silently.
	// A nudge-fired capture is a second look at a scene already handled, so an
	// entry there that resolves to nothing is noise rather than a card about to
	// be lost — the same reasoning as the multi-card case, and together they
	// account for most of what a session leaves in review.
	//
	// Never on an entry still holding a usable collector block, though. That
	// block is evidence a real card was in frame, resolveByBlock can name it
	// outright, and killing it would delete the only trace (observed live:
	// Quicksilver, Brash Blur arrived with a clean MSH/412 and a title read as
	// rules text). A scene-fired solo capture still queues: the one card of a
	// shot the user just placed must never vanish silently.
	if it.canonical == "" && it.errText == "" &&
		(it.siblings > 1 || it.fromNudge) && !hasCollectorBlock(it.raw) {
		why := fmt.Sprintf("phantom in a %d-card capture", it.siblings)
		if it.siblings <= 1 {
			why = "phantom in a nudge re-look"
		}
		m.note("outcome %q killed: %s", it.ocrLine, why)
		m.status = fmt.Sprintf("ignored %q: not a card", it.ocrLine)
		m.statusErr = false
		return m, m.scheduleNudge()
	}

	auto, finish, note := verdict(it)
	if auto {
		if seq, dup := dupCapture(m.recent, it.prints[0].ID, finish, now); dup {
			switch {
			case seq == it.captureSeq:
				// Two copies in one frame — a fanned playset. Queue for the
				// deliberate confirm; never drop.
				auto, it.dup = false, true
				note = "possible duplicate: same card twice in this capture"
			case it.siblings > 1 || it.fromNudge:
				// A lingering neighbour: the card added a moment ago is still
				// in frame beside the new one. An un-swapped pile is not a
				// playset signal — drop it silently (observed live: one card
				// queued five re-sightings of itself this way).
				m.note("outcome %q dropped: lingering neighbour of a just-added card", it.canonical)
				m.recentNames = recordName(m.recentNames, it.canonical, now)
				m.status = fmt.Sprintf("still seeing %s beside the new card", it.canonical)
				m.statusErr = false
				return m, m.scheduleNudge()
			default:
				// A deliberate solo re-scan: sequential playset scanning.
				auto, it.dup = false, true
				note = "possible duplicate: same card auto-added just now"
			}
		}
	}

	if auto {
		card := it.prints[0]
		res := Result{Card: card, Finish: finish, Qty: 1, ContainerID: m.dest.ID}
		if err := m.adder(res); err != nil {
			// The write failed, so the card is review-bound, not celebrated.
			m.note("outcome %q queued: add failed: %v", it.canonical, err)
			m.reviewFlash()
			nudge := m.scheduleNudge()
			it.note = "add failed: " + err.Error()
			m.review = append(m.review, it)
			next, cmd := m.reviewChanged()
			return next, tea.Batch(cmd, nudge)
		}
		m.note("outcome %q committed: %s/%s %s", card.Name,
			strings.ToUpper(card.Set), card.CollectorNumber, finish)
		_, evidenced := finishFromEvidence(card, it.finishHint)
		m.recent = recordCommit(m.recent, card.ID, finish, it.captureSeq, now, !evidenced)
		m.recentNames = recordName(m.recentNames, it.canonical, now)
		m.nudgeDrops = 0
		m.addedCount++
		m.addedValue += priceValue(card, finish)
		// After the increment, so the HUD total is the post-commit number.
		m.celebrate(priceValuePtr(card, finish))
		line := fmt.Sprintf("%s (%s/%s) %s · %s", card.Name,
			strings.ToUpper(card.Set), card.CollectorNumber, finish, priceForFinish(card, finish))
		m.tally = append(m.tally, line)
		m.summary.add("auto", line)
		return m, m.scheduleNudge()
	}

	// Queue-bound. An unresolved or shaky read that looks like a recently
	// processed name, seen in a nudge or multi-card context, is that card
	// still in frame wearing an OCR mangle — drop it rather than pile OCR
	// variants of an already-added card into review. A solo, non-nudge
	// capture still queues: a deliberately re-scanned worn card must not
	// vanish. A dup-flagged item skips the probe: it was recognized above as
	// a deliberate duplicate, and matching its own name here would swallow
	// the very queue entry the recognition chose to keep.
	if !it.dup && (it.fromNudge || it.siblings > 1) {
		probe := it.canonical
		if probe == "" {
			probe = it.ocrLine
		}
		if name, ok := similarRecent(m.recentNames, probe, now); ok {
			m.note("outcome %q dropped: OCR variant of %q, seen moments ago", probe, name)
			m.recentNames = recordName(m.recentNames, name, now)
			m.status = fmt.Sprintf("still seeing %s, waiting for the next card", name)
			m.statusErr = false
			return m, m.scheduleNudge()
		}
	}

	if it.canonical != "" {
		m.recentNames = recordName(m.recentNames, it.canonical, now)
	}
	m.note("outcome %q queued: %s", orDash(it.canonical), note)
	m.nudgeDrops = 0
	m.reviewFlash()
	it.note = note
	m.review = append(m.review, it)
	nudge := m.scheduleNudge()
	next, cmd := m.reviewChanged()
	return next, tea.Batch(cmd, nudge)
}

// chime plays the card-processed sound: the audible receipt that this card
// is handled — auto-added or queued — and the next one can be placed. Drops
// and swallows stay silent; they are the scanner recognizing a card it
// already handled, and a sound would ask the user to act on nothing.
func (m model) chime() {
	if m.session != nil {
		_ = m.session.Chime()
	}
}

// celebrate announces committed money on the camera HUD: the amount just
// added flashes with its tier's styling and sound, and the session total
// rides along. Fired at auto-commit and at review confirm (where it answers
// the queue-time question sound). Helpers without the hud feature get the
// plain chime instead.
func (m model) celebrate(price *float64) {
	if m.session == nil {
		return
	}
	if !m.hudCapable {
		m.chime()
		return
	}
	r := scan.HUDResult{Amount: price, Tier: tierFor(price, m.hudWin, m.hudJackpot)}
	// An unpriced commit moves nothing, so no total rides along — sending one
	// would only surface the counter to announce an unchanged number.
	if price != nil {
		t := m.addedValue
		r.Total = &t
	}
	_ = m.session.Result(r)
}

// reviewFlash marks a card that queued for review: a questioning two-note
// sound and a "Needs Review" flash instead of a price. Review means look at
// the terminal — a price celebration there would promise money the confirm
// may not deliver (the queued printing is unverified). The money lands via
// hudTotal when the review confirms it.
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

// hudTotal silently syncs the camera HUD's session counter after a commit
// that already celebrated (a queue confirm) or never will (a manual add
// while the camera is open). Review confirms routinely happen after the
// camera closed, hence the guards.
func (m model) hudTotal() {
	if m.session == nil || !m.hudCapable {
		return
	}
	t := m.addedValue
	_ = m.session.Result(scan.HUDResult{Total: &t})
}

// reviewChanged reacts to the queue growing: the close-time walk consumes the
// arrival immediately if it was waiting on one, and an open review list is
// rebuilt in place. Anywhere else, the counters in the chrome are enough.
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

// reviewing reports whether a review item's cascade is running.
func (m model) reviewing() bool { return m.current != nil }

// takeFromReview removes an item from the queue by id.
func (m *model) takeFromReview(id int) {
	for i, it := range m.review {
		if it.id == id {
			m.review = append(m.review[:i], m.review[i+1:]...)
			return
		}
	}
}

// recordSkip notes a dropped review item in the session summary.
func (m *model) recordSkip(it queueItem) {
	m.summary.add("skipped", reviewItem{it}.Title())
}

// showReviewList opens the queue as a picker.
func (m model) showReviewList() (tea.Model, tea.Cmd) {
	showPicker(&m, fmt.Sprintf("Review queue (%d)", len(m.review)), m.review, stateQueueReview,
		func(_ int, it queueItem) list.Item { return reviewItem{it} })
	return m, nil
}

// startReview re-enters the interactive cascade for one queued card, at
// whatever depth its background resolution reached: printings already fetched
// go straight to the picker (or auto-advance past it), a name alone refetches
// printings, and a card that never resolved lands on the prompt with its OCR
// text pre-filled. The item's note rides along as the banner, so the cascade
// opens by saying why the card queued.
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
		m.finish = ""
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
	// Printings were fetched and ranked in the background; onPrints re-ranks
	// idempotently and handles the single-printing auto-advance.
	return m.onPrints(printsMsg{name: it.canonical, cards: it.prints})
}

// afterCard is where a finished card — confirmed, skipped, or failed — hands
// control back: the close-time walk pulls the next queued card, a tab visit
// returns to the list, and the plain flow resets for the next capture or name.
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
			// The queue is dry but lookups are still in flight; hold on the
			// spinner and let reviewChanged pull the next arrival in.
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

// advanceAfterPrint moves past printing selection: it auto-skips the finish step
// when the printing has a single finish, otherwise shows the finish picker.
func (m model) advanceAfterPrint() (tea.Model, tea.Cmd) {
	finishes := finishOptions(*m.chosen)
	if len(finishes) <= 1 {
		if len(finishes) == 1 {
			m.finish = finishes[0]
		} else {
			m.finish = "nonfoil"
		}
		return m.toDest()
	}
	showPicker(&m, "Select a finish", finishes, stateFinishPick, func(_ int, f string) list.Item {
		return finishItem(f)
	})
	// A reviewed card's printed foil marker pre-selects its finish — the
	// cursor lands on what the border said, and enter accepts it.
	if m.reviewing() && m.current.finishHint != "" {
		for i, f := range finishes {
			if f == m.current.finishHint {
				m.list.Select(i)
				break
			}
		}
	}
	return m, nil
}

// toDest asks where the card goes — or doesn't: with zero or one destination
// the question has one answer, and the single-binder cascade stays exactly
// what it always was. The cursor opens on the last pick, so a bulk add into
// the same place is one enter per card.
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

// confirmAdd persists the pinpointed selection via the adder, records a banner,
// and loops back to the name prompt for the next card.
func (m model) confirmAdd() (tea.Model, tea.Cmd) {
	res := Result{Card: *m.chosen, Finish: m.finish, Qty: m.qtyValue(), ContainerID: m.dest.ID}
	if err := m.adder(res); err != nil {
		return m.failToName(err.Error())
	}
	m.addedCount++
	m.addedValue += float64(res.Qty) * priceValue(res.Card, res.Finish)
	if m.reviewing() {
		// A confirmed review answers the resolve-time question on the
		// camera window: the amount that just landed (qty-weighted), with
		// its tier's flash and sound — question at queue time, answer at
		// confirm time.
		var amt *float64
		if p := priceValuePtr(res.Card, res.Finish); p != nil {
			v := *p * float64(res.Qty)
			amt = &v
		}
		m.celebrate(amt)
	} else {
		// A manual add never asked a question; only the HUD's session
		// counter moves, silently.
		m.hudTotal()
	}
	m.status = fmt.Sprintf("✓ Added %d× %s (%s/%s) %s · %s",
		res.Qty, res.Card.Name, res.Card.Set, res.Card.CollectorNumber,
		res.Finish, priceForFinish(res.Card, res.Finish))
	// Naming the destination matters exactly when there was a choice.
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
	return m.afterCard()
}

// cancelToName abandons the in-progress add: esc means "get me out".
func (m model) cancelToName() (tea.Model, tea.Cmd) {
	if m.reviewing() {
		return m.cancelReview()
	}
	m.status = ""
	m.destForSession = false
	return m.resetForNext()
}

// cancelReview is esc during a review item's cascade. Mid-walk it means what
// esc always meant for a batch — get me out, dropping what's left; from a tab
// visit it just puts the card back and returns to the list.
func (m model) cancelReview() (tea.Model, tea.Cmd) {
	if m.walking {
		// Ask first. The card in hand stays in hand — nothing is dropped
		// until the gate is answered, so a stray esc costs a keystroke
		// rather than the rest of the queue.
		m.state = stateAbandonConfirm
		return m, nil
	}
	it := *m.current
	m.current = nil
	m.review = append([]queueItem{it}, m.review...)
	m.status, m.statusErr = "", false
	return m.showReviewList()
}

// abandonReviewWalk is the confirmed answer to the gate: drop what is left of
// the queue, exactly as esc used to do on its own.
func (m model) abandonReviewWalk() (tea.Model, tea.Cmd) {
	m.current = nil
	m.walking = false
	m.resolveGen++
	dropped := len(m.review) + m.resolving + 1
	m.resolving = 0
	m.review = nil
	m.summary.add("discarded", fmt.Sprintf("%d scanned cards discarded unprocessed", dropped))
	m.status = fmt.Sprintf("review abandoned · %d cards not added", dropped)
	m.statusErr = false
	return m.resetForNext()
}

// failToName shows an error banner and returns to the name prompt, keeping the
// session alive — or, mid-review, walks on: one card's failed write should
// cost that card, not the session.
func (m model) failToName(msg string) (tea.Model, tea.Cmd) {
	m.status = msg
	m.statusErr = true
	if m.reviewing() {
		m.summary.add("skipped", reviewItem{*m.current}.Title()+" · "+msg)
		return m.afterCard()
	}
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
	m.scannedPromoted = false
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

// pickerKey handles the keys every picker shares: esc cancels back to the name
// prompt and enter fires onEnter with the selection — unless the list's own
// filter is capturing input, which must keep every key. onEnter reports whether
// it consumed the item, so an unexpected type falls through untouched. One
// definition, because four hand-written copies of this contract had already
// been written and a fifth would forget the filter guard.
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

// showPicker fills the list with one item per element and moves to the picker
// state — the three steps every picker transition takes, kept together so a
// new picker cannot forget one.
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
	return max(m.height-chrome, 4)
}

// --- view ---

// escHelp names what esc does at the name prompt: standalone it ends the
// program, embedded it returns to the browser — the same key, two honest
// labels, worded like every other esc hint (the confirm gate speaks for
// itself when it appears).
func (m model) escHelp() string {
	return "ctrl+d done adding · " + m.escLabel()
}

// help renders a help line wrapped between its " · " entries, so a hint that
// would be cut mid-phrase by the view-wide word wrap moves whole to the next
// line instead — "esc close camera" never splits after "esc".
func (m model) help(s string) string {
	return m.theme.Help.Render(strings.Join(ui.WrapHelp(s, m.width), "\n"))
}

// escLabel is the esc hint alone, shared by every step where esc opens the
// leave gate.
func (m model) escLabel() string {
	if m.embedded {
		return "esc back"
	}
	return "esc quit"
}

// batchHelp appends the skip key to a cascade help line while a review item's
// cascade is running — the key only exists then, so advertising it elsewhere
// would lie.
func (m model) batchHelp(base string) string {
	if m.reviewing() {
		return base + " · ctrl+s skip card"
	}
	return base
}

// scanHeader renders the fixed banner shown for the rest of the cascade after a
// camera scan, so the user can confirm the OCR landed on the right card. When
// the raw OCR text differs from the canonical name, it's shown alongside so a
// wrong-but-plausible match is obvious. A review cascade prefixes how many
// cards are still waiting — the badge is what makes a long queue bearable.
func (m model) scanHeader() string {
	var badge string
	if m.reviewing() {
		switch {
		case m.walking:
			// The close-out walk is the one phase with a true fraction —
			// computed at render, since a late resolution can still land
			// and lengthen the walk.
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

// View word-wraps every state's content to the terminal width: bubbletea's
// renderer truncates overlong lines outright, so wrapping here — ANSI-aware,
// preserving styles across breaks — is what keeps help lines and status
// banners readable in a narrow window. Before the first WindowSizeMsg the
// width is unknown and content passes through untouched.
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
		scanHint := ""
		if m.scanner != nil {
			scanHint = "ctrl+o scan card · ctrl+r change camera · "
			if m.session != nil {
				scanHint = "ctrl+o back to camera · ctrl+r change camera · "
			}
		}
		help := scanHint + "enter search · " + m.escHelp() + " · ctrl+c force quit"
		if m.addedCount > 0 {
			help = m.sessionTally() + " · " + help
		}
		b.WriteString(m.help(help))
		return b.String()
	case stateCameraBusy:
		return fmt.Sprintf("%s looking for a connected iPhone…\n\n%s",
			m.spinner.View(), m.help("esc cancel · ctrl+c force quit"))
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
		// The live tally: the last few auto-commits, so an unattended write is
		// visible the moment it happens.
		const tallyShown = 4
		for _, line := range m.tally[max(0, len(m.tally)-tallyShown):] {
			b.WriteString(m.theme.OK.Render("✓ Auto-added: "+line) + "\n")
		}
		if len(m.tally) > 0 || len(m.review) > 0 || m.resolving > 0 {
			counter := fmt.Sprintf("%d auto-added · %d need review", len(m.tally), len(m.review))
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
			b.WriteString("Set a card down and the app will run auto capture. A manual trigger with spacebar also works.\n\n")
		case "held":
			b.WriteString("Captured. Swap in the next card.\n\n")
		default:
			b.WriteString("Frame the next card, then press space.\n\n")
		}
		keys := []string{": commands", "space capture", "←/→ rotate"}
		if m.framingCapable {
			keys = append(keys, "z framing")
		}
		if m.torchCapable {
			keys = append(keys, "t torch")
		}
		if m.effectsCapable {
			keys = append(keys, "v effects")
		}
		help := strings.Join(keys, " · ") + " · c close camera · " + m.escLabel() + " · ctrl+c force quit"
		if len(m.review) > 0 {
			help = fmt.Sprintf("tab review (%d) · %s", len(m.review), help)
		}
		if m.addedCount > 0 {
			help = m.sessionTally() + " · " + help
		}
		b.WriteString(m.help(help))
		return b.String()
	case stateQueueReview:
		return m.list.View() + "\n" +
			m.help("↑/↓ move · enter fix this card · ctrl+s drop it · tab/esc back to camera · ctrl+c force quit")
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
		// The browser's quit confirm, word for word in shape: red prompt,
		// dim y/n, one line. Same gate, same look — plus the cost, when
		// quitting would drop scanned-but-unsaved cards.
		prompt := "quit add session?"
		if n := len(m.review) + m.resolving; n > 0 {
			prompt = fmt.Sprintf("quit add session? %d unsaved scans will be dropped", n)
		}
		return m.theme.Err.Render(prompt) + m.theme.Help.Render("  y/n")
	case stateAbandonConfirm:
		n := len(m.review) + m.resolving + 1
		return m.theme.Err.Render(fmt.Sprintf(
			"abandon review? %d scanned cards will be dropped unsaved", n)) +
			m.theme.Help.Render("  y/n")
	case statePalette:
		var b strings.Builder
		b.WriteString(m.theme.Title.Render("Scanner commands") + "\n\n")
		fmt.Fprintf(&b, "Sound tiers · bulk under $%.2f · win at $%.2f · jackpot at $%.2f\n\n",
			m.hudWin, m.hudWin, m.hudJackpot)
		b.WriteString("  win <dollars>      the gold flash and bell start here\n")
		b.WriteString("  jackpot <dollars>  the coin shower starts here\n\n")
		b.WriteString(m.paletteInput.View())
		if m.paletteErr != "" {
			b.WriteString("\n" + m.theme.Err.Render(m.paletteErr))
		}
		b.WriteString("\n\n" + m.help(
			"enter run · esc back to camera · lasts this session (HOARD_SCAN_WIN/_JACKPOT persist)"))
		return b.String()
	case stateCapturing:
		return fmt.Sprintf("%s reading the card…\n\n%s",
			m.spinner.View(), m.help("esc close camera · ctrl+c force quit"))
	case stateLoading:
		return m.scanHeader() + fmt.Sprintf("%s searching Scryfall…\n\n%s",
			m.spinner.View(), m.help("ctrl+c to force quit"))
	case stateNamePick, statePrintPick, stateFinishPick, stateDestPick:
		return m.scanHeader() + m.list.View() + "\n" +
			m.help(m.batchHelp("↑/↓ move · / filter · enter select · esc cancel · ctrl+c force quit"))
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
	// The destination line appears exactly when there was a choice to recall —
	// a single-binder session's confirm screen is character-identical to what
	// it always showed.
	if len(m.dests) > 1 {
		out += "\nadd to: " + m.dest.Name
	}
	return out
}

// --- pure helpers (unit-tested) ---

// finishOptions maps a card's Scryfall finishes to the tool's finish names,
// in a stable normal→foil→etched order. The translation itself lives beside
// the Card type, shared, so this package holds no private copy of it.
func finishOptions(c scryfall.Card) []string {
	return scryfall.Finishes(c)
}

// printMarkers summarizes distinguishing traits of a printing for display.
func printMarkers(c scryfall.Card) string {
	var parts []string
	// Border colour used to show only when it was "borderless", which left the
	// pre-collector-number era unreadable: those cards carry no number to
	// verify, so the queue often offers a white-bordered and a black-bordered
	// printing of the same card, in the same year, by the same artist. The
	// border is the only thing telling them apart, and the rows rendered
	// identically. White is the one worth naming — black is the default across
	// most of Magic, and labelling every row "black" is noise.
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

// sessionTally is the "N added this session" help fragment, with the
// running value beside the count once anything priced has landed.
func (m model) sessionTally() string {
	t := fmt.Sprintf("%d added this session", m.addedCount)
	if m.addedValue > 0 {
		t += fmt.Sprintf(" ($%.2f)", m.addedValue)
	}
	return t
}

// priceValue is priceForFinish's numeric sibling: what one copy is worth,
// or 0 when the printing has no price for the finish.
func priceValue(c scryfall.Card, finish string) float64 {
	p := c.PriceUSD
	if scryfall.PricedAsFoil(finish) {
		p = c.PriceUSDFoil
	}
	if p == nil {
		return 0
	}
	return *p
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
