package catalog

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/spiffcs/hoard/internal/cardname"
	"github.com/spiffcs/hoard/internal/scryfall"
)

// autocompleteLimit matches what Scryfall's endpoint returns, so the picker is
// the same length whichever answered.
const autocompleteLimit = 20

// Autocomplete returns candidate card names for a partial query.
//
// Prefix matches first, then names containing the query elsewhere, because what
// somebody is typing is nearly always the start of a name and burying those
// under an alphabetical middle-of-the-name match makes the list feel wrong.
func (c *Catalog) Autocomplete(_ context.Context, q string) ([]string, error) {
	norm := cardname.Normalize(q)
	if norm == "" {
		return nil, nil
	}
	rows, err := c.db.Query(`
SELECT name FROM names
WHERE name_norm LIKE ? ESCAPE '\'
ORDER BY CASE WHEN name_norm LIKE ? ESCAPE '\' THEN 0 ELSE 1 END,
         length(name_norm), name
LIMIT ?`, "%"+escapeLike(norm)+"%", escapeLike(norm)+"%", autocompleteLimit)
	if err != nil {
		return nil, fmt.Errorf("catalog: autocompleting %q: %w", q, err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// SearchPrints returns every paper printing of a card, newest first.
//
// Matched on the normalized name rather than the literal one, so a name typed
// without its comma or apostrophe still finds its printings — the API's exact
// match would not, but the picker this feeds is reached from Autocomplete, which
// is equally forgiving.
func (c *Catalog) SearchPrints(_ context.Context, exactName string) ([]scryfall.Card, error) {
	norm := cardname.Normalize(exactName)
	if norm == "" {
		return nil, nil
	}
	rows, err := c.db.Query(`
SELECT `+cardColumns+`
FROM cards WHERE name_norm = ?
ORDER BY released_at DESC, set_code, collector_number`, norm)
	if err != nil {
		return nil, fmt.Errorf("catalog: searching prints of %q: %w", exactName, err)
	}
	defer rows.Close()

	var out []scryfall.Card
	for rows.Next() {
		card, err := scanCard(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, card)
	}
	return out, rows.Err()
}

// fuzzyCandidates is how many trigram-ranked names are scored properly.
//
// Trigram overlap is a cheap filter, not an answer — it favours long names
// simply for having more grams. Fifty is comfortably more than enough for the
// right name to survive to the edit-distance pass that actually decides.
const fuzzyCandidates = 50

// NamedFuzzy resolves possibly-imperfect text to a single card.
//
// Unlike Scryfall's endpoint, this is an identity check rather than a search.
// That distinction is the whole reason internal/tui carries plausibleMatch: the
// API resolves "option" to the card "Opt" because the query contains the name,
// which is right for a human typing a partial name and wrong for OCR, where any
// stray word in frame becomes a card. Here the same rule that used to reject the
// API's answer decides the answer, so the check cannot be skipped by accident.
//
// Returns (nil, nil) when nothing is a believable reading, matching the API
// client so callers fall back to manual entry the same way.
func (c *Catalog) NamedFuzzy(ctx context.Context, text string) (*scryfall.Card, error) {
	norm := cardname.Normalize(text)
	if norm == "" {
		return nil, nil
	}

	// An exact normalized hit needs no ranking and no candidate generation.
	var name string
	err := c.db.QueryRow(`SELECT name FROM names WHERE name_norm = ?`, norm).Scan(&name)
	if err == nil {
		return c.newestPrinting(ctx, name)
	}

	best, err := c.bestNameFor(norm)
	if err != nil || best == "" {
		return nil, err
	}
	if !cardname.Plausible(text, best) {
		return nil, nil
	}
	return c.newestPrinting(ctx, best)
}

// bestNameFor ranks trigram candidates by edit distance and returns the closest.
func (c *Catalog) bestNameFor(norm string) (string, error) {
	tris := cardname.Trigrams(norm)
	if len(tris) == 0 {
		return "", nil
	}
	args := make([]any, len(tris))
	for i, t := range tris {
		args[i] = t
	}
	args = append(args, fuzzyCandidates)

	rows, err := c.db.Query(`
SELECT n.name, COUNT(*) AS hits
FROM name_trigrams t JOIN names n ON n.name_norm = t.name_norm
WHERE t.tri IN (?`+strings.Repeat(",?", len(tris)-1)+`)
GROUP BY t.name_norm
ORDER BY hits DESC
LIMIT ?`, args...)
	if err != nil {
		return "", fmt.Errorf("catalog: finding candidates: %w", err)
	}
	defer rows.Close()

	type scored struct {
		name  string
		score float64
	}
	var cands []scored
	for rows.Next() {
		var name string
		var hits int
		if err := rows.Scan(&name, &hits); err != nil {
			return "", err
		}
		cands = append(cands, scored{name, cardname.Similarity(norm, cardname.Normalize(name))})
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(cands) == 0 {
		return "", nil
	}

	// Ties broken by the shorter name: with equal edit distance the shorter one
	// explains more of what was read, which is the same instinct Plausible
	// encodes when it insists a match account for the text.
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return len(cands[i].name) < len(cands[j].name)
	})
	return cands[0].name, nil
}

// newestPrinting is the most recent paper printing of a name, which is what the
// API's fuzzy endpoint also returns: one representative card, from which the
// caller's next step lists every printing.
func (c *Catalog) newestPrinting(ctx context.Context, name string) (*scryfall.Card, error) {
	prints, err := c.SearchPrints(ctx, name)
	if err != nil || len(prints) == 0 {
		return nil, err
	}
	return &prints[0], nil
}

// escapeLike neutralises the wildcards in a LIKE pattern, so a card name
// containing % or _ is matched literally rather than as a pattern.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
