package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// CardFacts is what hoard derives from a printing's stored Scryfall document:
// the sixteen characteristics the JSON holdings document carries as `facts`.
//
// Every field is a VIRTUAL generated column over cards.raw_json, so this costs
// no storage and no fetch — but it exists only where a document has been
// stored, which is why the query returns facts keyed by printing rather than
// a value per row: a printing with no document has no facts at all, not empty
// ones.
//
// The empty string means the card has no such value (an artifact has no power,
// an English printing no printed name), not that hoard failed to read one —
// the query filters unenriched printings out entirely, so absence from the map
// is the only way "unknown" is expressed.
//
// ManaCost and PromoTypes are also read by cardCols, into Card.ManaCost and
// the derived Card.Treatment. They are read again here so this type carries
// all sixteen and a caller needs one source, not two.
type CardFacts struct {
	Rarity   string
	TypeLine string
	// CMC is the mana value. A pointer because zero is a real one — every
	// land has it — and the JSON emission must not confuse it with absence.
	CMC        *float64
	ManaCost   string
	OracleText string
	// Power, Toughness and Loyalty are text because the game prints
	// non-numbers there ("*", "1+*").
	Power       string
	Toughness   string
	Loyalty     string
	SetName     string
	ReleasedAt  string
	Artist      string
	Layout      string
	FlavorText  string
	PromoTypes  []string
	PrintedName string
	// TCGplayerID is the marketplace's product id, nil for the few printings
	// TCGplayer does not list. It is printing-level, not finish-level.
	TCGplayerID *int64
}

// cardFactsCols selects the sixteen, in the order the scan below reads them.
// Text columns are coalesced so a card without a power (most of them) scans as
// empty rather than failing on NULL; cmc and tcgplayer_id stay nullable
// because their zero values are meaningful or misleading.
const cardFactsCols = `
SELECT c.scryfall_id,
       COALESCE(c.rarity, ''), COALESCE(c.type_line, ''), c.cmc,
       COALESCE(c.mana_cost, ''), COALESCE(c.oracle_text, ''),
       COALESCE(c.power, ''), COALESCE(c.toughness, ''), COALESCE(c.loyalty, ''),
       COALESCE(c.set_name, ''), COALESCE(c.released_at, ''),
       COALESCE(c.artist, ''), COALESCE(c.layout, ''),
       COALESCE(c.flavor_text, ''), c.promo_types,
       COALESCE(c.printed_name, ''), c.tcgplayer_id
FROM cards c`

// CardFactsInContainer returns the derived characteristics of every printing
// held in one container, keyed by Scryfall id.
//
// Scoped to a container rather than fetched per row: the columns are computed
// by json_extract over the whole stored document, so reading them once per
// printing instead of once per holding is the difference that keeps an export
// cheap. A printing with no stored document is simply absent from the map.
func (s *Store) CardFactsInContainer(containerID int64) (map[string]CardFacts, error) {
	rows, err := s.db.Query(cardFactsCols+`
WHERE c.raw_json IS NOT NULL
  AND c.scryfall_id IN (SELECT e.scryfall_id FROM card_entries e WHERE e.container_id = ?)`,
		containerID)
	if err != nil {
		return nil, fmt.Errorf("reading card facts: %w", err)
	}
	defer rows.Close()

	out := make(map[string]CardFacts)
	for rows.Next() {
		var id string
		var f CardFacts
		var promo sql.NullString
		if err := rows.Scan(&id,
			&f.Rarity, &f.TypeLine, &f.CMC,
			&f.ManaCost, &f.OracleText,
			&f.Power, &f.Toughness, &f.Loyalty,
			&f.SetName, &f.ReleasedAt, &f.Artist, &f.Layout,
			&f.FlavorText, &promo,
			&f.PrintedName, &f.TCGplayerID); err != nil {
			return nil, fmt.Errorf("reading card facts: %w", err)
		}
		f.PromoTypes = parsePromoTypes(promo)
		out[id] = f
	}
	return out, rows.Err()
}

// parsePromoTypes decodes the stored promo_types array into its tags, nil when
// the printing carries none.
//
// Decoded properly rather than hand-split like parseColorIdentity: colour
// identities are single letters from a closed set, but a promo tag is an
// arbitrary string WotC invents at will ("stepandcompleat"), and a hand-split
// would have to guess at escaping it does not control. A value that will not
// decode yields no tags rather than an error — a caller can do nothing better
// with one, and the tags are descriptive, never load-bearing.
func parsePromoTypes(v sql.NullString) []string {
	if !v.Valid || v.String == "" {
		return nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(v.String), &tags); err != nil {
		return nil
	}
	return tags
}
