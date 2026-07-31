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

Everything the ecosystem does not model — containers, boards, counts, movers,
opportunities — is hoard's own, documented in the schema descriptions
(generated from the model's doc comments) and in `docs/json.md`.

Prices are USD throughout (`priceUsd`, `valueUsd`, …), matching the sources
they come from (Scryfall's `usd`, MTGJSON's TCGplayer/Card Kingdom feeds). An
absent price field means *unpriced*, never free.

## Changelog

- **1.0.0** — initial model: `summary`, `holdings`, `unpriced`, `movers`,
  `arbitrage` document kinds under one envelope.
