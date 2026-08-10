# hoard JSON schemas

Every JSON document hoard emits (`--json`, `export --format json`) carries a
`schemaVersion` and validates against the schema file of the same version in
this directory. `schema-latest.json` is always a byte-for-byte copy of the
newest versioned file.

## Rules

- **Released schema files are immutable.** Once a version has shipped in a
  hoard release, its `schema-X.Y.Z.json` is never edited or deleted; a change
  to the model means a new version and a new file. A consumer holding an old
  document can always fetch the exact schema it was written against.
- **Generated, never hand-edited.** The source of truth is the Go model in
  `internal/hoardjson`; `make generate-json-schema` writes these files, and a
  test (`internal/hoardjson/schemagen`) fails if they drift from the model.
- **Versioning is [SchemaVer](https://docs.snowplow.io/docs/api-reference/iglu/common-architecture/schemaver/)**
  (`MODEL.REVISION.ADDITION`), which versions data shapes more precisely than
  semver versions code:
  - **MODEL** — breaking: a consumer of the old schema misreads new documents
    (field renamed or removed, meaning changed, vocabulary narrowed).
  - **REVISION** — compatible reshape: existing consumers keep working but
    should look (field newly optional, constraint loosened).
  - **ADDITION** — purely additive: a new optional field or document kind.
- **A new card characteristic — optional, absence meaning the card has no such
  value — is an ADDITION.** It is the recurring case here and the one the
  three definitions above leave a reader to infer.
- **Changing what an existing absence means is a REVISION.** A field that used
  to be omitted and now emits a value breaks no consumer, but it does change
  what one already reading the data concludes from it.
- **MODEL is a compatibility kill switch, not a release size.** It is the only
  component enforced at read time (`hoardjson.Read` refuses a document whose
  MODEL exceeds the build's), so bumping it locks every installed hoard out of
  documents it would otherwise read correctly. Spend it on renames, removals
  and meaning changes; never on a release that is merely large.

## Vocabulary: borrowed where the ecosystem has one

hoard's documents are a thin holdings overlay on identifiers other tools
already speak — there is no community standard for *collection* interchange,
so compatibility comes from joining cleanly against the card databases that
are standard:

| hoard field | joins against |
|---|---|
| `card.scryfallId` | Scryfall `id`; MTGJSON `identifiers.scryfallId` |
| `card.mtgjsonUuid` | MTGJSON `uuid` (absent when unmapped) |
| `card.setCode` | Scryfall `set`; MTGJSON `setCode` |
| `card.number` | Scryfall `collector_number`; MTGJSON `number` |
| `card.finish` | Scryfall `finishes` vocabulary: `nonfoil` \| `foil` \| `etched` |
| `card.lang` | Scryfall `lang`; MTGJSON `language` (spelled out there) |
| `card.condition` | MTGJSON `TcgplayerSkus[].condition` (spelled out there: `NEAR MINT`) |

Everything the ecosystem does not model — containers, boards, counts, movers,
opportunities — is hoard's own, documented in the schema descriptions
(generated from the model's doc comments).

Prices are USD throughout (`priceUsd`, `valueUsd`, …), matching the sources
they come from (Scryfall's `usd`, MTGJSON's TCGplayer/Card Kingdom feeds). An
absent price field means *unpriced*, never free.

## Changelog

- **1.0.0** — the first released schema: the `summary`, `holdings`,
  `unpriced`, `movers`, `market`, `report`, `watch` and `hoard` document kinds
  under one envelope, and `holdings.rows[].facts` — the sixteen card
  characteristics hoard derives from a printing's stored Scryfall document
  (rarity, type line, mana value and cost, oracle and flavor text, power,
  toughness, loyalty, set name, release date, artist, layout, promo types,
  printed name, TCGplayer product id). `facts` is absent when hoard has stored
  no document for the printing, a field inside it is absent when the card has
  no such value, and a `hoard` document never carries it at all — every
  printing there already embeds the same document verbatim in
  `printings[].raw`.

The model went through nine versions before this one. None of them is listed,
because none of them was ever released: the repository was private throughout,
the `$id` each of them names has never resolved, and no document carrying one
reached a consumer who could hold it against a schema. 1.0.0 is the first
version this file's immutability rule applies to.
