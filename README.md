# hoard

A small Go CLI that catalogs Magic: The Gathering cards in a local
SQLite database.

**Point your iPhone at a card and it gets filed.** On macOS, hoard uses
Continuity Camera to read a card's title with Apple's Vision OCR. It 
then matches it against Scryfall's fuzzy name search and drops it straight into
the add flow. The camera window stays open between shots, so working through a
box of cards is: frame, press space, confirm, repeat. See
[Scanning a card](#scanning-a-card).

You can also type a card's name, paste a Scryfall page URL, or import whole
**decks** from a deck-list link. Hoard also works using an exported deck list text. 
However a card gets in, hoard records how many you own across the loose collection or decks.
It also adds the current market price on each add. These are fetched from the
[Scryfall API](https://scryfall.com/docs/api).

## Requirements

- **Go 1.26.1 or newer** (`go version` to check). The module targets that
  version and will not build on older toolchains.
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

# Cards counting as $0.00 in your totals, and where they are held
hoard unpriced
```

Scryfall recalculates its price data roughly once a day, so `update-prices` is
worth running about that often. Running it several times in a day re-fetches the
entire catalog to arrive at the same numbers: it is the only command that talks
to Scryfall about every card you own, in batches of 75.

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
