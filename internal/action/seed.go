package action

import (
	"bytes"
	"fmt"

	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/store"
)

// SeedResult is what a seed put into the database, for the caller to report.
type SeedResult struct {
	Printings int
	Copies    int
	Decks     int
	DeckCards int
}

// SeedHoard applies an interchange document to a store.
//
// It is MergeHoard's second half without the first: no source database to open,
// no schema negotiation with a file the user pointed at, and no re-import ledger
// check — the caller supplies the bytes and owns the destination. `hoard demo`
// is the caller that wanted this, seeding a throwaway database from a document
// compiled into the binary.
//
// Sharing planMerge is the point rather than an economy. A demo built by some
// other path would be a second definition of "a populated hoard", free to drift
// from the real one; going through the planner means the sample database is
// assembled by exactly the code that assembles a merged one, and a bug that
// would corrupt a real merge shows up in the demo first.
//
// The destination is assumed empty. Nothing here forbids seeding a populated
// store — the planner would fold binders and skip known decks as it does for
// any merge — but that is not a case this is written for, and `hoard demo`
// avoids it by seeding only a database it just created.
func SeedHoard(st *store.Store, doc []byte, file string) (SeedResult, error) {
	var out SeedResult

	h, err := hoardjson.ReadHoard(bytes.NewReader(doc))
	if err != nil {
		return out, fmt.Errorf("reading %s: %w", file, err)
	}

	// planMerge reports through a MergeResult, so one is supplied to be written
	// into and mostly discarded. PerBinder must be non-nil: the planner assigns
	// into it rather than creating it.
	res := MergeResult{PerBinder: make(map[string]int)}
	plan, err := planMerge(st, h, MergeOptions{}, &res)
	if err != nil {
		return out, fmt.Errorf("planning %s: %w", file, err)
	}

	receipt := &store.ImportReceipt{
		Hash:  ContentHash(doc),
		File:  file,
		Cards: res.Copies + res.DeckCards,
	}
	if _, err := st.ApplyMerge(receipt, plan); err != nil {
		return out, fmt.Errorf("applying %s: %w", file, err)
	}

	return SeedResult{
		Printings: res.Printings,
		Copies:    res.Copies,
		Decks:     res.Decks,
		DeckCards: res.DeckCards,
	}, nil
}
