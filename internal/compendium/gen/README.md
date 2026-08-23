# The compendium generator

`hoard` normally opens *your* collection. This generator builds a different kind
of database — a **compendium**: every printing that matches a filter, one copy of
each, priced and backfilled. Point the browser at it and you can read a slice of
Magic you do not own exactly the way you read your own hoard.

Useful for pricing and building decks in a format before you own any of it.

## Build one

```console
$ task compendium -- -rarity mythic,rare -since 2020 mythics-rare.db
  seeding printings: 5,000
  …
  seeding printings: 31,912
  mapping card ids: fetching every set's identifiers from MTGJSON in one file
  mapping card ids: 31,895
  downloading price history: fetching 30 days of prices for 31,912 printings from MTGJSON (a large download)

seeded 31,912 printings, 48,049 entries
mapped 31,895 to MTGJSON (17 unmapped, so unpriced)
backfilled 469,573 observations and 224,925 bids over 30 days

browse it with:  HOARD_DB=mythics-rare.db hoard
it is stamped read-only: hoard refuses vacuum and every other write
```

The task just forwards its arguments, so this is the same thing:

```sh
go run ./internal/compendium/gen -rarity mythic,rare -since 2020 mythics-rare.db
```

Then browse it:

```sh
HOARD_DB=mythics-rare.db hoard
# or
hoard --db mythics-rare.db
```

That build takes about 40 seconds on a fast connection and lands around 300 MB.
Both grow as you widen the filter and as you raise `-days`.

## Flags

| Flag | Effect |
|---|---|
| `-rarity` | `common`, `uncommon`, `rare`, `special`, `mythic`, `bonus` — comma-separated. Unknown values are rejected before anything downloads. |
| `-sets` | Comma-separated set codes (`mh2,c21`). Case-insensitive. |
| `-since` | Keep only sets released in this year or later. |
| `-priced-only` | Drop printings Scryfall has no USD price for at all. |
| `-days` | Days of price history to backfill. Default 30, capped at 90. |

No filter flags means every paper printing, which is large and slow. Pick at
least one.

## What ends up in the file

The source is Scryfall's `default_cards` bulk bundle, streamed and filtered line
by line, plus MTGJSON identifiers so prices can be attached. Paper printings only.

Each surviving printing becomes one entry **per finish** at quantity 1 — that is
why 31,912 printings above produced 48,049 entries. A card with both a nonfoil
and a foil version appears twice, which is what makes the finish filters, the
movers screen and the price history behave normally.

The handful MTGJSON has no identifier for (17 above) are still browsable, just
unpriced. Prices come from the same backfill path `hoard backfill` uses, so the
market screens, sparklines and movers all work on a compendium as they do on a
collection.

## Read-only

The last thing a successful build does is stamp `compendium.mode` in the settings
table. On open, hoard notices and:

- every mutating command refuses, pointing you back at your own `--db`:

```console
$ HOARD_DB=mythics-rare.db hoard vacuum
error: hoard vacuum writes to the database, and this one is a compendium, not your collection.
Point --db or $HOARD_DB at your own hoard, or rebuild the compendium with the generator
```

  That covers `import`, `add`, `deck add`, `watch add`, `merge`, `repair`,
  `vacuum`, `update-prices`, `backfill` and the binder commands.

- the browser loads with writes disabled, so add, move, edit, binder and watch
  operations are never offered.

The stamp is the *only* thing making it read-only. It is an ordinary SQLite file
otherwise, and clearing that settings row would turn it back into a writable
hoard containing a very strange collection.

## Two gotchas

**Point it at a path that does not exist yet.** The generator opens whatever
path you give it and seeds into it. Aim it at your real collection and you get
your cards plus every matching printing in one database, stamped read-only. It
will not stop you.

**An interrupted build leaves an unstamped file.** Compendium mode is cleared at
the start and set at the end, so a run that dies partway through leaves a
writable, half-seeded database behind. Delete it and start again rather than
browsing it.

## Not the same as `hoard catalog`

`hoard catalog status` / `hoard catalog update` manage the card-name search index
in your cache directory — the thing that autocompletes names as you type. That is
a separate concept from the compendium databases this generator builds.
