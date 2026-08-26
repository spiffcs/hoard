# Compendiums

`hoard` normally opens your collection. `hoard compendium` builds a different kind of database called a compendium.

A **compendium** is every printing that matches a filter. This filter will generate a DB
with one copy of each card priced and backfilled 30 days. You can point the hoard browser at it
which lets you read a slice of Magic you do not own exactly the way you would read your own collection.

This is useful for pricing and building decks in a format before you own any of
it, browsing the market and looking at specific sets as a whole, or just enjoying a swift
no frills terminal browser for card data where a website might be slower or clunkier.

## Build one

```sh
hoard compendium --rarity mythic,rare --since 2020 mythics-rare.db
```

Then browse it:

```sh
hoard --db mythics-rare.db
# or
HOARD_DB=mythics-rare.db hoard
```

That build takes about 40 seconds on a fast connection and lands around 300 MB.
Both grow as you widen the filter and as you raise `--days`.

Your own hoard is never opened, read or written; the command creates the file
you name and nothing else. It refuses to build into a file that already exists,
so a mistyped path cannot mix a compendium into a real collection.

From a clone of hoard you can run, `task compendium -- --rarity mythic,rare --since 2020 m.db`
which forwards its arguments to the same command.

## Flags

| Flag | Effect |
|---|---|
| `--rarity` | `common`, `uncommon`, `rare`, `special`, `mythic`, `bonus` — comma-separated. Unknown values are rejected before anything downloads. |
| `--sets` | Comma-separated set codes (`mh2,c21`). Case-insensitive. |
| `--format` | Keep only cards Scryfall marks **legal** in one play format. `banned` and `restricted` are both dropped. |
| `--era` | Narrow `--format` to its own era like a set list for `premodern` and `aaa`, a release-date cutoff for `predh`. Refused for formats with no era, and required by `aaa`. An explicit `--sets` wins over it. |
| `--since` | Keep only sets released in this year or later. |
| `--priced-only` | Drop printings Scryfall has no USD price for at all. |
| `--days` | Days of price history to backfill. Default 30, capped at 90. |
| `--all` | Build every paper printing. Only needed when you pass no filter at all. |

Pass at least one filter, `--era` on its own is not one, since it takes its
sets from `--format`. 

Without a filter the build is every paper printing, which
is many gigabytes. This command is refused unless you also pass `--all`.

Note: the TUI has not been tested with ALL cards and I can't validate its performance.
I prefer to use smaller compendiums rather than one master one depending on what I'm working on.

## Formats

`--format` accepts the names Scryfall records legality for:

`alchemy`, `brawl`, `commander`, `competitivebrawl`, `duel`, `future`,
`gladiator`, `historic`, `legacy`, `modern`, `oathbreaker`, `oldschool`,
`pauper`, `paupercommander`, `penny`, `pioneer`, `predh`, `premodern`,
`standard`, `standardbrawl`, `timeless`, `tlr`, `vintage`.

Plus `aaa`, which Scryfall records no legality for and which exists here as an
era alone. See [Ebon Ante](#ebon-ante-aaa) below.

An unknown name is rejected before anything downloads.

### `--era`

Scryfall records legality **per card, not per printing**. A card legal in a
format reports that legality on every printing it has ever had, in either
direction in time: the Modern Horizons 2 Counterspell reports `premodern:
legal`, and the Alpha Lightning Bolt reports `modern: legal`. (`oldschool` is
the one exception as Scryfall varies it by printing, so the Alpha Serra Angel is
`oldschool: legal` while the Dominaria Remastered one is not.)

That is usually what you want. Every one of these formats lets you play any
printing of a legal card, so a Secret Lair Swords to Plowshares is as legal in
Premodern as the Ice Age one. Finding the copy that fits your budget is
the whole point of pricing a format/deck before you buy into it.

When you want the 'period-correct' pool instead like a specific set list to browse, or prices
for the original printings rather than the cheapest ones add `--era`:

```sh
hoard compendium --format premodern --era premodern-era.db
hoard compendium --format predh --era predh-era.db
```

Use `--era` if you're looking for a compendium that gives you views into those
sweet sweet black border classic frame cards (foil too if you got the $$).

Three formats carry an era today, and they are bounded differently:

| Format | Era | How it is expressed |
|---|---|---|
| `premodern` | Fourth Edition through Scourge | the 29 set codes |
| `predh` | everything before Commander 2011 | printings released before `2011-06-17` |
| `aaa` | Alpha through Alliances, plus five Apocalypse lands | 13 set codes, a five-card allowance, and one ban |

Other formats are era-bound in the real world and simply have no bound here
yet: Modern starts at Eighth Edition, Pioneer at Return to Ravnica, and
Standard rotates. 

`--era` on those is an error rather than a silent no-op, so a
typo cannot quietly widen your build. `--sets` still wins if you pass it, so
you can pin a subset of an era by hand.

Old School needs no `--era` at all: it is the one format Scryfall already
bounds by printing, so `--format oldschool` gives you period printings on its
own.

### Ebon Ante (`aaa`)

Alpha–Alliances Ante, played as Ebon Ante: Old School with real ante, 60-card
decks, one game a match, and a 20-card side to replace what you lose.

[Ebon Ante](https://docs.google.com/document/d/1uPMy2PQYGRAye9oYuZnPERsDD44O3RIFhNBoiQBKdig/edit?tab=t.0)

```sh
hoard compendium --format aaa --era ante.db
```

`--era` is not optional here. Scryfall records no `aaa` legality, so without a
bound there is nothing to filter on and the build would take every paper
printing. `--format aaa` on its own is refused rather than run.

The pool is thirteen whole sets:

`lea` `leb` `2ed` `3ed` `4ed` `arn` `atq` `leg` `drk` `fem` `hml` `ice` `all`

Two carve-outs sit on top of that. Apocalypse is not a set in the pool, but its
five enemy painlands are legal .

What the era cannot express, and what you should keep in your head instead:

- **Restricted and limited cards.** Ancestral Recall and the rest are
  restricted to one, and Mishra's Factory, Strip Mine and Maze of Ith to two.
  A compendium holds one printing of each card regardless, so these are
  deckbuilding limits, not pool limits.
- **"Any edition, original art, old frame."** The format lets you play a newer
  old-frame printing of a card in the pool, gold border and Timeshifted
  included. `--era` gives you the period-correct printings instead. Drop
  `--era` and there is no legality key to fall back on, so use `--sets` if you
  want a wider printing pool.

## What ends up in the file

The source is Scryfall's `default_cards` bulk bundle, streamed and filtered line
by line, plus MTGJSON identifiers so prices can be attached. Paper printings
only.

Each surviving printing becomes one entry **per finish** at quantity 1. A card
with both a nonfoil and a foil version appears twice, which is what makes the
finish filters, the movers screen and the price history behave normally.

Prices come from the same backfill path `hoard backfill-prices` uses, so the
market screens, sparklines and movers all work on a compendium as they do on a
collection.

A compendium is an ordinary hoard database. Nothing stops you writing to it, so
treat it as read-only by habit — `hoard vacuum` and the editing commands will
act on it like any other file.

## Not the same as `hoard catalog`

`hoard catalog status` / `hoard catalog update` manage the card-name search
index in your cache directory. That is the thing that autocompletes names as you
type. It is a separate concept from the compendium databases this command
builds.
