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
go build -o hoard .
```

Pure Go — no cgo or C toolchain required (uses `modernc.org/sqlite`).

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

## Development

```sh
go test ./...    # unit tests (URL parsing + store, no network needed)
go vet ./...
```
