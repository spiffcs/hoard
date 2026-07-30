# hoard

[![CI](https://github.com/spiffcs/hoard/actions/workflows/ci.yml/badge.svg)](https://github.com/spiffcs/hoard/actions/workflows/ci.yml)

A fast, local-first CLI for managing a Magic: The Gathering collection. Your
cards live in a SQLite database on your machine; hoard tracks what you own
across binders and decks, keeps market prices current, and shows the whole
collection in a two-pane terminal browser:

```
COLLECTION                      CARDS · BINDER                          335 · $2,893.87
NAME                     VALUE  NAME                SET/NUM  FINISH  QTY    PRICE    VALUE
Binder               $2,893.87  Solitude            mh2/32   -        ×4   $34.28  $137.12
Eldrazi Incursion …    $545.18  Bitterblossom       uma/85   -        ×4   $34.11  $136.44
Tricky Terrain Col…    $459.56  Ancient Tomb        uma/236  foil     ×1  $134.90  $134.90
Graveyard Overdriv…    $359.01  Stoneforge Mystic   2xm/31   -        ×4   $31.34  $125.36
──────────────────────────────────────────────────────────────────────────────────────────
1/23 · sorted by value
tab cards · ↑/↓ move · / filter · s sort · v views · d remove deck · u undo · q quit
```

Cards get in four ways: type a name, paste a Scryfall URL, import a whole deck
(an Archidekt link or an exported decklist file), or — on macOS — **point your
iPhone at the card** and let Vision OCR read it. Prices come from the
[Scryfall API](https://scryfall.com/docs/api), with
[MTGJSON](https://mtgjson.com/) filling the gaps Scryfall can't price.

## Install

Requires **Go 1.26+** and an internet connection for prices. The core is pure Go
(no cgo, no C toolchain).

```sh
git clone https://github.com/spiffcs/hoard && cd hoard
make build          # → ./hoard
```

Or, without cloning: `go install github.com/spiffcs/hoard@latest`.

On macOS with Xcode's Swift toolchain, `make all` also builds the iPhone
[card-scanning helper](docs/scanning.md); everything else works without it.

## First five minutes

```sh
# 1. Get some cards in — import a deck you already have online...
./hoard deck add https://archidekt.com/decks/7319967/high_power_aristocrats

#    ...or add loose cards interactively (searches as you type)
./hoard add

# 2. Fetch prices. The first run offers to build a local card catalog —
#    say yes; it makes almost everything instant and offline.
./hoard update-prices

# 3. Optional, once: import 90 days of price history from MTGJSON,
#    so "what moved this month?" has an answer on day one.
./hoard backfill-prices

# 4. Browse: filter, sort, drill into any card's printings and price history.
./hoard
```

The database is created on first run in a per-user data directory and its path
is printed once. `hoard help` lists every command.

## Everyday commands

| Command | |
|---|---|
| `hoard` | browse everything — filter, sort, edit quantities, card detail |
| `hoard add [name]` | add cards interactively; also takes a Scryfall URL with `--qty`/`--foil` |
| `hoard deck add <url>` | import or refresh a deck from an Archidekt link |
| `hoard deck add --file x.txt` | import an exported decklist (Moxfield et al.) |
| `hoard deck remove <name>` | delete a deck — any unambiguous part of the name works |
| `hoard binder new <name>` | create a named binder; also `list`, `rename`, `rm` |
| `hoard update-prices` | refresh prices (Scryfall updates daily) and report movers |
| `hoard movers --since 30d` | biggest risers and sinkers over a window |
| `hoard unpriced` | cards counting as $0.00, and where they're held |
| `hoard repair-finishes` | fix cards stored as a finish their printing doesn't have |
| `hoard arbitrage` | where vendors disagree about your cards' worth |
| `hoard catalog` | status of the local card catalog; `update` rebuilds it |

The browser ([full guide](docs/browsing.md)) replaces separate list/summary
commands: the left pane holds your binders and decks, the right pane their
cards. `/` filters by name or trait (`rarity:mythic finish:foil qty>1`),
`enter` opens a card's printings, holdings, and price-history sparklines, and
`v` cycles views: holdings → movers → unpriced → arbitrage. Piped or
redirected, `hoard` prints a plain summary table instead, so `hoard | grep`
works.

Pricing details — how history accumulates, MTGJSON gap-filling, the local
catalog, and vendor arbitrage — are covered in [docs/pricing.md](docs/pricing.md).

## Decks and binders

A **binder** holds loose cards; a **deck** is imported from a source and stays
true to it (re-importing the same link updates in place, and deck cards are
read-only in the browser). New cards land in the default binder; create more
with `hoard binder new` to keep, say, trade stock separate.

Moxfield's API is Cloudflare-blocked, so export the deck to text
(Moxfield → ⋯ → Export) and import the file:

```sh
hoard deck add --file my-deck.txt --name "My Edgar EDH" --source moxfield
```

The text importer understands common formats — `2 Sol Ring`, `1x Lightning
Bolt`, `1 Ulamog, the Infinite Gyre (UMA) 7 *F*` — plus `Commander`/`Sideboard`
headers. Unresolvable lines are reported, never silently dropped, and imports
are priced immediately, including foil-only printings the list mislabels.

## Scanning cards (macOS)

With the helper built (`make all`), press <kbd>ctrl+o</kbd> in an add session to
identify a card with your iPhone via Continuity Camera. The capture window stays
open between shots, so working through a box is: frame, press <kbd>space</kbd>,
confirm, repeat. OCR reads the title *and* the collector number, so the exact
printing is usually pre-selected — a suggestion you confirm, never a silent
decision. Setup and troubleshooting: [docs/scanning.md](docs/scanning.md).

## Database

| OS | Default location |
|---|---|
| macOS | `~/Library/Application Support/hoard/hoard.db` |
| Linux | `$XDG_DATA_HOME/hoard/hoard.db` (else `~/.local/share/hoard/hoard.db`) |
| Windows | `%AppData%\hoard\hoard.db` |

Override with `--db PATH` (anywhere on the command line) or `$HOARD_DB`. Schema
upgrades back up the old file alongside first (`hoard.db.bak-v1-20260729`), so
nothing is ever lost. The card catalog and price downloads live separately in
your cache directory and are always safe to delete.

## Development

```sh
make build     # go build -o hoard .
make test      # go test ./...   (no network needed)
make vet       # go vet ./...
make scan      # macOS camera helper
make clean     # remove ./hoard and ./bin
```

CI runs `gofmt`, `go vet`, `go test`, and `go build` on every push. Before
changing how prices are stored, read
[docs/mtgjson-storage.md](docs/mtgjson-storage.md).

## License

[MIT](LICENSE) — © 2026 Christopher Phillips.
