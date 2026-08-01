package catalog

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/cardname"
)

// stocked builds a catalog holding a handful of real card names, including the
// ones the scanner's own tests use as traps.
func stocked(t *testing.T) *Catalog {
	t.Helper()
	serveBundle(t, "2026-07-30T00:00:00Z", []string{
		card("opt", "Opt", "eld", "59", "0.25"),
		card("fog", "Fog", "m21", "180", "0.20"),
		card("sol1", "Sol Ring", "c21", "263", "2.00"),
		card("sol2", "Sol Ring", "mps", "1", "120.00"),
		card("els", "Elspeth, Knight-Errant", "mm2", "12", "6.00"),
		card("bit", "Bitterblossom", "uma", "85", "34.11"),
		card("sto", "Stoneforge Mystic", "2xm", "31", "31.34"),
		card("sli", "Slime Against Humanity", "mh3", "112", "1.50"),
		card("tar", "Tarmogoyf", "mm3", "144", "18.00"),
	})
	c := openTemp(t)
	if err := c.Update(context.Background(), nil); err != nil {
		t.Fatalf("Update: %v", err)
	}
	return c
}

func TestAutocompletePrefersPrefixes(t *testing.T) {
	c := stocked(t)
	got, err := c.Autocomplete(context.Background(), "sol")
	if err != nil {
		t.Fatalf("Autocomplete: %v", err)
	}
	if len(got) == 0 || got[0] != "Sol Ring" {
		t.Errorf("got %v, want Sol Ring first", got)
	}

	// Punctuation in the query is not a difference, so a name typed without its
	// comma still completes.
	got, _ = c.Autocomplete(context.Background(), "elspeth knight")
	if !slices.Contains(got, "Elspeth, Knight-Errant") {
		t.Errorf("got %v, want the comma-less query to complete", got)
	}

	// A substring match is offered, but after prefixes.
	got, _ = c.Autocomplete(context.Background(), "forge")
	if !slices.Contains(got, "Stoneforge Mystic") {
		t.Errorf("got %v, want a mid-name match", got)
	}

	if got, _ := c.Autocomplete(context.Background(), ""); got != nil {
		t.Errorf("empty query returned %v", got)
	}
}

// A name is one row in `names` however many printings carry it, so the picker
// must not show it repeatedly.
func TestAutocompleteDoesNotRepeatNames(t *testing.T) {
	c := stocked(t)
	got, _ := c.Autocomplete(context.Background(), "sol ring")
	var n int
	for _, name := range got {
		if name == "Sol Ring" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("Sol Ring appeared %d times in %v, want once", n, got)
	}
}

func TestSearchPrintsReturnsEveryPrintingNewestFirst(t *testing.T) {
	c := stocked(t)
	got, err := c.SearchPrints(context.Background(), "Sol Ring")
	if err != nil {
		t.Fatalf("SearchPrints: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d printings, want both", len(got))
	}
	// Prices and finishes come through, since this is what the add cascade shows.
	for _, card := range got {
		if card.PriceUSD == nil {
			t.Errorf("%s/%s has no price", card.Set, card.CollectorNumber)
		}
		if len(card.Finishes) == 0 {
			t.Errorf("%s/%s has no finishes; the finish picker would be empty",
				card.Set, card.CollectorNumber)
		}
	}
	if got, _ := c.SearchPrints(context.Background(), "Nonexistent Card"); got != nil {
		t.Errorf("unknown name returned %v, want nothing", got)
	}
}

// The whole point of the local matcher: it is an identity check, not a search.
// These are the cases that made internal/tui distrust Scryfall's fuzzy endpoint.
func TestNamedFuzzy(t *testing.T) {
	c := stocked(t)
	cases := []struct {
		text string
		want string // "" means no match
		why  string
	}{
		{"Sol Ring", "Sol Ring", "clean read"},
		{"Sol Rlng", "Sol Ring", "one-character OCR slip"},
		{"sol ring", "Sol Ring", "case is not a difference"},
		{"Elspeth Knight Errant", "Elspeth, Knight-Errant", "punctuation dropped"},
		{"Elspeth", "Elspeth, Knight-Errant", "partial read of a longer name"},
		{"Bitterblossom", "Bitterblossom", "clean read of a long name"},
		{"Bitterbiossom", "Bitterblossom", "l/i confusion, the classic OCR slip"},
		{"Opt", "Opt", "exact read of a short name"},
		{"opt.", "Opt", "trailing punctuation"},

		// The bug this exists to prevent. Scryfall's endpoint returns "Opt" for
		// all three because the text contains the name.
		{"option", "", "containing a short name is not being it"},
		{"options", "", "same, pluralized"},
		{"adopt", "", "short name embedded mid-word"},

		{"control have indestructible", "", "rules text is not a card"},
		{"Volkan Baga", "", "an artist line is not a card"},
		{"", "", "nothing read"},
	}
	for _, tc := range cases {
		got, _, err := c.NamedFuzzy(context.Background(), tc.text)
		if err != nil {
			t.Fatalf("NamedFuzzy(%q): %v", tc.text, err)
		}
		name := ""
		if got != nil {
			name = got.Name
		}
		if name != tc.want {
			t.Errorf("NamedFuzzy(%q) = %q, want %q — %s", tc.text, name, tc.want, tc.why)
		}
	}
}

// The match report is what the auto-commit decision keys on: an exact
// normalized hit must say so, and a fuzzy hit must carry the score bestNameFor
// ranked it by rather than discarding it.
func TestNamedFuzzyReportsExactAndSimilarity(t *testing.T) {
	c := stocked(t)

	_, match, err := c.NamedFuzzy(context.Background(), "Sol Ring")
	if err != nil {
		t.Fatalf("NamedFuzzy: %v", err)
	}
	if !match.Exact || match.Similarity != 1 {
		t.Errorf("clean read match = %+v, want exact with similarity 1", match)
	}

	// Punctuation and case vanish in normalization, so this is still exact.
	_, match, err = c.NamedFuzzy(context.Background(), "elspeth knight errant")
	if err != nil {
		t.Fatalf("NamedFuzzy: %v", err)
	}
	if !match.Exact {
		t.Errorf("normalized-equal read match = %+v, want exact", match)
	}

	got, match, err := c.NamedFuzzy(context.Background(), "Sol Rlng")
	if err != nil || got == nil {
		t.Fatalf("NamedFuzzy: %v, %v", got, err)
	}
	if match.Exact {
		t.Error("an OCR slip must not report an exact match")
	}
	want := cardname.Similarity(cardname.Normalize("Sol Rlng"), cardname.Normalize("Sol Ring"))
	if match.Similarity != want {
		t.Errorf("similarity = %v, want the real score %v", match.Similarity, want)
	}
}

// The representative printing has to be a real, complete card: the add cascade
// takes its name onward to SearchPrints and shows its price.
func TestNamedFuzzyReturnsAUsablePrinting(t *testing.T) {
	c := stocked(t)
	got, _, err := c.NamedFuzzy(context.Background(), "Sol Rlng")
	if err != nil || got == nil {
		t.Fatalf("NamedFuzzy: %v, %v", got, err)
	}
	if got.ID == "" || got.Set == "" || got.CollectorNumber == "" || got.ScryfallURL == "" {
		t.Errorf("incomplete card: %+v", got)
	}
	if got.PriceUSD == nil {
		t.Error("no price on the returned printing")
	}
}

// A name nothing in the catalog resembles must return nothing rather than the
// least-bad row: the caller reads nil as "ask the user" and a wrong card as
// truth.
func TestNamedFuzzyRefusesUnrelatedText(t *testing.T) {
	c := stocked(t)
	for _, text := range []string{
		"Wizards of the Coast",
		"Illustrated by Rebecca Guay",
		"zzzzqqqqxxxx",
		"3/4",
	} {
		got, _, err := c.NamedFuzzy(context.Background(), text)
		if err != nil {
			t.Fatalf("NamedFuzzy(%q): %v", text, err)
		}
		if got != nil {
			t.Errorf("NamedFuzzy(%q) = %q, want no match", text, got.Name)
		}
	}
}

// An empty catalog answers nothing rather than erroring, so a caller can layer
// it under the API without special-casing the un-built state.
func TestSearchOnAnEmptyCatalog(t *testing.T) {
	c := openTemp(t)
	ctx := context.Background()
	if got, err := c.Autocomplete(ctx, "sol"); err != nil || len(got) != 0 {
		t.Errorf("Autocomplete = %v, %v", got, err)
	}
	if got, err := c.SearchPrints(ctx, "Sol Ring"); err != nil || len(got) != 0 {
		t.Errorf("SearchPrints = %v, %v", got, err)
	}
	if got, _, err := c.NamedFuzzy(ctx, "Sol Ring"); err != nil || got != nil {
		t.Errorf("NamedFuzzy = %v, %v", got, err)
	}
}

// A card name containing a LIKE wildcard must be matched literally.
func TestAutocompleteEscapesWildcards(t *testing.T) {
	if got := escapeLike("100%_pure"); !strings.Contains(got, `\%`) || !strings.Contains(got, `\_`) {
		t.Errorf("escapeLike = %q, want the wildcards escaped", got)
	}
	c := stocked(t)
	// A bare wildcard must not match everything.
	got, err := c.Autocomplete(context.Background(), "%")
	if err != nil {
		t.Fatalf("Autocomplete: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a wildcard query matched %v", got)
	}
}
