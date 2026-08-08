<p align="center">
    <img src="docs/assets/hoard-logo.png" width="271" alt="A cavern with a mountain of gold — the hoard logo">
</p>

# hoard

**CLI for managing a Magic: The Gathering collection**.

```

## Install

Requires **Go 1.26+** and an internet connection for prices.

```sh
git clone https://github.com/spiffcs/hoard && cd hoard
make build          # → ./hoard
```

Or, without cloning: `go install github.com/spiffcs/hoard@latest`.

## Scripting and your data

Every card carries a Scryfall id and an MTGJSON uuid, so a document joins straight against either
ecosystem's bulk data.

## Decks and binders

## Scanning cards (macOS + iPhone)

## Database

| OS | Default location |
|---|---|
| macOS | `~/Library/Application Support/hoard/hoard.db` |
| Linux | `$XDG_DATA_HOME/hoard/hoard.db` (else `~/.local/share/hoard/hoard.db`) |
| Windows | `%AppData%\hoard\hoard.db` |

Override with `--db PATH` (anywhere on the command line) or an environment variable `$HOARD_DB`.
Schema upgrades back up the old file alongside first (`hoard.db.bak-v1-20260729`). 

The card catalog and price downloads live separately in
your cache directory and are always safe to delete.

## Development

```sh
make build     # go build -o hoard .
make test      # go test ./...   (no network needed)
make vet       # go vet ./...
```

## License

[MIT](LICENSE) — © 2026 Christopher Phillips.
