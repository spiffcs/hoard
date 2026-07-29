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

// NewScryfallSearcher returns a Searcher backed by the live Scryfall API.
func NewScryfallSearcher() Searcher { return scryfallSearcher{} }

// Run launches an interactive add session: the user searches for a card, walks
// the cascade, and confirms; each confirmed card is persisted via add and the
// session loops back for the next card until the user exits. initialName
// optionally pre-seeds the first search.
func Run(ctx context.Context, s Searcher, add Adder, initialName string) error {
	m := newModel(ctx, s, add, initialName)
	p := tea.NewProgram(m)
	final, err := p.Run()
	if err != nil {
		return err
	}
	return final.(model).err
}
