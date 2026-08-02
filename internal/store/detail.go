package store

import (
	"fmt"
)

// CardDetail is everything known about one printing: its identity, the market
// prices for each finish, and the descriptive fields derived from the stored
// Scryfall document.
//
// The descriptive fields are pointers because they are genuinely unknown until
// a refresh has stored a document to derive them from. A card added before
// migration v5, or imported from a decklist and not yet refreshed, has no
// rarity — which is not the same as having an empty one, and must not be
// displayed as though it were.
type CardDetail struct {
	Card // carries ColorIdentity and ManaCost for every listing row

	Rarity     *string
	SetName    *string
	TypeLine   *string
	OracleText *string
	Artist     *string
	ReleasedAt *string
	Layout     *string
	CMC        *float64

	// The card-frame fields (migration v11): the stat box — Power and
	// Toughness for creatures, Loyalty for planeswalkers — the flavor
	// text, and the normal-size card image's URL. Power and toughness are
	// text because the game prints non-numbers there ("*", "1+*").
	Power      *string
	Toughness  *string
	Loyalty    *string
	FlavorText *string
	ImageURI   *string

	// Enriched is false when no Scryfall document is stored for this printing,
	// so a caller can say "run update-prices" once rather than printing unknown
	// against every field in turn.
	Enriched bool
}

// cardDetailCols selects the identity and price columns alongside the generated
// ones, with the same fallback prices every other valuation query applies.
var cardDetailCols = `
SELECT ` + cardCols(altSourceExpr) + `,
       c.rarity, c.set_name, c.type_line, c.oracle_text,
       c.artist, c.released_at, c.layout, c.cmc,
       c.power, c.toughness, c.loyalty, c.flavor_text, c.image_uri,
       c.raw_json IS NOT NULL
FROM cards c ` + altJoinCards

// CardDetail returns one printing with its descriptive fields resolved.
func (s *Store) CardDetail(scryfallID string) (CardDetail, error) {
	row := s.db.QueryRow(cardDetailCols+` WHERE c.scryfall_id = ?`, scryfallID)

	var d CardDetail
	var aux cardAux
	if err := row.Scan(append(cardScanDest(&d.Card, &aux),
		&d.Rarity, &d.SetName, &d.TypeLine, &d.OracleText,
		&d.Artist, &d.ReleasedAt, &d.Layout, &d.CMC,
		&d.Power, &d.Toughness, &d.Loyalty, &d.FlavorText, &d.ImageURI,
		&d.Enriched)...); err != nil {
		return CardDetail{}, fmt.Errorf("reading card %s: %w", scryfallID, err)
	}
	aux.apply(&d.Card)
	return d, nil
}

// Holding is a quantity of one printing-and-finish sitting in one container.
type Holding struct {
	ContainerID   int64
	ContainerName string
	ContainerKind string // collection | deck
	Finish        string
	Board         string
	Quantity      int
}

// HoldingsOf reports every container holding a printing, and how many.
//
// This is the question the CLI could never answer: `list` sees the loose
// collection, `deck show` sees one deck, and neither can say that the four
// copies of a card are one loose and three spread across two decks.
func (s *Store) HoldingsOf(scryfallID string) ([]Holding, error) {
	rows, err := s.db.Query(`
SELECT ct.id, `+containerLabel+`, ct.kind, e.finish, e.board, e.quantity
FROM card_entries e
JOIN containers ct ON ct.id = e.container_id
WHERE e.scryfall_id = ?
-- The loose collection first, then decks by name: what you hold free to use
-- ranks above what is already committed to a list. Spelled as a CASE rather
-- than leaning on 'collection' sorting before 'deck' by luck of the alphabet.
ORDER BY CASE ct.kind WHEN '`+KindCollection+`' THEN 0 ELSE 1 END,
         ct.name, e.finish, e.board`, scryfallID)
	if err != nil {
		return nil, fmt.Errorf("reading holdings of %s: %w", scryfallID, err)
	}
	defer rows.Close()

	var out []Holding
	for rows.Next() {
		var h Holding
		if err := rows.Scan(&h.ContainerID, &h.ContainerName, &h.ContainerKind,
			&h.Finish, &h.Board, &h.Quantity); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// PricePoint is one observation in a card's price history.
type PricePoint struct {
	AsOf   string // RFC3339
	Price  float64
	Source string
}

// PriceSeries returns every price recorded for one printing and finish, oldest
// first.
//
// Movers reads two endpoints of a series; this reads the whole thing, which is
// what a sparkline needs. Only the days a price actually moved are stored, so
// the points are irregularly spaced — a renderer that assumes one point per day
// will compress a quiet month into the same width as a volatile week.
func (s *Store) PriceSeries(scryfallID, finish string) ([]PricePoint, error) {
	rows, err := s.db.Query(`
SELECT as_of, price_usd, source
FROM card_price_history
WHERE scryfall_id = ? AND finish = ?
ORDER BY as_of`, scryfallID, finish)
	if err != nil {
		return nil, fmt.Errorf("reading price series for %s: %w", scryfallID, err)
	}
	defer rows.Close()

	var out []PricePoint
	for rows.Next() {
		var p PricePoint
		if err := rows.Scan(&p.AsOf, &p.Price, &p.Source); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
