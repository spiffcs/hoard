package store

import (
	"slices"
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// catalog seeds a few printings with real Scryfall-shaped documents so the
// generated columns have something to resolve from.
func catalog(t *testing.T, s *Store) {
	t.Helper()
	docs := map[string]string{
		"bb": `{"name":"Bitterblossom","rarity":"mythic","set_name":"Ultimate Masters",
		       "type_line":"Kindred Enchantment — Faerie","cmc":2.0,"layout":"nonfoil",
		       "artist":"Jesper Ejsing","color_identity":["B"]}`,
		"tomb": `{"name":"Ancient Tomb","rarity":"rare","set_name":"Ultimate Masters",
		         "type_line":"Land","cmc":0.0,"layout":"nonfoil",
		         "artist":"Sam Burley","color_identity":[]}`,
		"sol": `{"name":"Sol Ring","rarity":"uncommon","set_name":"Commander 2021",
		        "type_line":"Artifact","cmc":1.0,"layout":"nonfoil",
		        "artist":"Mike Bierek","color_identity":[]}`,
		"solitude": `{"name":"Solitude","rarity":"mythic","set_name":"Modern Horizons 2",
		             "type_line":"Creature — Elemental Incarnation","cmc":5.0,"layout":"nonfoil",
		             "artist":"Igor Kieryluk","color_identity":["W"]}`,
		"bolas": `{"name":"Nicol Bolas","rarity":"mythic","set_name":"Core Set 2019",
		          "type_line":"Legendary Creature — Elder Dragon","cmc":4.0,"layout":"transform",
		          "artist":"Svetlin Velinov","color_identity":["B","R","U"]}`,
	}
	var cards []scryfall.Card
	for id, doc := range docs {
		cards = append(cards, scryfall.Card{
			ID: id, Set: "x", CollectorNumber: "1", Name: id,
			ScryfallURL: "http://x", Raw: []byte(doc),
		})
	}
	if err := s.UpsertPrintings(cards); err != nil {
		t.Fatalf("seeding catalog: %v", err)
	}
}

func matched(t *testing.T, s *Store, f TraitFilter) []string {
	t.Helper()
	ids, err := s.MatchingCardIDs(f)
	if err != nil {
		t.Fatalf("MatchingCardIDs: %v", err)
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

func TestMatchingCardIDs(t *testing.T) {
	s := newTestStore(t)
	catalog(t, s)

	tests := []struct {
		name   string
		filter TraitFilter
		want   []string
	}{
		{"rarity", TraitFilter{Rarities: []string{"mythic"}}, []string{"bb", "bolas", "solitude"}},
		{"type substring", TraitFilter{Types: []string{"creature"}}, []string{"bolas", "solitude"}},
		// Two type terms are ANDed, which is what typing two words means.
		{"types are ANDed", TraitFilter{Types: []string{"legendary", "creature"}}, []string{"bolas"}},
		{"artist", TraitFilter{Artists: []string{"jesper"}}, []string{"bb"}},
		{"set name", TraitFilter{SetNames: []string{"ultimate"}}, []string{"bb", "tomb"}},
		{"layout", TraitFilter{Layouts: []string{"transform"}}, []string{"bolas"}},
		{"cmc greater", TraitFilter{CMC: []NumCond{{">", 3}}}, []string{"bolas", "solitude"}},
		{"cmc range", TraitFilter{CMC: []NumCond{{">=", 1}, {"<=", 2}}}, []string{"bb", "sol"}},
		{"colour", TraitFilter{Colors: []string{"B"}}, []string{"bb", "bolas"}},
		// Colours are ANDed too: a two-colour term wants both, not either.
		{"colours ANDed", TraitFilter{Colors: []string{"B", "R"}}, []string{"bolas"}},
		{"colour and rarity", TraitFilter{Colors: []string{"W"}, Rarities: []string{"mythic"}}, []string{"solitude"}},
		{"nothing matches", TraitFilter{Rarities: []string{"common"}}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matched(t, s, tt.filter); !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// color:U must not match a UB card by way of its "B" entry, which is what a
// substring match on the stored JSON array would do.
func TestColorMatchesMembersNotSubstrings(t *testing.T) {
	s := newTestStore(t)
	catalog(t, s)

	// Nicol Bolas is ["B","R","U"]; Bitterblossom is ["B"].
	if got := matched(t, s, TraitFilter{Colors: []string{"U"}}); !slices.Equal(got, []string{"bolas"}) {
		t.Errorf("color:U = %v, want only the card that actually has U", got)
	}
	// And the empty array is not "every colour".
	if got := matched(t, s, TraitFilter{Colors: []string{"W"}}); slices.Contains(got, "tomb") {
		t.Errorf("colourless Ancient Tomb matched color:W: %v", got)
	}
}

// A trait query on rows with no stored document must return nothing rather than
// an arbitrary subset: NULL fails LIKE silently, and a filter that quietly drops
// half the collection is worse than one that visibly finds none of it.
func TestMatchingCardIDsSkipsUnenrichedRows(t *testing.T) {
	s := newTestStore(t)
	catalog(t, s)
	if err := s.UpsertPrintings([]scryfall.Card{{
		ID: "bare", Set: "x", CollectorNumber: "2", Name: "No Document", ScryfallURL: "http://x",
	}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	for _, f := range []TraitFilter{
		{Rarities: []string{"mythic"}},
		{Types: []string{"creature"}},
		{CMC: []NumCond{{">=", 0}}},
	} {
		if got := matched(t, s, f); slices.Contains(got, "bare") {
			t.Errorf("filter %+v matched the row with no document: %v", f, got)
		}
	}
}

// An empty filter means "no opinion" and must not be turned into a query with
// an empty WHERE clause.
func TestMatchingCardIDsEmptyFilter(t *testing.T) {
	s := newTestStore(t)
	catalog(t, s)
	ids, err := s.MatchingCardIDs(TraitFilter{})
	if err != nil {
		t.Fatalf("MatchingCardIDs: %v", err)
	}
	if ids != nil {
		t.Errorf("got %v, want nil for an empty filter", ids)
	}
}

// Values are bound, never interpolated. If they were assembled into the SQL
// this would drop the table rather than politely matching nothing.
func TestMatchingCardIDsBindsHostileValues(t *testing.T) {
	s := newTestStore(t)
	catalog(t, s)

	for _, hostile := range []string{
		`'; DROP TABLE cards--`,
		`%' OR '1'='1`,
		`") OR 1=1--`,
	} {
		got := matched(t, s, TraitFilter{Artists: []string{hostile}})
		if len(got) != 0 {
			t.Errorf("hostile artist %q matched %v", hostile, got)
		}
	}

	// The table is still there and still populated.
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM cards`).Scan(&n); err != nil {
		t.Fatalf("cards table is gone: %v", err)
	}
	if n != 5 {
		t.Errorf("cards = %d, want the 5 seeded", n)
	}
}

// The comparison operator is the one fragment that cannot be bound, so it is
// checked against a allow-list instead of trusted.
func TestMatchingCardIDsRejectsAnInvalidOperator(t *testing.T) {
	s := newTestStore(t)
	catalog(t, s)
	_, err := s.MatchingCardIDs(TraitFilter{CMC: []NumCond{{Op: "; DROP TABLE cards--", Value: 1}}})
	if err == nil {
		t.Fatal("an arbitrary operator was accepted")
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM cards`).Scan(&n); err != nil || n != 5 {
		t.Errorf("cards table damaged: %d, %v", n, err)
	}
}

func TestEnrichedCount(t *testing.T) {
	s := newTestStore(t)
	catalog(t, s)
	if err := s.UpsertPrintings([]scryfall.Card{{
		ID: "bare", Set: "x", CollectorNumber: "2", Name: "No Document", ScryfallURL: "http://x",
	}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	enriched, total, err := s.EnrichedCount()
	if err != nil {
		t.Fatalf("EnrichedCount: %v", err)
	}
	if enriched != 5 || total != 6 {
		t.Errorf("got %d/%d, want 5/6", enriched, total)
	}
}

// "common" is a substring of "uncommon", so a substring match on rarity returns
// the exact opposite of what was asked for. Rarity is a closed set and is
// matched exactly.
func TestRarityMatchesExactlyNotBySubstring(t *testing.T) {
	s := newTestStore(t)
	catalog(t, s)

	if got := matched(t, s, TraitFilter{Rarities: []string{"common"}}); len(got) != 0 {
		t.Errorf("rarity:common = %v, want nothing (the catalog has no commons)", got)
	}
	if got := matched(t, s, TraitFilter{Rarities: []string{"uncommon"}}); !slices.Equal(got, []string{"sol"}) {
		t.Errorf("rarity:uncommon = %v, want the uncommon", got)
	}
	// Case still does not matter.
	if got := matched(t, s, TraitFilter{Rarities: []string{"MYTHIC"}}); len(got) != 3 {
		t.Errorf("rarity:MYTHIC = %v, want the three mythics", got)
	}
}
