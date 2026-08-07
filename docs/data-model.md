# hoard's data model

What hoard stores, how a card is identified, and how to read it from something
that is not hoard.

The schema itself is published as SQL under
[schema/sqlite/](../schema/sqlite/README.md), generated from the migrations so
it cannot drift. This page is what the tables *mean*.

## Two databases, different promises

| | `hoard.db` | `catalog.db` |
|---|---|---|
| holds | what you own, and price history | a copy of Scryfall's bulk card data |
| lives in | the user data directory | the user cache directory |
| on a schema change | migrated, backed up first | deleted and rebuilt |
| if you lose it | price history is gone for good | one download |

`hoard.db` is the irreplaceable half, and is treated that way. Migrations apply
in order, each inside the transaction that stamps `PRAGMA user_version`, so a
half-applied migration cannot exist. A database written by a newer hoard is
refused rather than opened. Anything with data in it gets a `VACUUM INTO` backup
before a migration touches it — a database being created has nothing to preserve,
so a first run leaves no stray file. Durability is deliberate: `journal_mode` is
`DELETE` and `synchronous` is `FULL`, so every commit pays a real fsync.

`catalog.db` is a cache with no migrations at all — a schema bump discards it and
rebuilds from the next download. Build nothing on it. It exists so a lookup does
not need the network, and every byte is re-fetchable.

Only `hoard.db` has a stable published schema.

---

## Identity

This is the part most collection trackers get wrong, so it is worth stating
plainly.

### A printing is a Scryfall UUID

```sql
CREATE TABLE cards (
    scryfall_id      TEXT PRIMARY KEY,
    ...
```

Not a composite of `(set, collector_number, finish, language)`. Scryfall mints a
distinct id for every distinct printing, and hoard uses it directly.

That single choice is what makes the hard cases free. **Borderless,
extended-art, showcase, retro frame, promo, serialized and the Japanese
alternate-art printings are all separate Scryfall printings with their own id and
their own collector number** — so they are separate rows, priced separately,
counted separately, with no extra modelling. A borderless Sol Ring and the
mainline Sol Ring are no more related, as far as the schema is concerned, than
two different cards.

`set_code` and `collector_number` are carried beside the id for display and for
resolving a scan, but they are **not** the key and are not unique. Neither is the
name. `mtgjson_uuid` is a cached secondary mapping, indexed and nullable.

### A holding is a counted bucket

```sql
CREATE TABLE card_entries (
    container_id INTEGER NOT NULL REFERENCES containers(id) ON DELETE CASCADE,
    scryfall_id  TEXT NOT NULL REFERENCES cards(scryfall_id),
    finish       TEXT NOT NULL DEFAULT 'normal',
    board        TEXT NOT NULL DEFAULT 'main',
    quantity     INTEGER NOT NULL,
    PRIMARY KEY (container_id, scryfall_id, finish, board)
);
```

There is no row per physical card. A holding is *how many* of one printing, in
one finish, in one container, on one board. Every write is an upsert that adds to
the quantity, and moving a card between finishes or binders is insert-then-delete
so the two buckets merge rather than collide.

The consequence worth knowing: **there is nowhere to hang a fact about one
individual copy.** See "What hoard does not model" below.

---

## Finish and treatment are different axes

A frequent modelling error is to treat "surge foil" as a kind of foil. It is not,
in any source.

**Finish** is one of exactly three values, everywhere, forever:

```
nonfoil | foil | etched
```

Scryfall publishes these three across all ~107,000 paper printings and MTGJSON
publishes the same three. hoard stores the same three and validates them at a
single chokepoint (`validFinish` in `internal/store/holdings.go`), so a writer
added later cannot admit a fourth.

**Treatment** — ripple, surge, textured, gilded, neon ink, step-and-compleat — is
a `promo_types` tag on the printing, describing what that printing's *foil*
finish looks like. The nonfoil copy of a ripple-tagged printing is a plain card.
hoard derives the display word from the tag rather than storing it: any tag
ending in `foil` is a treatment, which is how WotC names them, plus a short list
for the ones that do not (`textured`, `embossed`, `gilded`, `neonink`, …).

**A variant printing** — borderless, extended art, showcase — is neither. It is a
different printing with a different Scryfall id, which is why it needs no axis at
all.

Why the distinction earns its keep: a treated foil is *one* Scryfall id, so every
vendor files a price under the same key while not necessarily selling the same
product under it. That is the one case where the Scryfall id alone is too coarse,
and it is why `cards` carries per-vendor product ids (`tcg_product_id`,
`tcg_alt_product_id`, `ck_foil_id`, `ck_etched_id`). See
[pricing.md](pricing.md#where-vendors-disagree).

---

## Vocabularies

| column | values |
|---|---|
| `card_entries.finish` | `nonfoil`, `foil`, `etched` |
| `card_entries.board` | `main`, `commander`, `side`, `maybe` |
| `containers.kind` | `collection`, `deck` |
| `containers.source` | `manual`, or the importing provider |

Two boundaries will surprise a reader querying the database directly.

**`normal` vs `nonfoil`.** hoard said `normal` until schema v8 and now says
`nonfoil`, matching Scryfall and MTGJSON. Migrations v8 and v12 renamed the
existing rows. MTGJSON's *price files* still say `normal`, so the translation
happens at that boundary and nowhere else. The `DEFAULT 'normal'` still visible
on `card_entries.finish` is inert — every writer supplies the finish explicitly —
and is left alone because migration text is frozen once released.

**`collection` vs `binder`.** `containers.kind` stores `collection`, but every
user-facing surface — the CSV's `Container Kind`, the JSON's `containerKind`, the
UI — says `binder`. Query the database and you will see `collection`.

---

## `raw_json` and generated columns

`cards.raw_json` holds the Scryfall card object exactly as Scryfall sent it,
about 5 KB per printing. Every descriptive column — `rarity`, `type_line`,
`mana_cost`, `oracle_text`, `artist`, `promo_types`, `color_identity` and the
rest — is a **VIRTUAL generated column** over it:

```sql
rarity TEXT GENERATED ALWAYS AS (json_extract(raw_json,'$.rarity')) VIRTUAL
```

They cost no storage, cannot fall out of sync with the document, and surfacing a
new Scryfall field is a one-line `ALTER` with no backfill and no rebuild.
Double-faced cards fall back to `card_faces[0]` where the top-level field is
absent.

Practical notes for anyone querying:

- These columns read `NULL` for a printing whose document has not been fetched
  yet. `raw_json IS NOT NULL` is the "is this card enriched" test.
- JSON-array columns (`promo_types`, `color_identity`) hold JSON text, not a
  delimited string: `'["ripplefoil"]'`. Use `json_each` to unpack them.
- Anything Scryfall publishes is already in `raw_json` even when no column
  exposes it — `frame_effects`, `border_color`, `full_art`, `lang`,
  `security_stamp`. `json_extract` reaches them today; they simply are not
  indexed.

---

## Prices

Four tables, all keyed by `scryfall_id`, all in USD.

| table | what it is |
|---|---|
| `cards.price_usd` / `price_usd_foil` | the latest Scryfall figures, overwritten each refresh |
| `card_prices_alt` | a fallback vendor's price, for printings Scryfall cannot price, with a per-finish source label |
| `card_price_history` | append-only observations, one row per printing-finish-date |
| `card_bid_history` | the same, for buylist bids |

Scryfall's USD figures come from TCGplayer alone, so a printing TCGplayer has no
record of is unpriced there; MTGJSON aggregates other vendors and often has it.
`card_prices_alt` is separate from `cards` because every refresh rewrites the
`cards` columns wholesale, and a fallback stored there would be nulled on the
next run.

**History is the one thing that cannot be re-fetched.** MTGJSON's archive reaches
back 90 days; everything older exists only in `card_price_history`. Nothing
deletes from `cards` today, so its `ON DELETE CASCADE` never fires — but any
future pruning of catalog rows must exempt printings with history.

A row is appended only when the price differs from the last observed one; a row
per card per day would grow the file by the size of the catalog daily.

**Absent is not zero.** A `NULL` price means unpriced. It never means free, and
summing it as zero silently understates a collection.

---

## What hoard does not model

Deliberate omissions, so you know they are choices rather than oversights.

**Condition.** Every copy is treated as near mint. A holding is a count, not a
row per card, so there is nowhere to record that one of your four is played.
Imports that carry a condition column count what they discarded and tell you
(`hoard import` reports it) rather than dropping it silently. The Moxfield export
writes `Near Mint` because Moxfield requires the column.

**Language.** Not stored. The catalog is built from Scryfall's `default_cards`
bundle — one row per printing, English where an English printing exists — so a
non-English printing of a card that also exists in English cannot currently be
resolved. Foreign-*only* printings, such as the War of the Spark Japanese
alternate-art planeswalkers, do have their own Scryfall id and collector number
and are handled normally. As with condition, an import counts non-English rows as
dropped rather than discarding them quietly.

**Purchase price and acquisition date.** Counted as dropped on import. hoard
values a collection at market, and has no cost-basis or P&L model.

**Per-vendor quote history.** Vendor quotes are re-derived each run and cached
for the day in a JSON file, not stored in SQLite. Only the derived price and bid
series persist.

---

## Reading a hoard from another tool

It is a plain SQLite file: no extensions, no custom functions, no encryption.

```sh
sqlite3 hoard.db 'PRAGMA user_version; PRAGMA application_id;'
```

`application_id` is `0x484F5244` — ASCII `HORD`.

Everything you own, in one query:

```sql
SELECT ct.name AS container, ct.kind, c.name, c.set_code, c.collector_number,
       e.finish, e.board, e.quantity, c.scryfall_id, c.mtgjson_uuid
FROM card_entries e
JOIN cards c       ON c.scryfall_id = e.scryfall_id
JOIN containers ct ON ct.id         = e.container_id
ORDER BY ct.name, c.name;
```

Value it the way hoard does. Three things matter, and dropping any of them gives
a number that quietly disagrees with `hoard report`: foil and etched both read
the foil column, a Scryfall price falls back to `card_prices_alt`, and a price
that is missing everywhere contributes nothing rather than zeroing the row:

```sql
SELECT SUM(e.quantity * COALESCE(
         CASE WHEN e.finish IN ('foil','etched')
              THEN COALESCE(c.price_usd_foil, a.price_usd_foil)
              ELSE COALESCE(c.price_usd,      a.price_usd) END, 0))
FROM card_entries e
JOIN cards c                ON c.scryfall_id = e.scryfall_id
LEFT JOIN card_prices_alt a ON a.scryfall_id = e.scryfall_id;
```

That is `entryValue` in `internal/store/store.go`, which every total in hoard is
built from. To see how much of the result is standing on a fallback rather than
Scryfall, `hoard report --json | jq .report.sources` breaks it down.

If you would rather not couple to the schema at all, `hoard export` writes CSV
([docs/csv.md](csv.md)) and every read command takes `--json`
([docs/json.md](json.md)), which is versioned and validated against a published
JSON Schema.

## Related

- [schema/sqlite/README.md](../schema/sqlite/README.md) — the generated DDL and
  its compatibility rules.
- [schema/json/README.md](../schema/json/README.md) — the versioned `--json` model.
- [docs/csv.md](csv.md) — CSV import and export.
- [docs/pricing.md](pricing.md) — where prices come from and how vendors are
  reconciled.
