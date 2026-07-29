// Package tui provides an interactive terminal cascade for adding a card to the
// collection by name: type a name, and it walks through only the questions
// needed (which card, which printing, which finish, how many) to pinpoint one
// exact entry, resolving candidates from Scryfall.
package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cphillips918/hoard/internal/scryfall"
)

// Searcher supplies the Scryfall lookups the cascade needs. It is an interface
// so the model can be driven by a fake in tests.
type Searcher interface {
	Autocomplete(ctx context.Context, q string) ([]string, error)
	SearchPrints(ctx context.Context, exactName string) ([]scryfall.Card, error)
}

// Result is the pinpointed selection to add to the collection.
type Result struct {
	Card   scryfall.Card
	Finish string // normal|foil|etched
	Qty    int
}

// scryfallSearcher adapts the package-level scryfall functions to Searcher.
type scryfallSearcher struct{}

func (scryfallSearcher) Autocomplete(ctx context.Context, q string) ([]string, error) {
	return scryfall.Autocomplete(ctx, q)
}
func (scryfallSearcher) SearchPrints(ctx context.Context, name string) ([]scryfall.Card, error) {
	return scryfall.SearchPrints(ctx, name)
}

// NewScryfallSearcher returns a Searcher backed by the live Scryfall API.
func NewScryfallSearcher() Searcher { return scryfallSearcher{} }

// Run launches the interactive cascade, optionally pre-seeded with a name typed
// on the command line. It returns the confirmed selection, or (nil, nil) if the
// user cancelled.
func Run(ctx context.Context, s Searcher, initialName string) (*Result, error) {
	m := newModel(ctx, s, initialName)
	p := tea.NewProgram(m)
	final, err := p.Run()
	if err != nil {
		return nil, err
	}
	fm := final.(model)
	if fm.err != nil {
		return nil, fm.err
	}
	return fm.result, nil
}
