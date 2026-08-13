package resolve

import (
	"context"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// scryfallLikeFetch models the two /cards/collection behaviours the retry pass
// exists to survive, rather than the permissive lookup fixtureFetch offers.
//
// Both are measured against the live API, not assumed:
//
//   - a name narrowed by a set matches only if the card really has that set.
//     Archidekt writes "(mb1)" for cards Scryfall does not list under mb1, and
//     the bare name finds them.
//   - a split card is matched by its FRONT FACE, never by the printed
//     "A // B" name. {"name":"Fire // Ice"} is not found; {"name":"Wear"}
//     returns the card, whose Name is then the full "Wear // Tear".
func scryfallLikeFetch(calls *int, cards ...scryfall.Card) func(context.Context, []scryfall.Identifier) ([]scryfall.Card, []scryfall.Identifier, error) {
	return func(_ context.Context, ids []scryfall.Identifier) ([]scryfall.Card, []scryfall.Identifier, error) {
		*calls++
		var found []scryfall.Card
		var notFound []scryfall.Identifier
		seen := map[string]bool{}
		for _, ident := range ids {
			var hit *scryfall.Card
			for i := range cards {
				c := &cards[i]
				switch {
				case ident.ID != "" && ident.ID == c.ID:
				case ident.Set != "" && ident.CollectorNumber != "":
					if !strings.EqualFold(ident.Set, c.Set) || ident.CollectorNumber != c.CollectorNumber {
						continue
					}
				case ident.Name != "":
					front, _, _ := strings.Cut(c.Name, " // ")
					if !strings.EqualFold(ident.Name, front) {
						continue
					}
					if ident.Set != "" && !strings.EqualFold(ident.Set, c.Set) {
						continue
					}
				default:
					continue
				}
				hit = c
				break
			}
			if hit == nil {
				notFound = append(notFound, ident)
				continue
			}
			if !seen[hit.ID] {
				seen[hit.ID] = true
				found = append(found, *hit)
			}
		}
		return found, notFound, nil
	}
}

// A name carrying a set that the card does not have must fall back to the bare
// name. Before this, the retry pass skipped every identifier that already had a
// name, so a name+set miss was simply unresolved — which is what an Archidekt
// text export produces for its whole maybeboard.
func TestResolveRetriesWhenTheSetIsWrong(t *testing.T) {
	bronto := scryfall.Card{ID: "bronto-id", Set: "xln", CollectorNumber: "175",
		Name: "Ancient Brontodon", Finishes: []string{"nonfoil"}}
	var calls int
	r := &Resolver{Fetch: scryfallLikeFetch(&calls, bronto)}

	res, err := r.Resolve(context.Background(), []Request{{
		Ident: scryfall.Identifier{Name: "Ancient Brontodon", Set: "mb1"},
		Name:  "Ancient Brontodon",
	}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Unresolved) != 0 {
		t.Fatalf("unresolved %v, want the bare-name retry to have found it", res.Unresolved)
	}
	if calls != 2 {
		t.Errorf("fetch called %d times, want 2 (bulk pass + name retry)", calls)
	}
	if got := res.Matches[0].Card.ID; got != "bronto-id" {
		t.Errorf("resolved to %q, want bronto-id", got)
	}
}

// A split card is written "A // B" by every decklist site and matched by
// neither half-joined name at Scryfall. The retry sends the front face.
func TestResolveRetriesSplitCardsByFrontFace(t *testing.T) {
	wear := scryfall.Card{ID: "wear-id", Set: "dgm", CollectorNumber: "135",
		Name: "Wear // Tear", Finishes: []string{"nonfoil"}}
	var calls int
	r := &Resolver{Fetch: scryfallLikeFetch(&calls, wear)}

	res, err := r.Resolve(context.Background(), []Request{{
		Ident: scryfall.Identifier{Name: "Wear // Tear", Set: "dgm"},
		Name:  "Wear // Tear",
	}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Unresolved) != 0 {
		t.Fatalf("unresolved %v, want the front-face retry to have found it", res.Unresolved)
	}
	if got := res.Matches[0].Card.ID; got != "wear-id" {
		t.Errorf("resolved to %q, want wear-id", got)
	}
}

// The retry must not fire for an identifier that is a bare name already: the
// request would be byte-identical to the one that just failed, so a second
// round trip buys nothing. This is the guard the wrong-set case had to be
// carved out of, so it is asserted rather than assumed.
func TestResolveDoesNotRetryABareName(t *testing.T) {
	var calls int
	r := &Resolver{Fetch: scryfallLikeFetch(&calls)}

	res, err := r.Resolve(context.Background(), []Request{{
		Ident: scryfall.Identifier{Name: "No Such Card"},
		Name:  "No Such Card",
	}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(res.Unresolved) != 1 {
		t.Fatalf("unresolved = %v, want the one card", res.Unresolved)
	}
	if calls != 1 {
		t.Errorf("fetch called %d times, want 1 — a bare name has nothing to drop", calls)
	}
}

func TestFrontFace(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Wear // Tear", "Wear"},
		{"Branchloft Pathway // Boulderloft Pathway", "Branchloft Pathway"},
		{"Sol Ring", "Sol Ring"},
		// Not a split marker: no spaces around it. Nothing to cut.
		{"Borrowing 100,000 Arrows", "Borrowing 100,000 Arrows"},
	} {
		if got := frontFace(tc.in); got != tc.want {
			t.Errorf("frontFace(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
