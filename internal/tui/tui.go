package tui

import (
	"context"
	"github.com/spiffcs/hoard/internal/finish"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/spiffcs/hoard/internal/cardname"
	"github.com/spiffcs/hoard/internal/scan"
	"github.com/spiffcs/hoard/internal/scryfall"
)

type Searcher interface {
	Autocomplete(ctx context.Context, q string) ([]string, error)
	SearchPrints(ctx context.Context, exactName string) ([]scryfall.Card, error)

	NamedFuzzy(ctx context.Context, text string) (*scryfall.Card, cardname.Match, error)
}

type Scanner interface {
	Devices(ctx context.Context) ([]scan.Device, error)

	Open(ctx context.Context, deviceID string) (ScanSession, error)

	Pair(deviceID, code string) error
}

type ScanSession interface {
	Capture() error

	Auto(on bool) error

	Torch(on bool) error

	Rearm() error

	Chime() error

	Result(scan.HUDResult) error

	Note(line string)

	Events() <-chan scan.Event

	Close() error
}

type Destination struct {
	ID   int64
	Name string
	Kind string
}

type Result struct {
	Card        scryfall.Card
	Finish      finish.Finish
	Qty         int
	ContainerID int64

	ReplacesFinish finish.Finish

	FinishGuessed bool

	PrintingGuessed bool
}

type Adder func(Result) error

type SummaryEntry struct {
	Kind string
	Line string
}

type Summary struct {
	Entries []SummaryEntry

	Ignored int
}

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

func Run(ctx context.Context, s Searcher, add Adder, sc Scanner, initialName string, dests []Destination) (Summary, error) {
	m := newModel(ctx, s, add, sc, initialName, dests)
	p := tea.NewProgram(m)
	final, err := p.Run()

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
