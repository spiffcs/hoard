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

- **1.1.4** — ADDITION: `card.condition` on the holdings document — the copies'
  wear (`nm`, `lp`, `mp`, `hp`, `dmg`), absent when nobody has assessed them.
  It describes the copies rather than the printing, so two holdings entries of
  one printing can differ by it, and it never affects a value: no source hoard
  reads publishes a per-condition price.
- **1.1.3** — ADDITION: `card.lang` (Scryfall's language code — `en`, `ja`,
  `zhs`; absent when hoard has not stored the card's document). Language is
  part of a printing's identity, since Scryfall mints a distinct id per
  language, so this names which one `scryfallId` refers to rather than adding
  a dimension to it.
- **1.1.2** — ADDITION: `market.comps` — every compared printing's
  per-vendor comp sheet (tcgplayer market, cardkingdom, manapool, the low
  ask, the buylist bid, and the spread as a fraction), ordered by value.
- **1.1.1** — ADDITION: `card.colorIdentity` (Scryfall `color_identity`
  letters; `[]` means colorless, absent means unknown to hoard) on the
  holdings, unpriced and movers card objects.
- **1.1.0** — REVISION: the market analysis re-anchored on tcgplayer's
  sales-derived market price — `marketUsd`/`belowMarket` replace
  `dearUsd`/`dearFrom`/`spread`; `ignoredListings` dropped.
- **1.0.2** — ADDITION: the `watch` document kind (one price-watch check:
  how many watches were evaluated, which just crossed their thresholds).
- **1.0.1** — ADDITION: the `report` document kind (dated valuation — totals,
  per-binder breakdown, top holdings, price-source coverage). Every 1.0.0
  document remains valid.
- **1.0.0** — initial model: `summary`, `holdings`, `unpriced`, `movers`,
  `arbitrage` document kinds under one envelope.
