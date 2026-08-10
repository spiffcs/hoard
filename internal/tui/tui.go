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
	// Devices lists the phones available to capture from: iPhones running
	// Hoardling, found on the local network. There is no other kind of
	// source — the Mac's own cameras were never eligible (a fixed, user-facing
	// lens cannot be aimed at a card on the desk) and the Continuity Camera
	// path that used to sit here has been removed.
	Devices(ctx context.Context) ([]scan.Device, error)
	// Open starts a capture session on the given phone, holding its camera up
	// until the session is closed. deviceID is empty to let the scanner pick.
	Open(ctx context.Context, deviceID string) (ScanSession, error)
	// Pair records the code for a device Open refused with scan.ErrNotPaired,
	// or one the user chose to pair with deliberately. The TUI collects the
	// digits and hands them over; where they are kept is not its business, for
	// the same reason the rotation preference is not.
	Pair(deviceID, code string) error
}

// ScanSession is a live link to the phone's camera. It stays open across many
// captures, so a run of cards is scanned without reopening the camera between
// each one — which would cost a browse, a handshake and a warm-up every time
// and force the user back to the keyboard to re-trigger it.
type ScanSession interface {
	// Capture takes a photo; the result arrives on Events as a scan event.
	Capture() error
	// Auto turns the phone's hands-free trigger on or off. Only sent to
	// sources that advertised the feature on their ready event.
	Auto(on bool) error
	// Torch turns the phone's flashlight on or off to light the card. Only
	// sent to sources that advertised "torch" on their ready event.
	Torch(on bool) error
	// Rearm nudges a parked auto trigger back to searching; the caller
	// discards the re-read if it is the card it already processed.
	Rearm() error
	// Chime plays the card-processed sound: the audible receipt fired when
	// a scan resolves — auto-added or queued for review — so the user knows
	// to place the next card without reading the screen. Fired at
	// resolution rather than capture time so a nudge-armed capture is
	// never silent.
	Chime() error
	// Result reports a resolved card's price outcome for the phone's HUD:
	// flash + tier sound when Tier is set, a silent update of the running
	// session counter when Total is set. Only sent to sources that
	// advertised "hud" on their ready event; older ones get
	// Chime instead. One sound per moment: an auto-committed card sounds
	// once, a reviewed card sounds at queue time (the question) and again
	// at confirm time (the answer, with the amount).
	Result(scan.HUDResult) error
	// Note appends one line to the session's telemetry log, when one is
	// open — the Go side's slice of the per-card latency breakdown. Best
	// effort and silent: telemetry must never affect the session.
	Note(line string)
	// Events streams what the phone reports; closed when the session is.
	Events() <-chan scan.Event
	// Close ends the session and releases the phone's camera.
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
	// ReplacesFinish, when set, restates a holding already added in this
	// session rather than adding another copy: the scan committed the nonfoil
	// default because no marker was legible, and a later look at the same card
	// read the real one. Adders that cannot re-key a finish may treat this as
	// an ordinary add — the result is a duplicate rather than a correction,
	// which is the safer way to be wrong.
	ReplacesFinish string
	// FinishGuessed marks a finish nothing on the card chose — the nonfoil
	// default written because no marker read. Adders that keep an audit trail
	// record it so the row can be checked against the physical card later;
	// adders that do not may ignore it.
	FinishGuessed bool
	// PrintingGuessed marks a printing no digits confirmed: the scan read a
	// name, a year and the footer's frame family, and those picked one row —
	// the frame stratum's context bet. Same audit contract as FinishGuessed:
	// record it where possible so a later look can correct the row, ignore it
	// where not.
	PrintingGuessed bool
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
	// Ignored counts captures that identified nothing — no name, no collector
	// block — and were dropped rather than queued for review.
	//
	// A count rather than an entry each, because there is nothing to say about
	// one: the whole point is that the capture held no card. It is reported at
	// all because silently discarding captures and correctly discarding them
	// look identical from the terminal, and a scanner quietly eating real cards
	// should be visible in the scrollback.
	Ignored int
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
	sum := fm.summary
	sum.Ignored = fm.ignored
	return sum, fm.err
}
