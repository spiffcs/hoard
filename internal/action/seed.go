package action

import (
	"bytes"
	"fmt"

	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/store"
)

type SeedResult struct {
	Printings int
	Copies    int
	Decks     int
	DeckCards int
}

func SeedHoard(st *store.Store, doc []byte, file string) (SeedResult, error) {
	var out SeedResult

	h, err := hoardjson.ReadHoard(bytes.NewReader(doc))
	if err != nil {
		return out, fmt.Errorf("reading %s: %w", file, err)
	}

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
