// Package store persists the MTG collection in SQLite.
package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver
)

// Card is a stored collection entry: one row per physical card, tracking normal
// and foil quantities plus the most recently fetched market prices.
type Card struct {
	ScryfallID       string
	SetCode          string
	CollectorNumber  string
	Name             string
	QtyNormal        int
	QtyFoil          int
	PriceUSD         *float64
	PriceUSDFoil     *float64
	ScryfallURL      string
	UpdatedAt        string
}

const schema = `
CREATE TABLE IF NOT EXISTS cards (
    scryfall_id       TEXT PRIMARY KEY,
    set_code          TEXT NOT NULL,
    collector_number  TEXT NOT NULL,
    name              TEXT NOT NULL,
    qty_normal        INTEGER NOT NULL DEFAULT 0,
    qty_foil          INTEGER NOT NULL DEFAULT 0,
    price_usd         REAL,
    price_usd_foil    REAL,
    scryfall_url      TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);`

// Store wraps the database handle.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path and ensures the
// schema exists.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening database %q: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("initializing schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// now returns the current time as an RFC3339 string.
func now() string { return time.Now().UTC().Format(time.RFC3339) }

// AddCard inserts a new card or, if it already exists, increments the relevant
// quantity by qty and refreshes prices. When foil is true, qty is added to the
// foil count; otherwise to the normal count.
func (s *Store) AddCard(c Card, foil bool, qty int) error {
	if foil {
		c.QtyFoil = qty
		c.QtyNormal = 0
	} else {
		c.QtyNormal = qty
		c.QtyFoil = 0
	}
	c.UpdatedAt = now()

	_, err := s.db.Exec(`
INSERT INTO cards (scryfall_id, set_code, collector_number, name,
                   qty_normal, qty_foil, price_usd, price_usd_foil,
                   scryfall_url, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(scryfall_id) DO UPDATE SET
    qty_normal     = qty_normal + excluded.qty_normal,
    qty_foil       = qty_foil   + excluded.qty_foil,
    price_usd      = excluded.price_usd,
    price_usd_foil = excluded.price_usd_foil,
    scryfall_url   = excluded.scryfall_url,
    updated_at     = excluded.updated_at`,
		c.ScryfallID, c.SetCode, c.CollectorNumber, c.Name,
		c.QtyNormal, c.QtyFoil, c.PriceUSD, c.PriceUSDFoil,
		c.ScryfallURL, c.UpdatedAt)
	if err != nil {
		return fmt.Errorf("adding card %s: %w", c.Name, err)
	}
	return nil
}

// UpdatePrices refreshes the stored prices for a card and bumps updated_at.
func (s *Store) UpdatePrices(scryfallID string, usd, usdFoil *float64) error {
	_, err := s.db.Exec(`
UPDATE cards SET price_usd = ?, price_usd_foil = ?, updated_at = ?
WHERE scryfall_id = ?`, usd, usdFoil, now(), scryfallID)
	if err != nil {
		return fmt.Errorf("updating prices for %s: %w", scryfallID, err)
	}
	return nil
}

// SetQuantities sets the exact normal and foil counts for a card. It returns
// the number of rows affected so callers can detect a missing card.
func (s *Store) SetQuantities(scryfallID string, normal, foil int) (int64, error) {
	res, err := s.db.Exec(`
UPDATE cards SET qty_normal = ?, qty_foil = ?, updated_at = ?
WHERE scryfall_id = ?`, normal, foil, now(), scryfallID)
	if err != nil {
		return 0, fmt.Errorf("setting quantities for %s: %w", scryfallID, err)
	}
	return res.RowsAffected()
}

// Remove deletes a card. It returns the number of rows deleted.
func (s *Store) Remove(scryfallID string) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM cards WHERE scryfall_id = ?`, scryfallID)
	if err != nil {
		return 0, fmt.Errorf("removing %s: %w", scryfallID, err)
	}
	return res.RowsAffected()
}

// Get returns a single card by Scryfall ID, or (nil, nil) if not found.
func (s *Store) Get(scryfallID string) (*Card, error) {
	row := s.db.QueryRow(`
SELECT scryfall_id, set_code, collector_number, name, qty_normal, qty_foil,
       price_usd, price_usd_foil, scryfall_url, updated_at
FROM cards WHERE scryfall_id = ?`, scryfallID)
	c, err := scanCard(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// List returns all cards ordered by name.
func (s *Store) List() ([]Card, error) {
	rows, err := s.db.Query(`
SELECT scryfall_id, set_code, collector_number, name, qty_normal, qty_foil,
       price_usd, price_usd_foil, scryfall_url, updated_at
FROM cards ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("listing cards: %w", err)
	}
	defer rows.Close()

	var cards []Card
	for rows.Next() {
		c, err := scanCard(rows)
		if err != nil {
			return nil, err
		}
		cards = append(cards, *c)
	}
	return cards, rows.Err()
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanCard(s scanner) (*Card, error) {
	var c Card
	if err := s.Scan(&c.ScryfallID, &c.SetCode, &c.CollectorNumber, &c.Name,
		&c.QtyNormal, &c.QtyFoil, &c.PriceUSD, &c.PriceUSDFoil,
		&c.ScryfallURL, &c.UpdatedAt); err != nil {
		return nil, err
	}
	return &c, nil
}
