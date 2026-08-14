package catalog

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/spiffcs/hoard/internal/cardname"
	"github.com/spiffcs/hoard/internal/scryfall"
)

const autocompleteLimit = 20

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

func (c *Catalog) PrintBySetNumber(ctx context.Context, set, number string) (*scryfall.Card, error) {
	return c.PrintBySetNumberLang(ctx, set, number, "")
}

func (c *Catalog) PrintBySetNumberLang(_ context.Context, set, number, lang string) (*scryfall.Card, error) {
	set, number, lang = strings.TrimSpace(set), strings.TrimSpace(number), strings.TrimSpace(lang)
	if set == "" || number == "" {
		return nil, nil
	}

	found, err := c.printsMatching(4,
		`set_code = ? COLLATE NOCASE
		   AND (collector_number = ? COLLATE NOCASE
		        OR (? <> ''
		            AND lang = ? COLLATE NOCASE
		            AND rtrim(collector_number, ?) = ? COLLATE NOCASE))`,
		set, number, lang, lang, scryfall.VariationMarkers, number)
	if err != nil {
		return nil, fmt.Errorf("catalog: looking up %s %s: %w", set, number, err)
	}
	if len(found) == 1 {
		return &found[0], nil
	}

	if lang != "" {
		var agreed []scryfall.Card
		for _, card := range found {
			if strings.EqualFold(card.Lang, lang) {
				agreed = append(agreed, card)
			}
		}
		if len(agreed) == 1 {
			return &agreed[0], nil
		}
	}
	return nil, nil
}

func (c *Catalog) printsMatching(limit int, where string, args ...any) ([]scryfall.Card, error) {
	rows, err := c.db.Query(`
SELECT `+cardColumns+`
FROM cards WHERE `+where+`
ORDER BY released_at DESC
LIMIT `+strconv.Itoa(limit), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var found []scryfall.Card
	for rows.Next() {
		card, err := scanCard(rows)
		if err != nil {
			return nil, err
		}
		found = append(found, card)
	}
	return found, rows.Err()
}

const fuzzyCandidates = 50

func (c *Catalog) NamedFuzzy(ctx context.Context, text string) (*scryfall.Card, cardname.Match, error) {
	norm := cardname.Normalize(text)
	if norm == "" {
		return nil, cardname.Match{}, nil
	}

	var name string
	err := c.db.QueryRow(`SELECT name FROM names WHERE name_norm = ?`, norm).Scan(&name)
	if err == nil {
		card, err := c.newestPrinting(ctx, name)
		return card, cardname.Match{Exact: true, Similarity: 1}, err
	}

	best, score, err := c.bestNameFor(norm)
	if err != nil || best == "" {
		return nil, cardname.Match{}, err
	}
	if !cardname.Plausible(text, best) {

		if cardname.PrefixCandidate(text, best) {
			card, err := c.newestPrinting(ctx, best)
			return card, cardname.Match{Similarity: score, PrefixOnly: true}, err
		}
		return nil, cardname.Match{}, nil
	}
	card, err := c.newestPrinting(ctx, best)
	return card, cardname.Match{Similarity: score}, err
}

func (c *Catalog) bestNameFor(norm string) (string, float64, error) {
	tris := cardname.Trigrams(norm)
	if len(tris) == 0 {
		return "", 0, nil
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
		return "", 0, fmt.Errorf("catalog: finding candidates: %w", err)
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
			return "", 0, err
		}
		cands = append(cands, scored{name, cardname.Similarity(norm, cardname.Normalize(name))})
	}
	if err := rows.Err(); err != nil {
		return "", 0, err
	}
	if len(cands) == 0 {
		return "", 0, nil
	}

	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return len(cands[i].name) < len(cands[j].name)
	})
	return cands[0].name, cands[0].score, nil
}

func (c *Catalog) newestPrinting(ctx context.Context, name string) (*scryfall.Card, error) {
	prints, err := c.SearchPrints(ctx, name)
	if err != nil || len(prints) == 0 {
		return nil, err
	}
	return &prints[0], nil
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}
