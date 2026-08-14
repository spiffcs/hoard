package store

import (
	"database/sql"
	"fmt"

	"github.com/spiffcs/hoard/internal/scryfall"
)

type Snapshot struct {
	Version    int
	Printings  []SourcePrinting
	Containers []Container
	Watches    []WatchStatus
}

type SourcePrinting struct {
	Card        scryfall.Card
	MTGJSONUUID string

	UpdatedAt string
}

func (s *Store) Snapshot() (Snapshot, error) {
	var snap Snapshot
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&snap.Version); err != nil {
		return snap, fmt.Errorf("reading schema version: %w", err)
	}

	var err error
	if snap.Printings, err = s.activePrintings(); err != nil {
		return snap, err
	}
	if snap.Containers, err = s.allContainers(); err != nil {
		return snap, err
	}
	if snap.Watches, err = s.ListWatches(); err != nil {
		return snap, err
	}
	return snap, nil
}

func (s *Store) activePrintings() ([]SourcePrinting, error) {
	rows, err := s.db.Query(`
SELECT c.scryfall_id, c.name, c.set_code, c.collector_number, c.scryfall_url,
       c.price_usd, c.price_usd_foil, c.price_usd_etched,
       COALESCE(c.mtgjson_uuid,''), c.updated_at, c.raw_json
FROM cards c
WHERE EXISTS (SELECT 1 FROM card_entries e WHERE e.scryfall_id = c.scryfall_id)
   OR EXISTS (SELECT 1 FROM watches w WHERE w.scryfall_id = c.scryfall_id)
ORDER BY c.scryfall_id`)
	if err != nil {
		return nil, fmt.Errorf("reading printings: %w", err)
	}
	defer rows.Close()

	out := make([]SourcePrinting, 0, 256)
	for rows.Next() {
		var p SourcePrinting
		var raw sql.NullString
		if err := rows.Scan(&p.Card.ID, &p.Card.Name, &p.Card.Set,
			&p.Card.CollectorNumber, &p.Card.ScryfallURL,
			&p.Card.PriceUSD, &p.Card.PriceUSDFoil, &p.Card.PriceUSDEtched,
			&p.MTGJSONUUID, &p.UpdatedAt, &raw); err != nil {
			return nil, fmt.Errorf("reading printings: %w", err)
		}
		if raw.Valid {
			p.Card.Raw = []byte(raw.String)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) allContainers() ([]Container, error) {
	rows, err := s.db.Query(`
SELECT id, kind, name, source, COALESCE(source_id,''),
       COALESCE(source_url,''), COALESCE(format,'')
FROM containers ORDER BY kind, name`)
	if err != nil {
		return nil, fmt.Errorf("reading containers: %w", err)
	}
	defer rows.Close()

	var out []Container
	for rows.Next() {
		var c Container
		if err := rows.Scan(&c.ID, &c.Kind, &c.Name, &c.Source, &c.SourceID,
			&c.SourceURL, &c.Format); err != nil {
			return nil, fmt.Errorf("reading containers: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
