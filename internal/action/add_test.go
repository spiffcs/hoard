package action

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/decksource"
	"github.com/spiffcs/hoard/internal/resolve"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

func deckDeps(t *testing.T, cards ...scryfall.Card) Deps {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	index := make(map[string]scryfall.Card, len(cards)*3)
	for _, c := range cards {
		index[c.ID] = c
		index[strings.ToLower(c.Set)+"/"+c.CollectorNumber] = c
		index[strings.ToLower(c.Name)] = c
	}
	return Deps{
		Store: st,
		Resolver: &resolve.Resolver{
			Fetch: func(_ context.Context, ids []scryfall.Identifier) ([]scryfall.Card, []scryfall.Identifier, error) {
				var found []scryfall.Card
				var missing []scryfall.Identifier
				seen := make(map[string]bool)
				for _, ident := range ids {
					c, ok := index[ident.Key()]
					if !ok {
						missing = append(missing, ident)
						continue
					}
					if !seen[c.ID] {
						seen[c.ID] = true
						found = append(found, c)
					}
				}
				return found, missing, nil
			},
		},
	}
}

func solRing() scryfall.Card {
	return scryfall.Card{ID: "sol-id-1", Set: "c21", CollectorNumber: "125", Name: "Sol Ring",
		ScryfallURL: "http://sol", PriceUSD: f(2), PriceUSDFoil: f(12.5),
		Finishes: []string{"nonfoil", "foil"}}
}

func parseDeck(t *testing.T, name, body string) *decksource.Deck {
	t.Helper()
	d, err := decksource.ParseText(name, "", "", "text", strings.NewReader(body))
	if err != nil {
		t.Fatalf("ParseText: %v", err)
	}
	return d
}

func TestDeckAddUnreadableLinesArePartial(t *testing.T) {
	d := deckDeps(t, solRing())
	deck := parseDeck(t, "Mixed", "1 Sol Ring\n~~~ garbage ~~~\nalso not a card line\n")
	if len(deck.Skipped) != 2 {
		t.Fatalf("fixture parsed %d skipped lines, want 2: %v", len(deck.Skipped), deck.Skipped)
	}

	res, err := DeckAdd(context.Background(), d, nil, deck, DeckAddOptions{})
	if !errors.Is(err, ErrPartial) {
		t.Errorf("err = %v, want ErrPartial — 2 lines could not be read", err)
	}

	if res.ID == 0 || res.Resolved != 1 {
		t.Errorf("result = %+v, want the deck created with 1 card resolved", res)
	}
}

func TestDeckAddDryRunUnreadableLinesArePartial(t *testing.T) {
	d := deckDeps(t, solRing())
	deck := parseDeck(t, "Mixed", "1 Sol Ring\n~~~ garbage ~~~\nalso not a card line\n")

	res, err := DeckAdd(context.Background(), d, nil, deck, DeckAddOptions{DryRun: true})
	if !errors.Is(err, ErrPartial) {
		t.Errorf("err = %v, want ErrPartial on the dry run too", err)
	}
	if res.ID != 0 {
		t.Errorf("dry run created deck #%d, want nothing written", res.ID)
	}
	decks, err := d.Store.ListDecks()
	if err != nil {
		t.Fatalf("ListDecks: %v", err)
	}
	if len(decks) != 0 {
		t.Errorf("dry run wrote %d decks, want none", len(decks))
	}
}

func TestDeckAddCleanListIsNotPartial(t *testing.T) {
	for _, tc := range []struct {
		name   string
		dryRun bool
	}{{"real", false}, {"dry run", true}} {
		t.Run(tc.name, func(t *testing.T) {
			d := deckDeps(t, solRing())
			deck := parseDeck(t, "Clean", "1 Sol Ring\n")
			if _, err := DeckAdd(context.Background(), d, nil, deck, DeckAddOptions{DryRun: tc.dryRun}); err != nil {
				t.Errorf("err = %v, want nil — every line read and resolved", err)
			}
		})
	}
}
