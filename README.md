# mtg-index

A small Go CLI that catalogs valuable Magic: The Gathering cards in a local
SQLite database. Add a card by pasting its Scryfall page URL; the tool records
how many you own (normal and foil) and the current market price for each finish,
fetched from the [Scryfall API](https://scryfall.com/docs/api).

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
```

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
