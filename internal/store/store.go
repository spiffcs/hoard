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
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cphillips918/hoard/internal/scryfall"
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
	// AltSource names the vendor a fallback price came from, and is empty when
	// Scryfall priced the card. Set only by the queries that read prices for
	// display, so callers can mark an estimate as such.
	AltSource string
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


// SQL fragments for reading prices with the MTGJSON fallback applied. They are
// consts rather than repeated text so the four valuation queries cannot drift
// apart and leave two commands reporting different totals for the same cards.
//
// They assume `c` is the cards table and `a` is card_prices_alt, joined by
// altJoinCards or altJoinEntries.
const (
	altJoinCards   = `LEFT JOIN card_prices_alt a ON a.scryfall_id = c.scryfall_id`
	altJoinEntries = `LEFT JOIN card_prices_alt a ON a.scryfall_id = e.scryfall_id`

	// effPriceUSD and effPriceFoil are the price to use for each finish: the
	// Scryfall figure when there is one, else the fallback.
	effPriceUSD  = `COALESCE(c.price_usd, a.price_usd)`
	effPriceFoil = `COALESCE(c.price_usd_foil, a.price_usd_foil)`

	// altSourceExpr names the vendor behind a fallback price, and is empty when
	// Scryfall priced the card. Display code uses it to mark estimates.
	//
	// This form is for queries with no single finish in scope, such as the
	// collection listing where a row aggregates both. It reports whichever
	// finish is actually falling back.
	altSourceExpr = `COALESCE(CASE
        WHEN c.price_usd IS NULL AND a.price_usd IS NOT NULL THEN a.source_usd
        WHEN c.price_usd_foil IS NULL AND a.price_usd_foil IS NOT NULL THEN a.source_usd_foil
    END, '')`

	// altSourceForEntry is the same idea where the row's finish is known, so it
	// names the vendor that supplied *that* finish. A card's two finishes can
	// come from different shops, and crediting the wrong one beside a price is
	// worse than saying nothing.
	altSourceForEntry = `COALESCE(CASE WHEN e.finish IN ('foil','etched')
        THEN CASE WHEN c.price_usd_foil IS NULL THEN a.source_usd_foil END
        ELSE CASE WHEN c.price_usd IS NULL THEN a.source_usd END
    END, '')`

	// entryValue values one card_entries row by its finish.
	entryValue = `COALESCE(CASE WHEN e.finish IN ('foil','etched')
        THEN ` + effPriceFoil + ` ELSE ` + effPriceUSD + ` END, 0)`

	// unpricedPredicate matches an entry no source can price for the finish it
	// is actually held in. The finish matters: a card owned only in non-foil
	// needs no foil price, and treating that as a gap would send every price
	// run chasing numbers that would never be used.
	unpricedPredicate = `(e.finish IN ('foil','etched')
         AND c.price_usd_foil IS NULL AND a.price_usd_foil IS NULL)
   OR (e.finish = 'normal'
         AND c.price_usd IS NULL AND a.price_usd IS NULL)`
)

// Store wraps the database handle.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite database at path, migrates any
// legacy schema, ensures the current schema and the singleton collection exist.
func Open(path string) (*Store, error) {
	// Ensure the parent directory exists (e.g. the per-user data directory on
	// first run).
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("creating database directory %q: %w", dir, err)
		}
	}
	// Enable foreign-key enforcement so ON DELETE CASCADE works, and give a
	// blocked write a few seconds rather than failing outright.
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("opening database %q: %w", path, err)
	}
	// One connection, deliberately. database/sql pools by default, which would
	// let a PRAGMA land on a different connection than the statement it was
	// meant to configure. For a single-user CLI serialising is free, and it
	// makes migrations safe.
	db.SetMaxOpenConns(1)

	s := &Store{db: db}
	if err := s.migrate(path); err != nil {
		db.Close()
		return nil, err
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

// PriceGap is a card that has no price for a finish it is actually owned in.
type PriceGap struct {
	ScryfallID string
	SetCode    string
	Name       string
}

// UnpricedByOwnedFinish returns cards Scryfall could not price for a finish the
// user actually holds.
//
// The finish matters: a card owned only in non-foil needs no foil price, and
// counting it as a gap would send every run chasing prices that will never be
// used. Cards already carrying a usable fallback are excluded too, so a second
// run is cheap.
func (s *Store) UnpricedByOwnedFinish() ([]PriceGap, error) {
	rows, err := s.db.Query(`
SELECT DISTINCT c.scryfall_id, c.set_code, c.name
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
` + altJoinCards + `
WHERE ` + unpricedPredicate + `
ORDER BY c.set_code, c.name`)
	if err != nil {
		return nil, fmt.Errorf("finding unpriced cards: %w", err)
	}
	defer rows.Close()
	var out []PriceGap
	for rows.Next() {
		var g PriceGap
		if err := rows.Scan(&g.ScryfallID, &g.SetCode, &g.Name); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// UnpricedRow is one card-and-finish that no source can price, with where it is
// held so the reader can see which totals are understated.
type UnpricedRow struct {
	Name            string
	SetCode         string
	CollectorNumber string
	Finish          string
	Copies          int
	HeldIn          string
}

// Unpriced lists the same gaps as UnpricedByOwnedFinish, broken out per finish
// and annotated for display.
//
// Kept separate from UnpricedByOwnedFinish because the two want different
// shapes: the fill needs distinct cards to look up, this needs one row per
// finish with its containers. They share unpricedPredicate so the two can never
// disagree about what counts as unpriced.
func (s *Store) Unpriced() ([]UnpricedRow, error) {
	rows, err := s.db.Query(`
SELECT c.name, c.set_code, c.collector_number, e.finish,
       SUM(e.quantity) AS copies,
       GROUP_CONCAT(DISTINCT ct.name) AS held_in
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
JOIN containers ct ON ct.id = e.container_id
` + altJoinCards + `
WHERE ` + unpricedPredicate + `
GROUP BY c.scryfall_id, e.finish
ORDER BY c.name, e.finish`)
	if err != nil {
		return nil, fmt.Errorf("listing unpriced cards: %w", err)
	}
	defer rows.Close()
	var out []UnpricedRow
	for rows.Next() {
		var u UnpricedRow
		if err := rows.Scan(&u.Name, &u.SetCode, &u.CollectorNumber,
			&u.Finish, &u.Copies, &u.HeldIn); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// AltPrice is a fallback price to record for one card.
//
// The two finishes carry their own vendor because they are looked up
// independently: the shop that prices a card's non-foil printing often has no
// figure for its foil, and vice versa.
type AltPrice struct {
	ScryfallID    string
	MTGJSONUUID   string
	PriceUSD      *float64
	PriceUSDFoil  *float64
	SourceUSD     string
	SourceUSDFoil string
}

// UpsertAltPrices records fallback prices, replacing any previous ones.
//
// Rows are rewritten rather than inserted once: a vendor's price moves, and a
// card Scryfall later learns to price should not be left showing a stale
// fallback underneath it.
func (s *Store) UpsertAltPrices(prices []AltPrice) error {
	if len(prices) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
INSERT INTO card_prices_alt (scryfall_id, mtgjson_uuid, price_usd, price_usd_foil,
                             source_usd, source_usd_foil, as_of)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(scryfall_id) DO UPDATE SET
    mtgjson_uuid    = excluded.mtgjson_uuid,
    price_usd       = excluded.price_usd,
    price_usd_foil  = excluded.price_usd_foil,
    source_usd      = excluded.source_usd,
    source_usd_foil = excluded.source_usd_foil,
    as_of           = excluded.as_of`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	ts := now()
	for _, p := range prices {
		if _, err := stmt.Exec(p.ScryfallID, p.MTGJSONUUID, p.PriceUSD,
			p.PriceUSDFoil, nullable(p.SourceUSD), nullable(p.SourceUSDFoil), ts); err != nil {
			return fmt.Errorf("recording fallback price for %s: %w", p.ScryfallID, err)
		}
	}
	return tx.Commit()
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

// FinishFix is one entry recorded in a finish its printing does not come in.
type FinishFix struct {
	Name            string
	SetCode         string
	CollectorNumber string
	Container       string
	Board           string
	From            string
	To              string
	Quantity        int
}

// hoardFinish translates Scryfall's finish names to the ones stored here.
// Scryfall says "nonfoil" where card_entries says "normal"; "foil" and "etched"
// agree.
func hoardFinish(scryfallFinish string) string {
	if scryfallFinish == "nonfoil" {
		return "normal"
	}
	return scryfallFinish
}

// FinishIsAvailable reports whether a printing comes in the given finish.
// available is Scryfall's finishes list for that printing; an empty list means
// unknown, which counts as available since there is nothing to contradict it.
func FinishIsAvailable(finish string, available []string) bool {
	if len(available) == 0 {
		return true
	}
	return slices.ContainsFunc(available, func(sf string) bool {
		return hoardFinish(sf) == finish
	})
}

// CorrectFinish returns the finish an entry should use and whether that differs
// from what was asked for.
//
// It only corrects when the printing comes in exactly one finish, since that is
// the only case with a single right answer. A printing available in several
// finishes where the recorded one is not among them is left alone: guessing
// there would be worse than reporting it.
//
// Shared by deck import and repair-finishes so both apply the same rule.
func CorrectFinish(finish string, available []string) (string, bool) {
	if FinishIsAvailable(finish, available) || len(available) != 1 {
		return finish, false
	}
	return hoardFinish(available[0]), true
}

// RepairFinishes corrects entries whose finish does not exist for that printing.
//
// A decklist with no foil marker imports as "normal", but plenty of printings
// are foil-only: precon commanders and Duel Decks reprints among them. The
// resulting entry asks for a price that cannot exist, so the card is valued at
// zero forever and no amount of price fetching helps.
//
// available maps a Scryfall ID to the finishes that printing actually comes in.
// A correction is only made when the printing has exactly one finish, since that
// is the only case with a single right answer; anything else is returned as
// ambiguous and left untouched rather than guessed at.
func (s *Store) RepairFinishes(available map[string][]string) (fixed, ambiguous []FinishFix, err error) {
	rows, err := s.db.Query(`
SELECT e.container_id, ct.name, e.scryfall_id, e.finish, e.board, e.quantity,
       c.name, c.set_code, c.collector_number
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
JOIN containers ct ON ct.id = e.container_id
ORDER BY c.name`)
	if err != nil {
		return nil, nil, fmt.Errorf("reading entries: %w", err)
	}
	defer rows.Close()

	type target struct {
		containerID int64
		scryfallID  string
		board       string
		from, to    string
		quantity    int
	}
	var todo []target
	for rows.Next() {
		var t target
		var f FinishFix
		if err := rows.Scan(&t.containerID, &f.Container, &t.scryfallID, &t.from,
			&t.board, &t.quantity, &f.Name, &f.SetCode, &f.CollectorNumber); err != nil {
			return nil, nil, err
		}
		finishes, known := available[t.scryfallID]
		if !known || len(finishes) == 0 {
			continue // nothing to judge against
		}
		if FinishIsAvailable(t.from, finishes) {
			continue // the recorded finish is real
		}

		f.Board, f.From, f.Quantity = t.board, t.from, t.quantity
		to, ok := CorrectFinish(t.from, finishes)
		if !ok {
			f.To = strings.Join(finishes, "|")
			ambiguous = append(ambiguous, f)
			continue
		}
		t.to, f.To = to, to
		fixed = append(fixed, f)
		todo = append(todo, t)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(todo) == 0 {
		return nil, ambiguous, nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	for _, t := range todo {
		// Insert-then-delete rather than UPDATE: the corrected finish may
		// already exist for this card in this container, and the primary key
		// includes finish. Merging the quantities is the only correct outcome.
		if _, err := tx.Exec(`
INSERT INTO card_entries (container_id, scryfall_id, finish, board, quantity)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(container_id, scryfall_id, finish, board)
DO UPDATE SET quantity = quantity + excluded.quantity`,
			t.containerID, t.scryfallID, t.to, t.board, t.quantity); err != nil {
			return nil, nil, fmt.Errorf("moving entry to %s: %w", t.to, err)
		}
		if _, err := tx.Exec(`
DELETE FROM card_entries
WHERE container_id=? AND scryfall_id=? AND finish=? AND board=?`,
			t.containerID, t.scryfallID, t.from, t.board); err != nil {
			return nil, nil, fmt.Errorf("removing old %s entry: %w", t.from, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return fixed, ambiguous, nil
}

// nullable stores an empty string as SQL NULL, so "no vendor for this finish"
// is distinguishable from a vendor whose name happens to be blank.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// KnownMTGJSONUUIDs returns the Scryfall-ID-to-UUID pairs already resolved, so a
// set file is downloaded at most once ever.
//
// The ids live on the card rather than beside a price, because resolving one
// costs a whole set-file download and the answer never changes. Keeping them
// only for cards that happened to need a fallback price would make any
// collection-wide price read re-download most of the catalog's set files.
func (s *Store) KnownMTGJSONUUIDs() (map[string]string, error) {
	rows, err := s.db.Query(`
SELECT scryfall_id, mtgjson_uuid FROM cards WHERE mtgjson_uuid IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var sid, uuid string
		if err := rows.Scan(&sid, &uuid); err != nil {
			return nil, err
		}
		out[sid] = uuid
	}
	return out, rows.Err()
}

// SaveMTGJSONUUIDs records resolved ids so their set files are never fetched
// again.
func (s *Store) SaveMTGJSONUUIDs(ids map[string]string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`UPDATE cards SET mtgjson_uuid = ? WHERE scryfall_id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for sid, uuid := range ids {
		if uuid == "" {
			continue
		}
		if _, err := stmt.Exec(uuid, sid); err != nil {
			return fmt.Errorf("caching mtgjson id for %s: %w", sid, err)
		}
	}
	return tx.Commit()
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
       `+effPriceUSD+`, `+effPriceFoil+`, c.scryfall_url, c.updated_at,
       `+altSourceExpr+`,
       COALESCE(SUM(CASE WHEN e.finish='normal' THEN e.quantity END), 0) AS qty_normal,
       COALESCE(SUM(CASE WHEN e.finish IN ('foil','etched') THEN e.quantity END), 0) AS qty_foil
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
`+altJoinCards+`
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
			&cc.PriceUSD, &cc.PriceUSDFoil, &cc.ScryfallURL, &cc.UpdatedAt, &cc.AltSource,
			&cc.QtyNormal, &cc.QtyFoil); err != nil {
			return nil, err
		}
		out = append(out, cc)
	}
	return out, rows.Err()
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
// The pivoted view ListCollection returns needs four columns to say what two
// can, since a row shows a normal price and a foil price whether or not either
// finish is owned. Splitting by finish also keeps etched distinct, which the
// pivot folds into foil.
func (s *Store) ListCollectionByFinish() ([]CollectionRow, error) {
	cid, err := s.collectionID()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
SELECT c.scryfall_id, c.set_code, c.collector_number, c.name,
       `+effPriceUSD+`, `+effPriceFoil+`, c.scryfall_url, c.updated_at,
       `+altSourceForEntry+`,
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
		if err := rows.Scan(&r.ScryfallID, &r.SetCode, &r.CollectorNumber, &r.Name,
			&r.PriceUSD, &r.PriceUSDFoil, &r.ScryfallURL, &r.UpdatedAt, &r.AltSource,
			&r.Finish, &r.Quantity, &r.Value); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Price is the market price for this row's finish.
func (r CollectionRow) Price() *float64 {
	if r.Finish == "foil" || r.Finish == "etched" {
		return r.PriceUSDFoil
	}
	return r.PriceUSD
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
       COALESCE(SUM(e.quantity * `+entryValue+`), 0) AS value
FROM containers ct
LEFT JOIN card_entries e ON e.container_id = ct.id
LEFT JOIN cards c ON c.scryfall_id = e.scryfall_id
`+altJoinEntries+`
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

const deckSelect = `
SELECT id, kind, name, source, COALESCE(source_id,''), COALESCE(source_url,''), COALESCE(format,'')
FROM containers WHERE kind=?`

func scanContainer(sc interface{ Scan(...any) error }) (*Container, error) {
	var c Container
	if err := sc.Scan(&c.ID, &c.Kind, &c.Name, &c.Source, &c.SourceID, &c.SourceURL, &c.Format); err != nil {
		return nil, err
	}
	return &c, nil
}

// DeckByRef resolves a deck by numeric id, exact name, or a case-insensitive
// fragment of its name.
//
// Deck names are long ("Duel Decks Anthology: Divine vs. Demonic (Demonic)"),
// so requiring the full string makes them impractical to type. A fragment is
// accepted whenever it names exactly one deck; when it names several the error
// lists them rather than picking one, since silently acting on the wrong deck is
// the worst outcome for `deck remove`.
func (s *Store) DeckByRef(ref string) (*Container, error) {
	// A bare integer is an id, never a name fragment.
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		c, err := scanContainer(s.db.QueryRow(deckSelect+` AND id=?`, KindDeck, id))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("no deck matching %q", ref)
		}
		return c, err
	}

	// An exact name wins outright, so a deck whose whole name is a fragment of
	// another's stays reachable.
	c, err := scanContainer(s.db.QueryRow(deckSelect+` AND name=? COLLATE NOCASE`, KindDeck, ref))
	if err == nil {
		return c, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	// Otherwise accept a fragment, as long as it picks out exactly one deck.
	// LIKE is already case-insensitive for ASCII in SQLite.
	rows, err := s.db.Query(deckSelect+` AND name LIKE ? ESCAPE '\' ORDER BY name`,
		KindDeck, "%"+escapeLike(ref)+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var matches []*Container
	for rows.Next() {
		m, err := scanContainer(rows)
		if err != nil {
			return nil, err
		}
		matches = append(matches, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no deck matching %q", ref)
	case 1:
		return matches[0], nil
	default:
		var b strings.Builder
		fmt.Fprintf(&b, "%q matches %d decks:", ref, len(matches))
		for _, m := range matches[:min(len(matches), 5)] {
			fmt.Fprintf(&b, "\n  %s", m.Name)
		}
		if len(matches) > 5 {
			fmt.Fprintf(&b, "\n  … and %d more", len(matches)-5)
		}
		b.WriteString("\nUse a longer fragment or the full name.")
		return nil, errors.New(b.String())
	}
}

// escapeLike neutralizes the wildcards in a user-supplied LIKE pattern, so a
// deck name containing % or _ is matched literally.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

// DeckEntries returns a deck's entries joined to catalog cards, ordered by
// board then name.
func (s *Store) DeckEntries(containerID int64) ([]EntryView, error) {
	rows, err := s.db.Query(`
SELECT c.scryfall_id, c.set_code, c.collector_number, c.name,
       `+effPriceUSD+`, `+effPriceFoil+`, c.scryfall_url, c.updated_at,
       `+altSourceForEntry+`,
       e.finish, e.board, e.quantity
FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
`+altJoinCards+`
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
			&v.Card.AltSource, &v.Finish, &v.Board, &v.Quantity); err != nil {
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
       COALESCE(SUM(e.quantity * `+entryValue+`), 0) AS value
FROM cards c
LEFT JOIN card_entries e ON e.scryfall_id = c.scryfall_id
`+altJoinCards+`
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
SELECT SUM(e.quantity * `+entryValue+`)
FROM card_entries e JOIN cards c ON c.scryfall_id = e.scryfall_id
`+altJoinEntries+`
WHERE e.container_id = ?`, cid).Scan(&v)
	if err != nil {
		return 0, err
	}
	return v.Float64, nil
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
// valued with the same foil/etched CASE as CollectionValue, so the collection
// and the decks stay directly comparable once they're summed into one total.
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
	if _, err := tx.Exec(schemaV1); err != nil {
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
