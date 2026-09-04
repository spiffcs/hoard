# Scripting

`--json` emits a versioned document instead of a table. `hoard schema` prints
the JSON Schema it validates against. The schema files are published under
[`schema/json/`](../schema/json/README.md), so a parser can pin a version.

## Documents can compose

`hoard export` chooses holdings and `hoard move` acts on them, so a bulk move
is a pipe. Filter terms are ANDed, so `set:` cannot name two sets at once.
Export each one and splice the rows together to sweep both into bulk in a
single move. Group the two exports so they share one pipe.

```fish
begin
    hoard export --binder Binder --json --filter 'price<1 set:cmd'
    hoard export --binder Binder --json --filter 'price<1 set:isd'
end | jq -s '.[0].holdings.rows = ([.[].holdings.rows] | add) | .[0]' \
    | hoard move --to Bulk --dry-run
```

```bash
{
    hoard export --binder Binder --json --filter 'price<1 set:cmd'
    hoard export --binder Binder --json --filter 'price<1 set:isd'
} | jq -s '.[0].holdings.rows = ([.[].holdings.rows] | add) | .[0]' \
    | hoard move --to Bulk --dry-run
```

Prints:
```
Would move 250 copies of 250 printings into "Bulk" · $87.77
```

`--dry-run` reports without writing, and the move asks before it writes unless
you pass `--yes`. Because the middle of that pipe is a published document, `jq`
narrows anything [`--filter`](filtering.md) cannot:

```console
$ hoard export --binder Binder --json \
  | jq '.holdings.rows |= map(select(.detail.artist == "Rebecca Guay"))' \
  | hoard move --to mood
```

Only binder cards move. Pipe `hoard export --all` and the rows belonging to
decks are skipped and counted, so a decklist is never touched.

## Reading the database directly

It is ordinary SQLite: read it with [any tool](../schema/sqlite/README.md), or
take versioned JSON out with `hoard export --format json`
([schema](../schema/json/README.md)).
