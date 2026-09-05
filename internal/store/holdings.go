package store

import (
	"database/sql"
	"fmt"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

func validFinish(f finish.Finish) error {
	_, err := finish.Parse(f.String())
	return err
}

const (
	ConditionUnknown = "unknown"
	ConditionNM      = "nm"
	ConditionLP      = "lp"
	ConditionMP      = "mp"
	ConditionHP      = "hp"
	ConditionDamaged = "dmg"
)

func orUnknown(condition string) string {
	if condition == "" {
		return ConditionUnknown
	}
	return condition
}

func validCondition(condition string) error {
	switch condition {
	case ConditionUnknown, ConditionNM, ConditionLP, ConditionMP, ConditionHP, ConditionDamaged:
		return nil
	}
	return fmt.Errorf("invalid condition %q", condition)
}

func (s *Store) AddCardFinish(c scryfall.Card, fin finish.Finish, qty int) error {
	cid, err := s.collectionID()
	if err != nil {
		return err
	}
	return s.AddCardFinishTo(cid, c, fin, qty)
}

func (s *Store) AddCardFinishTo(containerID int64, c scryfall.Card, fin finish.Finish, qty int) error {
	return s.AddCardFinishPaidTo(containerID, c, fin, qty, nil)
}

func (s *Store) AddCardFinishPaidTo(containerID int64, c scryfall.Card, fin finish.Finish, qty int, paid *float64) error {
	if err := validFinish(fin); err != nil {
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
INSERT INTO card_entries (container_id, scryfall_id, finish, condition, board, purchase_price, quantity)
VALUES (?, ?, ?, 'unknown', 'main', ?, ?)
ON CONFLICT(container_id, scryfall_id, finish, condition, board, COALESCE(purchase_price, -1))
DO UPDATE SET quantity = quantity + excluded.quantity`,
		cid, c.ID, fin, paid, qty); err != nil {
		return fmt.Errorf("adding %s to collection: %w", c.Name, err)
	}
	return tx.Commit()
}

type CollectionRow struct {
	Card
	Finish finish.Finish

	Condition string
	Quantity  int
	Value     float64

	PurchasePrice *float64
}

func (r CollectionRow) Price() *float64 {
	return r.Finish.EffectivePrice(r.PriceUSD, r.PriceUSDFoil, r.PriceUSDEtched)
}

func (s *Store) ListCollectionByFinish() ([]CollectionRow, error) {
	cid, err := s.collectionID()
	if err != nil {
		return nil, err
	}
	return s.BinderByFinish(cid)
}

func (s *Store) BinderByFinish(containerID int64) ([]CollectionRow, error) {
	rows, err := s.db.Query(`
SELECT `+cardCols(altSourceForEntry)+`,
       e.finish, e.condition, e.purchase_price,
       SUM(e.quantity) AS quantity,
       SUM(e.quantity * `+entryValue+`) AS value
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
`+altJoinCards+`
WHERE e.container_id = ?
GROUP BY c.scryfall_id, e.finish, e.condition, e.purchase_price
ORDER BY value DESC, c.name`, containerID)
	if err != nil {
		return nil, fmt.Errorf("listing collection: %w", err)
	}
	return scanCollectionRows(rows)
}

func (s *Store) AllByFinish() ([]CollectionRow, error) {
	rows, err := s.db.Query(`
SELECT ` + cardCols(altSourceForEntry) + `,
       e.finish, e.condition, e.purchase_price,
       SUM(e.quantity) AS quantity,
       SUM(e.quantity * ` + entryValue + `) AS value
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id` + countedEntries + `
` + altJoinCards + `
GROUP BY c.scryfall_id, e.finish, e.condition, e.purchase_price
ORDER BY value DESC, c.name`)
	if err != nil {
		return nil, fmt.Errorf("listing all holdings: %w", err)
	}
	return scanCollectionRows(rows)
}

func scanCollectionRows(rows *sql.Rows) ([]CollectionRow, error) {
	defer rows.Close()
	var out []CollectionRow
	for rows.Next() {
		var r CollectionRow
		var aux cardAux
		if err := rows.Scan(append(cardScanDest(&r.Card, &aux),
			&r.Finish, &r.Condition, &r.PurchasePrice, &r.Quantity, &r.Value)...); err != nil {
			return nil, err
		}
		aux.apply(&r.Card)
		out = append(out, r)
	}
	return out, rows.Err()
}

type OwnedFinish struct {
	ScryfallID      string
	MTGJSONUUID     string
	Name            string
	SetCode         string
	CollectorNumber string
	Finish          finish.Finish
	Copies          int

	Value     float64
	UnitPrice *float64

	ColorIdentity []string

	Lang string

	Treatment string

	TCGAltProductID string
	CKFoilID        string

	VendorIDsKnown bool
}

func (s *Store) OwnedByFinish() ([]OwnedFinish, error) {
	rows, err := s.db.Query(`
SELECT c.scryfall_id, COALESCE(c.mtgjson_uuid, ''), c.name, c.set_code,
       c.collector_number, e.finish,
       SUM(` + countedQuantity + `) AS copies,
       SUM(` + countedQuantity + ` * ` + entryValue + `) AS value,
       MAX(` + entryUnitPrice + `) AS unit_price,
       c.color_identity, c.promo_types,
       COALESCE(c.tcg_alt_product_id, ''), COALESCE(c.ck_foil_id, ''),
       c.ck_foil_id IS NOT NULL, COALESCE(c.lang, '')
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
JOIN containers ctc ON ctc.id = e.container_id
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
			&o.CollectorNumber, &o.Finish, &o.Copies, &o.Value, &o.UnitPrice, &colors, &promos,
			&o.TCGAltProductID, &o.CKFoilID, &o.VendorIDsKnown, &o.Lang); err != nil {
			return nil, err
		}
		o.ColorIdentity = parseColorIdentity(colors)
		o.Treatment = FoilTreatment(promos)
		out = append(out, o)
	}
	return out, rows.Err()
}

func (o OwnedFinish) Unpriced() bool { return o.UnitPrice == nil && o.Value == 0 }

type EntryKey struct {
	ContainerID int64
	ScryfallID  string
	Finish      finish.Finish
	Quantity    int
}

func (s *Store) EntryKeys() ([]EntryKey, error) {
	rows, err := s.db.Query(`
SELECT container_id, scryfall_id, finish, SUM(quantity)
FROM card_entries GROUP BY container_id, scryfall_id, finish`)
	if err != nil {
		return nil, fmt.Errorf("listing entry keys: %w", err)
	}
	defer rows.Close()
	var out []EntryKey
	for rows.Next() {
		var k EntryKey
		if err := rows.Scan(&k.ContainerID, &k.ScryfallID, &k.Finish, &k.Quantity); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

type CollectionTotals struct {
	DistinctCards int
	TotalCopies   int
	Value         float64
	Spent         float64
}

func (s *Store) CollectionTotals() (CollectionTotals, error) {
	if _, err := s.collectionID(); err != nil {
		return CollectionTotals{}, err
	}
	var t CollectionTotals
	err := s.db.QueryRow(`
SELECT COUNT(DISTINCT e.scryfall_id) AS distinct_cards,
       COALESCE(SUM(e.quantity), 0) AS total_copies,
       COALESCE(SUM(e.quantity * `+entryValue+`), 0) AS value,
       COALESCE(SUM(e.quantity * e.purchase_price), 0) AS spent
FROM card_entries e JOIN cards c ON c.scryfall_id = e.scryfall_id
JOIN containers ct ON ct.id = e.container_id
`+altJoinEntries+`
WHERE ct.kind = ? AND ct.counted = 1`, KindCollection).Scan(
		&t.DistinctCards, &t.TotalCopies, &t.Value, &t.Spent)
	if err != nil {
		return CollectionTotals{}, fmt.Errorf("collection totals: %w", err)
	}
	return t, nil
}
