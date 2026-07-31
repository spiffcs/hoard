// Package resolve turns import lines into catalog cards: the bulk Scryfall
// lookup, the multi-key matching, the retry-by-name second chance, and the
// finish correction, as one pipeline.
//
// Deck add and collection import used to each carry a copy of this, and the
// copies had drifted: import had grown the name retry and a test seam, deck
// add had neither. A missing card or a wrong finish costs the same whichever
// command hit it, so the pipeline is one decision, made here.
package resolve

import (
	"context"
	"strings"

	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

// Request is one line to resolve: its identifier, the name that rode along
// beside it (fuel for the retry pass, and the label a failure is reported
// under), and the finish the line claimed.
type Request struct {
	Ident  scryfall.Identifier
	Name   string
	Finish string
}

// Match is one request's answer. When OK, Card is the printing and Finish the
// finish to store — corrected away from the request's claim when the printing
// does not come in it, which Refinished reports.
type Match struct {
	OK         bool
	Card       scryfall.Card
	Finish     string
	Refinished bool
}

// Result is a whole batch's answers: Matches parallel to the requests, the
// counts and labels every caller reports, and the full card set for callers
// that upsert the catalog wholesale.
type Result struct {
	Matches    []Match
	Refinished int      // matches whose finish had to be corrected
	Unresolved []string // display labels of the requests nothing answered
	Found      []scryfall.Card
}

// Resolver resolves requests through Fetch — scryfall.FetchCollection when
// nil, a fixture-backed stand-in under test.
type Resolver struct {
	Fetch func(context.Context, []scryfall.Identifier) ([]scryfall.Card, []scryfall.Identifier, error)
}

func (r *Resolver) fetch(ctx context.Context, ids []scryfall.Identifier) ([]scryfall.Card, []scryfall.Identifier, error) {
	if r.Fetch != nil {
		return r.Fetch(ctx, ids)
	}
	return scryfall.FetchCollection(ctx, ids)
}

// Resolve answers every request in two bulk passes: the identifiers as given,
// then — for set+number pairs Scryfall does not know (a vendor's set code, a
// renumbered promo) that carry a name — a retry by that name. The retry's
// printing is whichever Scryfall picks, which beats losing the card entirely.
func (r *Resolver) Resolve(ctx context.Context, reqs []Request) (*Result, error) {
	idents := make([]scryfall.Identifier, len(reqs))
	for i, q := range reqs {
		idents[i] = q.Ident
	}
	found, _, err := r.fetch(ctx, idents)
	if err != nil {
		return nil, err
	}
	byKey := indexIDs(found)
	cards := make(map[string]scryfall.Card, len(found))
	for _, c := range found {
		cards[c.ID] = c
	}

	var retry []scryfall.Identifier
	queued := make(map[string]bool)
	for _, q := range reqs {
		if _, ok := byKey[q.Ident.Key()]; ok || q.Ident.Name != "" || q.Name == "" {
			continue
		}
		ident := scryfall.Identifier{Name: q.Name}
		if !queued[ident.Key()] {
			queued[ident.Key()] = true
			retry = append(retry, ident)
		}
	}
	if len(retry) > 0 {
		more, _, err := r.fetch(ctx, retry)
		if err != nil {
			return nil, err
		}
		found = append(found, more...)
		for _, c := range more {
			cards[c.ID] = c
		}
		for k, id := range indexIDs(more) {
			if _, ok := byKey[k]; !ok {
				byKey[k] = id
			}
		}
	}

	res := &Result{Matches: make([]Match, len(reqs)), Found: found}
	for i, q := range reqs {
		id, ok := byKey[q.Ident.Key()]
		if !ok && q.Name != "" {
			id, ok = byKey[strings.ToLower(q.Name)]
		}
		if !ok {
			label := q.Ident.Label()
			if q.Name != "" {
				label = q.Name
			}
			res.Unresolved = append(res.Unresolved, label)
			continue
		}
		card := cards[id]
		// A line claiming a finish the printing does not come in would store
		// an entry no price can ever attach to.
		finish, changed := store.CorrectFinish(q.Finish, card.Finishes)
		if changed {
			res.Refinished++
		}
		res.Matches[i] = Match{OK: true, Card: card, Finish: finish, Refinished: changed}
	}
	return res, nil
}

// indexIDs indexes the cards the bulk lookup returned under every key form an
// Identifier might use, so a line finds its card by whichever scheme
// addressed it. The scheme itself lives on scryfall.Identifier.Key.
func indexIDs(cards []scryfall.Card) map[string]string {
	m := make(map[string]string, len(cards)*3)
	for _, c := range cards {
		m[c.ID] = c.ID
		m[strings.ToLower(c.Set)+"/"+c.CollectorNumber] = c.ID
		m[strings.ToLower(c.Name)] = c.ID
	}
	return m
}
