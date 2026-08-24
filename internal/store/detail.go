package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/spiffcs/hoard/internal/finish"
)

type CardDetail struct {
	Card

	Rarity     string
	SetName    string
	TypeLine   string
	OracleText string
	Artist     string
	ReleasedAt string
	Layout     string

	CMC *float64

	Power      string
	Toughness  string
	Loyalty    string
	FlavorText string
	ImageURI   string

	TCGplayerID *int64

	CKURL     string
	CKFoilURL string

	PromoTypes []string

	PrintedName string

	Enriched bool
}

var cardDetailCols = `
SELECT ` + cardCols(altSourceExpr) + `,
       COALESCE(c.rarity, ''), COALESCE(c.set_name, ''),
       COALESCE(c.type_line, ''), COALESCE(c.oracle_text, ''),
       COALESCE(c.artist, ''), COALESCE(c.released_at, ''),
       COALESCE(c.layout, ''), c.cmc,
       COALESCE(c.power, ''), COALESCE(c.toughness, ''),
       COALESCE(c.loyalty, ''), COALESCE(c.flavor_text, ''),
       COALESCE(c.image_uri, ''),
       c.tcgplayer_id, COALESCE(c.ck_url, ''), COALESCE(c.ck_foil_url, ''),
       COALESCE(c.printed_name, ''),
       ` + enrichedExpr + `
FROM cards c ` + altJoinCards

func cardDetailScanDest(d *CardDetail, aux *cardAux) []any {
	return append(cardScanDest(&d.Card, aux),
		&d.Rarity, &d.SetName, &d.TypeLine, &d.OracleText,
		&d.Artist, &d.ReleasedAt, &d.Layout, &d.CMC,
		&d.Power, &d.Toughness, &d.Loyalty, &d.FlavorText, &d.ImageURI,
		&d.TCGplayerID, &d.CKURL, &d.CKFoilURL, &d.PrintedName,
		&d.Enriched)
}

func (a cardAux) applyDetail(d *CardDetail) {
	a.apply(&d.Card)
	d.PromoTypes = parsePromoTypes(a.promoTypes)
}

func (s *Store) CardDetail(scryfallID string) (CardDetail, error) {
	row := s.db.QueryRow(cardDetailCols+` WHERE c.scryfall_id = ?`, scryfallID)

	var d CardDetail
	var aux cardAux
	if err := row.Scan(cardDetailScanDest(&d, &aux)...); err != nil {
		return CardDetail{}, fmt.Errorf("reading card %s: %w", scryfallID, err)
	}
	aux.applyDetail(&d)
	return d, nil
}

func (s *Store) CardDetailsInContainer(containerID int64) (map[string]CardDetail, error) {
	rows, err := s.db.Query(cardDetailCols+`
WHERE c.scryfall_id IN (SELECT e.scryfall_id FROM card_entries e WHERE e.container_id = ?)`,
		containerID)
	if err != nil {
		return nil, fmt.Errorf("reading card details: %w", err)
	}
	defer rows.Close()

	out := make(map[string]CardDetail)
	for rows.Next() {
		var d CardDetail
		var aux cardAux
		if err := rows.Scan(cardDetailScanDest(&d, &aux)...); err != nil {
			return nil, fmt.Errorf("reading card details: %w", err)
		}
		aux.applyDetail(&d)
		out[d.ScryfallID] = d
	}
	return out, rows.Err()
}

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

type Holding struct {
	ContainerID   int64
	ContainerName string
	ContainerKind string
	Finish        finish.Finish

	Condition string
	Board     string
	Quantity  int

	ScryfallID      string
	SetCode         string
	CollectorNumber string

	Treatment string

	Guessed bool
}

func (s *Store) HoldingsOf(scryfallID string) ([]Holding, error) {
	rows, err := s.db.Query(`
SELECT ct.id, ct.name, ct.kind, e.finish, e.condition, e.board, e.quantity,
       EXISTS(SELECT 1 FROM finish_guesses g
              WHERE g.container_id = e.container_id
                AND g.scryfall_id = e.scryfall_id
                AND g.finish = e.finish) AS guessed
FROM card_entries e
JOIN containers ct ON ct.id = e.container_id
WHERE e.scryfall_id = ?
-- The loose collection first, then decks by name: what you hold free to use
-- ranks above what is already committed to a list. Spelled as a CASE rather
-- than leaning on 'collection' sorting before 'deck' by luck of the alphabet.
ORDER BY CASE ct.kind WHEN '`+KindCollection+`' THEN 0 ELSE 1 END,
         ct.name, e.finish, e.condition, e.board`, scryfallID)
	if err != nil {
		return nil, fmt.Errorf("reading holdings of %s: %w", scryfallID, err)
	}
	defer rows.Close()

	var out []Holding
	for rows.Next() {
		var h Holding
		if err := rows.Scan(&h.ContainerID, &h.ContainerName, &h.ContainerKind,
			&h.Finish, &h.Condition, &h.Board, &h.Quantity, &h.Guessed); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

func (s *Store) HoldingsOfName(name string) ([]Holding, error) {
	rows, err := s.db.Query(`
SELECT ct.id, ct.name, ct.kind, e.finish, e.condition, e.board, e.quantity,
       c.scryfall_id, c.set_code, c.collector_number, c.promo_types,
       EXISTS(SELECT 1 FROM finish_guesses g
              WHERE g.container_id = e.container_id
                AND g.scryfall_id = e.scryfall_id
                AND g.finish = e.finish) AS guessed
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
JOIN containers ct ON ct.id = e.container_id
WHERE c.name = ?
ORDER BY CASE ct.kind WHEN '`+KindCollection+`' THEN 0 ELSE 1 END,
         ct.name, c.set_code, c.collector_number, e.finish, e.condition, e.board`, name)
	if err != nil {
		return nil, fmt.Errorf("reading holdings of %q: %w", name, err)
	}
	defer rows.Close()

	var out []Holding
	for rows.Next() {
		var h Holding
		var promos sql.NullString
		if err := rows.Scan(&h.ContainerID, &h.ContainerName, &h.ContainerKind,
			&h.Finish, &h.Condition, &h.Board, &h.Quantity,
			&h.ScryfallID, &h.SetCode, &h.CollectorNumber, &promos, &h.Guessed); err != nil {
			return nil, err
		}
		h.Treatment = FoilTreatment(promos)
		out = append(out, h)
	}
	return out, rows.Err()
}

type PricePoint struct {
	AsOf   string
	Price  float64
	Source string
}

func (s *Store) PriceSeries(scryfallID string, fin finish.Finish) ([]PricePoint, error) {
	rows, err := s.db.Query(`
SELECT as_of, price_usd, source
FROM card_price_history
WHERE scryfall_id = ? AND finish = ?
ORDER BY as_of`, scryfallID, fin)
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

func Streak(s []PricePoint) int {
	run, dir := 0, 0
	for i := len(s) - 1; i > 0; i-- {
		switch step := cmpPrice(s[i].Price, s[i-1].Price); {
		case step == 0:
			continue
		case dir == 0:
			dir, run = step, 1
		case step == dir:
			run++
		default:
			return dir * run
		}
	}
	return dir * run
}

func cmpPrice(now, prev float64) int {
	switch {
	case now > prev:
		return 1
	case now < prev:
		return -1
	}
	return 0
}
