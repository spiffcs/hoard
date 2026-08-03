package store

import (
	"database/sql"
	"fmt"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// What is held and what it is worth, across the binder and every deck.

// validFinish rejects a finish card_entries cannot hold. One definition, so a
// writer added later cannot admit a value the rest of the schema disagrees
// with. The vocabulary is Scryfall's own (nonfoil|foil|etched) as of schema
// v8 — hoard used to say "normal" and translated at every boundary.
func validFinish(finish string) error {
	switch finish {
	case "nonfoil", "foil", "etched":
		return nil
	}
	return fmt.Errorf("invalid finish %q", finish)
}

// AddCardFinish ensures the card is in the catalog and adds qty copies of the
// given finish ("nonfoil", "foil", or "etched") to the default binder.
func (s *Store) AddCardFinish(c scryfall.Card, finish string, qty int) error {
	cid, err := s.collectionID()
	if err != nil {
		return err
	}
	return s.AddCardFinishTo(cid, c, finish, qty)
}

// AddCardFinishTo is AddCardFinish into a chosen container — a named binder,
// or a deck's mainboard.
func (s *Store) AddCardFinishTo(containerID int64, c scryfall.Card, finish string, qty int) error {
	if err := validFinish(finish); err != nil {
		return err
	}
	cid := containerID
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := upsertPrintingsTx(tx, []scryfall.Card{c}); err != nil {
		return err
	}
	if _, err := tx.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, board, quantity)
VALUES (?, ?, ?, 'main', ?)
ON CONFLICT(container_id, scryfall_id, finish, board)
DO UPDATE SET quantity = quantity + excluded.quantity`,
		cid, c.ID, finish, qty); err != nil {
		return fmt.Errorf("adding %s to collection: %w", c.Name, err)
	}
	return tx.Commit()
}

// CollectionRow is one loose-collection holding: a printing in one finish.
type CollectionRow struct {
	Card
	Finish   string
	Quantity int
	Value    float64
}

// Price is the market price for this row's finish.
func (r CollectionRow) Price() *float64 {
	if scryfall.PricedAsFoil(r.Finish) {
		return r.PriceUSDFoil
	}
	return r.PriceUSD
}

// ListCollectionByFinish returns the default binder one row per finish held,
// matching how deck show and unpriced present cards.
func (s *Store) ListCollectionByFinish() ([]CollectionRow, error) {
	cid, err := s.collectionID()
	if err != nil {
		return nil, err
	}
	return s.BinderByFinish(cid)
}

// BinderByFinish returns one binder's holdings, one row per finish held.
//
// One row per finish rather than a row per printing pivoted into normal/foil
// columns: the pivot needs four columns to say what two can, since it shows a
// normal price and a foil price whether or not either finish is owned. Splitting
// by finish also keeps etched distinct, which a pivot folds into foil.
func (s *Store) BinderByFinish(containerID int64) ([]CollectionRow, error) {
	rows, err := s.db.Query(`
SELECT `+cardCols(altSourceForEntry)+`,
       e.finish,
       SUM(e.quantity) AS quantity,
       SUM(e.quantity * `+entryValue+`) AS value
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
`+altJoinCards+`
WHERE e.container_id = ?
GROUP BY c.scryfall_id, e.finish
ORDER BY value DESC, c.name`, containerID)
	if err != nil {
		return nil, fmt.Errorf("listing collection: %w", err)
	}
	return scanCollectionRows(rows)
}

// AllByFinish returns every holding in the hoard — every binder and every
// deck — merged into one row per printing and finish. This is the
// browser's top-level view; binders and decks are subsets of it.
func (s *Store) AllByFinish() ([]CollectionRow, error) {
	rows, err := s.db.Query(`
SELECT ` + cardCols(altSourceForEntry) + `,
       e.finish,
       SUM(e.quantity) AS quantity,
       SUM(e.quantity * ` + entryValue + `) AS value
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
` + altJoinCards + `
GROUP BY c.scryfall_id, e.finish
ORDER BY value DESC, c.name`)
	if err != nil {
		return nil, fmt.Errorf("listing all holdings: %w", err)
	}
	return scanCollectionRows(rows)
}

// scanCollectionRows drains one of the by-finish listing queries; the two
// share their projection, so they must share their scan.
func scanCollectionRows(rows *sql.Rows) ([]CollectionRow, error) {
	defer rows.Close()
	var out []CollectionRow
	for rows.Next() {
		var r CollectionRow
		var aux cardAux
		if err := rows.Scan(append(cardScanDest(&r.Card, &aux),
			&r.Finish, &r.Quantity, &r.Value)...); err != nil {
			return nil, err
		}
		aux.apply(&r.Card)
		out = append(out, r)
	}
	return out, rows.Err()
}

// OwnedFinish is a printing you hold, in one specific finish.
//
// Per finish rather than per card because vendor quotes are per finish: a shop
// buying the non-foil says nothing about what it pays for the foil.
type OwnedFinish struct {
	ScryfallID      string
	MTGJSONUUID     string
	Name            string
	SetCode         string
	CollectorNumber string
	Finish          string
	Copies          int
	// Value is what hoard currently thinks these copies are worth, so a caller
	// can compare a vendor quote against the figure `summary` reports.
	Value float64
	// ColorIdentity is the printing's WUBRG identity, nil when unknown —
	// same semantics as Card.ColorIdentity.
	ColorIdentity []string
	// Treatment is the foil treatment's display word, empty for plain —
	// same semantics as Card.Treatment.
	Treatment string
}

// OwnedByFinish returns every printing held, split by finish.
func (s *Store) OwnedByFinish() ([]OwnedFinish, error) {
	rows, err := s.db.Query(`
SELECT c.scryfall_id, COALESCE(c.mtgjson_uuid, ''), c.name, c.set_code,
       c.collector_number, e.finish,
       SUM(e.quantity) AS copies,
       SUM(e.quantity * ` + entryValue + `) AS value,
       c.color_identity, c.promo_types
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
` + altJoinCards + `
GROUP BY c.scryfall_id, e.finish
ORDER BY value DESC, c.name`)
	if err != nil {
		return nil, fmt.Errorf("listing owned cards: %w", err)
	}
	defer rows.Close()
	var out []OwnedFinish
	for rows.Next() {
		var o OwnedFinish
		var colors, promos sql.NullString
		if err := rows.Scan(&o.ScryfallID, &o.MTGJSONUUID, &o.Name, &o.SetCode,
			&o.CollectorNumber, &o.Finish, &o.Copies, &o.Value, &colors, &promos); err != nil {
			return nil, err
		}
		o.ColorIdentity = parseColorIdentity(colors)
		o.Treatment = FoilTreatment(promos)
		out = append(out, o)
	}
	return out, rows.Err()
}

// EntryKey identifies one (container, printing, finish) membership fact —
// the join key the browser filters its analytical views with. Container ids
// rather than labels, because labels are not unique: two decks can share a
// name, and a deck can share one with a binder.
type EntryKey struct {
	ContainerID int64
	ScryfallID  string
	Finish      string
}

// EntryKeys returns every distinct membership fact in the hoard. Boards
// collapse: a card main and side in the same deck is one fact.
func (s *Store) EntryKeys() ([]EntryKey, error) {
	rows, err := s.db.Query(`
SELECT DISTINCT container_id, scryfall_id, finish FROM card_entries`)
	if err != nil {
		return nil, fmt.Errorf("listing entry keys: %w", err)
	}
	defer rows.Close()
	var out []EntryKey
	for rows.Next() {
		var k EntryKey
		if err := rows.Scan(&k.ContainerID, &k.ScryfallID, &k.Finish); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// CollectionTotals rolls the loose collection up the same way DeckSummary rolls
// up a deck, so `summary` can present the two alike.
type CollectionTotals struct {
	DistinctCards int
	TotalCopies   int
	Value         float64
}

// CollectionTotals returns the loose collection's distinct printings, total
// copies, and market value in one pass — summed across every binder, so the
// summary's BINDER line means "everything not in a deck" however the cards are
// organised.
//
// Copies are counted with the same COALESCE(SUM(quantity), 0) as ListDecks, and
// valued with the shared entryValue fragment, so the collection and the decks
// stay directly comparable once they're summed into one total.
func (s *Store) CollectionTotals() (CollectionTotals, error) {
	if _, err := s.collectionID(); err != nil {
		return CollectionTotals{}, err
	}
	var t CollectionTotals
	err := s.db.QueryRow(`
SELECT COUNT(DISTINCT e.scryfall_id) AS distinct_cards,
       COALESCE(SUM(e.quantity), 0) AS total_copies,
       COALESCE(SUM(e.quantity * `+entryValue+`), 0) AS value
FROM card_entries e JOIN cards c ON c.scryfall_id = e.scryfall_id
JOIN containers ct ON ct.id = e.container_id
`+altJoinEntries+`
WHERE ct.kind = ?`, KindCollection).Scan(&t.DistinctCards, &t.TotalCopies, &t.Value)
	if err != nil {
		return CollectionTotals{}, fmt.Errorf("collection totals: %w", err)
	}
	return t, nil
}
