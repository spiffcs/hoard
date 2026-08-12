package store

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// migration is one versioned schema change.
//
// Once a migration ships its Stmts are frozen forever: databases in the wild
// have already run them, so editing one changes history rather than the schema.
// New changes go on the end as a new version.
type migration struct {
	Version int
	Stmts   string
}

// migrations are applied in order to any database behind schemaVersion.
//
// Version 1 is the baseline every hoard already has. It keeps IF NOT EXISTS
// because it is also what bootstraps a database that predates versioning, where
// some of those tables exist and some do not. Every later migration is an
// explicit ALTER: a whole-schema blob cannot add a column to a table that
// already exists, and failing silently is exactly the trap this runner exists to
// close.
var migrations = []migration{
	{1, schemaV1},
	{2, splitAltPriceSources},
	{3, cacheMTGJSONIDs},
	{4, keepPriceHistory},
	{5, richCardData},
	{6, rememberPriceGaps},
	{7, rememberImports},
	{8, renameNonfoil},
	{9, valueSnapshots},
	{10, watchesTable},
	{11, cardFaceDetails},
	{12, renormalizeHistoryFinish},
	{13, bidHistory},
	{14, tcgplayerProductID},
	{15, cardKingdomLinks},
	{16, promoTypesColumn},
	{17, tcgAltProductColumn},
	{18, settingsTable},
	{19, defaultBinderRealName},
	{20, vendorProductIDs},
	{21, etchedPrices},
	{22, cardLanguage},
	{23, holdingCondition},
	{24, finishGuesses},
	{25, guessContainerRepoint},
	{26, guessContainerFK},
	{27, traitFilterIndex},
	{28, percentWatches},
	{29, contradictedPrices},
	{30, purgePhantomFinishSeries},
}

// schemaVersion is the version a database is brought up to.
var schemaVersion = migrations[len(migrations)-1].Version

// SchemaVersion is the schema version this build writes, the same number a
// migrated database carries in PRAGMA user_version. Exported so the schema
// dump under schema/sqlite/ can name the version it describes.
func SchemaVersion() int { return schemaVersion }

const schemaV1 = `
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
);

-- Fallback prices for printings Scryfall cannot price. Scryfall's USD figures
-- come from TCGplayer alone, so a printing TCGplayer has no record of is
-- unpriced there; MTGJSON aggregates other vendors and often has it.
--
-- Separate from cards because upsertPrintingsTx rewrites price_usd_foil from
-- every Scryfall response, so a fallback stored there would be nulled out on
-- the very next update-prices run.
CREATE TABLE IF NOT EXISTS card_prices_alt (
    scryfall_id    TEXT PRIMARY KEY REFERENCES cards(scryfall_id) ON DELETE CASCADE,
    mtgjson_uuid   TEXT NOT NULL,
    price_usd      REAL,
    price_usd_foil REAL,
    source         TEXT NOT NULL,
    as_of          TEXT NOT NULL
);`

// splitAltPriceSources records which vendor priced each finish.
//
// A card's two finishes can come from different shops, and v1 crammed that into
// one column as "normal/foil". That reads wrong on a per-finish row: a foil line
// would name the vendor that supplied the non-foil price. The backfill splits
// existing labels on the same "/" that wrote them.
const splitAltPriceSources = `
ALTER TABLE card_prices_alt ADD COLUMN source_usd TEXT;
ALTER TABLE card_prices_alt ADD COLUMN source_usd_foil TEXT;

UPDATE card_prices_alt SET
  source_usd = CASE WHEN price_usd IS NOT NULL THEN
      CASE WHEN instr(source, '/') > 0
           THEN substr(source, 1, instr(source, '/') - 1)
           ELSE source END
  END,
  source_usd_foil = CASE WHEN price_usd_foil IS NOT NULL THEN
      CASE WHEN instr(source, '/') > 0
           THEN substr(source, instr(source, '/') + 1)
           ELSE source END
  END;

ALTER TABLE card_prices_alt DROP COLUMN source;`

// cacheMTGJSONIDs keeps the Scryfall-to-MTGJSON id map on the card itself.
//
// The map previously existed only as a side effect of card_prices_alt, which is
// written solely for cards that had a price gap, so most owned cards had no id
// cached. Resolving one means downloading that card's whole set file, and the
// download cache is pruned daily, so anything reading prices across the entire
// collection would re-fetch dozens of set files every day it ran.
//
// The id is a property of the printing and never changes, so it belongs beside
// the printing and is worth keeping forever.
const cacheMTGJSONIDs = `
ALTER TABLE cards ADD COLUMN mtgjson_uuid TEXT;

UPDATE cards SET mtgjson_uuid = (
    SELECT a.mtgjson_uuid FROM card_prices_alt a WHERE a.scryfall_id = cards.scryfall_id
) WHERE EXISTS (
    SELECT 1 FROM card_prices_alt a WHERE a.scryfall_id = cards.scryfall_id
);

CREATE INDEX IF NOT EXISTS cards_mtgjson_uuid ON cards(mtgjson_uuid);`

// keepPriceHistory remembers what a printing used to cost. Every price write
// before this overwrote in place, so nothing could say what had moved.
//
// Rows are appended only when the price differs from the last observed one: a row
// per card per refresh would grow the database by the size of the catalog daily.
//
// The finish is the *price's* finish, not the one held — etched is valued from the
// foil price (see entryValue), so it shares that series rather than duplicating it.
//
// The seed turns the prices already on disk into a baseline, so the first refresh
// after upgrading has something to compare against.
//
// CAUTION on the CASCADE below: history is the one table that cannot be
// re-fetched — MTGJSON's archive reaches back 90 days, and everything older
// exists only here. Nothing deletes from cards today, so the cascade never
// fires; any future pruning of catalog rows must exempt printings with
// history, or years of observations vanish with a row that was "just a cache".
const keepPriceHistory = `
CREATE TABLE IF NOT EXISTS card_price_history (
    scryfall_id TEXT NOT NULL REFERENCES cards(scryfall_id) ON DELETE CASCADE,
    finish      TEXT NOT NULL,
    price_usd   REAL NOT NULL,
    source      TEXT NOT NULL,
    as_of       TEXT NOT NULL,
    PRIMARY KEY (scryfall_id, finish, as_of)
);

INSERT INTO card_price_history (scryfall_id, finish, price_usd, source, as_of)
SELECT c.scryfall_id, 'normal',
       COALESCE(c.price_usd, a.price_usd),
       CASE WHEN c.price_usd IS NOT NULL THEN 'scryfall'
            ELSE COALESCE(a.source_usd, 'fallback') END,
       CASE WHEN c.price_usd IS NOT NULL THEN c.updated_at ELSE a.as_of END
FROM cards c LEFT JOIN card_prices_alt a ON a.scryfall_id = c.scryfall_id
WHERE COALESCE(c.price_usd, a.price_usd) IS NOT NULL;

INSERT INTO card_price_history (scryfall_id, finish, price_usd, source, as_of)
SELECT c.scryfall_id, 'foil',
       COALESCE(c.price_usd_foil, a.price_usd_foil),
       CASE WHEN c.price_usd_foil IS NOT NULL THEN 'scryfall'
            ELSE COALESCE(a.source_usd_foil, 'fallback') END,
       CASE WHEN c.price_usd_foil IS NOT NULL THEN c.updated_at ELSE a.as_of END
FROM cards c LEFT JOIN card_prices_alt a ON a.scryfall_id = c.scryfall_id
WHERE COALESCE(c.price_usd_foil, a.price_usd_foil) IS NOT NULL;`

// richCardData keeps the whole Scryfall response and derives columns from it.
//
// Deriving rather than adding a real column per field makes every future field a
// one-line ALTER: VIRTUAL generated columns occupy no space in existing rows, so
// there is no rebuild and no backfill. Costs ~5.4 KB of JSON per card.
//
// No backfill here on purpose — update-prices refetches every owned card, so the
// first run after this fills raw_json. Until then the columns read NULL, which
// callers must show as unknown rather than as zero.
const richCardData = `
ALTER TABLE cards ADD COLUMN raw_json TEXT;

ALTER TABLE cards ADD COLUMN rarity TEXT
    GENERATED ALWAYS AS (json_extract(raw_json,'$.rarity')) VIRTUAL;
ALTER TABLE cards ADD COLUMN set_name TEXT
    GENERATED ALWAYS AS (json_extract(raw_json,'$.set_name')) VIRTUAL;
ALTER TABLE cards ADD COLUMN cmc REAL
    GENERATED ALWAYS AS (json_extract(raw_json,'$.cmc')) VIRTUAL;
ALTER TABLE cards ADD COLUMN artist TEXT
    GENERATED ALWAYS AS (json_extract(raw_json,'$.artist')) VIRTUAL;
ALTER TABLE cards ADD COLUMN released_at TEXT
    GENERATED ALWAYS AS (json_extract(raw_json,'$.released_at')) VIRTUAL;
ALTER TABLE cards ADD COLUMN layout TEXT
    GENERATED ALWAYS AS (json_extract(raw_json,'$.layout')) VIRTUAL;

ALTER TABLE cards ADD COLUMN type_line TEXT
    GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.type_line'),
                                  json_extract(raw_json,'$.card_faces[0].type_line'))) VIRTUAL;
ALTER TABLE cards ADD COLUMN mana_cost TEXT
    GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.mana_cost'),
                                  json_extract(raw_json,'$.card_faces[0].mana_cost'))) VIRTUAL;
ALTER TABLE cards ADD COLUMN oracle_text TEXT
    GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.oracle_text'),
                                  json_extract(raw_json,'$.card_faces[0].oracle_text'))) VIRTUAL;

-- Kept as the JSON array Scryfall sends. Colour is a set, not a string, so
-- filtering reads it with json_each rather than matching substrings — which
-- would make color:U also match a UB card, and color:R match nothing at all
-- when the array happens to be ordered ["B","R"].
ALTER TABLE cards ADD COLUMN color_identity TEXT
    GENERATED ALWAYS AS (json_extract(raw_json,'$.color_identity')) VIRTUAL;

CREATE INDEX IF NOT EXISTS cards_name ON cards(name);

-- card_entries had no index on scryfall_id, so every join from cards — which is
-- most of the valuation queries — scanned the whole table.
CREATE INDEX IF NOT EXISTS card_entries_card_id ON card_entries(scryfall_id);`

// rememberPriceGaps records that MTGJSON was asked about a card and had nothing.
//
// One card costs a 50 MB scan, and cards nothing can price are neither rare nor
// temporary, so without this every refresh pays that scan to re-learn the same
// answer. Only the *absence* is recorded — a price that was found lives in
// card_prices_alt, and a card with one is no longer a gap.
//
// A timestamp rather than a flag: MTGJSON adds vendors, so the check expires and
// the card is asked about again rather than written off.
const rememberPriceGaps = `
CREATE TABLE IF NOT EXISTS card_price_gaps (
    scryfall_id TEXT PRIMARY KEY REFERENCES cards(scryfall_id) ON DELETE CASCADE,
    checked_at  TEXT NOT NULL
);`

// applicationID marks the file as a hoard database in its SQLite header:
// "HORD" in ASCII, per the application-file-format convention, so a tool (or a
// future hoard command) can tell a hoard from any other .db without guessing
// at table names.
const applicationID = 0x484F5244

// v7: a receipt per imported file, keyed by content hash. Entry quantities
// accumulate, so without this a re-run of the same CSV silently doubles every
// count — the receipt is what lets `hoard import` refuse a repeat instead.
// The file name and date are for the refusal message, not for matching.
const rememberImports = `
CREATE TABLE IF NOT EXISTS import_ledger (
    hash        TEXT PRIMARY KEY,
    file        TEXT NOT NULL,
    cards       INTEGER NOT NULL,
    imported_at TEXT NOT NULL
);`

// v8: hoard's finish vocabulary said 'normal' where Scryfall and MTGJSON both
// say 'nonfoil'. With the JSON surface adopting ecosystem vocabulary, the
// discrepancy was renamed away at the source rather than translated at every
// boundary. Earlier frozen migrations (the v1 bootstrap default, the v4
// history seed, the legacy transform) still write 'normal' — they run before
// this one, which is the point.
const renameNonfoil = `
UPDATE card_entries SET finish = 'nonfoil' WHERE finish = 'normal';
UPDATE card_price_history SET finish = 'nonfoil' WHERE finish = 'normal';`

// v12: history backfilled after v8 carried MTGJSON's "normal" again — the
// ingest path never translated, and v8 only repaired the rows that existed
// when it ran. Movers joins on the store's vocabulary, so every such row
// was invisible (observed live: a 47k-row backfill that changed nothing on
// screen). Where a day exists under both names the nonfoil row stands — it
// was recorded first — and the leftovers drop.
const renormalizeHistoryFinish = `
UPDATE OR IGNORE card_price_history SET finish = 'nonfoil' WHERE finish = 'normal';
DELETE FROM card_price_history WHERE finish = 'normal';`

// v13: Card Kingdom's buylist bid history, in its own table rather than
// card_price_history: the retail table's readers (latestPrices,
// RecordPrices, Movers, the backfill bounds) group by (scryfall_id,
// finish) without source, the shared PK would collide with a retail row
// at the same as_of, and PriceSeries would interleave the two sides of
// the counter into one zig-zag series. Same shape and CASCADE as v4.
const bidHistory = `
CREATE TABLE card_bid_history (
    scryfall_id TEXT NOT NULL REFERENCES cards(scryfall_id) ON DELETE CASCADE,
    finish      TEXT NOT NULL,
    price_usd   REAL NOT NULL,
    source      TEXT NOT NULL,
    as_of       TEXT NOT NULL,
    PRIMARY KEY (scryfall_id, finish, as_of)
);`

// v14: TCGplayer's product id, surfaced from the stored Scryfall document
// like every other v5-style field — it is what turns the detail's
// tcgplayer link from a name search into the exact product page.
const tcgplayerProductID = `
ALTER TABLE cards ADD COLUMN tcgplayer_id INTEGER
    GENERATED ALWAYS AS (json_extract(raw_json,'$.tcgplayer_id')) VIRTUAL;`

// v15: Card Kingdom's product links, learned from the MTGJSON set files
// the uuid resolver already downloads. Stored (not generated) because the
// data comes from MTGJSON, not the Scryfall document — like mtgjson_uuid.
// NULL means never asked; empty string means asked and the feed had none,
// so absence does not re-fetch the set file forever.
const cardKingdomLinks = `
ALTER TABLE cards ADD COLUMN ck_url TEXT;
ALTER TABLE cards ADD COLUMN ck_foil_url TEXT;`

// v16: the printing's promo_types tags, surfaced from the stored Scryfall
// document like every other v5-style field. Foil treatments (ripple foil,
// surge foil, …) live here — Scryfall models a treatment as a tag on the
// printing whose foil finish is the treated copy, not as a finish of its
// own. The column stores the raw JSON array; mapping tags to display
// words happens in Go (FoilTreatment), so a new WotC treatment needs a
// map entry, never a migration.
const promoTypesColumn = `
ALTER TABLE cards ADD COLUMN promo_types TEXT
    GENERATED ALWAYS AS (json_extract(raw_json,'$.promo_types')) VIRTUAL;`

// v17: the TCGplayer product id of the printing's treated (or etched)
// version. TCGplayer sells ripple/surge/textured foils and etched cards
// as separate products, and no price feed we read carries their prices —
// this id is the key to fetching them from TCGplayer's public catalog.
// From MTGJSON set files like mtgjson_uuid; NULL means never asked, empty
// means asked and no split product exists, so absence does not re-fetch
// the set file forever (the ck_url convention).
const tcgAltProductColumn = `
ALTER TABLE cards ADD COLUMN tcg_alt_product_id TEXT;`

// v18: key-value preferences that survive a restart — the penny filters'
// state first (owner's call: a floor moved during a session should still
// be the floor tomorrow). Values are strings; typing lives in Go, so a
// new preference needs a key, never a migration.
const settingsTable = `
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);`

// v19: the default binder's stored name becomes its display name. The row
// was created as 'Collection' and every reader substituted LooseName via a
// CASE (the old containerLabel), which made containers.name dead weight for
// this one row — and made the default binder unrenameable. With the name
// real, renames are ordinary UPDATEs and the CASE is gone. Frozen earlier
// paths (the legacy transform) still insert 'Collection'; they run before
// this one, which is the point.
const defaultBinderRealName = `
UPDATE containers SET name = 'Binder' WHERE source_id = '__collection__';`

// v20: the per-vendor product ids MTGJSON publishes beside the ones v14 and
// v17 already store. They exist to answer a question the comps table could
// not: is this vendor's price for the card actually held?
//
// A treated foil (ripple, surge, …) is one Scryfall printing, so every
// vendor's foil bucket is keyed the same — but the vendors do not all sell
// the same product under it. TCGplayer splits the treated foil into its own
// listing (tcg_alt_product_id, v17); Card Kingdom publishes exactly one foil
// id per printing, so its foil bucket is the treated foil; Manapool publishes
// no identifier at all, which is why an unverifiable Manapool quote is the one
// the comp sheet drops. Storing the ids makes that rule follow the feed rather
// than a hardcoded opinion: if Card Kingdom starts splitting, or MTGJSON adds
// a Manapool id, the answer changes with the data.
//
// From the MTGJSON set files the uuid resolver already downloads. NULL means
// never asked, empty means asked and the feed had none — the ck_url
// convention, so absence does not re-fetch the set file forever.
const vendorProductIDs = `
ALTER TABLE cards ADD COLUMN ck_foil_id TEXT;
ALTER TABLE cards ADD COLUMN ck_etched_id TEXT;
ALTER TABLE cards ADD COLUMN tcg_product_id TEXT;`

// v21: etched stops borrowing the foil price.
//
// hoard has valued an etched copy at the foil price since v1, because the
// catalog had one foil column and Scryfall's etched figure was folded into it
// on the way in. That was a simplification, and the rest of the tool has since
// outgrown it: the comps sheet reads the vendors' own etched bucket, because
// etched is a real bucket in the feed and not a synonym for foil. The
// portfolio was the last place still saying otherwise,
// so a card whose etched printing trades well away from its foil one was
// carried at the wrong number in every total hoard reports.
//
// The column is nullable and reads fall back to foil, so a printing the feed
// prices only as a foil is valued exactly as before. Nothing is backfilled: the
// next update-prices fills it from the response it was already fetching.
//
// tcg_etched_product_id lands here too, because it is the same mistake one
// layer down. tcg_alt_product_id (v17) held whichever id the set file offered —
// the alternative-foil one, or the etched one when there was no alt-foil — and
// the overlay wrote every series it fetched into the foil bucket. So for a
// printing with an etched product and no alt-foil product, the etched product's
// price filled holes in the foil series. Splitting the ids lets each series
// land in the bucket its id actually names. Same tri-state as v20: NULL is
// never asked, empty is asked and the feed had none.
const etchedPrices = `
ALTER TABLE cards ADD COLUMN price_usd_etched REAL;
ALTER TABLE cards ADD COLUMN tcg_etched_product_id TEXT;`

// v22: the printing's language, and the name as printed on it.
//
// Language has always been part of a hoard's identity without being visible in
// it — Scryfall mints a distinct id per language, so a Japanese printing and an
// English one were never the same row. What was missing was any way to read that
// back: an export could not say which of the two a holding was, and a query
// could not find one.
//
// Both are generated over the stored document, so they cost nothing and cannot
// disagree with it. printed_name is the name in the card's own script (大峨頭の兜),
// absent on an English printing where Scryfall omits it.
const cardLanguage = `
ALTER TABLE cards ADD COLUMN lang TEXT
    GENERATED ALWAYS AS (json_extract(raw_json,'$.lang')) VIRTUAL;
ALTER TABLE cards ADD COLUMN printed_name TEXT
    GENERATED ALWAYS AS (json_extract(raw_json,'$.printed_name')) VIRTUAL;`

// v23: a holding gets a condition, and its own identity.
//
// Two changes to one table, because it is the one table that cannot be
// re-downloaded and rebuilding it twice is two chances to lose it.
//
// **Condition.** Wear is a fact about one copy, and a holding was a counted
// bucket with nowhere to put one: four Sol Rings in a binder were a single row,
// so "one of these is played" had no way to be said. Splitting the bucket means
// condition has to distinguish rows, hence its place in the UNIQUE below.
//
// The vocabulary is MTGJSON's, which is TCGplayer's: nm, lp, mp, hp, dmg — the
// five conditions TcgplayerSkus.json publishes. 'unknown' is hoard's own and means
// nobody has said, which is the honest state for every row this migration
// touches and for every card a scanner will ever read: a camera cannot assess
// wear.
//
// It is deliberately not 'nm' — claiming near mint for an unassessed copy throws
// away the difference between checked and unchecked, and that difference cannot
// be recovered later. And deliberately not the empty string, which in a column
// somebody is reading by hand is indistinguishable from a NULL, a trimmed
// value, or a bug. A word says what it means.
//
// (Spelled out rather than written as a pair of straight quotes: gofmt reads
// that pair in a doc comment as the old godoc convention for a closing
// typographic quote and rewrites it to a single curly character, which is how
// this sentence came to rule out a character that is not a value at all. See
// internal/sourcehygiene.)
//
// **The surrogate id.** Board says where a copy sits, not what it is, so it
// stops being part of the key. A row now has its own identity, which makes a
// deck edit an ordinary UPDATE of an ordinary column rather than a delete and a
// re-insert under a new key. Board stays inside the UNIQUE all the same: a deck
// can hold four of a card in the main and two in the side, and collapsing those
// into one row would lose the split that Archidekt and the text importer both
// produce.
//
// The rebuild runs with foreign keys enforced, which is safe here only because
// nothing references card_entries — it points at containers and cards, and
// neither points back. A table with dependents would need the pragma window that
// apply's single transaction cannot give it.
// The old table is renamed aside and the new one created under the real name,
// rather than the other way round — SQLite rewrites a renamed table's stored DDL
// and quotes the new name, which would leave `CREATE TABLE "card_entries"` in
// the published schema for no reason. Same shape migrateLegacy uses.
const holdingCondition = `
ALTER TABLE card_entries RENAME TO card_entries_pre_v23;

CREATE TABLE card_entries (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    container_id INTEGER NOT NULL REFERENCES containers(id) ON DELETE CASCADE,
    scryfall_id  TEXT NOT NULL REFERENCES cards(scryfall_id),
    finish       TEXT NOT NULL DEFAULT 'nonfoil',
    condition    TEXT NOT NULL DEFAULT 'unknown',
    board        TEXT NOT NULL DEFAULT 'main',
    quantity     INTEGER NOT NULL,
    UNIQUE (container_id, scryfall_id, finish, condition, board)
);

INSERT INTO card_entries
    (container_id, scryfall_id, finish, condition, board, quantity)
SELECT container_id, scryfall_id, finish, 'unknown', board, quantity
FROM card_entries_pre_v23;

DROP TABLE card_entries_pre_v23;

CREATE INDEX IF NOT EXISTS card_entries_card_id ON card_entries(scryfall_id);`

// v24: the scanner's finish guesses, one row per commit nothing on the card
// chose.
//
// The scanner writes the nonfoil default when no finish marker reads, which
// keeps a session hands-free at the price of a silently wrong row for every
// foil it missed — 7 of one 25-card session, measured 2026-08-06. The row
// itself cannot say it was a guess: card_entries buckets copies by (printing,
// finish, condition), so a guessed copy merging into a bucket of verified ones
// has no cell to carry the doubt. The doubt is a fact about one commit, so it
// gets its own table: one row per guessed commit, deleted when evidence
// arrives — a later read that corrects the finish, or a human who checked the
// card.
//
// Not a column on card_entries, deliberately: a flag on a quantity bucket
// would claim every copy in the bucket, and clearing it on the next verified
// add of the same printing would lose the one guess it existed to remember.
const finishGuesses = `
CREATE TABLE finish_guesses (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    container_id INTEGER NOT NULL,
    scryfall_id  TEXT NOT NULL REFERENCES cards(scryfall_id),
    finish       TEXT NOT NULL,
    guessed_at   TEXT NOT NULL
);
CREATE INDEX finish_guesses_card ON finish_guesses(scryfall_id, finish);`

// v25: guesses recorded while the scan's destination was the default binder
// were filed under container 0 — the adder's "default" sentinel, not a real
// container id — so any join against containers silently dropped them.
// Re-point them at the collection container. The store now resolves the
// sentinel before writing (see RecordFinishGuess), so no new 0-rows appear.
// A guess can only follow a commit and a commit creates the collection row,
// so when there is anything to update the subquery finds its target.
const guessContainerRepoint = `
UPDATE finish_guesses
SET container_id = (SELECT id FROM containers
                    WHERE source = 'manual' AND source_id = '__collection__')
WHERE container_id = 0
  AND EXISTS (SELECT 1 FROM containers
              WHERE source = 'manual' AND source_id = '__collection__');`

// v26: finish_guesses gets the containers FK v24 left out.
//
// With container_id a bare integer, deleting a container left its guess rows
// behind forever: `hoard guessed` kept listing cards to go check in a binder
// that no longer existed. Same rename-aside rebuild shape as v23 (SQLite
// cannot add an FK in place), and safe under enforced foreign keys for the
// same reason — nothing references finish_guesses. Ids are copied so "newest
// first" ordering and ClearFinishGuess's newest-row rule keep meaning what
// they meant.
//
// The WHERE is the FK applied retroactively: a guess whose container was
// deleted before the constraint existed is exactly the row the CASCADE would
// have removed, and copying it across would abort the whole migration on the
// new table's own constraint.
const guessContainerFK = `
ALTER TABLE finish_guesses RENAME TO finish_guesses_pre_v26;

CREATE TABLE finish_guesses (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    container_id INTEGER NOT NULL REFERENCES containers(id) ON DELETE CASCADE,
    scryfall_id  TEXT NOT NULL REFERENCES cards(scryfall_id),
    finish       TEXT NOT NULL,
    guessed_at   TEXT NOT NULL
);

INSERT INTO finish_guesses (id, container_id, scryfall_id, finish, guessed_at)
SELECT id, container_id, scryfall_id, finish, guessed_at
FROM finish_guesses_pre_v26
WHERE container_id IN (SELECT id FROM containers);

DROP TABLE finish_guesses_pre_v26;

CREATE INDEX finish_guesses_card ON finish_guesses(scryfall_id, finish);`

// v27: one covering index for the browse filter's trait predicates.
//
// The v5 trait columns are VIRTUAL, so MatchingCardIDs' table scan re-parsed
// ~5.4 KB of raw_json per row to evaluate them — re-run on every filter
// keystroke, a visible stutter at a few thousand printings. An index over a
// virtual column stores the computed value, so with every column the WHERE
// can name in one index (plus scryfall_id, the only output), the whole query
// is an index scan of short strings and raw_json is never touched.
// One composite index rather than one per column: the planner only scans a
// single index, and any per-column index would send the remaining predicates
// back to the row — and to the JSON parse this exists to avoid. The cost
// model never picks this index on its own (substring LIKEs give it nothing
// to seek on), so MatchingCardIDs names it with INDEXED BY; renaming it here
// breaks that query loudly.
const traitFilterIndex = `
CREATE INDEX IF NOT EXISTS cards_trait_filter ON cards(
    type_line, artist, layout, set_name, rarity, cmc, color_identity, scryfall_id);`

// v28: percent watches. A watch can now name a movement rather than a price.
//
// The direction vocabulary extends — drop|rise beside under|over — rather than
// a kind column being added, because op already keys the uniqueness constraint
// and a second discriminator would let one printing carry two watches that both
// read "under", one in dollars and one in percent.
//
// threshold is deliberately not reused to carry the percent: it is rendered as
// money in six places, so a polymorphic column would print "$0.10" for a ten
// percent watch in every one of them, and $0.10 is a plausible enough threshold
// that the bug would survive review.
//
// All four columns take defaults and nothing is rewritten, so the watches
// already standing keep op in (under, over), read pct as zero, and take the
// absolute branch of Met exactly as before.
const percentWatches = `
ALTER TABLE watches ADD COLUMN pct           REAL    NOT NULL DEFAULT 0;
ALTER TABLE watches ADD COLUMN min_move      REAL    NOT NULL DEFAULT 0;
ALTER TABLE watches ADD COLUMN window_days   INTEGER NOT NULL DEFAULT 30;
ALTER TABLE watches ADD COLUMN last_fired_at TEXT    NOT NULL DEFAULT '';`

// v29: prices refused for contradicting the asks standing on the same product,
// and the figure substituted for each.
//
// This is the first table that can outrank Scryfall. Everywhere else the
// fallback only fills a NULL — card_prices_alt is consulted when the catalog
// said nothing — and that ordering is why a wrong Scryfall figure had no way to
// be challenged: a $0.56 surge foil against a $97.55 cheapest ask on its own
// TCGplayer product, carried into every total hoard reports. So this one is
// consulted first, and it earns that by never holding an opinion, only a
// correction hoard can point at a source for.
//
// Shaped like card_prices_alt — one row per printing, a column per finish —
// rather than a row per (printing, finish). The effective-price expressions
// project all three finishes at once with no finish in scope, so the row-per-
// finish form would need three joins in every query that values a card, where
// this needs one beside the fallback join that is already there.
//
// It carries the refused figure as well as the substitute because a correction
// the owner cannot inspect is indistinguishable from hoard inventing prices.
// Etched gets its own column, unlike card_prices_alt: the compromise there is
// that its source has no etched series to store, which does not apply here.
const contradictedPrices = `
CREATE TABLE IF NOT EXISTS card_price_overrides (
    scryfall_id        TEXT PRIMARY KEY REFERENCES cards(scryfall_id) ON DELETE CASCADE,
    price_usd          REAL,
    price_usd_foil     REAL,
    price_usd_etched   REAL,
    refused_usd        REAL,
    refused_usd_foil   REAL,
    refused_usd_etched REAL,
    source             TEXT NOT NULL,
    reason             TEXT NOT NULL,
    as_of              TEXT NOT NULL
);`

// v9: the hoard's total value over time, one row per observation. Per-card
// history answers "what did this card do"; a value chart needs "what did the
// whole hoard do", and deriving that on the fly would revalue every entry
// against every historical date on each render.
//
// The table's data half — seeding the series from existing history — is Go
// (seedValueSnapshots below), not SQL: a date-by-date revaluation is a linear
// sweep in code and a planner tarpit as a query. Two SQL formulations ran for
// minutes on a 55k-row history before this was accepted.
const valueSnapshots = `
CREATE TABLE value_snapshots (
    as_of  TEXT PRIMARY KEY,
    binder REAL NOT NULL,
    decks  REAL NOT NULL,
    total  REAL NOT NULL,
    source TEXT NOT NULL DEFAULT 'observed'
);`

// v10: price watches. In the database rather than a prefs file because a
// watch carries state — last_state is what makes an alert fire once on a
// crossing instead of every cron run it sits past the threshold — and
// because alert data must not inherit a config file's swallow-all-errors
// loading. One watch per card, finish and direction: re-adding replaces the
// threshold rather than stacking a duplicate alert.
const watchesTable = `
CREATE TABLE watches (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    scryfall_id TEXT NOT NULL REFERENCES cards(scryfall_id),
    display     TEXT NOT NULL,
    finish      TEXT NOT NULL,
    op          TEXT NOT NULL,
    threshold   REAL NOT NULL,
    created_at  TEXT NOT NULL,
    last_state  TEXT NOT NULL DEFAULT '',
    UNIQUE(scryfall_id, finish, op)
);`

// cardFaceDetails projects the fields the card-frame detail layout reads:
// the stat box (power/toughness for creatures, loyalty for planeswalkers),
// the flavor text, and the card image's URL for the terminals that can
// draw one. Same one-line-ALTER pattern as richCardData (v5): VIRTUAL
// columns over raw_json, so nothing is rebuilt and unenriched rows read
// NULL. Multi-faced cards COALESCE to face 0, the face the columns above
// already show; image_uris in particular is absent at the root on
// transform/modal_dfc layouts.
const cardFaceDetails = `
ALTER TABLE cards ADD COLUMN power TEXT
    GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.power'),
                                  json_extract(raw_json,'$.card_faces[0].power'))) VIRTUAL;
ALTER TABLE cards ADD COLUMN toughness TEXT
    GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.toughness'),
                                  json_extract(raw_json,'$.card_faces[0].toughness'))) VIRTUAL;
ALTER TABLE cards ADD COLUMN loyalty TEXT
    GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.loyalty'),
                                  json_extract(raw_json,'$.card_faces[0].loyalty'))) VIRTUAL;
ALTER TABLE cards ADD COLUMN flavor_text TEXT
    GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.flavor_text'),
                                  json_extract(raw_json,'$.card_faces[0].flavor_text'))) VIRTUAL;
ALTER TABLE cards ADD COLUMN image_uri TEXT
    GENERATED ALWAYS AS (COALESCE(json_extract(raw_json,'$.image_uris.normal'),
                                  json_extract(raw_json,'$.card_faces[0].image_uris.normal'))) VIRTUAL;`

// migrate brings the database up to schemaVersion, backing it up first.
//
// The backup fires before anything rewrites data — including the legacy
// single-table transform below — because a backup taken after a transform can
// only preserve the transform's mistakes.
func (s *Store) migrate(path string) error {
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}
	// A file from a newer hoard is refused, not written. Every migration so
	// far has been additive, but that is circumstance rather than contract,
	// and this is the one file that cannot be re-downloaded after an older
	// binary scribbles on it.
	if v > schemaVersion {
		return fmt.Errorf(
			"database schema is v%d but this hoard understands v%d. Upgrade hoard; an older build must not write here",
			v, schemaVersion)
	}
	if v == schemaVersion {
		return s.stampApplicationID()
	}

	// Every hoard predating versioning reports user_version 0, and three
	// states share that value: an empty file, a database from the original
	// single-table build, and a v1 database that was never marked. Only the
	// empty file is fresh — a database created moments ago has nothing worth
	// preserving, and backing it up would leave a stray file beside every new
	// hoard.
	fresh := false
	if v == 0 {
		var hasCards bool
		if err := s.db.QueryRow(`
SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='cards')`).Scan(&hasCards); err != nil {
			return fmt.Errorf("inspecting schema: %w", err)
		}
		fresh = !hasCards
	}
	if !fresh {
		if err := s.backup(path, v); err != nil {
			return err
		}
	}

	if v == 0 {
		// The original build's wide cards table, migrated in place — after the
		// backup above, so its pre-state survives on disk.
		if err := s.migrateLegacy(); err != nil {
			return err
		}
		// Safe on all three v0 states: creates what is missing, touches what
		// is not.
		if _, err := s.db.Exec(schemaV1); err != nil {
			return fmt.Errorf("initializing schema: %w", err)
		}
		if err := s.setVersion(1); err != nil {
			return err
		}
		v = 1
	}

	// apply hands the driver a multi-statement string and trusts it to run
	// every statement. v23 rebuilds card_entries inside one such string —
	// rename aside, recreate, copy, drop — so a driver change that silently
	// stopped after the first statement would commit an empty holdings table
	// and the loss would look like a clean upgrade. Holdings are the one
	// table that cannot be re-downloaded, and no shipped migration removes
	// rows from it, so a shrinking count is proof of a destroyed rebuild.
	// The count is captured here, after the legacy transform has filled the
	// table but before any migration rewrites it.
	entriesBefore := int64(-1)
	if v < 23 {
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM card_entries`).Scan(&entriesBefore); err != nil {
			entriesBefore = -1 // no table to guard (nothing was at risk)
		}
	}

	for _, m := range migrations {
		if m.Version <= v {
			continue
		}
		if err := s.apply(m); err != nil {
			return fmt.Errorf("migrating to schema v%d: %w", m.Version, err)
		}
		if m.Version == 9 {
			if err := s.seedValueSnapshots(); err != nil {
				return fmt.Errorf("seeding value snapshots: %w", err)
			}
		}
	}

	if entriesBefore >= 0 {
		var after int64
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM card_entries`).Scan(&after); err != nil {
			return fmt.Errorf("verifying holdings after migration: %w", err)
		}
		if after < entriesBefore {
			// The versions are stamped by now, but failing here stops anything
			// from writing to the gutted table and names the way back.
			return fmt.Errorf(
				"migration left card_entries with %d rows where it had %d; refusing to continue — restore the pre-migration backup beside the database",
				after, entriesBefore)
		}
	}
	return s.stampApplicationID()
}

// seedValueSnapshots reconstructs the value series from the price history
// already held: one estimated point per recorded date, valuing today's
// quantities at that day's prices. Estimates — the quantities are current,
// not historical — hence source='seeded'; genuine observations (written by
// RecordPrices from v9 on) say 'observed'.
//
// One ordered pass over history, totals maintained incrementally: when a
// printing's price changes, the running totals move by copies × delta. The
// day's last write wins, matching "the newest observation at or before the
// date" — the same rule the movers baseline uses.
func (s *Store) seedValueSnapshots() error {
	type key struct{ sid, pfinish string }
	type held struct{ binder, decks int }
	owned := map[key]held{}
	rows, err := s.db.Query(`
SELECT e.scryfall_id,
       CASE WHEN e.finish IN ('foil','etched') THEN 'foil' ELSE 'nonfoil' END AS pfinish,
       CASE WHEN ct.kind = '` + KindCollection + `' THEN 1 ELSE 0 END AS in_binder,
       SUM(e.quantity)
FROM card_entries e
JOIN containers ct ON ct.id = e.container_id
GROUP BY e.scryfall_id, pfinish, in_binder`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var k key
		var inBinder, copies int
		if err := rows.Scan(&k.sid, &k.pfinish, &inBinder, &copies); err != nil {
			rows.Close()
			return err
		}
		h := owned[k]
		if inBinder == 1 {
			h.binder += copies
		} else {
			h.decks += copies
		}
		owned[k] = h
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// RFC 3339 sorts lexicographically, so ORDER BY as_of is chronological
	// and substr(as_of, 1, 10) is the day.
	rows, err = s.db.Query(`
SELECT scryfall_id, finish, price_usd, substr(as_of, 1, 10)
FROM card_price_history
WHERE scryfall_id IN (SELECT scryfall_id FROM card_entries)
ORDER BY as_of`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type snapshot struct {
		day           string
		binder, decks float64
	}
	var snaps []snapshot
	cur := map[key]float64{}
	var binderTotal, decksTotal float64
	day := ""
	for rows.Next() {
		var k key
		var price float64
		var d string
		if err := rows.Scan(&k.sid, &k.pfinish, &price, &d); err != nil {
			return err
		}
		if day != "" && d != day {
			snaps = append(snaps, snapshot{day, binderTotal, decksTotal})
		}
		day = d
		h, ok := owned[k]
		if !ok {
			continue // held card, but not in this price finish
		}
		delta := price - cur[k]
		binderTotal += float64(h.binder) * delta
		decksTotal += float64(h.decks) * delta
		cur[k] = price
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if day != "" {
		snaps = append(snaps, snapshot{day, binderTotal, decksTotal})
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`
INSERT INTO value_snapshots (as_of, binder, decks, total, source)
VALUES (?, ?, ?, ?, 'seeded')`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, sn := range snaps {
		if _, err := stmt.Exec(sn.day+"T00:00:00Z", sn.binder, sn.decks,
			sn.binder+sn.decks); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// stampApplicationID writes hoard's magic into the file header, once. Failure
// to read the pragma is not worth failing Open over — the id is a courtesy to
// tooling, not a load-bearing invariant.
func (s *Store) stampApplicationID() error {
	var id int64
	if err := s.db.QueryRow(`PRAGMA application_id`).Scan(&id); err != nil {
		return nil
	}
	if id == applicationID {
		return nil
	}
	_, err := s.db.Exec(fmt.Sprintf(`PRAGMA application_id = %d`, applicationID))
	return err
}

// apply runs one migration and stamps its version in the same transaction.
// SQLite keeps user_version in the file header, so it rolls back with the rest
// and a half-applied migration cannot look complete.
func (s *Store) apply(m migration) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(m.Stmts); err != nil {
		return err
	}
	// PRAGMA does not take bind parameters, and Version is an int constant
	// from this file rather than anything a caller supplies.
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, m.Version)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) setVersion(v int) error {
	_, err := s.db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v))
	if err != nil {
		return fmt.Errorf("stamping schema version: %w", err)
	}
	return nil
}

// backup copies the database before a migration touches it.
//
// VACUUM INTO is used rather than copying the file: it is atomic, it accounts
// for any journal or WAL content, and it needs no second connection. A hoard is
// not re-downloadable, so this is worth the disk.
func (s *Store) backup(path string, fromVersion int) error {
	if path == "" || path == ":memory:" {
		return nil // in-memory databases, i.e. tests
	}
	dest := fmt.Sprintf("%s.bak-v%d-%s", path, fromVersion, time.Now().Format("20060102"))
	if _, err := os.Stat(dest); err == nil {
		return nil // already backed up at this version today
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	// VACUUM INTO refuses to overwrite, which is why the stat above matters.
	if _, err := s.db.Exec(`VACUUM INTO ?`, dest); err != nil {
		return fmt.Errorf("backing up database before migration: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Backed up database to %s before upgrading schema.\n", dest)
	pruneBackups(path, dest)
	return nil
}

// keptBackups is how many pre-migration snapshots survive a prune.
//
// One is the actual safety net — the state immediately before the migration you
// just ran. The extra two cover the case where a migration's damage is only
// noticed after the next one has already been applied.
const keptBackups = 3

// pruneBackups removes all but the most recent snapshots of one database, since
// they otherwise accumulate one per migration forever and are not small.
//
// keep is the current run's backup, never a removal candidate. The match is
// deliberately narrow — the exact `.bak-v<n>-<date>` shape written here — so a
// hand-made backup is not swept up by a cleanup nobody asked for. Best-effort:
// not worth failing a migration over.
func pruneBackups(dbPath, keep string) {
	dir := filepath.Dir(dbPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := filepath.Base(dbPath) + ".bak-v"

	// Ordered by the parsed version number, not by name: the version is not
	// zero-padded, so lexically bak-v10 < bak-v9 and a name sort would prune
	// the newer snapshot while keeping the older — the exact file the net
	// exists to preserve. The date (zero-padded, so string order is fine)
	// breaks ties for one version backed up on different days. A name whose
	// tail does not parse is not one of ours and is left alone.
	type backup struct {
		path    string
		version int
		date    string
	}
	var mine []backup
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		full := filepath.Join(dir, e.Name())
		if full == keep {
			continue
		}
		tail := strings.TrimPrefix(e.Name(), prefix)
		ver, date, ok := strings.Cut(tail, "-")
		if !ok {
			continue
		}
		v, err := strconv.Atoi(ver)
		if err != nil {
			continue
		}
		mine = append(mine, backup{full, v, date})
	}
	if len(mine) < keptBackups {
		return
	}

	slices.SortFunc(mine, func(a, b backup) int {
		if c := cmp.Compare(a.version, b.version); c != 0 {
			return c
		}
		return strings.Compare(a.date, b.date)
	})
	for _, old := range mine[:len(mine)-(keptBackups-1)] {
		os.Remove(old.path)
	}
}

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

// v30: delete price and bid history for finishes the printing does not come in.
//
// A vendor can file one product's price under a bucket the card has no copy of
// — Manapool quoted the surge-foil-only Aragorn and Arwen under `normal`, and
// the unpivot recorded it as a non-foil series at $125.04 beside the card's
// real foil series, drawn as a second sparkline for a copy that cannot exist.
// Five such series stood in a live hoard, two non-foil and three foil.
//
// This deletes from card_price_history, which the schema calls the one table
// worth protecting, and that is deliberate rather than careless. The rule here
// is not "this price looks wrong" — a wrong price is still a fact about what a
// vendor said, and those rows stay. It is "this printing has no such copy", so
// the series describes a card nobody can own and no query can correctly join
// to. Nothing values it, nothing charts it truthfully, and leaving it in place
// only preserves the ability to draw it again.
//
// Etched is left alone. It is stored under 'foil' wherever the printing has no
// etched price of its own (see effPriceEtched), so a literal reading of the
// finishes list would delete series that are working exactly as intended.
//
// A printing whose Scryfall document was never stored has an unknown finishes
// list and is skipped: only a positive denial may delete anything.
const purgePhantomFinishSeries = `
DELETE FROM card_price_history WHERE rowid IN (
  SELECT h.rowid FROM card_price_history h JOIN cards c ON c.scryfall_id = h.scryfall_id
   WHERE h.finish IN ('nonfoil','foil')
     AND json_extract(c.raw_json, '$.finishes') IS NOT NULL
     AND json_extract(c.raw_json, '$.finishes') NOT LIKE '%"' || h.finish || '"%'
     AND NOT (h.finish = 'foil'
              AND json_extract(c.raw_json, '$.finishes') LIKE '%"etched"%'));

DELETE FROM card_bid_history WHERE rowid IN (
  SELECT b.rowid FROM card_bid_history b JOIN cards c ON c.scryfall_id = b.scryfall_id
   WHERE b.finish IN ('nonfoil','foil')
     AND json_extract(c.raw_json, '$.finishes') IS NOT NULL
     AND json_extract(c.raw_json, '$.finishes') NOT LIKE '%"' || b.finish || '"%'
     AND NOT (b.finish = 'foil'
              AND json_extract(c.raw_json, '$.finishes') LIKE '%"etched"%'));`
