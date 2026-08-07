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
	// statePairIntro tells the user to open the app *before* searching for it.
	// Without this the first thing a new user meets is a failed search, and the
	// instruction that would have prevented it is in the error message.
	statePairIntro
	// statePairCode collects the six digits an iPhone-app source shows on
	// screen, once per phone.
	statePairCode
	// statePairBusy waits on the phone answering the pairing check. Its own
	// state because that check is a round trip over the network — blocking the
	// update loop for it froze the whole TUI with no spinner and no way to tell
	// a slow phone from a hung one.
	statePairBusy
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
	codeInput textinput.Model
	// The capture command line (:): its input and the last parse error.
	paletteInput textinput.Model
	paletteErr   string
	// addPalette is the name step's command drawer (see addpalette.go), open
	// when non-nil. Distinct from paletteInput above, which is the capture
	// step's older value-taking command line.
	addPalette *addPalette
	list       list.Model
	spinner    spinner.Model

	width, height int

	// cascade selections
	prints []scryfall.Card
	chosen *scryfall.Card
	finish string

	// printsAll is every printing the search returned, kept when `prints` has
	// been narrowed to those answering the scanned collector number. nil when
	// no narrowing happened, which is also how the toggle knows there is
	// nothing to toggle. The full list stays reachable because the narrowing is
	// the scanner's belief and not a fact: the digits can be misread, and a
	// picker that has hidden the right row with no way back is worse than one
	// showing nineteen.
	printsAll []scryfall.Card

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
	tally []string
	// How many rows the tally is scrolled back from its newest, so a long
	// session's history stays reachable without the receipt taking the screen.
	//
	// Zero means pinned to the newest, which is the resting state: a scanning
	// session is watched, not read, and the row that matters is the one that
	// just landed. Scrolled back, it *stays* back — see recordTally, which
	// grows the offset alongside the slice so an arriving card does not slide
	// the rows out from under someone reading them.
	tallyOffset int
	summary     Summary
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
	// torchCapable marks a source whose camera has a torch (the "torch"
	// feature); torchOn mirrors the light's state, session-scoped like the
	// torch itself.
	torchCapable bool
	torchOn      bool
	// hudCapable marks a helper whose camera window renders price results
	// (the "hud" feature); hudWin/hudJackpot are the celebration-tier
	// thresholds, read from the environment once at construction.
	hudCapable     bool
	hudWin         float64
	hudJackpot     float64
	nudgeGen       int
	nudgeSentAt    time.Time
	lastScanNudged bool
	// lastScanMoved tags a capture the source fired because a card *moved*
	// rather than because one was placed — it watched a box hold the spot while
	// still looking like the card it had already read. Weak evidence in the one
	// direction that matters: a repeat carrying this is the card already
	// counted, whatever the clock says.
	lastScanMoved bool
	// lastScanReplaced tags a capture the source fired because a box held the
	// watched spot wearing a face it did not recognize — its positive claim
	// that a *different* card is on the mat. The mirror of lastScanMoved, and
	// the only reason strong enough to outrank sameCardFloor.
	//
	// Deliberately not "removed or replaced". Removed means the captured card
	// left the watched rect, which is equally what a card picked up, looked
	// at, and set back down does — no claim about identity at all.
	lastScanReplaced bool
	// lastScanFaceDelta is how far the nearest card in frame sat from the one
	// already read, on the sample that fired. Nil when the source did not
	// measure it: too old to send it, or fired for a reason that never runs
	// the comparison. See scan.Event.FaceDelta.
	lastScanFaceDelta *float64
	// pending holds the last sighting the duplicate rules suppressed, so `+`
	// can overrule them. Nil when there is nothing to promote.
	pending    *pendingDup
	nudgeDrops int
	// secondLookFor is the canonical name of the card the last unverified-
	// printing queue asked another look at, and it is what bounds that retry to
	// one attempt: a card queueing under a name already sitting here has had its
	// second look and queues at the normal cadence. Cleared by any commit, so
	// the same card met again later in the pile gets a fresh chance.
	secondLookFor string
	// secondLookPending is a retry waiting for the phone to say it is listening.
	// Consumed by the next "armed" the trigger reports — see onSessionEvent.
	secondLookPending bool
	// deferredFlashFor is a card sitting in review whose *phone* signal has not
	// been sent, because a second look is still out on it. Held rather than
	// dropped: the flash still owes the operator an answer, it just owes it
	// after the retry rather than before. Resolved by flushDeferredFlash or
	// cleared by clearDeferredFlash — never left dangling, or a queued card
	// goes unannounced.
	deferredFlashFor string
	// ignored counts captures that identified nothing and were dropped rather
	// than queued. Reported once at the end of the session: dropping them is
	// right, but doing it invisibly would make a scanner that is quietly
	// throwing away real cards indistinguishable from one that is working.
	ignored int
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
	// justPairedID is the phone this run has just finished pairing with, and it
	// is consumed by the very next camera list.
	//
	// A one-shot rather than a preference. Having typed a six-digit code onto a
	// phone a second ago, being asked "scan with which phone?" and offered that
	// one beside another is asking a question whose answer was the last thing
	// the user did. But a *later* ctrl+o is a different situation — there the
	// picker is how someone switches phones at all — so this clears as soon as
	// it is used and the choice reverts to being offered.
	justPairedID string
	// pairing is set while the camera flow is being used to set a phone up
	// rather than to open a session. The discovery, the picker and the code
	// prompt are the same three steps either way; only the ending differs.
	pairing bool

	// the live camera session. It outlives individual cards: the window stays up
	// while the cascade runs, and the flow returns here after each add. gen tags
	// events so a closed session's stragglers are ignored.
	session    ScanSession
	sessionGen int

	// session state
	status    string // banner shown on the name prompt (last add / error / cancel)
	statusErr bool   // style the banner as an error
	// statusSeq is the captureSeq whose outcome the banner is describing, so a
	// resolve can tell "my own capture already wrote this" from "this is left
	// over from a capture ago". One capture can yield several cards and only
	// one of them may have something to say — a lingering neighbour reporting
	// "Still seeing Sol Ring" while its sibling commits silently — so the
	// clear is per capture, not per resolve.
	statusSeq  int
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

	ci := textinput.New()
	ci.Placeholder = "123456"
	ci.CharLimit = 7
	ci.Width = 12

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
		codeInput:    ci,
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
		// A live session: ctrl+o is the way back to it, not a way to reopen it.
		m.status = ""
		m.state = stateCapture
		return nil
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

	case pairedMsg:
		return m.onPaired(msg)
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
	// An error banner reports the last thing that went wrong, and the next
	// keystroke is proof it has been read. The worst offender is the helper's
	// "no iPhone running Hoardling was found on this network" guidance: three
	// lines of it, sticky across states, still sitting above the prompt long
	// after its reader gave up and went back to typing names.
	//
	// Cleared here, before dispatch, so it goes for every key in every state —
	// esc, a hotkey from the help line, ':' opening the drawer, a character in
	// the name field — while a handler below that sets its own banner still
	// wins, because it runs after this.
	//
	// Errors only. A success receipt ("Added Sol Ring ×1") is a record of what
	// happened rather than a complaint about it, and typing the next card's
	// name is no reason to take it away.
	if m.statusErr {
		m.status, m.statusErr = "", false
	}
	// ctrl+s skips the review item whose cascade is running, wherever it is:
	// one unwanted card should cost a keystroke, not the session. Gated on the
	// list's filter, which must keep every printable.
	if msg.Type == tea.KeyCtrlS && m.reviewing() && !m.list.SettingFilter() {
		m.recordSkip(*m.current)
		m.status, m.statusErr = "skipped", false
		return m.afterCard()
	}
	// ctrl+d finishes the add session from anywhere: everything confirmed so
	// far is already saved, and the receipt says so. Pending review work does
	// not block it — it asks, through the same gate esc uses, so leaving is a
	// decision rather than an errand. See finishAdding.
	//
	// Not while a confirm is already up: ctrl+d there would re-enter the gate
	// with leaveFrom pointing at the gate itself, and declining would loop.
	if msg.Type == tea.KeyCtrlD &&
		m.state != stateLeaveConfirm && m.state != stateAbandonConfirm &&
		m.state != stateClosePrompt {
		return m.finishAdding()
	}
	// The name step's command drawer owns the keyboard while it is open.
	if m.addPalette != nil {
		return m.handleAddPaletteKey(msg)
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
		case tea.KeyCtrlP:
			// Pairing a phone deliberately, rather than meeting the prompt on the
			// way into a scan: set one up before scanning, re-pair after a code is
			// rotated, or just see what is on the network.
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
			// Two steps, not `return m, m.beginScan()`: beginScan takes a
			// pointer receiver and sets the next state, and the order of a
			// value copy against a mutating call in one return statement is
			// not specified. The copy can be taken before the mutation, which
			// returns the *previous* state and looks like the key did nothing.
			cmd := m.beginScan()
			return m, cmd
		case tea.KeyRunes:
			// ':' opens the palette, but only on an empty field — the same
			// key the browser uses, without taking a character away from the
			// thing this view is mostly for. With a name part-typed it is
			// just a colon, which is what someone mid-word meant by it.
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
			// A phone this machine has never paired with needs its code
			// before a session can open. Once per phone, not once per run —
			// and always, when the user came here to pair on purpose.
			if m.pairing || ci.dev.NeedsPairing {
				m.codeInput.SetValue("")
				m.codeInput.Focus()
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
		case tea.KeyUp:
			// The receipt's history, scrolled with up/down because that is
			// what every other list in this program scrolls with.
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
				// "One more of that." The duplicate rules suppressed a
				// sighting and said so; this overrules them.
				//
				// `=` too, unshifted on the same physical key: the operator's
				// other hand is holding cards, and a shift-reach to correct
				// the scanner is exactly the kind of stop this flow exists to
				// avoid.
				return m.promotePending()
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
		case tea.KeyRunes:
			// d drops the card under the cursor — the browser's removal key,
			// on a list that reads like the browser's lists. No y/n gate
			// behind it: a queued scan is nothing that was ever saved, and the
			// summary still records the drop. Anything else falls through to
			// the list, which owns / for filtering.
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
		// The way back to the full list, when the scanned number narrowed it.
		// One-way on purpose: re-narrowing would need the user to remember what
		// was hidden, and the whole reason to reach for this key is that the
		// number is wrong.
		if msg.Type == tea.KeyCtrlA && m.printsAll != nil {
			all := m.printsAll
			m.printsAll, m.prints = nil, all
			// Still ranked, and the match still marked. Restoring the hidden
			// rows is not the same as forgetting what was read — the reason to
			// reach for this key is to look past the number, not to lose it.
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
	case statePairCode:
		m.codeInput, cmd = m.codeInput.Update(msg)
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
	// marks it. Heavily reprinted cards make this worth doing — Sol Ring has
	// well over a hundred printings.
	cards, matched := rankByScan(msg.cards, m.scannedSet, m.scannedNumber)
	// rankByScan only knows collector numbers. A card from before they were
	// printed has none, so it returns "no match" even when the background
	// resolve had already promoted a printing on the copyright year — leaving
	// the row it chose unmarked and the reason invisible.
	if !matched && m.scannedPromoted {
		matched = true
	}

	// A number that pinned some of these printings answers the question the
	// picker is asking, so the picker should stop asking it about the rest.
	// Live: Victimize read a clean 413 and offered nineteen rows to choose
	// between, of which one could be it. Promoting the right row to the top was
	// never the same as removing the eighteen that the card itself rules out.
	//
	// Fails open, twice over. An empty match set means the digits are a misread
	// or the name landed on the wrong card — that is the `Card #N isn't among
	// these` path below, and hiding everything there would leave a picker with
	// nothing in it. A full match set narrows nothing and is left alone.
	//
	// And when it narrows to exactly one, that is the answer and the picker is
	// skipped. This function used to hold the opposite rule — a number promotes
	// a printing and never picks it, so a misread digit stays visible — and the
	// rule is now scoped to where it earns its keep. Showing a one-row list is
	// not review: nobody reads a list of one, they press enter. What it costs is
	// a keystroke on every scanned card, which across a bulk session is the
	// difference the hands-free flow exists to make.
	//
	// The risk this accepts, stated plainly: a misread digit that happens to
	// name a *different real printing of the same card* now commits silently.
	// That is one row to correct, and it is the same trade the ambiguous-number
	// branch of `verdict` already makes for the same stated reason.
	m.printsAll = nil
	if idxs := numberMatches(cards, m.scannedSet, m.scannedNumber, ""); len(idxs) > 0 &&
		len(idxs) < len(cards) {
		kept := make([]scryfall.Card, 0, len(idxs))
		for _, i := range idxs {
			kept = append(kept, cards[i])
		}
		m.printsAll, cards, matched = cards, kept, true
		if len(cards) == 1 {
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
		// Either the digits were misread or the name match is wrong. Saying so
		// beats silently showing an unranked list as though nothing was scanned.
		m.status = fmt.Sprintf("Card #%s isn't among these printings · pick manually", m.scannedNumber)
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

// onCameras handles the camera list. A single camera is used without asking;
// several are offered as a picker; none is an error the user can act on.
func (m model) onCameras(msg camerasMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		// The helper's own "no iPhone found" guidance is the useful message here,
		// so pass it through unprefixed.
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
			// Back to the instruction, not out to the name prompt. "I hadn't
			// opened it yet" is the overwhelmingly likely reason, and the fix
			// is to open it and press enter — not to start the flow again.
			m.state = statePairIntro
			m.status = "No phone found. Is Hoardling open and on screen?"
			m.statusErr = true
			return m, nil
		}
		if len(phones) == 1 {
			// Don't ask someone to choose between one thing — the scan path
			// right below has always auto-selected a lone camera, and pairing
			// has no reason to be different.
			m.cameraID = phones[0].ID
			m.cameraName = phones[0].Name
			m.codeInput.SetValue("")
			m.codeInput.Focus()
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
		// A phone paired moments ago is not a question. Consumed either way:
		// if it is no longer in the list the picker is the right answer, and
		// the flag must not survive to surprise a later ctrl+o.
		if id := m.justPairedID; id != "" {
			m.justPairedID = ""
			for _, d := range msg.devices {
				if d.ID != id {
					continue
				}
				m.cameraID, m.cameraName = d.ID, d.Name
				m.cameraChosen = true
				return m, tea.Batch(m.spinner.Tick, m.openSessionCmd())
			}
		}
		showPicker(&m, "Scan with which phone?", msg.devices, stateCameraPick, func(_ int, d scan.Device) list.Item {
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
			// Silent when the trigger fired it, loud when a person did.
			//
			// "Reframe and capture again" is exactly right after someone
			// presses space and gets nothing: they asked, and this is the
			// answer. It is noise after a hands-free fire, because nobody
			// asked — the trigger goes off on a hand mid-swap and on bare desk,
			// and the ledger has always budgeted ~7% of captures reading
			// nothing. Telling the operator to reframe a capture they did not
			// request, in the error style, turns an expected event into an
			// alarm; one session produced seven of them, five while the card
			// they were about to scan was still in the air.
			if !msg.ev.Auto {
				m.status = "Nothing readable in that frame · reframe and capture again"
				m.statusErr = true
			}
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
		// Why this capture happened, from the source that watched it happen.
		//
		// This used to be inferred here from a clock — whether a nudge had been
		// sent within the last four seconds — and the comment on that window
		// conceded the flaw: a real scan can race the nudge onto the wire. The
		// phone takes three distinct code paths when it re-arms and now says
		// which one it took, so the guess is gone.
		//
		// The clock survives only for a helper too old to send the field. That
		// is a compatibility path, not the mechanism.
		// `moved` is the source saying it thinks this is the card it already
		// read — a box held the place and still looks like the capture. It is
		// a real capture, so it is not a nudge echo, but it is the opposite of
		// evidence that a card was placed and the duplicate rules treat it so.
		m.lastScanMoved = msg.ev.FireReason == scan.FireMoved
		// `replaced` is the same machinery answering the other way: the box on
		// the watched spot does *not* look like the capture. Kept with the
		// measurement behind it, because the claim alone is a boolean from a
		// threshold that was interpolated rather than measured, and the
		// duplicate rules need to know whether it cleared that threshold by a
		// hair or by a mile.
		m.lastScanReplaced = msg.ev.FireReason == scan.FireReplaced
		m.lastScanFaceDelta = msg.ev.FaceDelta
		switch msg.ev.FireReason {
		case scan.FireNudge:
			m.lastScanNudged = true
		case scan.FireRemoved, scan.FireReplaced, scan.FireMoved:
			m.lastScanNudged = false
		default:
			m.lastScanNudged = !m.nudgeSentAt.IsZero() &&
				m.now().Sub(m.nudgeSentAt) < nudgeEchoWindow
		}
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
		// Assigned unconditionally, not set-on-true. A ready event can arrive
		// more than once — a network source re-announces after a reconnect —
		// and a sticky flag would keep sending Rearm() to a source that no
		// longer advertises the feature, which answers with an error event per
		// nudge for the rest of the session.
		m.autoCapable = slices.Contains(msg.ev.Features, "auto")
		if m.autoCapable {
			_ = m.session.Auto(true)
			m.autoState = "armed"
		} else {
			m.autoState = ""
		}
		m.hudCapable = slices.Contains(msg.ev.Features, "hud")
		// The torch always starts dark, so the mirror starts false
		// regardless of what the last session left it at.
		m.torchCapable = slices.Contains(msg.ev.Features, "torch")
		m.torchOn = false
		return m, again

	case scan.EventAuto:
		m.autoState = msg.ev.State
		// A pending retry waits for this rather than for a timer. The trigger
		// will not re-fire on a card it has already read — it fires on
		// placement — so the retry is a Rearm, and a Rearm only lands once the
		// phone is listening for one.
		//
		// Timing it instead was the first attempt, and it cannot be tuned into
		// this. Measured over one session's 18 captures, the phone's own
		// held→armed gap is bimodal: four at ~130ms and fourteen at 760-855ms.
		// Any single constant is either late for the fast half or fired into
		// `held` for the slow half, and a Rearm sent into `held` is a retry that
		// silently never happens. The phone already says when it is ready, so
		// asking it is both quicker than the safe constant and safer than the
		// quick one.
		if m.secondLookPending && msg.ev.State == "armed" {
			m.secondLookPending = false
			if m.session != nil && m.autoCapable {
				_ = m.session.Rearm()
				m.nudgeSentAt = m.now()
			}
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
	// Anything unrecognized: nothing to do but keep listening. A source is free
	// to send events this build has never heard of.
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

	// The banner describes one capture's outcome, and this resolve is the
	// first word from a newer one — so whatever is up there is stale. Clearing
	// once here rather than at each exit is what keeps it from outliving the
	// event it described: every path below that still has something to say
	// sets its own text, and the paths that end in a commit or a queue now
	// leave the line empty instead of stranding an "Ignored "O L": not a card"
	// above a fresh "✓ Auto-added" row. The receipt already records the
	// commit, so the stale line was the only thing on screen still claiming to
	// be current.
	//
	// Keyed on the capture, not the resolve, because one capture can produce
	// several cards and only one of them may have something worth saying. A
	// lingering neighbour reports "Still seeing Sol Ring" while the new card
	// beside it commits silently; clearing per resolve let the sibling's
	// commit wipe that note depending purely on which lookup landed first.
	//
	// Two things deliberately do not clear. The straggler guard above returns
	// first, so a discarded session's late arrival cannot wipe the banner of
	// the session that replaced it. And while a cascade is open the banner
	// belongs to the card on screen — startReview puts the queue note there to
	// say why it queued — so a background resolve landing behind it leaves it
	// alone.
	if !m.reviewing() && it.captureSeq != m.statusSeq {
		m.status, m.statusErr = "", false
		m.statusSeq = it.captureSeq
	}

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
	m.note("resolve %q line=%d name=%dms prints=%dms rank=%s match=%s set=%s num=%s%s prints=%d%s%s%s",
		resolved, it.lineIdx, msg.nameDur.Milliseconds(), msg.printsDur.Milliseconds(),
		it.rank, matchDesc(it.match), orDash(it.raw.SetCode), orDash(it.raw.CollectorNumber),
		numberSourceSuffix(it.raw.NumberSource), len(it.prints), borderSuffix(it),
		finishSuffix(it), siblingSuffix(it))

	// A nudge-fired re-read of any recently processed card is the trigger
	// seeing what we already know — swallow it, and stop nudging: one echo is
	// confirmation enough that the card is parked, and further nudges would
	// photograph the same card forever (observed live at a slow start).
	// Nudging resumes with the next real scan. The check is against the whole
	// recent set, not a single last name: a multi-card recheck echoes every
	// card of the previous capture, and remembering only the last one let the
	// rest dup-queue (observed live: a re-shot pair queued both).
	// The finish correction used to sit here: a look carrying a marker that
	// contradicted a finish recorded moments ago *by default* rewrote the
	// earlier row instead of adding a card.
	//
	// Removed deliberately. A second scan is a second card — scanning a nonfoil
	// and then its foil is two cards, and rewriting the first row made that
	// impossible to express. The removed function's own comment had already
	// spotted the conflict: "Two copies of a card, one foil and one not,
	// scanned back to back look exactly like this, and rewriting the first row
	// would be as wrong as dropping the second."
	//
	// The cost is real and worth naming. It existed for an observed case — a
	// foil Inspired Fire recorded nonfoil, its marker legible on the very next
	// capture — and without it that becomes one wrong row *plus* one right one
	// rather than one corrected row. Two things make the trade better than it
	// was: the footer recovery pass rescues most missed markers, and the star
	// reads correctly on 93 of 95 captures that reach the footer.
	//
	// recentCommit.finishGuessed stays: it still marks rows nothing on the card
	// chose, which is the audit question docs/scanner-limits.md asks.
	// Name alone here, deliberately, and it is not the same question the
	// duplicate window asks.
	//
	// A nudge echo means *nothing was placed*: a timer expired on this side and
	// asked for another look at a scene nobody touched. Whatever finish that
	// look reports is a second reading of one card, so a difference between the
	// two is OCR noise and not a second card. Keying this gate on the printing
	// as well would let that noise through as a phantom.
	//
	// The foil case does not need it. A card actually laid down reports
	// `removed` or `replaced`, so `fromNudge` is false and this gate never runs.
	// A better read of a card still sitting unresolved in review replaces it,
	// however this capture fired.
	//
	// This ran only for nudge echoes and that was too narrow. The shutter often
	// fires while the card is still being lowered: the name reads off the top
	// of the card but the footer does not, so it queues as "printing
	// unverified", and about a second later the settled card is captured again
	// and reads perfectly. That second capture arrives as `replaced` — the held
	// window genuinely did change while the hand withdrew — so `fromNudge` is
	// false and the upgrade was skipped entirely. Observed live in a stacking
	// run: three pairs, each a queued phantom one second ahead of the correct
	// commit of the same card.
	//
	// Safe to widen because "better" is strictly higher printing evidence. Two
	// real copies scanned in a row read *equally* well, so neither displaces
	// the other and both survive — the discriminator is the quality of the
	// read, not the timing, which is what makes this something other than a
	// duplicate rule in disguise.
	//
	// Only ever a queued entry, never the one under the cursor: swapping a card
	// out from under someone mid-cascade is worse than a stale rank.
	upgraded, replacedQueued := m.upgradeQueued(it)
	if replacedQueued {
		m.note("outcome %q re-read: %s beats the queued %s, replacing it",
			it.canonical, it.rank, upgraded)
	}

	if it.fromNudge && it.canonical != "" && seenRecently(m.recentNames, it.canonical, now) {
		// The swallow assumes an echo is the same card saying the same thing,
		// which is true when the first look already committed. It is not true
		// when the first look *queued*: then the card is unresolved, this look
		// is a second reading of it, and it has just replaced that entry — so
		// swallowing it too would leave the card with no representation at all.
		//
		// Observed live: Prodigal Sorcerer's first capture abstained on the
		// border ("ring not uniform") and queued as "printing unverified: 23
		// printings"; its echo read the border cleanly, ranked year+border, and
		// was thrown away as a duplicate. The worse read won purely by
		// arriving first.
		if !replacedQueued {
			m.note("outcome %q dropped: nudge echo of a card already handled", it.canonical)
			m.nudgeDrops++
			m.recentNames = recordName(m.recentNames, it.canonical, now)
			m.status = fmt.Sprintf("Still seeing %s, waiting for the next card", it.canonical)
			m.statusErr = false
			// Rescheduled, not stopped. This returned nil, so a single echo
			// ended the recheck loop and the card sat there until the phone
			// happened to re-fire on its own — observed live at 73.5s for a
			// suppression the ten-second window should have released.
			//
			// Every other drop in this function reschedules; this was the
			// outlier, and the back-off below is what keeps the rescheduling
			// from becoming a permanent 5.5s poll of a parked card.
			return m, m.scheduleNudge()
		}
		// Otherwise fall through to the normal path, which commits it if the
		// new evidence clears the bar and re-queues it with the better
		// printing if it does not.
	}

	// A capture that identified nothing is not a card, and does not queue.
	//
	// Nothing means nothing: no name the catalog or Scryfall would accept, and
	// no collector block. What reaches here is ability text, a brand line, an
	// artist credit, a fragment of rules copy that read title-like, or an empty
	// frame — and a review entry for one of those offers the user a single
	// action, which is to discard it. Observed live: `"I tample"`,
	// `"creature, then put a +1/+1 counter"`, and a capture that read literally
	// nothing, all queued in one session of 25.
	//
	// This used to require a multi-card capture or a nudge re-look, on the
	// reasoning that the only card of a scene-fired shot must never vanish
	// silently. Both halves have since stopped meaning what they meant.
	// `siblings > 1` cannot happen at all now — the phone reads one card per
	// frame — and `fromNudge` was itself broken, matching a wire value the
	// phone has never sent. So the rule had quietly narrowed to almost nothing
	// while the queue filled with junk.
	//
	// The safety it was protecting is real but was aimed at the wrong thing: a
	// card is lost when evidence is thrown away, not when a reading of nothing
	// is. Two things still guard it. An entry that read *anything* off a card's
	// footer survives — a set code, a number, even a copyright year, and even
	// when the digits are too suspect to name a card with, because that is
	// still proof a card was in frame (observed live: Quicksilver, Brash Blur
	// arrived with a clean MSH/412 and a title read as rules text). And an
	// entry that failed with an *error* is never killed either: a lookup that
	// timed out is not a verdict about the card.
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
				// Two copies in one frame — a fanned playset. Two cards visible
				// is two cards, so both commit. This used to queue for a
				// deliberate confirm, which cost a stop on exactly the pile a
				// hands-free session exists for.
				it.dup = true
			case it.siblings > 1 || it.fromNudge:
				// A lingering neighbour: the card added a moment ago is still
				// in frame beside the new one. An un-swapped pile is not a
				// playset signal — drop it silently (observed live: one card
				// queued five re-sightings of itself this way).
				return m.suppressRepeat(it, finish, prior, now,
					"lingering neighbour of a just-added card", true)
			case it.fromMoved:
				// The source's own answer, and the one measure that beats
				// everything: a box held the watched spot and still looked like
				// the card already read, so it declined to claim a placement.
				// Honoured however long ago the last sighting was.
				return m.suppressRepeat(it, finish, prior, now,
					"same card, the source says it only moved", false)
			case it.placedDecisively():
				// The same machinery answering the other way, and answering
				// convincingly: a box on the watched spot wearing a face that
				// is not the captured card's, by a margin over the threshold
				// that decides it. The source looked at both cards. The floor
				// below only counts seconds, and it exists to stand in for
				// exactly this evidence — so where the evidence arrives, it
				// governs.
				//
				// This is what the floor was costing. Observed live: a second
				// No-Dachi stacked 1671ms after the first, reported `replaced`
				// with face=32.5 against a 20.0 threshold, dropped anyway on
				// the clock, and not written until 73s later when the window
				// aged out. The floor's own 3856ms measurement was fitted on
				// card *swaps* — remove, then place — and stacking skips the
				// removal, so it never described the faster motion at all.
				//
				// `it.dup` rides along exactly as in the deliberate-re-scan
				// case below: the outcome line and the session summary record
				// that a repeat was seen, which is how a false positive here
				// would ever be noticed.
				it.dup = true
			case since < sameCardFloor:
				// The fallback, now covering only what the source cannot
				// answer: a helper too old to send a reason, a manual shutter
				// with no trigger decision behind it, a `removed` that says a
				// card left without saying what replaced it, or a `replaced`
				// too marginal to trust. On a repeat under three seconds none
				// of those is credible, because nobody swaps a card in under
				// 3856ms. See sameCardFloor.
				return m.suppressRepeat(it, finish, prior, now,
					fmt.Sprintf("same card, re-read %dms after the last sighting",
						since.Milliseconds()), false)
			default:
				// A deliberate solo re-scan, slow enough to have been a real
				// swap, and the phone has said a card was placed — it watched
				// the last one leave, or watched this one laid over it. A
				// second scan is a second card, so it commits.
				//
				// It used to queue. That cost three stops on a playset of four,
				// and was not even consistent: a copy scanned outside the
				// ten-second window committed anyway, so the same physical
				// action gave different answers depending on how fast the
				// operator was.
				//
				// `it.dup` still rides along so the outcome line and the
				// session summary record that a repeat was seen — the only way
				// a false positive would ever be noticed.
				it.dup = true
			}
		}
	}

	if auto {
		card := it.prints[0]
		_, evidenced := finishFromEvidence(card, it.finishHint)
		res := Result{Card: card, Finish: finish, Qty: 1, ContainerID: m.dest.ID,
			FinishGuessed: !evidenced}
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
		// The chosen row's own set/number, which is not always what was read
		// off the card — and when they differ the log must say so, because
		// that divergence is exactly how a border shuffle once committed
		// SLD/604 off a clean M15/223 read with nothing in the session log to
		// show for it.
		chosen := ""
		if read := it.raw.SetCode; read != "" && !strings.EqualFold(read, card.Set) {
			chosen = fmt.Sprintf(" (read %s/%s)", strings.ToUpper(it.raw.SetCode),
				orDash(it.raw.CollectorNumber))
		}
		guessed := ""
		if !evidenced {
			guessed = " (finish guessed)"
		}
		m.note("outcome %q committed: %s/%s %s%s%s", card.Name,
			strings.ToUpper(card.Set), card.CollectorNumber, finish, chosen, guessed)
		m.recent = recordCommit(m.recent, card.ID, finish, it.captureSeq, now, !evidenced)
		m.recentNames = recordName(m.recentNames, it.canonical, now)
		m.nudgeDrops = 0
		// The retry bound is about one card's run of bad reads, not about the
		// session. Once anything commits, a later copy of the same card that
		// reads badly deserves its own second look — and a retry still waiting
		// to be sent is moot, because the card it was for is written.
		m.secondLookFor, m.secondLookPending = "", false
		// This is the case the held flash was held for: the retry read the card
		// properly, upgradeQueued took its entry back out of review, and the
		// review the phone was never told about turned out not to be one.
		// Only for *this* card — another card's held signal is still owed.
		m.clearDeferredFlash(it.canonical)
		// A new card landed, so "was that a second copy" no longer refers to
		// anything the operator is looking at.
		m.pending = nil
		m.addedCount++
		m.addedValue += priceValue(card, finish)
		// After the increment, so the HUD total is the post-commit number.
		m.celebrate(priceValuePtr(card, finish), finish)
		line := fmt.Sprintf("%s (%s/%s) %s · %s", card.Name,
			strings.ToUpper(card.Set), card.CollectorNumber, finish, priceForFinish(card, finish))
		m.recordTally(line)
		m.summary.add("auto", line)
		return m, m.scheduleNudge()
	}

	// Queue-bound, and the first question is whether this is the card that was
	// just committed, read worse the second time.
	//
	// The same-card floor above only guards the commit path, so a repeat whose
	// read *degraded* slipped past it into review. Observed live: Ancient
	// Silverback committed on M15/168, then 918ms later the same card came back
	// with no collector number and a set code of "TAP" scavenged out of its
	// rules text, ranked nothing, and queued as "printing unverified: 7
	// printings". The first read had already answered the question the queue
	// entry was asking.
	//
	// Same tests as the commit path, and the same exception: a sighting the
	// source decisively calls a placement is a second card however badly it
	// read, so it queues for the cascade instead of vanishing. It cannot
	// auto-commit — the read is the reason it got here — but "the operator
	// confirms it" and "nobody ever sees it" are very different answers.
	//
	// Otherwise: the source calling it a move, or a repeat too fast to be a
	// swap. A worse read is not new evidence.
	if !it.dup && it.canonical != "" && !it.placedDecisively() {
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

	// An unresolved or shaky read that looks like a recently processed name,
	// seen in a nudge or multi-card context, is that card still in frame
	// wearing an OCR mangle — drop it rather than pile OCR variants of an
	// already-added card into review. A solo, non-nudge capture still queues: a
	// deliberately re-scanned worn card must not vanish. A dup-flagged item
	// skips the probe: it was recognized above as a deliberate duplicate, and
	// matching its own name here would swallow the very queue entry the
	// recognition chose to keep.
	if !it.dup && (it.fromNudge || it.siblings > 1) {
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

	if it.canonical != "" {
		m.recentNames = recordName(m.recentNames, it.canonical, now)
	}
	m.note("outcome %q queued: %s", orDash(it.canonical), note)
	m.nudgeDrops = 0
	m.pending = nil
	// The phone's review signal waits on the retry. A card we are about to
	// photograph again is not yet a review — it is a card we have not finished
	// reading — and telling the phone otherwise flashes a stop the operator did
	// not need to see, on a card that the second look usually rescues. The
	// entry still goes into the queue immediately, because if the retry never
	// answers the card must already be there.
	secondLook := m.wantsSecondLook(it)
	if !secondLook {
		m.reviewFlash()
		m.clearDeferredFlash(it.canonical)
	}
	it.note = note
	m.review = append(m.review, it)
	// The ordinary quiet-period timer is still armed, and stays armed, as the
	// backstop: a helper too old to report its trigger state, or one whose
	// "armed" goes missing, still gets its retry — just at the swap cadence
	// instead of immediately. Whichever arrives first wins, and a real capture
	// voids the loser.
	nudge := m.scheduleNudge()
	if secondLook {
		m.secondLookFor, m.secondLookPending = it.canonical, true
		m.deferredFlashFor = it.canonical
		m.note("outcome %q: looking again", it.canonical)
	}
	next, cmd := m.reviewChanged()
	return next, tea.Batch(cmd, nudge)
}

// suppressRepeat drops a repeat sighting of a card already handled, and keeps
// it on hand in case the drop was wrong.
//
// Dropped rather than queued: no write, no sound, no review stop. A
// suppression is the scanner recognizing a card it already dealt with, and
// every one of those it makes a review entry out of is a stop on the pile a
// hands-free session exists to avoid.
//
// The anchor rolls forward so a card announcing itself once a second stays
// suppressed for as long as it keeps doing it — see touchCommit.
//
// What is new is that the sighting survives the drop. The status line says so,
// and `+` writes it. See pendingDup for why the silence needed an exit.
func (m model) suppressRepeat(it queueItem, finish string, prior recentCommit,
	now time.Time, why string, besideNew bool) (tea.Model, tea.Cmd) {
	// The duplicate rules just said this is physically the card already
	// written. If that row's finish was the nonfoil default — nothing on the
	// card chose it — and this look carries a real marker, the re-read is not
	// noise to drop but the evidence the first look was missing: re-key the
	// row instead of leaving a silently wrong one. Observed live: Brainsurge
	// committed nonfoil off silence, read foil(sparkle-luma) 800ms later.
	//
	// Only ever guess → evidence. An evidenced row is never reopened (a
	// contradicting later read of the same card is OCR noise, see the echo
	// gate in onResolveDone), and a guess cannot replace a guess because two
	// defaults cannot differ.
	if card := it.prints[0]; finish != prior.finish && prior.finishGuessed {
		if _, evidenced := finishFromEvidence(card, it.finishHint); evidenced {
			res := Result{Card: card, Finish: finish, Qty: 1,
				ContainerID: m.dest.ID, ReplacesFinish: prior.finish}
			if err := m.adder(res); err != nil {
				m.note("outcome %q dropped: %s (finish correction failed: %v)",
					it.canonical, why, err)
			} else {
				m.note("outcome %q corrected: %s/%s %s → %s, %s", it.canonical,
					strings.ToUpper(card.Set), card.CollectorNumber,
					prior.finish, finish, why)
				m.recent = rekeyCommit(m.recent, card.ID, prior.finish, finish, now)
				m.recentNames = recordName(m.recentNames, it.canonical, now)
				m.addedValue += priceValue(card, finish) - priceValue(card, prior.finish)
				line := fmt.Sprintf("%s (%s/%s) %s · corrected from %s", card.Name,
					strings.ToUpper(card.Set), card.CollectorNumber, finish, prior.finish)
				m.recordTally(line)
				m.summary.add("auto", line)
				m.status = fmt.Sprintf("Corrected %s to %s", it.canonical, finish)
				m.statusErr = false
				return m, m.scheduleNudge()
			}
		}
	}
	m.note("outcome %q dropped: %s", it.canonical, why)
	m.recent = touchCommit(m.recent, it.prints[0].ID, now)
	m.recentNames = recordName(m.recentNames, it.canonical, now)
	m.pending = &pendingDup{it: it, finish: finish, at: now}
	seeing := fmt.Sprintf("Still seeing %s", it.canonical)
	if besideNew {
		seeing += " beside the new card"
	}
	// An instruction, not a report. The old line described the scanner's state
	// and left the operator with nothing to do about it — which was accurate
	// and useless in the one case that matters, where the suppression is
	// wrong.
	m.status = seeing + " — press + if that's a second copy"
	m.statusErr = false
	return m, m.scheduleNudge()
}

// promotePending writes the suppressed sighting `+` was pressed on.
//
// It goes through the ordinary commit bookkeeping rather than a shortcut of
// its own: the same adder, the same recent-commit window, the same celebration
// and receipt. A card promoted by hand is a card, and a session where it read
// differently in the scrollback would be one nobody could audit.
//
// The summary kind is "duplicate-confirmed", which already means exactly this
// — a repeat the operator confirmed — and already renders and counts.
func (m model) promotePending() (tea.Model, tea.Cmd) {
	p := m.pending
	if p == nil || m.now().Sub(p.at) > pendingDupWindow {
		// Answered rather than ignored. The status line invited this key, so a
		// silent no-op would read as the program having missed it.
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
		m.review = append(m.review, it)
		next, cmd := m.reviewChanged()
		return next, cmd
	}
	m.note("outcome %q committed: %s/%s %s (promoted by hand)", card.Name,
		strings.ToUpper(card.Set), card.CollectorNumber, p.finish)
	// Banked under a fresh captureSeq so the copy just written cannot itself be
	// read as the fanned-playset case by whatever the next capture brings.
	m.captureSeq++
	m.recent = recordCommit(m.recent, card.ID, p.finish, m.captureSeq, now, !evidenced)
	m.recentNames = recordName(m.recentNames, p.it.canonical, now)
	m.addedCount++
	m.addedValue += priceValue(card, p.finish)
	m.celebrate(priceValuePtr(card, p.finish), p.finish)
	line := fmt.Sprintf("%s (%s/%s) %s · %s", card.Name,
		strings.ToUpper(card.Set), card.CollectorNumber, p.finish,
		priceForFinish(card, p.finish))
	m.recordTally(line)
	m.summary.add("duplicate-confirmed", line)
	m.status = "✓ Added a second copy of " + card.Name
	m.statusErr = false
	return m, nil
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
func (m model) celebrate(price *float64, finish string) {
	if m.session == nil {
		return
	}
	if !m.hudCapable {
		m.chime()
		return
	}
	r := scan.HUDResult{
		Amount: price, Tier: tierFor(price, m.hudWin, m.hudJackpot), Finish: finish,
	}
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

// clearDeferredFlash drops a held review signal because the question it was
// waiting on has been answered — the card committed, or it has just flashed for
// real. Name-matched, so one card's retry cannot swallow another's signal.
func (m *model) clearDeferredFlash(name string) {
	if name != "" && m.deferredFlashFor == name {
		m.deferredFlashFor = ""
	}
}

// flushDeferredFlash sends a review signal that was held for a retry which never
// answered, and reports whether it sent one.
//
// The catch-all, and the reason holding the flash is safe at all. Every path
// that could resolve a retry clears the hold explicitly; this covers the path
// where nothing resolves it — the operator pockets the card, the phone stops
// reporting, the retry photograph never happens. Without it a queued card would
// sit in the list having never made a sound, which is worse than the early flash
// this whole mechanism exists to avoid.
func (m *model) flushDeferredFlash() bool {
	if m.deferredFlashFor == "" {
		return false
	}
	m.deferredFlashFor = ""
	m.reviewFlash()
	return true
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

// upgradeQueued drops a queued entry that a new read of the same card beats,
// reporting the rank it displaced.
//
// "Beats" is strictly higher printing evidence and nothing else. Equal ranks
// change nothing worth the churn, and a *lower* rank arriving later is exactly
// the echo the swallow exists to absorb.
//
// The item being reviewed right now (m.current) is deliberately out of scope.
// It is on screen with a cascade open against it, and replacing it underneath
// the operator would be a worse bug than the one this fixes.
func (m *model) upgradeQueued(it queueItem) (scanMatch, bool) {
	if it.canonical == "" {
		return 0, false
	}
	for i, q := range m.review {
		if q.canonical == it.canonical && it.rank > q.rank {
			m.review = append(m.review[:i], m.review[i+1:]...)
			return q.rank, true
		}
	}
	return 0, false
}

// tallyShown is how many auto-commits the capture view shows at once.
//
// Ten rows rather than the whole list: the receipt shares the screen with the
// status line, the counter and the key help, and an unbounded one pushed all of
// that off the bottom on a long pile. The rest is reachable with ↑/↓.
const tallyShown = 10

// recordTally appends one auto-commit to the live receipt.
//
// If the operator has scrolled back, the offset grows with the slice so the
// same rows stay put. Otherwise an arriving card would shunt the history along
// by one under a reader's eyes — which is exactly the moment they are least
// able to follow it.
func (m *model) recordTally(line string) {
	m.tally = append(m.tally, line)
	if m.tallyOffset > 0 {
		m.tallyOffset++
	}
	m.clampTally()
}

// tallyMaxOffset is the furthest back the receipt can scroll: the oldest row
// sitting at the top of the window.
func (m model) tallyMaxOffset() int {
	return max(0, len(m.tally)-tallyShown)
}

// clampTally holds the offset inside the history that exists.
func (m *model) clampTally() {
	m.tallyOffset = min(max(m.tallyOffset, 0), m.tallyMaxOffset())
}

// scrollTally moves the receipt's window, positive for older rows.
func (m model) scrollTally(by int) (tea.Model, tea.Cmd) {
	m.tallyOffset += by
	m.clampTally()
	return m, nil
}

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
		m.celebrate(amt, res.Finish)
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
	m.status = fmt.Sprintf("Review abandoned · %d cards not added", dropped)
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

// submitPairCode records the code the phone is showing and opens the session.
//
// Validated here rather than after a failed connection because the failure mode
// otherwise is a browse that finds the phone, a handshake that is refused, and
// a timeout — three steps away from the typo that caused it.
// pairedMsg carries the result of a pairing check.
type pairedMsg struct{ err error }

// pairCmd asks the phone to accept a code, off the update loop.
func (m model) pairCmd(deviceID, code string) tea.Cmd {
	sc := m.scanner
	return func() tea.Msg {
		return pairedMsg{err: sc.Pair(deviceID, code)}
	}
}

// onPaired lands the pairing check.
func (m model) onPaired(msg pairedMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		// Back to the code prompt rather than out to the name prompt: a
		// mistyped digit is the likeliest cause and retyping six characters is
		// the whole fix.
		m.state = statePairCode
		m.status = "Pairing failed: " + msg.err.Error()
		m.statusErr = true
		m.codeInput.Focus()
		return m, nil
	}
	// Straight into a session. This used to stop at the name prompt on the
	// reasoning that the user asked to set a phone up, not to start scanning —
	// dogfooding said otherwise, and it is right: having just picked a phone and
	// typed its code, wanting to scan with it is the only plausible next intent.
	//
	// The pairing check disconnects after verifying, so this reconnects a moment
	// later. That is the seam to look at if the hand-off ever feels ragged.
	m.pairing = false
	m.cameraChosen = true
	// The next camera list goes straight to this phone rather than offering it
	// beside the others, which is a question the last six keystrokes already
	// answered.
	m.justPairedID = m.cameraID
	m.status = fmt.Sprintf("Paired with %s", m.cameraName)
	// statusErr is sticky across states, so a banner left over from an earlier
	// failure would render this success in the error style.
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
	// Off the update loop. Pair reaches the phone to check the code, so it is a
	// round trip over the network — running it inline froze the TUI with no
	// spinner, which is indistinguishable from a crash.
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
	return "ctrl+d done · " + m.escLabel()
}

// help renders a help line wrapped between its " · " entries, so a hint that
// would be cut mid-phrase by the view-wide word wrap moves whole to the next
// line instead — "esc close camera" never splits after "esc".
func (m model) help(s string) string {
	return m.theme.Help.Render(strings.Join(ui.WrapHelp(s, m.width), "\n"))
}

// escLabel is the esc hint alone, shared by every step where esc opens the
// leave gate.
func (m model) escLabel() string { return "esc " + m.escWord() }

// escWord is escLabel without its key, for lines composed from ui.HelpEntry
// rather than spliced from strings.
func (m model) escWord() string {
	if m.embedded {
		return "back"
	}
	return "quit"
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
		// The drawer, when it is open, replaces the hint line rather than
		// stacking under it: the palette is the exhaustive answer to the
		// question the hint line answers in curated prose, and showing both
		// says the same thing twice in two registers.
		if m.addPalette != nil {
			b.WriteString(m.addPaletteView())
			return b.String()
		}
		// Composed from entries rather than spliced from strings, so the
		// palette hint is the same shared value the browser's views use and
		// a conditional slot is an append rather than a substring. See
		// ui.HelpCommands.
		e := []ui.HelpEntry{ui.HelpCommands}
		if m.scanner != nil {
			// Pairing leads the scanning pair. It is the once-per-phone step
			// that has to happen before scanning works at all, so it is what
			// a first-time reader needs to find — and once a phone is paired
			// the reader has stopped reading this line anyway.
			//
			// A live session is returned to, not opened, and the word says so.
			open := "scan"
			if m.session != nil {
				open = "camera"
			}
			e = append(e, ui.K("ctrl+p", "pair"), ui.K("ctrl+o", open))
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
		if m.status != "" && m.statusErr {
			banner = m.theme.Err.Render(m.status) + "\n\n"
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
		// The live tally: a window onto the auto-commits, newest last, so an
		// unattended write is visible the moment it happens.
		if len(m.tally) > 0 {
			end := len(m.tally) - min(m.tallyOffset, m.tallyMaxOffset())
			start := max(0, end-tallyShown)
			for _, line := range m.tally[start:end] {
				b.WriteString(m.theme.OK.Render("✓ Auto-added: "+line) + "\n")
			}
			// Only once there is history to miss. Saying "1-3 of 3" on a short
			// session is noise about a control nobody needs yet.
			if len(m.tally) > tallyShown {
				b.WriteString(m.help(fmt.Sprintf("showing %d-%d of %d · ↑/↓ history",
					start+1, end, len(m.tally))) + "\n")
			}
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
			b.WriteString("Set a card down and the app will run auto capture. Press spacebar to manually trigger a scan.\n\n")
		case "held":
			b.WriteString("Captured. Swap in the next card.\n\n")
		default:
			b.WriteString("Frame the next card, then press space.\n\n")
		}
		keys := []string{": commands", "space capture"}
		if len(m.tally) > tallyShown {
			keys = append(keys, "↑/↓ history")
		}
		if m.torchCapable {
			keys = append(keys, "t torch")
		}
		help := strings.Join(keys, " · ") + " · c close camera · " + m.escLabel() + " · ctrl+c force quit"
		if len(m.review) > 0 {
			help = fmt.Sprintf("Press tab to review (%d) · %s", len(m.review), help)
		}
		if m.addedCount > 0 {
			help = m.sessionTally() + " · " + help
		}
		b.WriteString(m.help(help))
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
	p := finishPrice(c, finish)
	if p == nil {
		return 0
	}
	return *p
}

func priceForFinish(c scryfall.Card, finish string) string {
	p := finishPrice(c, finish)
	if p == nil {
		return "—"
	}
	return "$" + priceStr(p)
}

// finishPrice is one printing's price for a finish, on the same rule the store
// values a holding by: etched reads its own figure and falls back to foil.
func finishPrice(c scryfall.Card, finish string) *float64 {
	if scryfall.PricedAsFoil(finish) {
		return scryfall.EffectiveFoilPrice(finish, c.PriceUSDFoil, c.PriceUSDEtched)
	}
	return c.PriceUSD
}

func priceStr(p *float64) string {
	if p == nil {
		return "—"
	}
	return strconv.FormatFloat(*p, 'f', 2, 64)
}
