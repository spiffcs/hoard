package store

import (
	"fmt"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// What is held and what it is worth, across the binder and every deck.

// AddCard ensures the card is in the catalog and adds qty copies to the loose
// collection as normal or foil.
func (s *Store) AddCard(c scryfall.Card, foil bool, qty int) error {
	finish := "normal"
	if foil {
		finish = "foil"
	}
	return s.AddCardFinish(c, finish, qty)
}

// validFinish rejects a finish card_entries cannot hold. One definition, so a
// writer added later cannot admit a value the rest of the schema disagrees with
// — "nonfoil" being the obvious one, since that is Scryfall's spelling of the
// finish this package calls "normal".
func validFinish(finish string) error {
	switch finish {
	case "normal", "foil", "etched":
		return nil
	}
	return fmt.Errorf("invalid finish %q", finish)
}

// AddCardFinish ensures the card is in the catalog and adds qty copies of the
// given finish ("normal", "foil", or "etched") to the loose collection.
func (s *Store) AddCardFinish(c scryfall.Card, finish string, qty int) error {
	if err := validFinish(finish); err != nil {
		return err
	}
	cid, err := s.collectionID()
	if err != nil {
		return err
	}
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

// ListCollectionByFinish returns the loose collection one row per finish held,
// matching how deck show and unpriced present cards.
//
// One row per finish rather than a row per printing pivoted into normal/foil
// columns: the pivot needs four columns to say what two can, since it shows a
// normal price and a foil price whether or not either finish is owned. Splitting
// by finish also keeps etched distinct, which a pivot folds into foil.
func (s *Store) ListCollectionByFinish() ([]CollectionRow, error) {
	cid, err := s.collectionID()
	if err != nil {
		return nil, err
	}
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
ORDER BY value DESC, c.name`, cid)
	if err != nil {
		return nil, fmt.Errorf("listing collection: %w", err)
	}
	defer rows.Close()

	var out []CollectionRow
	for rows.Next() {
		var r CollectionRow
		if err := rows.Scan(append(cardScanDest(&r.Card),
			&r.Finish, &r.Quantity, &r.Value)...); err != nil {
			return nil, err
		}
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
}

// OwnedByFinish returns every printing held, split by finish.
func (s *Store) OwnedByFinish() ([]OwnedFinish, error) {
	rows, err := s.db.Query(`
SELECT c.scryfall_id, COALESCE(c.mtgjson_uuid, ''), c.name, c.set_code,
       c.collector_number, e.finish,
       SUM(e.quantity) AS copies,
       SUM(e.quantity * ` + entryValue + `) AS value
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
		if err := rows.Scan(&o.ScryfallID, &o.MTGJSONUUID, &o.Name, &o.SetCode,
			&o.CollectorNumber, &o.Finish, &o.Copies, &o.Value); err != nil {
			return nil, err
		}
		out = append(out, o)
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
// copies, and market value in one pass.
//
// Copies are counted with the same COALESCE(SUM(quantity), 0) as ListDecks, and
// valued with the shared entryValue fragment, so the collection and the decks
// stay directly comparable once they're summed into one total.
func (s *Store) CollectionTotals() (CollectionTotals, error) {
	cid, err := s.collectionID()
	if err != nil {
		return CollectionTotals{}, err
	}
	var t CollectionTotals
	err = s.db.QueryRow(`
SELECT COUNT(DISTINCT e.scryfall_id) AS distinct_cards,
       COALESCE(SUM(e.quantity), 0) AS total_copies,
       COALESCE(SUM(e.quantity * `+entryValue+`), 0) AS value
FROM card_entries e JOIN cards c ON c.scryfall_id = e.scryfall_id
`+altJoinEntries+`
WHERE e.container_id = ?`, cid).Scan(&t.DistinctCards, &t.TotalCopies, &t.Value)
	if err != nil {
		return CollectionTotals{}, fmt.Errorf("collection totals: %w", err)
	}
	return t, nil
}
