// Package tui provides an interactive terminal cascade for adding a card to the
// collection by name: type a name, and it walks through only the questions
// needed (which card, which printing, which finish, how many) to pinpoint one
// exact entry, resolving candidates from Scryfall.
package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cphillips918/hoard/internal/scan"
	"github.com/cphillips918/hoard/internal/scryfall"
)

// Searcher supplies the Scryfall lookups the cascade needs. It is an interface
// so the model can be driven by a fake in tests.
type Searcher interface {
	Autocomplete(ctx context.Context, q string) ([]string, error)
	SearchPrints(ctx context.Context, exactName string) ([]scryfall.Card, error)
	NamedFuzzy(ctx context.Context, text string) (*scryfall.Card, error)
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
	// Events streams what the camera window reports; closed when the session is.
	Events() <-chan scan.Event
	// Close ends the session and shuts the window.
	Close() error
}

// Result is the pinpointed selection to add to the collection.
type Result struct {
	Card   scryfall.Card
	Finish string // normal|foil|etched
	Qty    int
}

// Adder persists a confirmed selection. It is called once per card the user
// confirms during a session; returning an error surfaces a banner but keeps the
// session open.
type Adder func(Result) error

// scryfallSearcher adapts the package-level scryfall functions to Searcher.
type scryfallSearcher struct{}

func (scryfallSearcher) Autocomplete(ctx context.Context, q string) ([]string, error) {
	return scryfall.Autocomplete(ctx, q)
}
func (scryfallSearcher) SearchPrints(ctx context.Context, name string) ([]scryfall.Card, error) {
	return scryfall.SearchPrints(ctx, name)
}
func (scryfallSearcher) NamedFuzzy(ctx context.Context, text string) (*scryfall.Card, error) {
	return scryfall.NamedFuzzy(ctx, text)
}

// NewScryfallSearcher returns a Searcher backed by the live Scryfall API.
func NewScryfallSearcher() Searcher { return scryfallSearcher{} }

// Run launches an interactive add session: the user searches for a card (by
// typing a name or scanning one with the camera), walks the cascade, and
// confirms; each confirmed card is persisted via add and the session loops back
// for the next card until the user exits. sc may be nil to disable the camera
// scan action. initialName optionally pre-seeds the first search.
func Run(ctx context.Context, s Searcher, add Adder, sc Scanner, initialName string) error {
	m := newModel(ctx, s, add, sc, initialName)
	p := tea.NewProgram(m)
	final, err := p.Run()
	// However the program ended — quit, esc, or a crash — the camera window must
	// not outlive it.
	if fm, ok := final.(model); ok && fm.session != nil {
		_ = fm.session.Close()
	}
	if err != nil {
		return err
	}
	return final.(model).err
}
