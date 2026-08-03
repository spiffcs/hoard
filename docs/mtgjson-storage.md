# Should hoard store its data as MTGJSON?

**Status:** historical design record · **Date:** 2026-07-28, amended 2026-07-30
and 2026-08-02

This document is kept for its decision history (the alternatives weighed in
§A–E); its "as built" claims describe late July and the code has moved on.
Corrections as of 2026-08-02: the schema is at **v15** (`card_bid_history` v13,
`tcgplayer_id` v14, `ck_url`/`ck_foil_url` v15 — three migrations after §D's
"no migration was needed"); the backfill ingests **Card Kingdom bids** as well
as TCGplayer retail; the `arbitrage` command is now `market`; the migration
runner lives in `internal/store/migrate.go` (not store.go) and the export/price
commands split out of main.go into per-command files. Current behavior is
documented in [pricing.md](pricing.md) and [browsing.md](browsing.md).

The migration runner (§1), richer card data (§2) and price history (§3) all
shipped, along with a 90-day MTGJSON backfill that this document originally
declined — see §D for what changed and what did not. Exports (§4) and offline
(§5) remain proposals.

§2 shipped as migration v5 rather than v2, and it is what the interactive browser
filters on: without the generated columns there is no rarity or type to filter
by. Two notes from building it. `card_faces[0]` fallbacks are load-bearing for
`mana_cost` and `oracle_text` — on a 1,573-card collection they rescue 9 and 11
cards respectively, all transform and flip layouts — but the `type_line` fallback
never fires, because Scryfall does supply a combined top-level value. And rarity
has to be matched exactly rather than by substring: "common" is a substring of
"uncommon", so a LIKE returns the opposite of what was asked.

§1's `SetMaxOpenConns(1)` and `busy_timeout` were already in `Open` by the time
this was re-read; that item is stale.

A design investigation, written to be read before any of it is built. All sizes
and API facts below were measured live on the date above; re-check them if this
sits for a while.

## Context

hoard stores 8 columns per card (`scryfall_id`, `set_code`, `collector_number`,
`name`, `price_usd`, `price_usd_foil`, `scryfall_url`, `updated_at`) and nothing
else. The question raised was whether to adopt [MTGJSON](https://mtgjson.com/)
as the storage or export format, driven by four goals: **richer card data**,
**export/interop**, **price history**, and **offline operation**.

Investigation says MTGJSON is the wrong answer for all four, but the goals are
real and three of them are cheap. The surprise is where the data already is:
Scryfall returns **63 top-level fields per card** and hoard decodes 13 of them,
dropping `rarity`, `type_line`, `cmc`, `oracle_id`, `tcgplayer_id`,
`cardmarket_id`, `legalities` and the rest at `json.Unmarshal`. Most of "richer
card data" is a parsing bug, not a missing data source.

This plan does the cheap work first, and deliberately shapes the price schema so
MTGJSON can be added later as inserts rather than a fifth migration.

## The three questions, answered

**Does Scryfall support MTGJSON?** No. MTGJSON *consumes* Scryfall (plus Gatherer
and TCGplayer), rebuilding nightly. The only bridge is `identifiers.scryfallId`,
which happens to be hoard's primary key.

**Should we store everything as MTGJSON?** No. MTGJSON is a reference catalogue
with no ownership model — there is nowhere to record "I own 3 of these." Only its
`Deck` model has quantity (`CardDeck.count`/`isFoil`), and that embeds ~130
properties of full card data.

**Should we redesign the store to export MTGJSON?** No. The store's *shape* is
already right; MTGJSON can't express it. And no collection tracker imports
MTGJSON — Moxfield, Deckbox, Archidekt, Manabox and MTGGoldfish all interchange
via CSV and plain text decklists.

### Measured, on the wire (compressed, as actually downloaded)

| | wire | on disk |
|---|---|---|
| MTGJSON `AllPrintings.sqlite.xz` | 119.1 MB | ~666 MB, queryable |
| MTGJSON `AllPrices.json.xz` (90d) | 44.4 MB | ~1.2 GB |
| MTGJSON `AllPricesToday.json.xz` | 4.1 MB | 50 MB |
| Scryfall `default_cards.jsonl.gz` | 77.0 MB | ~616 MB, JSONL |
| hoard.db today | — | ~471 KB |

Both catalogues are the same order of magnitude. The real distinction is
**format**: JSONL streams, so hoard can `gzip.NewReader` → decode → keep only
wanted columns, never landing 616 MB. A `.sqlite` must exist in full before one
row can be read.

**What MTGJSON uniquely has:** buylist prices and multi-provider breadth
(Card Kingdom, TCGplayer, Cardmarket, Manapool; retail *and* buylist). Scryfall's
`prices` is retail-only: `{usd, usd_foil, usd_etched, eur, eur_foil, tix}`. For a
valuation tool, "what could I sell this for" is the more honest number and
Scryfall cannot provide it at any price.

**What MTGJSON does not uniquely have:** cross-market IDs. Verified live —
Scryfall returns `tcgplayer_id: 236137`, `cardmarket_id: 559712`, `mtgo_id`,
`multiverse_ids`, `oracle_id` on the call hoard already makes and discards.

## Decision

Do not adopt MTGJSON now. Do this instead, in order:

1. Delete dead code, then build a real migration runner (no schema change).
2. Store the raw Scryfall JSON and expose fields as generated columns.
3. Add an append-only price history table shaped for multi-source data.
4. Add the exports people can actually import.
5. Revisit MTGJSON **only** for buylist prices.

---

## 1. Groundwork

**Delete five dead store methods.** `SetCollectionQuantities`,
`RemoveFromCollection`, `FindCollectionCard`, `TotalsByCard`, `CollectionValue`
have no non-test callers since the CLI trim. ~120 lines that would otherwise need
migrating. Their tests go too.

**`internal/store/store.go` `Open`:** add `db.SetMaxOpenConns(1)` and
`_pragma=busy_timeout(5000)`. Required for correctness, not tuning: `database/sql`
pools connections, so a `PRAGMA foreign_keys=OFF` during a table rebuild can land
on a different connection than the following `Exec`. Single-user CLI, so pinning
to one connection also eliminates `SQLITE_BUSY`.

**Replace the `schema` const with a migration list.** The current
`CREATE TABLE IF NOT EXISTS` blob makes `ALTER`-shaped changes silent no-ops on
existing databases. Remove the possibility rather than working around it:

```go
type migration struct {
    Version int
    Stmts   string // frozen once shipped; never references a moving const
}
var migrations = []migration{
    {1, schemaV1},      // today's 3 tables verbatim, IF NOT EXISTS removed
    {2, richCardData},
    {3, priceHistory},
}
```

Every database in the wild reports `PRAGMA user_version = 0`, and three states
share that value, so bootstrap must disambiguate before stamping:

- no `cards` table → fresh, replay everything
- `cards` has `qty_normal` → run the frozen legacy transform, stamp 1
- otherwise → already v1, **stamp only, change nothing**

Then `VACUUM INTO 'hoard.db.bak-v<n>-<date>'` before applying anything, and apply
each migration in a transaction that also sets `user_version` (it lives in the
header and rolls back with the tx). Note `PRAGMA user_version = ?` rejects bind
parameters; `fmt.Sprintf` the int.

Also freeze `migrateLegacy`: it currently does `tx.Exec(schema)`, so the moment
columns are added the historical migration starts building a *future* schema.

## 2. Richer card data (migration v2)

The key move: **keep the bytes**. `internal/scryfall/scryfall.go` discards the
response at unmarshal time. Capture it as `json.RawMessage`, store it, and derive
fields from it.

```sql
ALTER TABLE cards ADD COLUMN raw_json         TEXT;
ALTER TABLE cards ADD COLUMN price_usd_etched REAL;

ALTER TABLE cards ADD COLUMN rarity    TEXT GENERATED ALWAYS AS (json_extract(raw_json,'$.rarity'))    VIRTUAL;
ALTER TABLE cards ADD COLUMN type_line TEXT GENERATED ALWAYS AS (json_extract(raw_json,'$.type_line')) VIRTUAL;
ALTER TABLE cards ADD COLUMN cmc       REAL GENERATED ALWAYS AS (json_extract(raw_json,'$.cmc'))       VIRTUAL;
ALTER TABLE cards ADD COLUMN set_name  TEXT GENERATED ALWAYS AS (json_extract(raw_json,'$.set_name'))  VIRTUAL;
ALTER TABLE cards ADD COLUMN oracle_id TEXT GENERATED ALWAYS AS (json_extract(raw_json,'$.oracle_id')) VIRTUAL;

CREATE INDEX cards_name           ON cards(name);
CREATE INDEX card_entries_card_id ON card_entries(scryfall_id);
```

SQLite permits `ADD COLUMN` for **VIRTUAL** generated columns, so every future
field is a one-line ALTER with no backfill and no re-download. This retires the
"each new field needs a hand-written migration plus a sniff" problem permanently.
Cost: ~1,170 rows × ~5.4 KB ≈ **6 MB**, against a 471 KB database. Both
`json_extract` and generated columns are far below `modernc.org/sqlite v1.54.0`.

The second index matters independently — `card_entries` has no index on
`scryfall_id` today, so joins from `cards` full-scan.

**Fix etched in the same migration, completely.** Doing the price column alone
makes things worse. `EntryView.Price()` and four SQL queries use a two-way
`CASE WHEN finish IN ('foil','etched')`, and `ListCollection` pivots etched into
`QtyFoil`. Widen the `CASE` to three-way *and* the pivot together, or `list` will
show an etched card in the FOIL column at the correct etched price.

Existing data can't be repaired: `price_usd_foil` currently holds either a foil or
an etched price with no marker. The migration should say so and prompt for
`update-prices`. (Live DB: exactly one etched entry, so the blast radius is tiny.)

## 3. Price history (migration v3)

hoard overwrites `price_usd` on every `update-prices`, so history is destroyed
daily. No external source is needed to fix that.

```sql
CREATE TABLE card_prices (
  scryfall_id TEXT NOT NULL REFERENCES cards(scryfall_id) ON DELETE CASCADE,
  as_of       TEXT NOT NULL,                  -- 'YYYY-MM-DD'
  source      TEXT NOT NULL,                  -- 'scryfall' | 'tcgplayer' | 'cardkingdom' | ...
  kind        TEXT NOT NULL DEFAULT 'retail', -- 'retail' | 'buylist'
  finish      TEXT NOT NULL,                  -- 'nonfoil' | 'foil' | 'etched' (v8 renamed 'normal')
  currency    TEXT NOT NULL DEFAULT 'USD',
  price       REAL NOT NULL,
  PRIMARY KEY (scryfall_id, as_of, source, kind, finish, currency)
) WITHOUT ROWID;

CREATE INDEX card_prices_as_of ON card_prices(as_of);
```

Four deliberate choices: `finish` is a key so the etched bug becomes
unrepresentable; `source`/`kind`/`currency` exist from day one so **adding
MTGJSON later is inserts, not a migration**; keying on `as_of` *date* stops a
100-card `deck add` from writing 100 spurious history points, since
`upsertCatalogTx` runs on every add; `WITHOUT ROWID` for a wide composite key.

Keep `cards.price_usd*` as a denormalized latest-value cache so the valuation
queries need only their `CASE` widened, not a rewrite.

**Add staleness auto-refresh**, or history stays sparse — `update-prices` only
runs when typed, which yields perhaps a dozen irregular points per quarter. On any
command, if the newest `as_of` is older than today and the last refresh was >20h
ago, refresh (≈16 batched requests for ~1,170 cards).

## 4. Exports

Three separate features; only the first two are new code.

**Text decklist emitter — do this first.** `internal/decksource/textlist.go` is a
parser with no emitter. It already understands
`1 Ulamog, the Infinite Gyre (UMA) 7 *F*` and `Commander`/`Sideboard`/`Maybeboard`
headers. ~40 lines gives the universal deck format every site imports, plus a free
round-trip property test: `ParseText(Emit(deck)) == deck`. Highest value per line
in this plan; 22 of 23 containers are decks.

**Collection CSV.** The real Moxfield header is
`Count, Tradelist Count, Name, Edition, Condition, Language, Foil, Tags, Last Modified, Collector Number, Alter, Proxy, Purchase Price`
— note **`Edition`**, not "Set Code"; their importer matches on header names, so
this must be verified against a live import before shipping. hoard has no
condition, language, or purchase price, so those emit blank and a round trip is
knowingly lossy. Document that.

**JSON backup.** A versioned dump of containers + entries + catalog. This is what
makes future schema changes safe.

## 5. Offline — scope it before building it

Split the requirement:

- **Offline for cards you already own** needs no download at all, only steps 2–3.
  This is most of the value.
- **Offline acquisition** needs the 77 MB `default_cards.jsonl.gz`, and the hard
  part is not the download. `NamedFuzzy` is a *server-side Scryfall algorithm*,
  and the camera scanner depends on it: OCR text → `NamedFuzzy` → card. Neither
  MTGJSON nor Scryfall's bulk files ship it, so going offline means writing a name
  matcher and accepting a worse scan hit-rate.

The good news is architectural: `internal/tui/tui.go:18` already defines
`Searcher` (`Autocomplete`, `SearchPrints`, `NamedFuzzy`), so an offline
implementation is a drop-in with no TUI changes, and `model_test.go` already
drives the model through a fake. Treat any local catalogue as a **cache, never an
authority** — on miss, fall through to the API — which keeps `hoard add` working
for cards newer than the snapshot regardless of source.

## Sequencing

1. Delete dead methods; `SetMaxOpenConns(1)` + `busy_timeout`.
2. Migration runner, `user_version` bootstrap, `VACUUM INTO` backup. **No schema
   change** — v1 is a stamp. Land and test in isolation.
3. v2: `raw_json`, generated columns, indexes, three-way etched fix.
4. v3: `card_prices`, write-on-upsert, staleness refresh.
5. Exports: decklist emitter → collection CSV → JSON backup.
6. Reopen MTGJSON for buylist prices only.

## Verification

```sh
go build ./... && go vet ./... && go test ./...
```

- **Migration equivalence (the critical test).** Build three databases — legacy
  (`qty_normal`), current-modern-unstamped, and fresh — migrate all three, assert
  identical `sqlite_master` dumps and identical `user_version`.
  `TestLegacyMigration` in `internal/store/store_test.go` is ~90% of the harness
  already.
- **Backup on a real hoard.** Copy the live DB aside, migrate, confirm
  `hoard.db.bak-*` appears and `summary` totals match pre-migration exactly.
- **Etched.** Insert a card with distinct foil and etched prices; assert `list`
  shows it in its own column and `summary` values it at the etched price.
- **History.** Run `update-prices` twice in one day, assert one row per
  (card, finish); import a 100-card deck, assert no new price rows.
- **Round trip.** `ParseText(Emit(deck))` equals the original for all 22 decks.
- **CSV.** Import the output into a real Moxfield account before calling it done.

## Alternatives considered

Recorded in full so the reasoning survives if someone reopens this in six months.

### A. Adopt MTGJSON's schema fully — rejected

`hoard.db` becomes `AllPrintings.sqlite` (`meta`, `sets`, `cards`, `tokens`,
`cardIdentifiers`, `cardLegalities`, `cardRulings`, `cardForeignData`,
`cardPurchaseUrls`, `tokenIdentifiers`, `setTranslations`, four `setBooster*`
tables) with `containers` and `card_entries` grafted on and re-keyed to `uuid`.

- ~666 MB on disk for ~1,170 owned cards, against 471 KB today.
- Refresh means downloading a whole new file nightly and transplanting your two
  tables into it. Your data becomes the thing that must survive someone else's
  build.
- The schema is **generated by scanning the data each build**
  (`_generate_sql_schema_dict` infers column types from the cards themselves), so
  columns can change without deprecation. This is the strongest single argument
  against MTGJSON and applies to the sidecar option too.
- Prices aren't included; `cardPrices` comes from a separate `AllPricesToday`.
- A foreign key to a static catalogue would break `hoard add` for any card newer
  than the snapshot, gutting the camera-scan workflow.
- Re-keying `card_entries` from `scryfall_id` to MTGJSON's self-generated v5
  `uuid` is a one-way door.

### B. Sidecar, attached read-only — viable, declined for now

```sql
ATTACH 'mtgjson-ref.sqlite' AS ref;
SELECT c.name, mc.rarity, i.cardKingdomId
  FROM cards c
  JOIN ref.cardIdentifiers i ON i.scryfallId = c.scryfall_id
  JOIN ref.cards mc          ON mc.uuid = i.uuid;
```

Much better than (A), and most objections to (A) evaporate: refresh is `mv` one
file, hoard's data never moves, and **SQLite forbids foreign keys across attached
databases**, so it structurally cannot break `add`. Declined because after steps
2–3 the only remaining enrichment is rulings, foreign-language names, booster
contents and `cardKingdomId`. A bad trade for 119 MB, not an absurd one. Residual
risks if revisited: `cardIdentifiers.scryfallId` may not be indexed in the
generated schema and you cannot add an index to a snapshot you're about to
replace; and `modernc.org/sqlite` would be opening a file produced by CPython's
SQLite build, which is an untested surface for a project whose pitch is "pure Go."

### C. Ingest Scryfall bulk data — deferred into the offline decision

`default_cards.jsonl.gz` (77 MB) streams, so hoard can decode line by line and
keep only wanted columns without ever landing ~616 MB. This is the right
mechanism *if* offline acquisition is wanted; see §5 for why the download is the
easy part.

### D. 90-day price backfill — declined for portfolios, since **done** for per-card trends

*Originally declined; revisited and partly built as `hoard backfill-prices`. The
original reasoning, and what changed, both below.*

Declined because backfilled prices for cards you did not own then produce a
**counterfactual portfolio** — "your collection was worth $3,200 in April" is
false if half of it was bought in June. Charting that would ship a lying graph.
Per-*card* trends were noted as a legitimate reason to revisit, since those are
true regardless of ownership.

`hoard movers` turned out to be exactly that carve-out, so the backfill was
built for it. `ownedByPriceFinish` has no time dimension: it multiplies past
*prices* by *current* quantities, and never asserts what the hoard was worth on
any past date. **The counterfactual objection still stands in full for any
collection-value-over-time chart**, and that remains unbuilt for this reason.

Two cost notes from the original assessment need correcting:

- The uuid↔scryfallId map is no longer a cost at all. Migration 3
  (`cacheMTGJSONIDs`) caches `cards.mtgjson_uuid`, which was already populated
  for ~85% of the live catalog by ordinary `update-prices` and `arbitrage` runs;
  `resolveMTGJSONIDs` fills the remainder from per-set files it keeps
  permanently. Neither `AllIdentifiers` (105.8 MB) nor a ~70 MB sweep of set
  files is needed.
- **44.4 MB was the `.xz`.** `internal/mtgjson` only decompresses gzip, and
  `AllPrices.json.gz` is **148.6 MB** — measured 2026-07-29. Still affordable for
  a one-off, but more than three times the figure quoted here, and the reason the
  backfill is its own command rather than part of `update-prices`.

As built: TCGplayer retail only, because Scryfall's USD comes from TCGplayer and
any other vendor's series would step at the join; observations compacted to the
days a price actually moved; bounded to dates strictly before existing history so
imported and observed prices never describe the same day; and `ON CONFLICT DO
NOTHING`, so an observation always outranks a reconstruction. No migration was
needed — `card_price_history.source` already took a vendor name.

### E. Export decks as MTGJSON — rejected

No consumer. MTGJSON's downstream users (Cockatrice's card database, Forge,
analytics tooling) consume it as reference data, not as a user's collection.
Collection trackers interchange via CSV and plain text decklists. A conformant
`CardDeck` also carries ~130 properties of embedded card data, so a valid export
would require the full catalogue anyway.

## Open questions

Things not settled during the investigation; each needs a decision or a live check
before the relevant step starts.

1. **Verify the Moxfield CSV header against a real import.** Their importer
   matches on header names and the column is `Edition`, not "Set Code" — a wrong
   header silently no-matches the field that matters most. Blocks step 4.
2. **Does `condition` belong in `card_entries`' primary key?** If you ever want to
   hold an NM and a played copy of the same printing separately, it must be in the
   key. Adding it as a plain column later means a full table rebuild plus updating
   three `ON CONFLICT (container_id, scryfall_id, finish, board)` clauses. This is
   a one-way door; decide before step 3.
3. **Retention policy for `card_prices`.** ~1,170 cards × ~1.5 priced finishes ×
   365 days ≈ 640k rows/year from Scryfall alone (~30–50 MB/yr), and ~8x that if
   MTGJSON's providers are added. Design the policy now (daily for 90 days, weekly
   beyond), implement later.
4. **Is offline acquisition worth a homegrown fuzzy matcher?** The download is
   easy; replacing Scryfall's server-side `NamedFuzzy` is not, and the camera
   scanner's hit-rate depends on it. Possibly the answer is "offline for cards you
   own, online to acquire," which needs no catalogue at all.
5. **Should `raw_json` be compressed?** ~5.4 KB/card uncompressed. Fine at this
   scale; revisit only if the collection grows an order of magnitude.

## Critical files

- `internal/store/store.go` — migration runner, schema, etched `CASE` ×4, dead code
- `internal/scryfall/scryfall.go` — capture `json.RawMessage`; already returns the
  cross-market IDs
- `internal/decksource/textlist.go` — add the emitter beside the parser
- `internal/store/store_test.go` — extend `TestLegacyMigration`
- `main.go` — `update-prices` staleness check, export commands
