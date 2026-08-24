package resolve

import (
	"context"
	"strings"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

type Request struct {
	Ident  scryfall.Identifier
	Name   string
	Finish finish.Finish
}

type Requester interface {
	Request() Request
}

func Requests[T Requester](items []T) []Request {
	out := make([]Request, len(items))
	for i, item := range items {
		out[i] = item.Request()
	}
	return out
}

type Match struct {
	OK         bool
	Card       scryfall.Card
	Finish     finish.Finish
	Refinished bool
}

type Result struct {
	Matches    []Match
	Refinished int
	Unresolved []string
	Found      []scryfall.Card
}

type Resolver struct {
	Fetch func(context.Context, []scryfall.Identifier) ([]scryfall.Card, []scryfall.Identifier, error)
}

func (r *Resolver) fetch(ctx context.Context, ids []scryfall.Identifier) ([]scryfall.Card, []scryfall.Identifier, error) {
	if r.Fetch != nil {
		return r.Fetch(ctx, ids)
	}
	return scryfall.FetchCollection(ctx, ids)
}

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
		if _, ok := byKey[q.Ident.Key()]; ok || q.Name == "" {
			continue
		}

		if q.Ident.Name != "" && q.Ident.Set == "" {
			continue
		}
		ident := scryfall.Identifier{Name: frontFace(q.Name)}
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

		finish, changed := store.CorrectFinish(q.Finish, scryfall.Finishes(card))
		if changed {
			res.Refinished++
		}
		res.Matches[i] = Match{OK: true, Card: card, Finish: finish, Refinished: changed}
	}
	return res, nil
}

func frontFace(name string) string {
	if front, _, ok := strings.Cut(name, " // "); ok {
		return strings.TrimSpace(front)
	}
	return name
}

func indexIDs(cards []scryfall.Card) map[string]string {
	m := make(map[string]string, len(cards)*3)
	for _, c := range cards {
		m[c.ID] = c.ID
		m[strings.ToLower(c.Set)+"/"+c.CollectorNumber] = c.ID
		m[strings.ToLower(c.Name)] = c.ID
	}
	return m
}
