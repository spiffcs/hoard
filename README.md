<p align="center">
    <img src="docs/assets/hoard-logo.png" width="150" alt="A cavern with a mountain of gold — the hoard logo">
</p>

# hoard

[![Validations](https://github.com/spiffcs/hoard/actions/workflows/validations.yaml/badge.svg)](https://github.com/spiffcs/hoard/actions/workflows/validations.yaml)
[![Release](https://img.shields.io/github/v/release/spiffcs/hoard?sort=semver)](https://github.com/spiffcs/hoard/releases)
[![License](https://img.shields.io/github/license/spiffcs/hoard)](LICENSE)

Track a Magic: The Gathering collection in a single SQLite file on your machine.

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

## Try it

Add cards by Scryfall link, then value what you hold:

```console
$ hoard add https://scryfall.com/card/uma/7/ulamog-the-infinite-gyre
✓ Added 1× Ulamog, the Infinite Gyre (uma/7) as nonfoil into Binder · $37.35

$ hoard report
VALUATION · prices as of 11 Aug 2026

BINDER     2  $586.33
DECKS · 0  0    $0.00

TOTAL      2  $586.33
...
```

Run `hoard` with no arguments for the browser, or `hoard help` for every command.

## What catches people out

**Adding by name needs a terminal.** `hoard add "Counterspell"` opens a picker,
so in a script it stops with `adding by name needs an interactive terminal`.
Pass a Scryfall URL instead.

**Prices are estimates**, aggregated daily from third parties rather than quoted
live. An absent price means unpriced, never free.

**The scanner is a separate application.** The release binary is the TUI and CLI
only; card capture is a Swift app for macOS and iPhone, built from `scan/`.

## Your data

| OS | Default location |
|---|---|
| macOS | `~/Library/Application Support/hoard/hoard.db` |
| Linux | `$XDG_DATA_HOME/hoard/hoard.db` (else `~/.local/share/hoard/hoard.db`) |
| Windows | `%AppData%\hoard\hoard.db` |

Override with `--db PATH` or `$HOARD_DB`. A schema upgrade backs the old file
up alongside it first. The card catalog and price downloads live in your cache
directory and are always safe to delete.

It is ordinary SQLite: read it with [any tool](schema/sqlite/README.md), or take
versioned JSON out with `hoard export --format json`
([schema](schema/json/README.md)).

## More

[CONTRIBUTING.md](CONTRIBUTING.md) · [RELEASE.md](RELEASE.md) · [SECURITY.md](SECURITY.md)

## License and legal

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
