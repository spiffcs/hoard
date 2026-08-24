# The compendium generator

`hoard` normally opens *your* collection. This generator builds a different kind
of database called a compendium.

A **compendium** is every printing that matches a filter, one copy of
each, priced and backfilled. You can point the browser at it which let's you read a slice of
Magic you do not own exactly the way you read your own hoard.

This is useful for pricing and building decks in a format before you own any of it.

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
| `-legal` | Keep only cards Scryfall marks **legal** in one format (`premodern`, `legacy`, `modern`, …). `banned` and `restricted` are both dropped. Unknown format names are rejected before anything downloads. |
| `-format` | Shorthand for a format's legality *and* its era's sets (`premodern`). Anything you pass yourself wins over what the shorthand would supply. |
| `-since` | Keep only sets released in this year or later. |
| `-priced-only` | Drop printings Scryfall has no USD price for at all. |
| `-days` | Days of price history to backfill. Default 30, capped at 90. |

No filter flags means every paper printing, which is large and slow. Try to pick at
least one.

## What ends up in the file

The source is Scryfall's `default_cards` bulk bundle, streamed and filtered line
by line, plus MTGJSON identifiers so prices can be attached. Paper printings only.

Each surviving printing becomes one entry **per finish** at quantity 1.
A card with both a nonfoil and a foil version appears twice, which is what makes the finish filters, the
movers screen and the price history behave normally.

Prices come from the same backfill path `hoard backfill` uses, so the
market screens, sparklines and movers all work on a compendium as they do on a
collection.

## Not the same as `hoard catalog`

`hoard catalog status` / `hoard catalog update` manage the card-name search index
in your cache directory. This is the thing that autocompletes names as you type. That is
a separate concept from the compendium databases this generator builds.
