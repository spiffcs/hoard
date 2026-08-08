# hoard SQLite schema

`hoard.db` is a plain SQLite database with no extensions, no custom functions
and no encryption. Anything that can open a SQLite file can read a hoard —
`sqlite3`, Python's `sqlite3`, DBeaver, `datasette`, a Go or Rust driver.

The files here are the schema hoard creates, published so you can read the
format without running hoard. `schema-latest.sql` is always a byte-for-byte
copy of the newest versioned file.

## Rules

- **Generated, never hand-edited.** The source of truth is the migration list
  in `internal/store/migrate.go`; `make generate-sqlite-schema` migrates an
  empty database and dumps it, and a test
  (`internal/store/schemagen`) fails if the files drift from the migrations.
- **Released schema files are immutable.** Once a version has shipped, its
  `schema-v<N>.sql` is never edited or deleted. A migration is append-only and
  writes a new file, so a consumer holding an old database can always fetch the
  exact schema it was written against.
- **The version is on the file.** A migrated database carries it in
  `PRAGMA user_version`, and `PRAGMA application_id` is `0x484F5244` — the
  ASCII for `HORD`, which tells you a stray `.db` is hoard's before you open it.

```sh
sqlite3 hoard.db 'PRAGMA user_version; PRAGMA application_id;'
```

## Reading a hoard from another tool

Two joins get you everything. `card_entries` is what you own, `cards` is the
printing catalog, `containers` names the binder or deck:

```sql
SELECT ct.name AS container, ct.kind, c.name, c.set_code, c.collector_number,
       e.finish, e.board, e.quantity
FROM card_entries e
JOIN cards c      ON c.scryfall_id  = e.scryfall_id
JOIN containers ct ON ct.id          = e.container_id
ORDER BY ct.name, c.name;
```

`cards.scryfall_id` is a Scryfall UUID and `cards.mtgjson_uuid` an MTGJSON one,
so a query joins directly against either ecosystem's bulk data without matching
on names.

## Two databases, different promises

Only `hoard.db` is covered by the rules above.

| | `hoard.db` | `catalog.db` |
|---|---|---|
| holds | what you own, and price history | a copy of Scryfall's bulk card data |
| lives in | the user data directory | the user cache directory |
| on a schema change | migrated, backed up first | deleted and rebuilt |
| if you lose it | price history is gone for good | one download |

`catalog.db` is a cache. Its schema is a constant in
`internal/catalog/catalog.go` with no migrations and no published file, because
a version bump discards and rebuilds it. Do not build anything on it — read
`hoard.db`, or use `hoard export` and the `--json` documents in
[schema/json](../json/README.md).

## Related

- [schema/json/README.md](../json/README.md) — the versioned `--json` output.
