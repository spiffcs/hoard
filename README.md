# hoard

[![CI](https://github.com/spiffcs/hoard/actions/workflows/ci.yml/badge.svg)](https://github.com/spiffcs/hoard/actions/workflows/ci.yml)

A small Go CLI that catalogs Magic: The Gathering cards in a local
SQLite database.

**Point your iPhone at a card and it gets filed.** On macOS, hoard uses
Continuity Camera to read a card's title with Apple's Vision OCR. It then
matches it against Scryfall's fuzzy name search and drops it straight into
the add flow. The camera window stays open between shots, so working through a
box of cards is: frame, press space, confirm, repeat. See
[Scanning a card](#scanning-a-card).

Cards get in three other ways: type a name, paste a Scryfall page URL, or import
a whole **deck** — from an Archidekt link, or from a decklist you exported to a
text file. However a card arrives, hoard records how many you own across the
loose collection and every deck, and stores the current market price from the
[Scryfall API](https://scryfall.com/docs/api).

## Contents

- [Requirements](#requirements)
- [Quick start](#quick-start)
- [Usage](#usage) — [what moved](#what-moved), [price history](#starting-with-history-you-dont-have-yet), [missing prices](#when-scryfall-has-no-price), [vendor spreads](#where-vendors-disagree), [adding cards](#adding-cards)
- [Decks](#decks)
- [Database location](#database-location)
- [Scanning a card](#scanning-a-card)
- [Development](#development)
- [License](#license)

## Requirements

- **Go 1.26 or newer** (`go version` to check). The module targets that version
  and will not build on older toolchains.
- An internet connection for the Scryfall API. Nothing else is needed: the core is
  pure Go, with no cgo or C toolchain (it uses `modernc.org/sqlite`).
- Optional, macOS only: Xcode's Swift toolchain, if you want to
  [scan cards with an iPhone camera](#scanning-a-card).

## Quick start

```sh
git clone https://github.com/spiffcs/hoard && cd hoard

make all                             # builds ./hoard and the iPhone scan helper
                                     # (the helper needs macOS + Xcode; on other
                                     #  platforms it is skipped, not an error)

./hoard add                          # add cards interactively (start here)
                                     #   ctrl+o scans a card with your iPhone
./hoard list                         # what's in the loose collection
./hoard summary                      # grand total across collection + decks
```

`make all` is `make build` plus `make scan`. If you only want the CLI, or you
don't have Xcode's Swift toolchain, `make build` (or `go build -o hoard .`) is
enough; every command except camera scanning works without the helper, and
<kbd>ctrl+o</kbd> reports that it's unavailable rather than failing.

The database is created on first run in a per-user data directory, and its path is
printed once so you know where it went. See
[Database location](#database-location) to put it somewhere else.

Run `hoard help` (or `hoard` with no arguments) for the full command list.

## Usage

> **Global flags must come before the command.** `hoard --db ./my.db summary` works;
> `hoard summary --db ./my.db` silently uses the default database instead.

```sh
# Add cards interactively (see below), the main way in
hoard add

# Show the collection and its total value
hoard list

# Grand total value: loose collection + each deck
hoard summary

# Refresh market prices for every card in the catalog
hoard update-prices

# What has risen and fallen since a month ago
hoard movers --since 30d

# One-off: load the last 90 days of prices from MTGJSON
hoard backfill-prices

# Cards counting as $0.00 in your totals, and where they are held
hoard unpriced

# Where vendors disagree about what your cards are worth
hoard arbitrage
```

Scryfall recalculates its price data roughly once a day, so `update-prices` is
worth running about that often. Running it several times in a day re-fetches the
entire catalog to arrive at the same numbers: it is the only command that talks
to Scryfall about every card you own, in batches of 75.

### What moved

Every refresh records the prices it sees, so `update-prices` ends by saying what
changed since the last one:

```
Updated prices for 1,256 of 1,256 cards.

NAME                     SET/NUM  FINISH    WAS       NOW  CHANGE  QTY   IMPACT
RISERS
  Breena, the Demagogue  c21/1    foil    $3.00  →  $4.32  +44.0%   ×2   +$2.64
  Sol Ring               c21/1    -       $1.00  →  $1.10  +10.0%  ×40   +$4.00

SINKERS
  Flame Discharge        neo/142  -       $0.25  →  $0.10  -60.0%   ×1   -$0.15

2 printings moved since the last refresh. Net change: +$6.49
```

The list is ordered by **impact** — the move multiplied by how many copies you
hold, across the loose collection and every deck — not by the change in sticker
price. Forty commons that each gained a dime moved the hoard more than one mythic
that gained a dollar, and sorting on the per-card figure buries that.

`hoard movers` asks the same question over a longer window:

```sh
hoard movers                 # since 30 days ago
hoard movers --since 7d      # or 2w, or 48h
hoard movers --limit 25      # more than the default 10 per section
```

The window names a date, not a period, because prices are observed when a refresh
runs rather than continuously: `--since 7d` compares today's price against the
last one recorded *on or before* a week ago. On a hoard refreshed every few
weeks, that baseline may itself be older than the window, and the footer says so
when history does not reach back as far as the question.

Only prices that actually changed are stored, so the table stays small — most of
a collection does not move on a given day. A card added between refreshes gets
its first observation on the next one, as a baseline rather than as a rise from
nothing.

### Starting with history you don't have yet

History begins the first time you run `update-prices`, so a new hoard answers
`movers --since 30d` with a day of data and a footer apologising for it. MTGJSON
publishes the last ninety days, which closes that gap in one download:

```sh
hoard backfill-prices
```

Run it once. It is deliberately not part of `update-prices`: the ninety-day
archive is around 150 MB against the 5 MB of the daily file, and the download
cache is cleared nightly, so folding it in would risk re-fetching the lot on any
day you refresh prices. Running it again is harmless and inserts nothing — the
import stops at the day your own history begins.

Backfilled prices come from **TCGplayer**, which is the source Scryfall's USD
figures come from, so the imported series joins up with your own instead of
stepping at the seam. Where a real observation and an imported one land on the
same day, the real one stands.

Two things it cannot reach: printings MTGJSON has no id for, and printings
TCGplayer never priced — the same gap `unpriced` reports. Those simply have no
history before you started recording, and the command says how many there were,
because `movers` would otherwise just quietly list fewer cards.

Ninety days is the ceiling. `--since 6m` will still tell you the history only
goes back so far.

### When Scryfall has no price

Scryfall's USD prices come from TCGplayer alone, so a printing TCGplayer has no
record of is simply unpriced there. This is not rare. The Modern Horizons 3
Commander ripple foils have no USD foil price at all, which left one deck valued
at $134.72 instead of $459.54.

When `update-prices` finds cards with no price for a finish you actually own, it
consults [MTGJSON](https://mtgjson.com/), which aggregates several vendors:

```
Updated prices for 1,256 of 1,256 cards.
  111 cards have no price for a finish you own; checking MTGJSON...
  filled 96 from cardkingdom, manapool, tcgplayer.
  15 still unpriced anywhere.
```

Nothing is downloaded unless there is a gap to fill, and the files are cached for
the day in your cache directory. A Scryfall price always wins; these only ever
stand in where there is none. Because they come from a different shelf than the
rest of hoard's numbers, they are marked with `*` and a footnote naming the
vendors:

```
main   1  Planar Nexus   m3c/80  foil  $37.99  $37.99*

* estimated: Scryfall has no price for this printing; from cardkingdom via MTGJSON
```

Cardmarket is deliberately not used: it quotes euros, and a second currency
inside a dollar total would be misleading.

### Cards that cannot be priced

`hoard unpriced` shows every card counting as $0.00 and which deck each one is
dragging down, because a card valued at nothing otherwise looks the same as a
card genuinely worth nothing:

```
NAME                  SET/NUM  FINISH  COPIES  HELD IN
Ambush Commander      evg/1    -            1  Duel Decks Anthology: Elves vs. …
Angelic Protector     tpr/2    -            1  Duel Decks Anthology: Divine vs.…

19 copies across 15 cards count as $0.00.
Try: hoard repair-finishes, then hoard update-prices
```

### Where vendors disagree

`summary` reports one price per card as though it were the truth. It isn't: the
same card is quoted by three US vendors at once, on both sides of the counter,
and they disagree more than a single figure suggests. `hoard arbitrage` shows
where, in three sections:

```
ARBITRAGE  a shop pays more than the cheapest retail
Graveborn Muse     lgn/73   -   $11.17 manapool   $13.50 cardkingdom  +$2.33
Ugin's Labyrinth   mh3/359  -   $14.43 manapool   $16.50 cardkingdom  +$2.07

EASY TO SELL  buylist is close to retail
Arcane Denial      msc/147  -    $1.37 retail      $1.35 cardkingdom   98.5%
Living Death       tdc/185  -    $2.83 retail      $2.75 cardkingdom   97.2%

CHEAPEST VS DEAREST  where the vendors disagree
Siege-Gang Lieut.  m3c/61  foil  $4.49 cardkingdom $41.68 manapool   +828.3%
Copy Land          m3c/47  foil  $2.49 cardkingdom $11.95 manapool   +379.9%
```

The first section is the only unambiguous one: a shop offering more than the
cheapest asking price is free money, though in practice a couple of dollars a
card. The second tells you what you could turn into cash near sticker price,
against a median card that fetches about half. The third is where to buy, and
where a copy you own is being sold for more than you would guess.

`--min` sets the floor on what you would pay (default `$1`), because a 900%
spread between $0.20 and $1.99 is arithmetic rather than an opportunity.
`--limit` sets rows per section.

One listing in every few hundred is simply wrong. Manapool quotes one card at
over $138,000 against Card Kingdom's $2.49, so a price no other vendor comes
within 20x of is discarded and counted in the footer rather than shown as the
find of the century. Everything here is a vendor's asking or offering price on
one day, not a guaranteed sale.

Often the cause is not a missing price but a wrong finish. A decklist with no
`*F*` marker imports as non-foil, and plenty of printings are foil-only: precon
commanders and Duel Decks reprints among them. That entry then asks for a price
that cannot exist, so no amount of refreshing will ever fill it.

`hoard repair-finishes` finds those and corrects them, using the list of finishes
Scryfall already publishes for each printing:

```
NAME                        SET/NUM  QTY  WAS     NOW   IN
Ambush Commander            evg/1      1  normal  foil  Duel Decks Anthology: Elves v…
Licia, Sanguine Tribune     c17/40     1  normal  foil  Vampiric Bloodlust (Commander…

Corrected 12 entries. Run hoard update-prices to value them.
```

It only acts where there is a single right answer, meaning the printing comes in
exactly one finish. Anything genuinely ambiguous is reported and left alone. It
is safe to re-run: a second pass finds nothing.

### Adding cards

`hoard add` opens an interactive add session (a TUI). It searches Scryfall by
name, then asks only the questions needed to pinpoint one exact entry: which
card if the name is ambiguous, which printing, which finish, how many. Confirm,
and it loops back so you can add another card without restarting. Type to filter
long printing lists or search by card number. Press <kbd>esc</kbd> at the name prompt (or
<kbd>ctrl+c</kbd> anytime) to exit.

```sh
hoard add                                # start an empty add session
hoard add Ulamog, the Infinite Gyre      # pre-seed the first search
```

This needs a real terminal, so it can't be piped or run from a script.

Inside a session, <kbd>ctrl+o</kbd> scans a card with your camera (macOS only;
see [Scanning a card](#scanning-a-card)) and <kbd>ctrl+r</kbd> switches which
camera it uses.

A Scryfall page URL also works and skips the session entirely, which is useful
in scripts:

```sh
hoard add https://scryfall.com/card/uma/7/ulamog-the-infinite-gyre --qty 2
hoard add https://scryfall.com/card/uma/7/ulamog-the-infinite-gyre --foil
```

`--qty` and `--foil` apply to this URL form only; in a session you're asked for the
finish and quantity directly.

### What `summary` looks like

`summary` groups the hoard into two sections and ranks decks by value, with a bar
showing each one's share of the grand total:

```
COLLECTION                                           100  $1,901.70  ████▉
DECKS · 22                                         1,878  $1,987.58  █████

  Vampiric Bloodlust (Commander 2017)                100    $198.12  ▌
  Draconic Domination (Commander 2017 Precon)        100    $172.41  ▍
  Tricky Terrain Collector's Edition (Modern Hori…   100    $164.59  ▍
  …
  Duel Decks Anthology: Jace vs. Chandra (Chandra)    60     $15.05  ▏

TOTAL                                              1,978  $3,889.28
```

The two section bars tile the column exactly, so they double as the scale for the
deck bars beneath them. A blank bar means a deck is worth $0.00, usually because
its prices haven't been fetched yet; run `hoard update-prices`.

## Decks

```sh
# Import (or refresh) a deck from an Archidekt link
hoard deck add https://archidekt.com/decks/7319967/high_power_aristocrats

# Moxfield's API is Cloudflare-blocked, so export that deck to text
# (Moxfield → ⋯ → Export) and import the file:
hoard deck add --file my-deck.txt --name "My Edgar EDH" --source moxfield

# List decks with card counts and value, most valuable first
hoard deck list

# Show a deck's cards
hoard deck show vampiric

# Delete a deck
hoard deck remove "My Edgar EDH"
```

`deck show` and `deck remove` accept **any part of a deck's name**, case-insensitively,
as long as it matches one deck. If it matches several, they're listed so you can
narrow it down rather than the wrong deck being acted on:

```
$ hoard deck show "Duel Decks"
error: "Duel Decks" matches 8 decks:
  Duel Decks Anthology: Divine vs. Demonic (Demonic)
  Duel Decks Anthology: Divine vs. Demonic (Divine)
  …
Use a longer fragment or the full name.
```

An exact name always wins over a fragment, so a deck called `Elves` is still reachable
even when other names contain that word.

Re-importing the same deck link updates it in place (no duplicates). Cards a deck
references are added to the shared catalog, so `update-prices` refreshes prices for
loose cards and decks together. Any card that can't be resolved on import is reported
and skipped, never silently dropped.

An imported deck is priced immediately, including the two cases that would
otherwise leave it looking worthless. A line with no `*F*` marker parses as
non-foil, but precon commanders and Duel Decks reprints are often foil-only, so
the finish is corrected to the one the printing actually comes in. Anything
Scryfall still cannot price is then looked up in MTGJSON, exactly as
`update-prices` would:

```
$ hoard deck add --file precon.txt --name "Vampiric Bloodlust"
Imported deck #27 "Vampiric Bloodlust" (text): 100 cards resolved.
  4 recorded as foil: the list said otherwise but the printing has no non-foil.
  9 cards have no price for a finish you own; checking MTGJSON...
  filled 9 from cardkingdom.
```

Nothing extra is downloaded when the import leaves no gaps.

The text importer understands common decklist formats such as `2 Sol Ring`,
`1x Lightning Bolt`, `1 Ulamog, the Infinite Gyre (UMA) 7 *F*`, and section headers
like `Commander` / `Sideboard` / `Maybeboard`. `--name` defaults to the file name,
and `--source` is a free-form provider label (`moxfield`, `text`, …) recorded for
provenance.

## Database location

The database lives in a per-user data directory, so the same hoard is used no matter
which directory you run the command from:

| OS      | Default location                                       |
|---------|--------------------------------------------------------|
| macOS   | `~/Library/Application Support/hoard/hoard.db`          |
| Linux   | `$XDG_DATA_HOME/hoard/hoard.db` (else `~/.local/share/hoard/hoard.db`) |
| Windows | `%AppData%\hoard\hoard.db`                              |

The directory is created on first run, and the resolved path is printed the first
time the database is initialized. Override the location with the `--db` flag or the
`HOARD_DB` environment variable (both take precedence over the default):

```sh
hoard --db ~/hoard/collection.db list     # note: --db precedes the command
export HOARD_DB=~/hoard/collection.db
```

When a new version of hoard needs to change the database's shape, it upgrades it
on first run and copies the old file alongside first, named like
`hoard.db.bak-v1-20260729`. Nothing is ever deleted, so if an upgrade goes wrong
the previous state is sitting next to the original. Those copies are safe to
remove once you are happy.

Piping or redirecting turns off styling, truncation, and bars automatically, so
`hoard summary | grep` sees whole names and no escape sequences.

## Scanning a card

> macOS only, and it needs the `hoard-scan.app` helper built by `make all` (or
> `make scan` on its own).
>
> hoard looks for the helper next to its own binary, then in a `bin/` directory
> beside it, so running `./hoard` from the repo just works. If you move `hoard`
> onto your `PATH`, either bring `bin/hoard-scan.app` along with it or point at
> it directly:
>
> ```sh
> export HOARD_SCAN=~/src/hoard/bin/hoard-scan.app
> ```
>
> Everything else works without the helper; if it isn't found, the in-app scan
> action reports that it's unavailable rather than failing.

Inside an add session (`hoard add`), press <kbd>ctrl+o</kbd> to identify a card with
your iPhone instead of typing its name.

Scanning uses **Continuity Camera only**, meaning your iPhone and never the Mac's
built-in webcam. A fixed, user-facing camera can't be aimed at a card on the desk, so rather
than fall back to one and produce unreadable captures, hoard tells you no iPhone is
connected. If you have more than one iPhone paired you're asked which to use; the
choice is remembered for the session so bulk scanning doesn't ask again, and
<kbd>ctrl+r</kbd> at the prompt re-runs detection or switches phones.

A window opens with the live feed **and stays open**. Frame a card, press
<kbd>space</kbd>, and the add prompts run in the terminal; once the card is saved you're
back at framing for the next one.

The preview starts rotated a quarter-turn clockwise, which is what a portrait-held
iPhone needs: Continuity Camera hands over a landscape frame and macOS often can't
tell how the phone is being held. If the framing is still wrong use **←/→** to rotate the
preview, and the corrected angle is saved to `scan.json` beside the database, so you
only need to fix it once. The window title always shows the current angle and how much of it
came from macOS's automatic correction.

The card's title is read on-device with Apple's Vision OCR. It is then matched to a real card via
Scryfall's fuzzy name search.

The bottom border is read too, in a second pass. Magic cards have carried a
collector number since Exodus (1998), printed bottom centre on older frames and
bottom left from the M15 frame (2014) onward, where it sits alongside the set
code. When that read succeeds, the matching printing is moved to the top of the
printing list and marked `← scanned`, with the cursor already on it. This matters
for reprinted cards: Sol Ring has over a hundred printings, and the number in
your hand says which one it is.

The card is located in the frame first, and the border is then read relative to the
card's own bottom edge, so the card does not need to fill the shot or sit at any
particular height — anywhere in frame, roughly upright, is enough.

It is deliberately a suggestion rather than a decision. A misread digit is visible
before you commit, and enter is all it takes to accept. If the number matches none
of the printings, the list is left in its normal order and hoard says so rather
than quietly pretending nothing was read. Cards too old to carry a number, or a
border too blurred to read, simply fall back to the ordinary printing picker.

### Notes and troubleshooting

- Continuity Camera needs an iPhone signed into the same Apple ID, nearby and
  unlocked-then-locked, with Continuity Camera enabled (Settings › General › AirPlay &
  Continuity). A USB cable can also be used as it's the most reliable way to get it connected.
- If you tapped **Disconnect** on the phone during a previous session, toggle that same
  Continuity Camera setting off and on to make it offer itself again.
- Detection waits up to 2.5s for a phone to publish itself; `HOARD_SCAN_WAIT=5` raises it.
- To confirm what the helper can see, independent of the TUI:
  `./bin/hoard-scan.app/Contents/MacOS/hoard-scan --list-devices`
- To check what a photo of a card actually reads as, without a camera:
  `./bin/hoard-scan.app/Contents/MacOS/hoard-scan --image card.heic --rotate 0`.
  It takes the same code path as a live capture and reports `collectorNumber`,
  `setCode`, and the raw `bottomLines` it matched against, which is the quickest
  way to see why a border did not read.
- The first scan prompts for camera permission (System Settings › Privacy & Security ›
  Camera). On-device OCR only, so no images leave your machine.
- Backing out is always available: <kbd>Esc</kbd> in the capture window, or
  <kbd>esc</kbd> in the terminal, cancels the scan and returns to the prompt without
  ending the session.
- If OCR misreads the name, you land back at the prompt with the recognized text
  pre-filled, so you can fix it and search manually.

## Development

```sh
make build     # go build -o hoard .
make test      # go test ./...   (no network needed)
make vet       # go vet ./...
make scan      # macOS camera helper (see above)
make all       # build + scan
make clean     # remove ./hoard and ./bin
```

CI runs `gofmt`, `go vet`, `go test` and `go build` on every push and pull
request, so a change that only fails formatting is caught before review.

There is one design document worth reading before making changes to how prices
are stored: [docs/mtgjson-storage.md](docs/mtgjson-storage.md) covers why hoard
keeps its own schema rather than adopting MTGJSON's, and which parts of that
investigation have since been built.

## License

[MIT](LICENSE) — © 2026 Christopher Phillips.
