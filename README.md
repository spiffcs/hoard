<p align="center">
    <img src="docs/assets/hoard-logo.png" width="150" alt="The hoard logo: an ornate red and gold treasure chest, a gold dragon crest on its lid">
</p>

# hoard

[![Validations](https://github.com/spiffcs/hoard/actions/workflows/validations.yaml/badge.svg)](https://github.com/spiffcs/hoard/actions/workflows/validations.yaml)
[![Release](https://img.shields.io/github/v/release/spiffcs/hoard?sort=semver)](https://github.com/spiffcs/hoard/releases)
[![License](https://img.shields.io/github/license/spiffcs/hoard)](LICENSE)

A terminal collection tracker for Magic: The Gathering.

Your cards and decks live in a single SQLite file on your machine, priced daily
from MTGJSON and TCGCSV. Metadata and card information come from Scryfall.

<p align="center">
    <img src="docs/assets/demo.gif" width="100%" alt="A valuation report, then the browser: card detail with price history, the movers, watches and market screens, and a rarity filter">
</p>

## Install

```sh
curl -sSfL https://tools.aithirne.com/hoard/install.sh | sh -s -- -b /usr/local/bin
```

Or `go install github.com/spiffcs/hoard/cmd/hoard@latest` (Go 1.26+), or take an
archive from the [releases page](https://github.com/spiffcs/hoard/releases). The
macOS binaries are signed and notarized, so they open without a Gatekeeper
prompt; [RELEASE.md](RELEASE.md) covers verifying them.

Want to look before importing anything? `hoard demo` opens the browser on a
small sample collection, in a database of its own — your hoard is never opened.

## Bring your collection in

Most people arrive with an export from somewhere else. `hoard import` reads
ManaBox, Moxfield and Delver Lens CSVs, and hoard's own. It sniffs the header,
so the format usually needs no naming:

```console
$ hoard import manabox-export.csv
✓ Imported 1,284 cards (manabox format): 1,284 rows resolved.
```

Decks come from a decklist URL, or from a pasted list:

```console
$ hoard deck add https://moxfield.com/decks/xxxxxxxxxxxxxxxxxxxxxx
$ pbpaste | hoard deck add --file - --name "Modern Burn"
```

Loose cards go in one at a time, by Scryfall link:

```console
$ hoard add https://scryfall.com/card/uma/7/ulamog-the-infinite-gyre
✓ Added 1× Ulamog, the Infinite Gyre (uma/7) as nonfoil into Binder · $37.35
```

`--dry-run` works on all three: resolve and report, write nothing. On an import,
`--preserve-binders` recreates the file's own binders instead of putting
everything in one.

## Try It

Run `hoard` with no arguments for the browser, or `hoard help` for every command.
With nothing imported yet, `hoard demo` shows the same browser on sample data;
`hoard demo --reset` starts that sample over.

<p align="center">
    <img src="docs/assets/browse.png" width="100%" alt="The browser: binders and decks listed by value on the left, every card on the right with its set, finish, quantity and price, card names colored by color identity">
</p>

## Your data

| OS | Default location |
|---|---|
| macOS | `~/Library/Application Support/hoard/hoard.db` |
| Linux | `$XDG_DATA_HOME/hoard/hoard.db` (else `~/.local/share/hoard/hoard.db`) |

Override with `--db PATH` or `$HOARD_DB`. A schema upgrade backs the old file
up alongside it first. The card catalog and price downloads live in your cache
directory and are always safe to delete.

It is ordinary SQLite: read it with [any tool](schema/sqlite/README.md), or take
versioned JSON out with `hoard export --format json`
([schema](schema/json/README.md)).

## Scripting it

`--json` emits a versioned document instead of a table. `hoard schema` prints the
JSON Schema it validates against, and the schema files are published under
[`schema/json/`](schema/json/README.md) so a parser can pin a version.

Commands that do not honour `--json` **refuse** it rather than ignoring it —
output that silently dropped the flag would be indistinguishable, to a parser,
from output that respected it. `--dry-run` resolves and reports without writing.

Exit codes are decided in one place and are stable:

| Code | Meaning |
|---|---|
| 0 | Success |
| 1 | Failure — the error is on stderr |
| 2 | Partial Add: some of the cards landed, some did not |
| 3 | A watch fired. A result, not a failure — the alert is on stdout |
| 130 | Interrupted (SIGINT) |

```sh
hoard update-prices && hoard watch
```

## More

[CONTRIBUTING.md](CONTRIBUTING.md) · [AGENTS.md](AGENTS.md) · [RELEASE.md](RELEASE.md)
· [SECURITY.md](SECURITY.md) · [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)

## License and Legal

Code is [MIT](LICENSE) — © 2026 Christopher Phillips. Card imagery in this
repository remains the property of Wizards of the Coast and is **not** covered
by that license; see [NOTICE](NOTICE).

hoard is unofficial Fan Content permitted under the Fan Content Policy. Not
approved/endorsed by Wizards. Portions of the materials used are property of
Wizards of the Coast. ©Wizards of the Coast LLC.

Card data courtesy of [Scryfall](https://scryfall.com), [MTGJSON](https://mtgjson.com)
(MIT, © 2018–Present Zach Halpern) and [TCGCSV](https://tcgcsv.com). hoard is not
affiliated with, endorsed by, or sponsored by any of them. All prices are daily
estimates from third-party aggregators, not quotes, with absolutely no guarantee
— see stores for final prices.
