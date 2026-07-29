# hoard

A small Go CLI that catalogs valuable Magic: The Gathering cards in a local
SQLite database. Add loose cards by pasting a Scryfall page URL, and import whole
**decks** from a deck-list link (or a pasted/exported text list). The tool records
how many of each card you own — across the loose collection and every deck — and
the current market price for each finish, fetched from the
[Scryfall API](https://scryfall.com/docs/api).

## Data model

Storage is normalized and provider-agnostic (no deck-site is referenced structurally):

- **`cards`** — the card catalog: identity + market price, one row per printing,
  shared across everything.
- **`containers`** — the singleton loose *collection* plus one row per *deck*; a
  generic `source` slug (`archidekt`, `moxfield`, `text`, `manual`) records origin.
- **`card_entries`** — quantity of a card (by finish and board) inside a container.

## Build

```sh
go build -o hoard .        # or: make build
```

The core is pure Go — no cgo or C toolchain required (uses `modernc.org/sqlite`).

### Optional: camera scan helper (macOS)

To enable scanning cards with your iPhone camera (see [Scanning a card](#scanning-a-card)),
build the small native helper (requires macOS + Xcode's Swift toolchain):

```sh
make scan                  # builds bin/hoard-scan.app via swiftc
# or: make all             # binary + scan helper together
```

Everything else works without it; if the helper isn't built, the in-app scan action
simply reports that it's unavailable.

## Usage

```sh
# Add cards interactively by name — no link needed. Opens an interactive add
# session (TUI): look a card up on Scryfall, answer only the questions needed to
# pinpoint one exact entry (which card if the name is ambiguous, which printing,
# which finish, how many), confirm — then it loops back so you can add another
# card without restarting. Type to filter long printing lists. Press esc at the
# name prompt (or ctrl+c anytime) to exit.
hoard add                         # start an empty add session
hoard add Ulamog, the Infinite Gyre   # pre-seed the first search
# In the session, press ctrl+o to scan a card with your camera (macOS, see below);
# ctrl+r switches which camera it uses.

# Add two non-foil copies of Ulamog by URL (non-interactive)
hoard add https://scryfall.com/card/uma/7/ulamog-the-infinite-gyre --qty 2

# Add a foil copy of the same card
hoard add https://scryfall.com/card/uma/7/ulamog-the-infinite-gyre --foil

# Show the collection and its total value
hoard list

# Refresh market prices for every card
hoard update-prices

# Set exact quantities (omit a flag to leave that count unchanged)
hoard set-qty https://scryfall.com/card/uma/7/... --normal 3 --foil 1

# Remove a card
hoard remove https://scryfall.com/card/uma/7/...

# Grand total value: loose collection + each deck
hoard summary
```

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
deck bars beneath them. A blank bar means a deck is worth $0.00 — usually one
whose prices haven't been fetched yet; run `hoard update-prices`.

### Scanning a card

Inside an add session (`hoard add`), press **`ctrl+o`** to identify a card with your
iPhone instead of typing its name.

Scanning uses **Continuity Camera only** — your iPhone, never the Mac's built-in webcam.
A fixed, user-facing camera can't be aimed at a card on the desk, so rather than fall back
to one and produce unreadable captures, hoard tells you no iPhone is connected. If you
have more than one iPhone paired you're asked which to use; the choice is remembered for
the session so bulk scanning doesn't ask again, and **`ctrl+r`** at the prompt re-runs
detection or switches phones.

A window then opens with the live feed **and stays open**. Frame a card, press **space**,
and the cascade runs in the terminal; once the card is saved you're back at framing for
the next one — no reopening the camera per card. Space and ←/→ work in either window, so
you can leave focus in the terminal. **Esc** closes the camera and returns to the name
prompt; the add session keeps going either way.

The preview starts rotated a quarter-turn clockwise, which is what a
portrait-held iPhone needs — Continuity Camera hands over a landscape frame and macOS
often can't tell how the phone is being held. If the framing is still wrong, **←/→**
rotate the preview, and the corrected angle is saved to `scan.json` beside the database,
so you only fix it once. The window title always shows the current angle and how much of
it came from macOS's automatic correction.

Press **`ctrl+r`** at either the prompt or the capture step to switch phones, and
**`ctrl+o`** at the prompt to jump back to a camera that's already open.

The card's title is read on-device with Apple's Vision OCR, matched to a
real card via Scryfall's fuzzy name search, and dropped into the normal printing → finish
→ quantity flow — with the identified card name pinned as a header the rest of the way, so
you can confirm the read was correct before adding.

Requirements and notes:
- macOS only, and the `hoard-scan.app` helper must be built (`make scan`).
- Continuity Camera needs an iPhone signed into the same Apple ID, nearby and unlocked-then-
  locked, with Continuity Camera enabled (Settings › General › AirPlay & Continuity). A USB
  cable is the most reliable way to get it connected.
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
- Backing out is always available: **Esc** in the capture window, or **esc** in the
  terminal, cancels the scan and returns to the prompt without ending the session.
- If OCR misreads the name, you land back at the prompt with the recognized text
  pre-filled, so you can fix it and search manually — never a dead end.

### Decks

```sh
# Import (or refresh) a deck from an Archidekt link
hoard deck add https://archidekt.com/decks/7319967/high_power_aristocrats

# Moxfield's API is Cloudflare-blocked, so export that deck to text
# (Moxfield → ⋯ → Export) and import the file:
hoard deck add --file my-deck.txt --name "My Edgar EDH" --source moxfield

# List decks with card counts and value
hoard deck list

# Show a deck's cards grouped by board (commander/main/side/maybe)
hoard deck show 1            # by id
hoard deck show "My Edgar EDH"   # or by name

# Delete a deck
hoard deck remove 1
```

Re-importing the same deck link updates it in place (no duplicates). Cards a deck
references are added to the shared catalog, so `update-prices` refreshes prices for
loose cards and decks together. Any card that can't be resolved on import is
reported and skipped, never silently dropped.

The text importer understands common decklist formats — `2 Sol Ring`,
`1x Lightning Bolt`, `1 Ulamog, the Infinite Gyre (UMA) 7 *F*`, and section headers
like `Commander` / `Sideboard` / `Maybeboard`.

### Database location

The database lives in a per-user data directory, so the same hoard is used no
matter which directory you run the command from:

| OS      | Default location                                       |
|---------|--------------------------------------------------------|
| macOS   | `~/Library/Application Support/hoard/hoard.db`          |
| Linux   | `$XDG_DATA_HOME/hoard/hoard.db` (else `~/.local/share/hoard/hoard.db`) |
| Windows | `%AppData%\hoard\hoard.db`                              |

The directory is created on first run, and the resolved path is printed the
first time the database is initialized. Override the location with the `--db`
flag or the `HOARD_DB` environment variable (both take precedence over the
default):

```sh
hoard --db ~/hoard/collection.db list
export HOARD_DB=~/hoard/collection.db
```

### Output width and color

Tables are laid out to fit the terminal exactly, truncating long names with `…`
rather than letting rows wrap. Widen the terminal and names un-truncate on their
own; `hoard deck list` always shows deck names in full.

Two environment variables override the defaults:

```sh
HOARD_WIDTH=100 hoard summary   # lay out for a specific width
HOARD_WIDTH=0   hoard summary   # never truncate, whatever the terminal size
NO_COLOR=1      hoard summary   # same layout, no bold/faint styling
```

Piping or redirecting turns off styling, truncation, and bars automatically, so
`hoard summary | grep` sees whole names and no escape sequences.

## Development

```sh
go test ./...    # unit tests (URL parsing + store, no network needed)
go vet ./...
```
