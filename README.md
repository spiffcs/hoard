# hoard

A small Go CLI that catalogs valuable Magic: The Gathering cards in a local
SQLite database. Add loose cards by pasting a Scryfall page URL, and import whole
**decks** from a deck-list link (or a pasted/exported text list). The tool records
how many of each card you own — across the loose collection and every deck — and
the current market price for each finish, fetched from the
[Scryfall API](https://scryfall.com/docs/api).

## Requirements

- **Go 1.26.1 or newer** (`go version` to check) — the module targets that version
  and will not build on older toolchains.
- An internet connection for the Scryfall API. Nothing else is needed: the core is
  pure Go, with no cgo or C toolchain (it uses `modernc.org/sqlite`).
- Optional, macOS only: Xcode's Swift toolchain, if you want to
  [scan cards with an iPhone camera](#scanning-a-card).

## Quick start

```sh
git clone https://github.com/spiffcs/hoard && cd hoard
go build -o hoard .                  # or: make build

./hoard add                          # add cards interactively — start here
./hoard list                         # what's in the loose collection
./hoard summary                      # grand total across collection + decks
```

The database is created on first run in a per-user data directory, and its path is
printed once so you know where it went. See
[Database location](#database-location) to put it somewhere else.

Run `hoard help` (or `hoard` with no arguments) for the full command list.

## Usage

> **Global flags must come before the command.** `hoard --db ./my.db summary` works;
> `hoard summary --db ./my.db` silently uses the default database instead.

```sh
# Add cards interactively (see below) — the main way in
hoard add

# Show the collection and its total value
hoard list

# Grand total value: loose collection + each deck
hoard summary

# Refresh market prices for every card in the catalog
hoard update-prices
```

### Adding cards

`hoard add` opens an interactive add session (a TUI): search Scryfall by name, then
answer only the questions needed to pinpoint one exact entry — which card if the name
is ambiguous, which printing, which finish, how many — confirm, and it loops back so
you can add another card without restarting. Type to filter long printing lists. Press
<kbd>esc</kbd> at the name prompt (or <kbd>ctrl+c</kbd> anytime) to exit.

```sh
hoard add                                # start an empty add session
hoard add Ulamog, the Infinite Gyre      # pre-seed the first search
```

This needs a real terminal — it can't be piped or run from a script.

Inside a session, <kbd>ctrl+o</kbd> scans a card with your camera (macOS — see
[Scanning a card](#scanning-a-card)) and <kbd>ctrl+r</kbd> switches which camera it uses.

A Scryfall page URL also works, and skips the session entirely — useful in scripts:

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
deck bars beneath them. A blank bar means a deck is worth $0.00 — usually one whose
prices haven't been fetched yet; run `hoard update-prices`.

## Decks

```sh
# Import (or refresh) a deck from an Archidekt link
hoard deck add https://archidekt.com/decks/7319967/high_power_aristocrats

# Moxfield's API is Cloudflare-blocked, so export that deck to text
# (Moxfield → ⋯ → Export) and import the file:
hoard deck add --file my-deck.txt --name "My Edgar EDH" --source moxfield

# List decks with card counts and value, most valuable first
hoard deck list

# Show a deck's cards, grouped by board (commander/main/side/maybe)
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

The text importer understands common decklist formats — `2 Sol Ring`,
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

To start over, delete that file — it is the entire hoard.

## Output width and color

Tables are laid out to fit the terminal exactly, truncating long names with `…`
rather than letting rows wrap. Widen the terminal and names un-truncate on their own;
`hoard deck list` always shows deck names in full.

Two environment variables override the defaults:

```sh
HOARD_WIDTH=100 hoard summary   # lay out for a specific width
HOARD_WIDTH=0   hoard summary   # never truncate, whatever the terminal size
NO_COLOR=1      hoard summary   # same layout, no bold/faint styling
```

Piping or redirecting turns off styling, truncation, and bars automatically, so
`hoard summary | grep` sees whole names and no escape sequences.

## Scanning a card

> macOS only, and it needs the `hoard-scan.app` helper:
>
> ```sh
> make scan                  # builds bin/hoard-scan.app via swiftc
> # or: make all             # binary + scan helper together
> ```
>
> Everything else works without it; if the helper isn't built, the in-app scan
> action simply reports that it's unavailable.

Inside an add session (`hoard add`), press <kbd>ctrl+o</kbd> to identify a card with
your iPhone instead of typing its name.

Scanning uses **Continuity Camera only** — your iPhone, never the Mac's built-in
webcam. A fixed, user-facing camera can't be aimed at a card on the desk, so rather
than fall back to one and produce unreadable captures, hoard tells you no iPhone is
connected. If you have more than one iPhone paired you're asked which to use; the
choice is remembered for the session so bulk scanning doesn't ask again, and
<kbd>ctrl+r</kbd> at the prompt re-runs detection or switches phones.

A window then opens with the live feed **and stays open**. Frame a card, press
<kbd>space</kbd>, and the cascade runs in the terminal; once the card is saved you're
back at framing for the next one — no reopening the camera per card. Space and ←/→
work in either window, so you can leave focus in the terminal. <kbd>Esc</kbd> closes
the camera and returns to the name prompt; the add session keeps going either way.

The preview starts rotated a quarter-turn clockwise, which is what a portrait-held
iPhone needs — Continuity Camera hands over a landscape frame and macOS often can't
tell how the phone is being held. If the framing is still wrong, **←/→** rotate the
preview, and the corrected angle is saved to `scan.json` beside the database, so you
only fix it once. The window title always shows the current angle and how much of it
came from macOS's automatic correction.

Press <kbd>ctrl+r</kbd> at either the prompt or the capture step to switch phones, and
<kbd>ctrl+o</kbd> at the prompt to jump back to a camera that's already open.

The card's title is read on-device with Apple's Vision OCR, matched to a real card via
Scryfall's fuzzy name search, and dropped into the normal printing → finish → quantity
flow — with the identified card name pinned as a header the rest of the way, so you can
confirm the read was correct before adding.

### Notes and troubleshooting

- Continuity Camera needs an iPhone signed into the same Apple ID, nearby and
  unlocked-then-locked, with Continuity Camera enabled (Settings › General › AirPlay &
  Continuity). A USB cable is the most reliable way to get it connected.
- If you tapped **Disconnect** on the phone during a previous session, toggle that same
  Continuity Camera setting off and on to make it offer itself again.
- Detection waits up to 2.5s for a phone to publish itself; `HOARD_SCAN_WAIT=5` raises it.
- To confirm what the helper can see, independent of the TUI:
  `./bin/hoard-scan.app/Contents/MacOS/hoard-scan --list-devices`
- To debug OCR without a camera, run it against a photo of a card at a given rotation —
  it takes the same code path as a live capture:
  `./bin/hoard-scan.app/Contents/MacOS/hoard-scan --image card.heic --rotate 90`
  If the reported name is rules text or the copyright line, the image reached Vision
  rotated the wrong way.
- To capture what a real scan actually sent to OCR, set `HOARD_SCAN_DEBUG_DIR=/some/dir`
  before running `hoard add`. Each capture writes `capture-raw.png` (straight off the
  camera) and `capture-ocr.png` (after rotation, exactly what Vision read).
- The first scan prompts for camera permission (System Settings › Privacy & Security ›
  Camera). On-device OCR only — no images leave your machine.
- Backing out is always available: <kbd>Esc</kbd> in the capture window, or
  <kbd>esc</kbd> in the terminal, cancels the scan and returns to the prompt without
  ending the session.
- If OCR misreads the name, you land back at the prompt with the recognized text
  pre-filled, so you can fix it and search manually — never a dead end.

## Data model

Storage is normalized and provider-agnostic (no deck-site is referenced structurally):

- **`cards`** — the card catalog: identity + market price, one row per printing,
  shared across everything.
- **`containers`** — the singleton loose *collection* plus one row per *deck*; a
  generic `source` slug (`archidekt`, `moxfield`, `text`, `manual`) records origin.
- **`card_entries`** — quantity of a card (by finish and board) inside a container.

Rendering for every CLI table lives in `internal/ui`, so column layout, money
formatting, and terminal detection are shared rather than re-implemented per command.

## Development

```sh
make build     # go build -o hoard .
make test      # go test ./...   — no network needed
make vet       # go vet ./...
make scan      # macOS camera helper (see above)
make all       # build + scan
make clean     # remove ./hoard and ./bin
```

Tests cover URL and decklist parsing, the store's aggregation queries, the TUI's pure
helpers, and the table/layout engine in `internal/ui`. None of them hit the network or
touch your real database.
