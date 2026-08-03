package store

// `hoard vacuum`: deleting the orphaned printings that corrections leave
// behind. Re-pointing entries (deck repin, the detail's set editor) keeps
// the old cards rows; nothing refreshes or displays them, but they bloat
// the file and their price history describes copies never owned.

import "fmt"

// VacuumPrintings deletes every orphaned printing — no holding, no watch
// points at it — and compacts the file. The schema's cascade takes the
// orphan's price history, bid history, gap markers, and fallback prices
// with it: deliberate, despite the keep-history warning on the schema,
// because an orphan's observations are junk from a misresolved import,
// not years of data about a card owned. Held and watched printings are
// structurally exempt — card_entries and watches reference cards without
// a cascade, so deleting one would refuse anyway.
func (s *Store) VacuumPrintings() (removed int, err error) {
	res, err := s.db.Exec(`
DELETE FROM cards WHERE scryfall_id IN (
  SELECT c.scryfall_id FROM cards c
  WHERE NOT EXISTS (SELECT 1 FROM card_entries e WHERE e.scryfall_id = c.scryfall_id)
    AND NOT EXISTS (SELECT 1 FROM watches w WHERE w.scryfall_id = c.scryfall_id))`)
	if err != nil {
		return 0, fmt.Errorf("deleting orphaned printings: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n > 0 {
		if err := s.Compact(); err != nil {
			return int(n), err
		}
	}
	return int(n), nil
}

// Compact rewrites the database file to reclaim the pages deletions free —
// the vendor-switch retirements and orphan purges otherwise leave the
// file its high-water size forever.
func (s *Store) Compact() error {
	if _, err := s.db.Exec(`VACUUM`); err != nil {
		return fmt.Errorf("compacting the database: %w", err)
	}
	return nil
}
