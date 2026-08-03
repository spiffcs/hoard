// Package tui provides an interactive terminal cascade for adding a card to the
// collection by name: type a name, and it walks through only the questions
// needed (which card, which printing, which finish, how many) to pinpoint one
// exact entry. Candidates come from whatever Searcher the caller supplies —
// the local catalog, the Scryfall API, or a layering of both.
package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/cardname"
	"github.com/spiffcs/hoard/internal/scan"
	"github.com/spiffcs/hoard/internal/scryfall"
)

// Searcher supplies the Scryfall lookups the cascade needs. It is an interface
// so the model can be driven by a fake in tests.
type Searcher interface {
	Autocomplete(ctx context.Context, q string) ([]string, error)
	SearchPrints(ctx context.Context, exactName string) ([]scryfall.Card, error)
	// NamedFuzzy resolves possibly-imperfect text to a single card, reporting
	// how the match earned its answer — the auto-commit decision needs to tell
	// an exact hit from a barely-plausible one.
	NamedFuzzy(ctx context.Context, text string) (*scryfall.Card, cardname.Match, error)
}

// Scanner opens camera sessions for the scan action. It is optional (a nil
// Scanner disables scanning); errors are surfaced as a banner, so the add
// session continues.
type Scanner interface {
	// Devices lists the cameras available to capture from. In practice these are
	// iPhones offered via Continuity Camera; webcams are not eligible.
	Devices(ctx context.Context) ([]scan.Device, error)
	// Open starts a capture session on the given camera, leaving its window up
	// until the session is closed. deviceID is empty to let the scanner pick.
	Open(ctx context.Context, deviceID string) (ScanSession, error)
}

// ScanSession is a live camera window. It stays open across many captures, so a
// run of cards is scanned without reopening the camera between each one — which
// would cost a warm-up every time and force the user back to the keyboard to
// re-trigger it.
type ScanSession interface {
	// Capture takes a photo; the result arrives on Events as a scan event.
	Capture() error
	// Rotate turns the preview a quarter-turn.
	Rotate(left bool) error
	// Auto turns the helper's hands-free trigger on or off. Only sent to
	// helpers that advertised the feature on their ready event.
	Auto(on bool) error
	// AutoFraming turns the camera's auto-framing (Center Stage) on or off —
	// the only framing adjustment macOS offers. The helper starts every
	// session with it off, since its subject-tracking crop frames cards too
	// close. Only sent to helpers that advertised "framing" on their ready
	// event.
	AutoFraming(on bool) error
	// Torch turns the phone's flashlight on or off to light the card. Only
	// sent to helpers that advertised "torch" on their ready event — which
	// Continuity Camera currently never does (the flashlight isn't bridged
	// to the Mac); the control exists for the day it is.
	Torch(on bool) error
	// VideoEffects opens the system's Video Effects panel, where Studio
	// Light — the software lighting that IS available — lives. Only sent to
	// helpers that advertised "effects" on their ready event.
	VideoEffects() error
	// Rearm nudges a parked auto trigger back to searching; the caller
	// discards the re-read if it is the card it already processed.
	Rearm() error
	// Chime plays the card-processed sound: the audible receipt fired when
	// a scan resolves — auto-added or queued for review — so the user knows
	// to place the next card without reading the screen. Fired at
	// resolution rather than capture time so a nudge-armed capture is
	// never silent.
	Chime() error
	// Result reports a resolved card's price outcome for the camera HUD's
	// price treatment: flash + tier sound when Tier is set, a silent update
	// of the running session counter when Total is set. Only sent to
	// helpers that advertised "hud" on their ready event; older helpers get
	// Chime instead. One sound per moment: an auto-committed card sounds
	// once, a reviewed card sounds at queue time (the question) and again
	// at confirm time (the answer, with the amount).
	Result(scan.HUDResult) error
	// Note appends one line to the session's telemetry log, when one is
	// open — the Go side's slice of the per-card latency breakdown. Best
	// effort and silent: telemetry must never affect the session.
	Note(line string)
	// Events streams what the camera window reports; closed when the session is.
	Events() <-chan scan.Event
	// Close ends the session and shuts the window.
	Close() error
}

// Destination is somewhere a confirmed card can go: a binder, or a deck's
// mainboard. The cascade only displays these — what an ID means is the
// caller's business, which is what keeps this package free of store imports.
type Destination struct {
	ID   int64
	Name string
	Kind string // "binder" | "deck", display only
}

// Result is the pinpointed selection to add to the collection. ContainerID is
// the chosen destination's ID, or 0 when the caller supplied no destinations —
// in which case wherever the Adder defaults to is the destination.
type Result struct {
	Card        scryfall.Card
	Finish      string // normal|foil|etched
	Qty         int
	ContainerID int64
}

// Adder persists a confirmed selection. It is called once per card the user
// confirms during a session; returning an error surfaces a banner but keeps the
// session open.
type Adder func(Result) error

// SummaryEntry is one line of a session's receipt. Kind is "auto" (committed
// without user action), "reviewed" (confirmed through the cascade), "skipped",
// or "discarded".
type SummaryEntry struct {
	Kind string
	Line string
}

// Summary is the session's receipt: what the scan flow did on its own and what
// the user decided, in order. The caller prints it after the program exits, so
// the terminal scrollback records every unattended write.
type Summary struct {
	Entries []SummaryEntry
}

// Count returns how many entries of one kind the session produced.
func (s Summary) Count(kind string) int {
	n := 0
	for _, e := range s.Entries {
		if e.Kind == kind {
			n++
		}
	}
	return n
}

func (s *Summary) add(kind, line string) {
	s.Entries = append(s.Entries, SummaryEntry{Kind: kind, Line: line})
}

// Run launches an interactive add session: the user searches for a card (by
// typing a name or scanning one with the camera), walks the cascade, and
// confirms; each confirmed card is persisted via add and the session loops back
// for the next card until the user exits. sc may be nil to disable the camera
// scan action. initialName optionally pre-seeds the first search.
//
// dests is everywhere a card may be put. With one (or none) the cascade never
// asks — the single-binder flow is exactly what it always was; with more, a
// destination step follows the finish, remembering the last pick so a bulk
// add is one enter per card.
func Run(ctx context.Context, s Searcher, add Adder, sc Scanner, initialName string, dests []Destination) (Summary, error) {
	m := newModel(ctx, s, add, sc, initialName, dests)
	p := tea.NewProgram(m)
	final, err := p.Run()
	// However the program ended — quit, esc, or a crash — the camera window must
	// not outlive it.
	fm, ok := final.(model)
	if ok && fm.session != nil {
		_ = fm.session.Close()
	}
	if err != nil {
		return Summary{}, err
	}
	return fm.summary, fm.err
}
