// Package store persists what you own: the binder, the decks, and the printings
// in them.
//
//   - cards        — one row per printing, shared across every container.
//   - containers   — the singleton binder plus one row per deck. `source` is a
//     generic provider slug so no list site is referenced structurally.
//   - card_entries — how many of a printing, in which finish and board, in which
//     container.
//   - card_prices_alt    — fallback vendor prices for printings Scryfall
//     cannot price, applied by the shared SQL fragments below.
//   - card_price_history — dated price observations, one per refresh or
//     backfill, feeding movers and the detail sparklines. Irreplaceable in
//     practice: MTGJSON's archive reaches back 90 days, so any observation
//     older than that exists nowhere but here.
//   - card_price_gaps    — printings already found unpriced everywhere, so a
//     refresh stops re-downloading bundles to chase them.
//
// Not to be confused with internal/catalog, which is a disposable local copy of
// Scryfall's whole card database. This package holds the irreplaceable half.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spiffcs/hoard/internal/scryfall"
	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver
)

// Container kinds. These are stored values, never shown; see LooseName for what
// the singleton is called on screen.
const (
	KindCollection = "collection"
	KindDeck       = "deck"
)

// LooseName is what the cards outside any deck are called on screen.
//
// "Collection" is the whole hoard — binder plus decks — so using it for the loose
// half made the summary contradict itself, listing a COLLECTION of 335 above a
// TOTAL of 2,214.
//
// The stored container is still named "Collection" with kind `collection`; both
// are internal and not worth a migration. containerLabel substitutes this one.
const LooseName = "Binder"

// containerLabel is a container's display name: its own name, or LooseName for
// the default binder. Keyed on the sentinel source_id rather than the kind,
// because user-created binders share the kind and keep their own names. It
// assumes the containers table is aliased `ct`.
const containerLabel = `CASE WHEN ct.source_id = '` + collectionSourceID +
	`' THEN '` + LooseName + `' ELSE ct.name END`

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
	if scryfall.PricedAsFoil(e.Finish) {
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

// cardCols selects the Card fields, in the order cardScanDest reads them.
// altSource is altSourceExpr or altSourceForEntry, whichever the query's scope
// permits. Projection and scan sit side by side so a new Card field is one
// edit here, not a coordinated edit per query — a mismatch used to surface
// only as a runtime scan error.
func cardCols(altSource string) string {
	return `c.scryfall_id, c.set_code, c.collector_number, c.name,
       ` + effPriceUSD + `, ` + effPriceFoil + `, c.scryfall_url, c.updated_at,
       ` + altSource
}

// cardScanDest is the scan targets matching cardCols, for the caller to extend
// with its query's own columns.
func cardScanDest(c *Card) []any {
	return []any{&c.ScryfallID, &c.SetCode, &c.CollectorNumber, &c.Name,
		&c.PriceUSD, &c.PriceUSDFoil, &c.ScryfallURL, &c.UpdatedAt, &c.AltSource}
}

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
	// blocked write a few seconds rather than failing outright. Transactions
	// begin immediate: several writers here read a value and write it back in
	// one transaction, and a deferred BEGIN that upgrades to a write lock under
	// a concurrent process gets an instant SQLITE_BUSY that busy_timeout never
	// retries — taking the lock up front turns that race into a plain wait.
	//
	// Durability is the default and deliberately so: journal_mode=DELETE with
	// synchronous=FULL. This file is the irreplaceable half, and every commit
	// paying a real fsync is the choice. Anyone switching to WAL later must set
	// synchronous explicitly — WAL quietly weakens it to NORMAL semantics.
	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate")
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
