// Package store persists the MTG collection and decks in SQLite.
//
// The model is normalized into three tables:
//
//   - cards        — the card catalog: identity + market price, one row per
//     printing, shared (deduplicated) across the collection and every deck.
//   - containers   — the singleton loose "collection" plus one row per deck.
//     The `source` column is a generic provider slug so no list site is
//     referenced structurally.
//   - card_entries — quantity of a card (by finish and board) inside a container.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/cphillips918/mtg_index/internal/scryfall"
	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver
)

// Container kinds.
const (
	KindCollection = "collection"
	KindDeck       = "deck"
)

// collectionSourceID is the fixed source_id of the singleton loose collection,
// letting it share the UNIQUE(source, source_id) constraint with decks.
const collectionSourceID = "__collection__"

// Card is a catalog entry: a distinct printing plus its latest market prices.
type Card struct {
	ScryfallID      string
	SetCode         string
	CollectorNumber string
	Name            string
	PriceUSD        *float64
	PriceUSDFoil    *float64
	ScryfallURL     string
	UpdatedAt       string
}

// Entry is a quantity of a card (finish + board) to place in a container.
type Entry struct {
	ScryfallID string
	Finish     string // normal|foil|etched
	Board      string // main|commander|side|maybe
	Quantity   int
}

// Container is the loose collection or a deck.
type Container struct {
	ID        int64
	Kind      string
	Name      string
	Source    string
	SourceID  string
	SourceURL string
	Format    string
}

// DeckMeta describes a deck to upsert (its entries are supplied separately).
type DeckMeta struct {
	Name      string
	Source    string
	SourceID  string
	SourceURL string
	Format    string
}

// EntryView is an entry joined to its catalog card, for display and valuation.
type EntryView struct {
	Card     Card
	Finish   string
	Board    string
	Quantity int
}

// Price returns the market price for this entry's finish (foil vs. normal).
func (e EntryView) Price() *float64 {
	if e.Finish == "foil" || e.Finish == "etched" {
		return e.Card.PriceUSDFoil
	}
	return e.Card.PriceUSD
}

// Value returns quantity × finish price (0 when the price is unknown).
func (e EntryView) Value() float64 {
	if p := e.Price(); p != nil {
		return float64(e.Quantity) * *p
	}
	return 0
}

// DeckSummary is a deck plus rolled-up counts and value for listings.
type DeckSummary struct {
	Container
	DistinctCards int
	TotalCopies   int
	Value         float64
}

// CollectionCard is a catalog card pivoted back into normal/foil quantities as
// held in the loose collection, matching the original `list` output.
type CollectionCard struct {
	Card
	QtyNormal int
	QtyFoil   int
}

// OwnedRow aggregates how many of a card are owned across all containers.
type OwnedRow struct {
	Card
	TotalCopies int
	Value       float64
}

const schema = `
CREATE TABLE IF NOT EXISTS cards (
    scryfall_id      TEXT PRIMARY KEY,
    set_code         TEXT NOT NULL,
    collector_number TEXT NOT NULL,
    name             TEXT NOT NULL,
    price_usd        REAL,
    price_usd_foil   REAL,
    scryfall_url     TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS containers (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    kind       TEXT NOT NULL,
    name       TEXT NOT NULL,
    source     TEXT NOT NULL DEFAULT 'manual',
    source_id  TEXT,
    source_url TEXT,
    format     TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(source, source_id)
);

CREATE TABLE IF NOT EXISTS card_entries (
    container_id INTEGER NOT NULL REFERENCES containers(id) ON DELETE CASCADE,
    scryfall_id  TEXT NOT NULL REFERENCES cards(scryfall_id),
    finish       TEXT NOT NULL DEFAULT 'normal',
    board        TEXT NOT NULL DEFAULT 'main',
    quantity     INTEGER NOT NULL,
    PRIMARY KEY (container_id, scryfall_id, finish, board)
);`

// Store wraps the database handle.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path, migrates any
// legacy schema, ensures the current schema and the singleton collection exist.
func Open(path string) (*Store, error) {
	// Enable foreign-key enforcement so ON DELETE CASCADE works.
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("opening database %q: %w", path, err)
	}
	s := &Store{db: db}
	if err := s.migrateLegacy(); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("initializing schema: %w", err)
	}
	if _, err := s.collectionID(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// collectionID returns the id of the singleton loose collection, creating it on
// first use.
func (s *Store) collectionID() (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM containers WHERE source='manual' AND source_id=?`,
		collectionSourceID).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	ts := now()
	res, err := s.db.Exec(`
INSERT INTO containers (kind, name, source, source_id, created_at, updated_at)
VALUES (?, 'Collection', 'manual', ?, ?, ?)`,
		KindCollection, collectionSourceID, ts, ts)
	if err != nil {
		return 0, fmt.Errorf("creating collection container: %w", err)
	}
	return res.LastInsertId()
}

// UpsertCatalogCards inserts or refreshes catalog cards (identity + price).
func (s *Store) UpsertCatalogCards(cards []scryfall.Card) error {
	if len(cards) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := upsertCatalogTx(tx, cards); err != nil {
		return err
	}
	return tx.Commit()
}

func upsertCatalogTx(tx *sql.Tx, cards []scryfall.Card) error {
	stmt, err := tx.Prepare(`
INSERT INTO cards (scryfall_id, set_code, collector_number, name,
                   price_usd, price_usd_foil, scryfall_url, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(scryfall_id) DO UPDATE SET
    set_code         = excluded.set_code,
    collector_number = excluded.collector_number,
    name             = excluded.name,
    price_usd        = excluded.price_usd,
    price_usd_foil   = excluded.price_usd_foil,
    scryfall_url     = excluded.scryfall_url,
    updated_at       = excluded.updated_at`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	ts := now()
	for _, c := range cards {
		if _, err := stmt.Exec(c.ID, c.Set, c.CollectorNumber, c.Name,
			c.PriceUSD, c.PriceUSDFoil, c.ScryfallURL, ts); err != nil {
			return fmt.Errorf("upserting catalog card %s: %w", c.Name, err)
		}
	}
	return nil
}

// AllCatalogIDs returns every Scryfall ID in the catalog (for bulk price refresh).
func (s *Store) AllCatalogIDs() ([]string, error) {
	rows, err := s.db.Query(`SELECT scryfall_id FROM cards`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// UpdatePrices refreshes stored prices for the given catalog cards.
func (s *Store) UpdatePrices(cards []scryfall.Card) error {
	return s.UpsertCatalogCards(cards)
}

// --- Collection operations (operate on the singleton collection container) ---

// AddCard ensures the card is in the catalog and adds qty copies to the loose
// collection as normal or foil.
func (s *Store) AddCard(c scryfall.Card, foil bool, qty int) error {
	finish := "normal"
	if foil {
		finish = "foil"
	}
	return s.AddCardFinish(c, finish, qty)
}

// AddCardFinish ensures the card is in the catalog and adds qty copies of the
// given finish ("normal", "foil", or "etched") to the loose collection.
func (s *Store) AddCardFinish(c scryfall.Card, finish string, qty int) error {
	switch finish {
	case "normal", "foil", "etched":
	default:
		return fmt.Errorf("invalid finish %q", finish)
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

	if err := upsertCatalogTx(tx, []scryfall.Card{c}); err != nil {
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

// SetCollectionQuantities sets the exact normal/foil counts for a card in the
// loose collection. A count of 0 removes that finish's entry. Returns whether
// the card was present in the collection.
func (s *Store) SetCollectionQuantities(scryfallID string, normal, foil int) (bool, error) {
	cid, err := s.collectionID()
	if err != nil {
		return false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var existed bool
	if err := tx.QueryRow(`SELECT EXISTS(
        SELECT 1 FROM card_entries WHERE container_id=? AND scryfall_id=?)`,
		cid, scryfallID).Scan(&existed); err != nil {
		return false, err
	}
	if err := setFinishQty(tx, cid, scryfallID, "normal", normal); err != nil {
		return false, err
	}
	if err := setFinishQty(tx, cid, scryfallID, "foil", foil); err != nil {
		return false, err
	}
	return existed, tx.Commit()
}

func setFinishQty(tx *sql.Tx, cid int64, scryfallID, finish string, qty int) error {
	if qty <= 0 {
		_, err := tx.Exec(`DELETE FROM card_entries
            WHERE container_id=? AND scryfall_id=? AND finish=? AND board='main'`,
			cid, scryfallID, finish)
		return err
	}
	_, err := tx.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, board, quantity)
VALUES (?, ?, ?, 'main', ?)
ON CONFLICT(container_id, scryfall_id, finish, board)
DO UPDATE SET quantity = excluded.quantity`,
		cid, scryfallID, finish, qty)
	return err
}

// RemoveFromCollection deletes all of a card's entries from the loose
// collection. Returns the number of entry rows removed.
func (s *Store) RemoveFromCollection(scryfallID string) (int64, error) {
	cid, err := s.collectionID()
	if err != nil {
		return 0, err
	}
	res, err := s.db.Exec(`DELETE FROM card_entries WHERE container_id=? AND scryfall_id=?`,
		cid, scryfallID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// FindCollectionCard looks up a loose-collection card by set code and collector
// number, returning nil if it is not in the collection.
func (s *Store) FindCollectionCard(set, number string) (*Card, error) {
	cid, err := s.collectionID()
	if err != nil {
		return nil, err
	}
	row := s.db.QueryRow(`
SELECT c.scryfall_id, c.set_code, c.collector_number, c.name,
       c.price_usd, c.price_usd_foil, c.scryfall_url, c.updated_at
FROM cards c
JOIN card_entries e ON e.scryfall_id = c.scryfall_id
WHERE e.container_id = ? AND c.set_code = ? AND c.collector_number = ?
LIMIT 1`, cid, set, number)
	var c Card
	err = row.Scan(&c.ScryfallID, &c.SetCode, &c.CollectorNumber, &c.Name,
		&c.PriceUSD, &c.PriceUSDFoil, &c.ScryfallURL, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ListCollection returns loose-collection cards pivoted into normal/foil
// quantities, ordered by name.
func (s *Store) ListCollection() ([]CollectionCard, error) {
	cid, err := s.collectionID()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
SELECT c.scryfall_id, c.set_code, c.collector_number, c.name,
       c.price_usd, c.price_usd_foil, c.scryfall_url, c.updated_at,
       COALESCE(SUM(CASE WHEN e.finish='normal' THEN e.quantity END), 0) AS qty_normal,
       COALESCE(SUM(CASE WHEN e.finish IN ('foil','etched') THEN e.quantity END), 0) AS qty_foil
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
WHERE e.container_id = ?
GROUP BY c.scryfall_id
ORDER BY c.name`, cid)
	if err != nil {
		return nil, fmt.Errorf("listing collection: %w", err)
	}
	defer rows.Close()

	var out []CollectionCard
	for rows.Next() {
		var cc CollectionCard
		if err := rows.Scan(&cc.ScryfallID, &cc.SetCode, &cc.CollectorNumber, &cc.Name,
			&cc.PriceUSD, &cc.PriceUSDFoil, &cc.ScryfallURL, &cc.UpdatedAt,
			&cc.QtyNormal, &cc.QtyFoil); err != nil {
			return nil, err
		}
		out = append(out, cc)
	}
	return out, rows.Err()
}

// --- Deck operations ---

// UpsertDeck inserts or updates a deck by (source, source_id) and replaces its
// entries wholesale, so re-importing is idempotent. Returns the deck's id.
func (s *Store) UpsertDeck(meta DeckMeta, entries []Entry) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	ts := now()
	if _, err := tx.Exec(`
INSERT INTO containers (kind, name, source, source_id, source_url, format, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(source, source_id) DO UPDATE SET
    name       = excluded.name,
    source_url = excluded.source_url,
    format     = excluded.format,
    updated_at = excluded.updated_at`,
		KindDeck, meta.Name, meta.Source, meta.SourceID, meta.SourceURL, meta.Format, ts, ts); err != nil {
		return 0, fmt.Errorf("upserting deck %q: %w", meta.Name, err)
	}

	var id int64
	if err := tx.QueryRow(`SELECT id FROM containers WHERE source=? AND source_id=?`,
		meta.Source, meta.SourceID).Scan(&id); err != nil {
		return 0, err
	}

	if _, err := tx.Exec(`DELETE FROM card_entries WHERE container_id=?`, id); err != nil {
		return 0, err
	}
	stmt, err := tx.Prepare(`
INSERT INTO card_entries (container_id, scryfall_id, finish, board, quantity)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(container_id, scryfall_id, finish, board)
DO UPDATE SET quantity = quantity + excluded.quantity`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	for _, e := range entries {
		if _, err := stmt.Exec(id, e.ScryfallID, e.Finish, e.Board, e.Quantity); err != nil {
			return 0, fmt.Errorf("inserting deck entry: %w", err)
		}
	}
	return id, tx.Commit()
}

// ListDecks returns all decks with rolled-up card counts and value.
func (s *Store) ListDecks() ([]DeckSummary, error) {
	rows, err := s.db.Query(`
SELECT ct.id, ct.name, ct.source, COALESCE(ct.source_url,''), COALESCE(ct.format,''),
       COUNT(e.scryfall_id) AS distinct_cards,
       COALESCE(SUM(e.quantity), 0) AS total_copies,
       COALESCE(SUM(e.quantity * COALESCE(
           CASE WHEN e.finish IN ('foil','etched') THEN c.price_usd_foil ELSE c.price_usd END, 0)), 0) AS value
FROM containers ct
LEFT JOIN card_entries e ON e.container_id = ct.id
LEFT JOIN cards c ON c.scryfall_id = e.scryfall_id
WHERE ct.kind = ?
GROUP BY ct.id
ORDER BY ct.name`, KindDeck)
	if err != nil {
		return nil, fmt.Errorf("listing decks: %w", err)
	}
	defer rows.Close()

	var out []DeckSummary
	for rows.Next() {
		var d DeckSummary
		d.Kind = KindDeck
		if err := rows.Scan(&d.ID, &d.Name, &d.Source, &d.SourceURL, &d.Format,
			&d.DistinctCards, &d.TotalCopies, &d.Value); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeckByRef resolves a deck by numeric id or (case-insensitive) exact name.
func (s *Store) DeckByRef(ref string) (*Container, error) {
	var row *sql.Row
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		row = s.db.QueryRow(`
SELECT id, kind, name, source, COALESCE(source_id,''), COALESCE(source_url,''), COALESCE(format,'')
FROM containers WHERE kind=? AND id=?`, KindDeck, id)
	} else {
		row = s.db.QueryRow(`
SELECT id, kind, name, source, COALESCE(source_id,''), COALESCE(source_url,''), COALESCE(format,'')
FROM containers WHERE kind=? AND name=? COLLATE NOCASE`, KindDeck, ref)
	}
	var c Container
	err := row.Scan(&c.ID, &c.Kind, &c.Name, &c.Source, &c.SourceID, &c.SourceURL, &c.Format)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("no deck matching %q", ref)
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// DeckEntries returns a deck's entries joined to catalog cards, ordered by
// board then name.
func (s *Store) DeckEntries(containerID int64) ([]EntryView, error) {
	rows, err := s.db.Query(`
SELECT c.scryfall_id, c.set_code, c.collector_number, c.name,
       c.price_usd, c.price_usd_foil, c.scryfall_url, c.updated_at,
       e.finish, e.board, e.quantity
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
WHERE e.container_id = ?
ORDER BY
    CASE e.board WHEN 'commander' THEN 0 WHEN 'main' THEN 1 WHEN 'side' THEN 2 ELSE 3 END,
    c.name`, containerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EntryView
	for rows.Next() {
		var v EntryView
		if err := rows.Scan(&v.Card.ScryfallID, &v.Card.SetCode, &v.Card.CollectorNumber, &v.Card.Name,
			&v.Card.PriceUSD, &v.Card.PriceUSDFoil, &v.Card.ScryfallURL, &v.Card.UpdatedAt,
			&v.Finish, &v.Board, &v.Quantity); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// RemoveContainer deletes a container (and, via cascade, its entries). Returns
// the number of containers removed.
func (s *Store) RemoveContainer(id int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM containers WHERE id=?`, id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// --- Aggregates ---

// TotalsByCard returns each catalog card with the total copies owned across all
// containers (collection + decks) and total value, ordered by descending value.
func (s *Store) TotalsByCard() ([]OwnedRow, error) {
	rows, err := s.db.Query(`
SELECT c.scryfall_id, c.set_code, c.collector_number, c.name,
       c.price_usd, c.price_usd_foil, c.scryfall_url, c.updated_at,
       COALESCE(SUM(e.quantity), 0) AS total_copies,
       COALESCE(SUM(e.quantity * COALESCE(
           CASE WHEN e.finish IN ('foil','etched') THEN c.price_usd_foil ELSE c.price_usd END, 0)), 0) AS value
FROM cards c
LEFT JOIN card_entries e ON e.scryfall_id = c.scryfall_id
GROUP BY c.scryfall_id
HAVING total_copies > 0
ORDER BY value DESC, c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []OwnedRow
	for rows.Next() {
		var o OwnedRow
		if err := rows.Scan(&o.ScryfallID, &o.SetCode, &o.CollectorNumber, &o.Name,
			&o.PriceUSD, &o.PriceUSDFoil, &o.ScryfallURL, &o.UpdatedAt,
			&o.TotalCopies, &o.Value); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// CollectionValue returns the total market value of the loose collection.
func (s *Store) CollectionValue() (float64, error) {
	cid, err := s.collectionID()
	if err != nil {
		return 0, err
	}
	var v sql.NullFloat64
	err = s.db.QueryRow(`
SELECT SUM(e.quantity * COALESCE(
    CASE WHEN e.finish IN ('foil','etched') THEN c.price_usd_foil ELSE c.price_usd END, 0))
FROM card_entries e JOIN cards c ON c.scryfall_id = e.scryfall_id
WHERE e.container_id = ?`, cid).Scan(&v)
	if err != nil {
		return 0, err
	}
	return v.Float64, nil
}

// --- Legacy migration ---

// migrateLegacy upgrades a database created by the original single-table build
// (a `cards` table with a qty_normal column) to the current normalized schema.
// It is a no-op on fresh databases and on already-migrated ones.
func (s *Store) migrateLegacy() error {
	var hasLegacy bool
	err := s.db.QueryRow(`
SELECT EXISTS(
    SELECT 1 FROM pragma_table_info('cards') WHERE name='qty_normal')`).Scan(&hasLegacy)
	if err != nil {
		return fmt.Errorf("checking for legacy schema: %w", err)
	}
	if !hasLegacy {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`ALTER TABLE cards RENAME TO cards_legacy`); err != nil {
		return fmt.Errorf("migrate: renaming legacy cards: %w", err)
	}
	if _, err := tx.Exec(schema); err != nil {
		return fmt.Errorf("migrate: creating new schema: %w", err)
	}

	ts := now()
	res, err := tx.Exec(`
INSERT INTO containers (kind, name, source, source_id, created_at, updated_at)
VALUES (?, 'Collection', 'manual', ?, ?, ?)`,
		KindCollection, collectionSourceID, ts, ts)
	if err != nil {
		return fmt.Errorf("migrate: creating collection: %w", err)
	}
	cid, err := res.LastInsertId()
	if err != nil {
		return err
	}

	// Copy identity + prices into the new catalog.
	if _, err := tx.Exec(`
INSERT INTO cards (scryfall_id, set_code, collector_number, name,
                   price_usd, price_usd_foil, scryfall_url, updated_at)
SELECT scryfall_id, set_code, collector_number, name,
       price_usd, price_usd_foil, scryfall_url, updated_at
FROM cards_legacy`); err != nil {
		return fmt.Errorf("migrate: copying catalog: %w", err)
	}
	// Move quantities into collection entries (one row per non-zero finish).
	if _, err := tx.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, board, quantity)
SELECT ?, scryfall_id, 'normal', 'main', qty_normal FROM cards_legacy WHERE qty_normal > 0`, cid); err != nil {
		return fmt.Errorf("migrate: copying normal quantities: %w", err)
	}
	if _, err := tx.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, board, quantity)
SELECT ?, scryfall_id, 'foil', 'main', qty_foil FROM cards_legacy WHERE qty_foil > 0`, cid); err != nil {
		return fmt.Errorf("migrate: copying foil quantities: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE cards_legacy`); err != nil {
		return fmt.Errorf("migrate: dropping legacy table: %w", err)
	}
	return tx.Commit()
}
