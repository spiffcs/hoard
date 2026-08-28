# hoard JSON schemas

Every JSON document hoard emits (`--json`, `export --format json`) carries a
`schemaVersion` and validates against the file of that version here.
`schema-latest.json` is a byte-for-byte copy of the newest.

Print the schema your build follows:

```console
$ hoard schema | head -3
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://raw.githubusercontent.com/spiffcs/hoard/main/schema/json/schema-1.1.1.json",
```

## Rules

- **Generated, never hand-edited.** The source of truth is the Go model in
  `internal/hoardjson`; `make generate-json-schema` writes these files, and a
  test fails if they drift from the model.
- **Released files are immutable.** Once a version ships, its
  `schema-X.Y.Z.json` is never edited or deleted, so a consumer holding an old
  document can always fetch the schema it was written against.
- **Versioning is [SchemaVer](https://docs.snowplow.io/docs/api-reference/iglu/common-architecture/schemaver/)**
  (`MODEL.REVISION.ADDITION`). MODEL breaks existing consumers; REVISION
  reshapes compatibly but changes what you should conclude; ADDITION is purely
  additive.
- **MODEL is a compatibility kill switch, not a release size.** It is the only
  component enforced at read time (`hoardjson.Read` refuses a document whose
  MODEL exceeds the build's), so bumping it locks every installed hoard out of
  documents it would otherwise read correctly.

## Joining against other tools

hoard's documents are a holdings overlay on identifiers other tools already
speak, so compatibility comes from joining cleanly rather than from a standard:

| hoard field | joins against |
|---|---|
| `card.scryfallId` | Scryfall `id`; MTGJSON `identifiers.scryfallId` |
| `card.mtgjsonUuid` | MTGJSON `uuid` (absent when unmapped) |
| `card.setCode` | Scryfall `set`; MTGJSON `setCode` |
| `card.number` | Scryfall `collector_number`; MTGJSON `number` |
| `card.finish` | Scryfall `finishes`: `nonfoil` \| `foil` \| `etched` |
| `card.lang` | Scryfall `lang` |

Prices are USD throughout. An absent price field means *unpriced*, never free.

## Versions

- **1.0.0**: the first released schema: the `summary`, `holdings`, `unpriced`,
  `movers`, `market`, `report`, `watch` and `hoard` kinds, and the sixteen card
  characteristics under `holdings.rows[].detail`.
- **1.0.1**: percent watches: a watch can name a movement rather than a price.
- **1.1.0**: `oldAsOf` on a movers change. A REVISION, because `oldUsd` changed
  meaning underneath it: a movers window became a range to measure across
  rather than a bar a printing has to clear.
- **1.1.1**: the `watches`, `binders` and `guessed` kinds, plus `pctChange` on
  a movers change.
- **1.1.2**: the `refused` kind: prices hoard declined to report because the
  asks on the card's own listing contradicted them, and what it used instead.
- **1.1.3**: `settling` on a movers change, marking a row whose set is too new
  for its price to average over anything.
- **1.1.4**: `containerId` on a holdings row. A name can be renamed between a
  document being written and being read; an id cannot. It is meaningful only in
  the database that emitted the document.
- **1.2.0**: `paid` on a holdings row: what the holder recorded paying per copy.
  A REVISION rather than an ADDITION, because absent means *unrecorded* and not
  *free*, so anything summing the field has to know the difference.
- **1.2.1**: the `version` kind: the build behind a hoard, and the release it
  knows about. `version.update` is absent when the build is current, when no
  check has been made, and on a build hoard cannot advise on.

The reasoning behind each bump is in the `SchemaVersion` doc comment in
[internal/hoardjson/hoardjson.go](../../internal/hoardjson/hoardjson.go).
