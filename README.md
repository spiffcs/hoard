# mtg-index

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
go build -o mtg .
```

Pure Go — no cgo or C toolchain required (uses `modernc.org/sqlite`).

## Usage

```sh
# Add two non-foil copies of Ulamog (fetches current prices)
mtg add https://scryfall.com/card/uma/7/ulamog-the-infinite-gyre --qty 2

# Add a foil copy of the same card
mtg add https://scryfall.com/card/uma/7/ulamog-the-infinite-gyre --foil

# Show the collection and its total value
mtg list

# Refresh market prices for every card
mtg update-prices

# Set exact quantities (omit a flag to leave that count unchanged)
mtg set-qty https://scryfall.com/card/uma/7/... --normal 3 --foil 1

# Remove a card
mtg remove https://scryfall.com/card/uma/7/...

# Grand total value: loose collection + each deck
mtg summary
```

### Decks

```sh
# Import (or refresh) a deck from an Archidekt link
mtg deck add https://archidekt.com/decks/7319967/high_power_aristocrats

# Moxfield's API is Cloudflare-blocked, so export that deck to text
# (Moxfield → ⋯ → Export) and import the file:
mtg deck add --file my-deck.txt --name "My Edgar EDH" --source moxfield

# List decks with card counts and value
mtg deck list

# Show a deck's cards grouped by board (commander/main/side/maybe)
mtg deck show 1            # by id
mtg deck show "My Edgar EDH"   # or by name

# Delete a deck
mtg deck remove 1
```

Re-importing the same deck link updates it in place (no duplicates). Cards a deck
references are added to the shared catalog, so `update-prices` refreshes prices for
loose cards and decks together. Any card that can't be resolved on import is
reported and skipped, never silently dropped.

The text importer understands common decklist formats — `2 Sol Ring`,
`1x Lightning Bolt`, `1 Ulamog, the Infinite Gyre (UMA) 7 *F*`, and section headers
like `Commander` / `Sideboard` / `Maybeboard`.

### Database location

Defaults to `./mtg_index.db`. Override with the `--db` flag or the
`MTG_INDEX_DB` environment variable:

```sh
mtg --db ~/mtg/collection.db list
```

## Development

```sh
go test ./...    # unit tests (URL parsing + store, no network needed)
go vet ./...
```
