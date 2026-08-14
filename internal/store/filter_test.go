package store

import (
	"slices"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
)

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

		{"types are ANDed", TraitFilter{Types: []string{"legendary", "creature"}}, []string{"bolas"}},
		{"artist", TraitFilter{Artists: []string{"jesper"}}, []string{"bb"}},
		{"set name", TraitFilter{SetNames: []string{"ultimate"}}, []string{"bb", "tomb"}},
		{"layout", TraitFilter{Layouts: []string{"transform"}}, []string{"bolas"}},
		{"cmc greater", TraitFilter{CMC: []NumCond{{">", 3}}}, []string{"bolas", "solitude"}},
		{"cmc range", TraitFilter{CMC: []NumCond{{">=", 1}, {"<=", 2}}}, []string{"bb", "sol"}},
		{"colour", TraitFilter{Colors: []string{"B"}}, []string{"bb", "bolas"}},

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

func TestMatchingCardIDsReadsFromTheTraitIndex(t *testing.T) {
	s := newTestStore(t)
	catalog(t, s)

	rows, err := s.db.Query(`EXPLAIN QUERY PLAN
SELECT scryfall_id FROM cards INDEXED BY cards_trait_filter
WHERE type_line IS NOT NULL AND lower(type_line) LIKE ?`, "%creature%")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, aux int
		var detail string
		if err := rows.Scan(&id, &parent, &aux, &detail); err != nil {
			t.Fatalf("scan: %v", err)
		}
		plan = append(plan, detail)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "cards_trait_filter") {
		t.Errorf("plan does not read the trait index:\n%s", joined)
	}
}

func TestColorMatchesMembersNotSubstrings(t *testing.T) {
	s := newTestStore(t)
	catalog(t, s)

	if got := matched(t, s, TraitFilter{Colors: []string{"U"}}); !slices.Equal(got, []string{"bolas"}) {
		t.Errorf("color:U = %v, want only the card that actually has U", got)
	}

	if got := matched(t, s, TraitFilter{Colors: []string{"W"}}); slices.Contains(got, "tomb") {
		t.Errorf("colourless Ancient Tomb matched color:W: %v", got)
	}
}

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

	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM cards`).Scan(&n); err != nil {
		t.Fatalf("cards table is gone: %v", err)
	}
	if n != 5 {
		t.Errorf("cards = %d, want the 5 seeded", n)
	}
}

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

func TestEnrichedCountOnAnEmptyCatalog(t *testing.T) {
	s := newTestStore(t)

	enriched, total, err := s.EnrichedCount()
	if err != nil {
		t.Fatalf("EnrichedCount on an empty catalog: %v", err)
	}
	if enriched != 0 || total != 0 {
		t.Errorf("got %d/%d, want 0/0", enriched, total)
	}
}

func TestRarityMatchesExactlyNotBySubstring(t *testing.T) {
	s := newTestStore(t)
	catalog(t, s)

	if got := matched(t, s, TraitFilter{Rarities: []string{"common"}}); len(got) != 0 {
		t.Errorf("rarity:common = %v, want nothing (the catalog has no commons)", got)
	}
	if got := matched(t, s, TraitFilter{Rarities: []string{"uncommon"}}); !slices.Equal(got, []string{"sol"}) {
		t.Errorf("rarity:uncommon = %v, want the uncommon", got)
	}

	if got := matched(t, s, TraitFilter{Rarities: []string{"MYTHIC"}}); len(got) != 3 {
		t.Errorf("rarity:MYTHIC = %v, want the three mythics", got)
	}
}
