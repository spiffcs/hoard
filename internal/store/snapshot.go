package store

// Reading a whole hoard out for interchange — what `hoard merge` carries from
// one database to another.
//
// This covers only what the holdings export cannot express on its own: the
// printing catalog with its documents, container identity (including binders
// holding nothing, which have no rows to appear in), and the standing watches.
// The holdings themselves come from action.Deps.ExportRows, which already
// produces exactly the shape the document wants.

import (
	"database/sql"
	"fmt"

	"github.com/spiffcs/hoard/internal/scryfall"
)

// Snapshot is one database's transferable contents.
type Snapshot struct {
	// Version is the schema the source database is stamped with.
	Version    int
	Printings  []SourcePrinting
	Containers []Container
	Watches    []WatchStatus
}

// SourcePrinting is one catalog row as it must cross to another hoard: the
// Scryfall card the store writes back, plus the two columns the store owns
// rather than Scryfall.
//
// Card.Raw carries the printing's Scryfall document verbatim. Everything hoard
// derives — rarity, type line, oracle text, mana cost, artist, art, color
// identity — is a generated column over it, so a printing that arrives without
// one is counted and priced correctly but reads as blank everywhere else.
type SourcePrinting struct {
	Card        scryfall.Card
	MTGJSONUUID string
	// UpdatedAt is when this row was last refreshed. A merge keeps whichever
	// side is newer, so an old database cannot overwrite fresher prices.
	UpdatedAt string
}

// Snapshot reads the whole hoard. It is safe on a read-only handle: nothing
// here writes, which is what lets `hoard merge` promise it never touched the
// database it read from.
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

// activePrintings reads every catalog row something refers to. The predicate
// is ActivePrintingIDs': re-pointing entries at corrected printings leaves the
// old rows behind as orphans, and carrying those to another hoard would
// transfer catalog nobody owns.
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

// allContainers reads every binder and deck with its identity. Binders that
// hold nothing are included deliberately: an empty binder is organization the
// user built, and it produces no holdings row to be inferred from.
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
