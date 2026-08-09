# `hoard merge` — folding one hoard into another

A hoard database is one hoard. There is no `collection_id` anywhere in the
schema, no multi-collection concept, and the only way to keep two collections
apart is to point `--db` / `$HOARD_DB` at two files. That is the right model —
but it left no way to change your mind. A bulk box catalogued separately, a
collection inherited on a thumb drive, the database from a second machine: all
of them were stuck where they were.

```sh
hoard merge other.db --dry-run    # what would happen
hoard merge other.db              # do it
```

## Why a document, not a SQLite splice

The obvious implementation is `ATTACH` and a pile of `INSERT ... SELECT`. It is
rejected here for three reasons.

**Rowids do not survive the trip.** `containers.id` is an `AUTOINCREMENT`
rowid, and `card_entries.container_id` points at it. Two hoards each have a
binder with id 1 and they are not the same binder. Anything spliced has to be
re-keyed anyway, so the "just copy the rows" simplicity is imaginary.

**The vetting lives above the SQL.** Binder names go through
`validateNewBinderName`, finishes through `validFinish`, watches through
`validateWatch`. An `INSERT ... SELECT` walks around all of it, and a merge is
exactly when you want it: the incoming rows were written by a *different build*
of hoard.

**A private path is an untested path.** The interchange document is the same one
`-o` hands the user and the same model every `--json` command emits, so it is
exercised by everything. A merge-only SQL path would be exercised by merges
alone.

So the candidate is read out as a `hoard` document, the document is read back,
and the plan is built from what was read. The round trip costs microseconds and
makes "the model carries everything a merge needs" a fact the code proves on
every run rather than a claim in a comment.

## What crosses

| | Carried | Notes |
|---|---|---|
| Holdings | ✅ | Binder and deck, with finish, condition and board |
| The card catalog | ✅ | Including `raw_json` — see below |
| Binders | ✅ | By name, empty ones included |
| Decks | ✅ | With `source`, `source_id`, `source_url`, `format` and boards |
| Watches | ✅ | Without last-fired state |
| Price history | ❌ | `card_price_history`, `card_bid_history` |
| Alt prices, price gaps | ❌ | `card_prices_alt`, `card_price_gaps` |
| Dated valuations | ❌ | `value_snapshots` — see below |
| Finish guesses | ❌ | `finish_guesses` |
| Settings | ❌ | `settings` |
| Import ledger | ❌ | `import_ledger` — see below |

**`raw_json` is the reason a merge is worth doing properly.** About twenty
columns in `cards` — `rarity`, `type_line`, `oracle_text`, `mana_cost`,
`artist`, `image_uri`, `color_identity`, `cmc` — are SQLite *generated VIRTUAL*
columns computed from that one blob. A merge that carried identity and prices
but not the document would leave every one of them empty for any printing the
receiving hoard had never seen. The cards would be held, counted and priced
correctly, and would read as blank everywhere else. So the document carries the
Scryfall response verbatim, which is also what makes the whole operation work
with no network at all.

**`value_snapshots` is not carried because it cannot be.** It is one hoard's
dated totals. Two series cannot be interleaved into a third that means
anything, and rows from the same day collide on the `as_of` primary key
regardless.

**`import_ledger` is not carried on purpose.** Inheriting the candidate's
hashes would make the receiving hoard refuse a file it never actually
imported.

## Conflicts

Nothing is guessed at silently; every rule below reports what it did.

| Entity | Rule |
|---|---|
| Printings | Upsert on `scryfall_id`. The **newer `updated_at` wins**, so merging an old database cannot drag fresh prices backwards. A card document is filled in even when the row loses the freshness contest — filling a `NULL` is never a downgrade. |
| Holdings | Accumulate on `(container, printing, finish, condition, board)`. Two hoards holding the same card means you own both lots. |
| Binders | Matched case-insensitively by name; unmatched names are created. |
| Decks | Identified by `(source, source_id)`. A deck already here is **left alone** and reported, because `UpsertDeck` replaces wholesale and would discard repins and conditions assessed by hand. `--replace-decks` opts in. |
| Watches | Identified by `(printing, finish, direction)`. An existing watch **keeps its own threshold** and is reported. `--replace-watches` opts in. |

### The two default binders necessarily become one

Both hoards have a default binder, and `store.ReservedBinderNames`
(`"Binder"`, `"Collection"`) always resolve to whichever binder is the default
here — a rule that exists so old exports round-trip after a rename. A merge
inherits it. If the other hoard's default binder still carries a reserved name,
its loose cards land in this hoard's default binder. The command says so out
loud rather than letting it be discovered later.

Give the binder a distinct name in the other hoard first if you want it kept
separate.

## Safety

**The candidate is never written to.** It is opened through
`store.OpenSource`, which sets `PRAGMA query_only(1)` — SQLite itself rejects a
write, so this is enforced rather than intended. `store.Open` is the wrong door
here: it migrates on open, and even at a matching version it creates the
singleton collection if one is missing.

**An out-of-date candidate is upgraded only if you say so.** Reading a database
at an older schema means migrating it, and migrating is a write to a file you
pointed at rather than opened. So `merge` asks, copies the file to
`<name>.premerge-v<N>` first, and — if the upgrade fails — puts the original
back and tells you plainly that your data is intact. Declining merges nothing.

**One transaction.** Catalog, binders, holdings, decks and watches land
together or not at all. Holdings accumulate, so a half-applied merge could not
be told apart from cards actually owned, and could not be safely re-run.

**Merging a hoard into itself is refused**, by `os.SameFile` rather than string
comparison, so two names for one file are still one file.

## The re-merge guard, and its limit

A merge is identified by the SHA-256 of its document bytes, recorded in
`import_ledger`. Merging an **unchanged** candidate twice is refused; `--again`
says the doubling is intentional.

The document therefore contains no path and no timestamp — only content. An
`exportedAt` field would change the hash on every run and silently disable the
guard. Provenance lives on the receipt instead.

**The limit is real and worth knowing.** Change the candidate at all — add one
card — and the hash moves, so a second merge adds *everything* again, not just
the new card. The guard catches the common repeat, not the dangerous one. Use
`--dry-run` before any re-merge, and note that hoard has no `restore`: copy your
database first if the merge is large.

## Known limitation: two decks with one name

A holdings row names its container by name, so two decks in the candidate that
share a name cannot be told apart. Rather than guess, `merge` refuses and asks
you to rename one. (hoard cannot address such a pair by name anywhere else
either — `containerByRef` takes the first exact-name match.)

## Where the code is

| | |
|---|---|
| `internal/hoardjson/hoardjson.go` | the `hoard` document kind and its payload |
| `internal/hoardjson/read.go` | `Read` / `ReadHoard` — the only consumer in the package |
| `internal/hoardjson/convert.go` | `FromSnapshot` |
| `internal/store/source.go` | `FileVersion`, `OpenSource` — reading without writing |
| `internal/store/snapshot.go` | `Snapshot` — the catalog, containers and watches |
| `internal/store/merge.go` | `ApplyMerge` — one transaction |
| `internal/store/printings.go` | `mergePrintingsTx` — the newer-wins upsert |
| `internal/action/merge.go` | `MergeHoard` and `planMerge` — every conflict rule |
| `internal/command/merge.go` | the command and its report |
