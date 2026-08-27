# Compendiums

`hoard` normally opens the cards you own. `hoard compendium` builds a database
of cards you **don't**: every printing that matches a filter, one copy of each,
priced and backfilled with history.

Point the browser at it and you can read a slice of Magic exactly the way you
read your own collection: the same screens, the same <kbd>/</kbd>
[filters](filtering.md), the same sparklines and movers. `compendium` is the thing to
reach for when you want to price a format before buying into it, browse a set as
a whole, or just look a card up without waiting on a website.

- [Build one](#build-one)
- [Choosing a slice](#choosing-a-slice)
- [Build a whole format](#build-a-whole-format)
- [Period-correct printings with `--era`](#period-correct-printings-with---era)
- [Ebon Ante (`aaa`)](#ebon-ante-aaa)
- [What ends up in the file](#what-ends-up-in-the-file)
- [Good to know](#good-to-know)

## Build one

Name a filter and an output file. Every mythic and rare printed since 2020:

```console
$ hoard compendium --rarity mythic,rare --since 2020 mythics-rare.db
  ✓ downloading catalog ████████████ 74.0/74.0 MB · 31,980 cards
  ✓ mapping card ids ███████████▉ 31,929/31,980 cards
  ✓ downloading price history ████████████ 143.6/143.6 MB · resolving card ids · set 334/334
  ✓ recording history
  ✓ compacting the database
  ! 178 sets are not in MTGJSON, so their printings are unpriced.
✓ Seeded 31,980 printings, 48,148 entries.
  ! 51 have no MTGJSON id, so they are unpriced.
Backfilled 473,667 observations and 226,074 bids.
Browse it: hoard --db mythics-rare.db
```

Both warnings are normal on a wide build. Prices are attached through MTGJSON
identifiers and a few printings have none, mostly Secret Lair drops and promos,
so they are seeded and browsable but carry no price history. The skipped sets
are almost all token sets, which MTGJSON does not carry.

The five step lines redraw in place, so the whole build stays in one screen no
matter how long it runs. Piped to a file it falls back to plain appended lines.

Then browse it like any other hoard:

```sh
hoard --db mythics-rare.db
# or
HOARD_DB=mythics-rare.db hoard
```

That build takes about 40 seconds on a fast connection and lands around 300 MB.
Both numbers grow as you widen the filter and as you raise `--days`.

## Choosing a slice

| Flag | Effect |
|---|---|
| `--rarity` | `common`, `uncommon`, `rare`, `special`, `mythic`, `bonus`, comma-separated. Unknown values are rejected before anything downloads. |
| `--sets` | Comma-separated set codes (`mh2,c21`). Case-insensitive. |
| `--since` | Keep only sets released in this year or later. |
| `--format` | Keep only cards Scryfall marks **legal** in one play format. `banned` and `restricted` are both dropped. |
| `--era` | Narrow `--format` to its own era. See [below](#period-correct-printings-with---era). |
| `--priced-only` | Drop printings Scryfall has no USD price for at all. |
| `--days` | Days of price history to backfill. Default 30, clamped to 90. |
| `--all` | Build every paper printing. Only needed when you pass no filter at all. |

A build finishes with a full price recording of its own — gaps filled, today's
prices written to the history, contradicted prices refused. So a fresh
compendium opens with its charts and its total value already populated, and
`hoard update-prices` has nothing left to do on the first launch.

Tokens, emblems and art-series cards are dropped from every build, filtered or
not. Scryfall's bulk file carries them as ordinary rows — priced, rarity-tagged
and paper-legal — so they would otherwise pass `--since`, `--rarity` and `--all`
alike. Nothing you can play with is affected.

Filters combine, so `--rarity rare --sets mh2` gives you the rares in Modern
Horizons 2 and nothing else.

**Pass at least one of `--rarity`, `--sets`, `--since` or `--format`.** Without
one, the build is every paper printing in Magic (many gigabytes), so hoard
refuses unless you also pass `--all` to say you meant it. `--era` does not count
as a filter, because it takes its sets from `--format`.

> A whole-Magic compendium is untested territory: I have not validated how the
> browser performs at that size. Smaller compendiums, built for whatever you are
> working on, are the way I use this.

## Build a whole format

`--format` filters on the legality Scryfall records, so one flag gets you a
format's entire card pool:

```sh
hoard compendium --format premodern premodern.db
hoard compendium --format pauper pauper.db
```

It accepts any format Scryfall records legality for:

`alchemy`, `brawl`, `commander`, `competitivebrawl`, `duel`, `future`,
`gladiator`, `historic`, `legacy`, `modern`, `oathbreaker`, `oldschool`,
`pauper`, `paupercommander`, `penny`, `pioneer`, `predh`, `premodern`,
`standard`, `standardbrawl`, `timeless`, `tlr`, `vintage`.

Plus `aaa`, which Scryfall records no legality for and which lives here as an
era alone; see [Ebon Ante](#ebon-ante-aaa). An unknown name is rejected before
anything downloads.

Note this is a *play* format, not the CSV dialect that `import --format` means.

### Why you get modern reprints in an old format

Scryfall records legality **per card, not per printing**. A legal card reports
that legality on every printing it has ever had, in both directions in time: the
Modern Horizons 2 Counterspell reports `premodern: legal`, and the Alpha
Lightning Bolt reports `modern: legal`.[^1]

That is usually what you want. Every one of these formats lets you play any
printing of a legal card, so a Secret Lair Swords to Plowshares is as legal in
Premodern as the Ice Age one, and finding the copy that fits your budget is the
whole point of pricing a format before you buy into it.

[^1]: `oldschool` is the exception: Scryfall varies it by printing, so the Alpha
Serra Angel is `oldschool: legal` while the Dominaria Remastered one is not.
Which means `--format oldschool` gives you period printings on its own, with no
`--era` needed.

## Period-correct printings with `--era`

Add `--era` when you want the period pool instead, whether that is a specific
set list to browse or prices for the original printings rather than the cheapest
ones:

```sh
hoard compendium --format premodern --era premodern-era.db
hoard compendium --format predh --era predh-era.db
```

This is the one to reach for if what you're after is black-bordered, old-frame
classics (foil too, if you have the budget).

Three formats carry an era today, bounded three different ways:

| Format | Era | How it is expressed |
|---|---|---|
| `premodern` | Fourth Edition through Scourge | 29 set codes |
| `predh` | everything before Commander 2011 | printings released before `2011-06-17` |
| `aaa` | Alpha through Alliances, plus five Apocalypse lands | 13 set codes, a five-card allowance, and one ban |

Other formats are era-bound in the real world and simply have no bound here yet:
Modern starts at Eighth Edition, Pioneer at Return to Ravnica, and Standard
rotates. `--era` on those is an **error** rather than a silent no-op, so a typo
cannot quietly widen your build.

An explicit `--sets` wins over an era's set list, so you can pin a subset of an
era by hand.

## Ebon Ante (`aaa`)

Alpha–Alliances Ante, played as [Ebon Ante][ebon-ante]: Old School with real
ante, 60-card decks, one game a match, and a 20-card side to replace what you
lose.

[ebon-ante]: https://docs.google.com/document/d/1uPMy2PQYGRAye9oYuZnPERsDD44O3RIFhNBoiQBKdig/edit?tab=t.0

```sh
hoard compendium --format aaa --era ante.db
```

`--era` is **not optional** here. Scryfall records no `aaa` legality, so without
a bound there is nothing to filter on and the build would take every paper
printing. `--format aaa` on its own is refused rather than run.

The pool is thirteen whole sets:

`lea` `leb` `2ed` `3ed` `4ed` `arn` `atq` `leg` `drk` `fem` `hml` `ice` `all`

Two carve-outs sit on top of that:

- **Five Apocalypse lands are in.** Apocalypse is not a set in the pool, but its
  enemy painlands (Battlefield Forge, Caves of Koilos, Llanowar Wastes, Shivan
  Reef and Yavimaya Coast) are legal, so the build pulls those five cards out of
  a set it otherwise ignores.
- **Mind Twist is out.** It is banned, and the build drops it from the pool
  entirely even though its sets are included.

Passing your own `--sets` here replaces the thirteen *and* the Apocalypse
allowance, since the five lands ride along with the era's set list. The Mind
Twist ban still applies.

<details>
<summary>What the era can't express, and should stay in your head instead</summary>

<br>

- **Restricted and limited cards.** Ancestral Recall and the rest are restricted
  to one, and Mishra's Factory, Strip Mine and Maze of Ith to two. A compendium
  holds one printing of each card regardless, so these are deckbuilding limits,
  not pool limits.
- **"Any edition, original art, old frame."** The format lets you play a newer
  old-frame printing of a card in the pool, gold border and Timeshifted
  included. `--era` gives you the period-correct printings instead. Dropping
  `--era` is not an option here, since there is no legality key to fall back on,
  so build a wider printing pool with `--sets` on its own, without `--format`.

</details>

## What ends up in the file

The source is Scryfall's `default_cards` bulk bundle, streamed and filtered line
by line, plus MTGJSON identifiers so prices can be attached. Paper printings
only.

Each surviving printing becomes one entry **per finish**, at quantity 1. A card
with both a nonfoil and a foil version appears twice, which is what makes the
finish filters, the movers screen and the price history behave normally.

Prices come from the same backfill path `hoard backfill-prices` uses, so the
market screens, sparklines and movers all work on a compendium exactly as they
do on a collection.

## Good to know

**Your own hoard is never touched.** The command opens, reads and writes nothing
but the file you name.

**It will not build into a file that already exists.** A mistyped path cannot
mix a compendium into a real collection.

**A compendium is an ordinary hoard database.** Nothing stops you writing to it,
so treat it as read-only by habit. `hoard vacuum` and the editing commands will
act on it like any other file.

**This is not `hoard catalog`.** `hoard catalog status` / `hoard catalog update`
manage the card-name search index in your cache directory, the thing that
autocompletes names as you type. Separate concept, separate storage.
