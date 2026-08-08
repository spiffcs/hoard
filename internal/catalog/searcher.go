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

// PrintBySetNumber resolves a printing from its collector block alone.
//
// A card whose title will not read is not necessarily unidentifiable: the
// bottom band names it just as precisely, and "MSH 412" is one printing and no
// other. Scanning is where that matters — an old frame's serif title, or a
// glare across the name, leaves the block as the only legible key, and without
// this the card becomes an unidentifiable queue entry (observed live:
// Quicksilver, Brash Blur arrived with a perfect MSH/412 block and its title
// read as a line of rules text).
//
// Both halves are required. A bare number is shared by every set ever printed,
// so a number without a set is not evidence of anything. Returns nil when the
// block matches no printing, which is the honest answer for a misread.
func (c *Catalog) PrintBySetNumber(ctx context.Context, set, number string) (*scryfall.Card, error) {
	return c.PrintBySetNumberLang(ctx, set, number, "")
}

// PrintBySetNumberLang resolves a printing from its collector block, using the
// language printed on the card to choose between same-numbered siblings.
//
// A printing published only in another language sits beside its English
// namesake under the same set and number, told apart only by a marker the card
// does not print. `war/97` is Liliana, Dreadhorde General at $8.06 and
// `war/97★` is the Japanese alternate art at $113.45 — one scan, fourteen times
// the money, and the number alone always picked the cheap one.
//
// lang is Scryfall's code ("ja"), empty when the frame printed none or the read
// did not clear the helper's vocabulary. It is only ever consulted alongside a
// set code that matches, because a line of rules text can parse as a set row
// and donate a language the card never printed — and the same fabrication
// invents the set code beside it, which matches nothing. See numberMatches in
// internal/tui/autoscan.go, which applies the same rule to the ranked list.
//
// Ambiguity fails closed: if the language does not name exactly one row, the
// caller is better served by the ordinary path than by a guess.
func (c *Catalog) PrintBySetNumberLang(_ context.Context, set, number, lang string) (*scryfall.Card, error) {
	set, number, lang = strings.TrimSpace(set), strings.TrimSpace(number), strings.TrimSpace(lang)
	if set == "" || number == "" {
		return nil, nil
	}
	// Rows that answer to the number: the exact one, plus a marker-trimmed
	// sibling when the card's own set row vouches for it with its language.
	//
	// Both are gathered in one pass rather than exact-first, because
	// exact-first cannot see the case this exists for: `war/97` answers
	// exactly, so `war/97★` — the row the card actually is — would never be
	// reached. The language decides between them below.
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
	// Several answered. The language is the only thing that separates a
	// foreign-only printing from its English namesake, so it decides — and if
	// it does not name exactly one, this fails closed as it always has.
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

// printsMatching runs one printing lookup, newest first, stopping at limit rows
// — enough to tell "one answer" from "ambiguous", which is all any caller asks.
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

// fuzzyCandidates is how many trigram-ranked names are scored properly.
//
// Trigram overlap is a cheap filter, not an answer — it favours long names
// simply for having more grams. Fifty is comfortably more than enough for the
// right name to survive to the edit-distance pass that actually decides.
const fuzzyCandidates = 50

// NamedFuzzy resolves possibly-imperfect text to a single card.
//
// Unlike Scryfall's endpoint, this is an identity check rather than a search.
// That distinction is the whole reason cardname.Plausible exists: the
// API resolves "option" to the card "Opt" because the query contains the name,
// which is right for a human typing a partial name and wrong for OCR, where any
// stray word in frame becomes a card. Here the same rule that used to reject the
// API's answer decides the answer, so the check cannot be skipped by accident.
//
// Returns a nil card when nothing is a believable reading, matching the API
// client so callers fall back to manual entry the same way. The Match reports
// how a non-nil answer was earned: an exact normalized hit, or a fuzzy match
// with its similarity score — the score bestNameFor always computed and used
// to discard, now surfaced for the auto-commit decision.
func (c *Catalog) NamedFuzzy(ctx context.Context, text string) (*scryfall.Card, cardname.Match, error) {
	norm := cardname.Normalize(text)
	if norm == "" {
		return nil, cardname.Match{}, nil
	}

	// An exact normalized hit needs no ranking and no candidate generation.
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
		// A truncated title is not an identity — the prefix rule that said
		// otherwise stole cards — but it is worth nominating, flagged as
		// such: the resolver can confirm a nomination against the frame's
		// collector number, which is signal the fragment cannot fake.
		// Callers MUST check Match.PrefixOnly before treating the card as an
		// answer; today the only consumer is the scan resolver, which does.
		if cardname.PrefixCandidate(text, best) {
			card, err := c.newestPrinting(ctx, best)
			return card, cardname.Match{Similarity: score, PrefixOnly: true}, err
		}
		return nil, cardname.Match{}, nil
	}
	card, err := c.newestPrinting(ctx, best)
	return card, cardname.Match{Similarity: score}, err
}

// bestNameFor ranks trigram candidates by edit distance and returns the
// closest with its similarity score.
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

	// Ties broken by the shorter name: with equal edit distance the shorter one
	// explains more of what was read, which is the same instinct Plausible
	// encodes when it insists a match account for the text.
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return len(cands[i].name) < len(cands[j].name)
	})
	return cands[0].name, cands[0].score, nil
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
