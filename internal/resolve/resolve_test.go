package resolve

import (
	"context"
	"github.com/spiffcs/hoard/internal/finish"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
)

func fixtureFetch(calls *int, cards ...scryfall.Card) func(context.Context, []scryfall.Identifier) ([]scryfall.Card, []scryfall.Identifier, error) {
	index := make(map[string]scryfall.Card, len(cards)*3)
	for _, c := range cards {
		index[c.ID] = c
		index[strings.ToLower(c.Set)+"/"+c.CollectorNumber] = c
		index[strings.ToLower(c.Name)] = c
	}
	return func(_ context.Context, ids []scryfall.Identifier) ([]scryfall.Card, []scryfall.Identifier, error) {
		*calls++
		var found []scryfall.Card
		var notFound []scryfall.Identifier
		seen := map[string]bool{}
		for _, ident := range ids {
			c, ok := index[ident.Key()]
			if !ok {
				notFound = append(notFound, ident)
				continue
			}
			if !seen[c.ID] {
				seen[c.ID] = true
				found = append(found, c)
			}
		}
		return found, notFound, nil
	}
}

func TestResolveRetriesByNameAndCorrectsFinishes(t *testing.T) {
	sol := scryfall.Card{ID: "sol-id", Set: "c21", CollectorNumber: "125", Name: "Sol Ring",
		Finishes: []string{"nonfoil", "foil"}}
	precon := scryfall.Card{ID: "precon-id", Set: "znc", CollectorNumber: "1", Name: "Obuun",
		Finishes: []string{"foil"}}

	var calls int
	r := &Resolver{Fetch: fixtureFetch(&calls, sol, precon)}
	res, err := r.Resolve(context.Background(), []Request{

		{Ident: scryfall.Identifier{Set: "zzz", CollectorNumber: "999"}, Name: "Sol Ring", Finish: finish.Nonfoil},

		{Ident: scryfall.Identifier{ID: "precon-id"}, Name: "Obuun", Finish: finish.Nonfoil},

		{Ident: scryfall.Identifier{Name: "No Such Card"}, Name: "No Such Card", Finish: finish.Nonfoil},
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if calls != 2 {
		t.Errorf("fetch calls = %d, want 2 (bulk + name retry)", calls)
	}
	if m := res.Matches[0]; !m.OK || m.Card.ID != "sol-id" {
		t.Errorf("retried match = %+v, want Sol Ring via the name pass", m)
	}
	if m := res.Matches[1]; !m.OK || m.Finish != finish.Foil || !m.Refinished {
		t.Errorf("foil-only match = %+v, want finish corrected to foil", m)
	}
	if res.Refinished != 1 {
		t.Errorf("Refinished = %d, want 1", res.Refinished)
	}
	if len(res.Unresolved) != 1 || res.Unresolved[0] != "No Such Card" {
		t.Errorf("Unresolved = %v, want the ghost's label", res.Unresolved)
	}
	if res.Matches[2].OK {
		t.Error("the ghost resolved")
	}
}
