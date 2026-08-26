# hoard SQLite schema

`hoard.db` is plain SQLite: no extensions, no custom functions, no encryption.
Anything that opens a SQLite file can read a hoard: `sqlite3`, Python's
`sqlite3`, DBeaver, `datasette`, a Go or Rust driver.

The files here are the schema hoard creates, published so you can read the
format without running hoard. `schema-latest.sql` is a byte-for-byte copy of
the newest versioned file.

```console
$ sqlite3 ~/Library/Application\ Support/hoard/hoard.db \
    'PRAGMA user_version; PRAGMA application_id;'
30
1213157956
```

`user_version` is the schema version. `application_id` is `0x484F5244`, ASCII
for `HORD`, which tells you a stray `.db` is hoard's before you open it.

## Reading a hoard from another tool

Two joins get you everything. `card_entries` is what you own, `cards` the
printing catalog, `containers` the binder or deck:

```sql
SELECT ct.name AS container, ct.kind, c.name, c.set_code, c.collector_number,
       e.finish, e.board, e.quantity
FROM card_entries e
JOIN cards c       ON c.scryfall_id = e.scryfall_id
JOIN containers ct ON ct.id         = e.container_id
ORDER BY ct.name, c.name;
```

`cards.scryfall_id` and `cards.mtgjson_uuid` join straight against either
ecosystem's bulk data, so nothing has to match on names.

## Rules

- **Generated, never hand-edited.** The source of truth is the migration list
  in `internal/store/migrate.go`; `make generate-sqlite-schema` migrates an
  empty database and dumps it, and a test fails if the files drift.
- **Not yet stable.** Until 1.0, migrations may be rewritten and released files
  replaced, so a database written by an older build is not guaranteed to open.
  Read `schema-latest.sql` and expect it to move.

## Do not build on catalog.db

Only `hoard.db` is covered by those rules.

| | `hoard.db` | `catalog.db` |
|---|---|---|
| holds | what you own, and price history | a copy of Scryfall's bulk card data |
| lives in | the user data directory | the user cache directory |
| on a schema change | migrated, backed up first | deleted and rebuilt |
| if you lose it | price history is gone for good | one download |

`catalog.db` is a cache: no migrations, no published schema, discarded on a
version bump. Read `hoard.db`, or use `hoard export` and the
[`--json` documents](../json/README.md).
