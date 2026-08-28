<p align="center">
    <img src="docs/assets/hoard-logo.png" width="250" alt="The hoard logo: an ornate red and gold treasure chest, a gold dragon crest on its lid">
</p>

# hoard

[![Validations](https://github.com/spiffcs/hoard/actions/workflows/validations.yaml/badge.svg)](https://github.com/spiffcs/hoard/actions/workflows/validations.yaml)
[![Release](https://img.shields.io/github/v/release/spiffcs/hoard?sort=semver)](https://github.com/spiffcs/hoard/releases)
[![License](https://img.shields.io/github/license/spiffcs/hoard)](LICENSE)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/spiffcs/hoard/badge)](https://scorecard.dev/viewer/?uri=github.com/spiffcs/hoard)
[![Buy Me A Coffee](https://img.shields.io/badge/support-buy%20me%20a%20coffee-FFDD00?logo=buymeacoffee&logoColor=black)](https://buymeacoffee.com/spiffcs)

A collection tracker for Magic: The Gathering on the command line.

Data is stored as a single SQLite file on your machine. The cards are priced daily from [MTGJSON](https://mtgjson.com/) and [TCGCSV](https://tcgcsv.com/). Metadata and card information come from [Scryfall](https://scryfall.com/).

<p align="center">
    <img src="docs/assets/demo.gif" width="100%" alt="The browser on the sample collection: a card's price history with a six-check fall named, what a set is still missing and what finishing it would cost, binders and decks, the command palette, then the movers and market screens">
</p>

<p align="center">
    <sub>Recorded from <code>hoard demo</code> a sample collection that ships with hoard, not a real one.</sub>
</p>

## Quickstart

### Install

```sh
curl -sSfL https://tools.aithirne.com/hoard/install.sh | sh -s -- -b "$HOME/.local/bin"
```

Make sure `$HOME/.local/bin` is on your path!

Or `go install github.com/spiffcs/hoard/cmd/hoard@latest` (Go 1.26+), or take an archive from the [releases page](https://github.com/spiffcs/hoard/releases).

<details>
<summary>Where that script comes from, and how to verify it</summary>

<br>

`tools.aithirne.com` is from the maintainer (see [triage](https://github.com/spiffcs/triage) as well).

The script it serves is [`install.sh`](install.sh) from this repository. The release workflow uploads that exact file. Please read it before running it if you're curious about the install process.

```sh
diff <(curl -sSfL https://tools.aithirne.com/hoard/install.sh) \
     <(curl -sSfL https://raw.githubusercontent.com/spiffcs/hoard/main/install.sh)
```

It downloads from this repository's releases and verifies the SHA-256. You can add `-v` to verify the Sigstore signature too if you have cosign. A trailing tag pins the version instead of taking the latest.

```sh
curl -sSfL https://tools.aithirne.com/hoard/install.sh | sh -s -- -v -b "$HOME/.local/bin" v0.1.0
```

The macOS binaries are signed and notarized and should open without a Gatekeeper prompt; [RELEASE.md](RELEASE.md) covers verifying them.

</details>

### Look before you import anything

```sh
hoard demo
```

`hoard demo` opens the browser on a small sample collection. This is a demo database of its own: your main hoard DB is never opened or clobbered, and `hoard demo --reset` starts the sample over.

<p align="center">
    <img src="docs/assets/browse.png" width="100%" alt="The browser: the sets you own cards from listed by value on the left, every card on the right with its set, finish, quantity and price, card names colored by color identity">
</p>

Once you have cards of your own, `hoard` with no arguments opens the same browser on them. Inside it, <kbd>/</kbd> filters, <kbd>:</kbd> opens the command palette, and <kbd>a</kbd> starts the add flow.

## What it does

`hoard` on its own opens the browser, and `hoard help` lists every command.
These are the ones worth knowing about first.

**Getting cards in**

| Command | |
| --- | --- |
| `add` | Add cards by name, link, or list |
| `import` | Add a collection CSV from another app, or from hoard |
| `deck` | Import, refresh and remove decks |
| `folder` | Group decks into folders |
| `binder` | Organize the loose collection into labelled parts |
| `move` | Move piped holdings into a binder |

**Keeping it priced**

| Command | |
| --- | --- |
| `update-prices` | Refresh prices (Scryfall updates daily) |
| `movers` | Biggest risers and sinkers you hold |
| `market` | Vendor prices vs TCGplayer's last-sold prices |
| `watch` | Check price watches (no network; exit 3 = fired) |
| `report` | Dated valuation: totals, binders, top holdings |

**Getting data out**

| Command | |
| --- | --- |
| `export` | Holdings as CSV or JSON, in hoard's format or theirs |
| `merge` | Fold another hoard database into this one |
| `schema` | The JSON Schema that this build's `--json` output follows |

Two more have sections of their own below: `compendium` builds a database of Magic you don't own, and `demo` opens the browser on a sample collection.

## Import your collection

Most people arrive with an export from somewhere else. `hoard import` reads [ManaBox](https://manabox.app/), [Moxfield](https://moxfield.com/) and [Delver Lens](https://www.delverlab.com/) CSVs. It is also backwards compatible with hoard's own format. If you have some kind of format that's incompatible then please file an issue and I'll work with you to see how your case can be imported.

```console
$ hoard import manabox-export.csv
✓ Imported 1,284 cards (manabox format): 1,284 rows resolved.
```

Decks can be imported from an Archidekt URL, or from a pasted list:

```console
$ hoard deck add https://archidekt.com/decks/1234567/my-deck
$ pbpaste | hoard deck add --file - --name "Modern Burn"
```

Archidekt is the only site with a URL importer.[^1] Try exporting the deck to text and pipe it in: the plain `1 Sol Ring` / `1x Sol Ring (C21) 125` shape that Moxfield, MTGGoldfish, TappedOut, EDHREC and Scryfall all export is read directly, foil markers (`*F*`) and `SB:` sideboard lines will be included.

Loose cards can go in one at a time by Scryfall link:

```console
$ hoard add https://scryfall.com/card/uma/7/ulamog-the-infinite-gyre
✓ Added 1× Ulamog, the Infinite Gyre (uma/7) as nonfoil into Binder · $37.35
```

`--dry-run` works on all three: resolve and report, write nothing. On an import, `--preserve-binders` recreates the file's own binders instead of putting everything in one.

Loose cards also have a full add flow in the TUI. [Adding cards](docs/adding-cards.md) walks through typing a card in, and through building, pairing and scanning with the iPhone helper (experimental; it needs more attention before I try to publish it to the App Store).

[^1]: Moxfield's API is behind Cloudflare, and most others have none.

## Browse Magic you don't own

The browser does not have to be pointed at cards you own. [`hoard compendium`](docs/compendium.md) builds a database from a specified slice of Magic. That can be all Magic, go nuts. A handful of sets priced and backfilled with price data can help when building or pricing decks for a new format.

```sh
hoard compendium --rarity mythic,rare --since 2020 mythics-rare.db
hoard --db mythics-rare.db
```

To build a whole format instead of a date range, `hoard compendium --format premodern premodern.db` does Premodern in one command. `--format` takes any format Scryfall records legality for, including `legacy`, `modern`, `pauper` and `commander`. Legality is recorded per card rather than per printing, so that build keeps later reprints of legal cards too, often the cheapest copy. Add `--era` for the period-correct set list instead. See the [compendium docs](docs/compendium.md) for more.

## Where your data lives

| OS    | Default location                                                          |
| ----- | ------------------------------------------------------------------------- |
| macOS | `~/Library/Application Support/hoard/hoard.db`                            |
| Linux | `$XDG_DATA_HOME/hoard/hoard.db` (else `~/.local/share/hoard/hoard.db`)    |

Override with `--db PATH` or `$HOARD_DB`. A schema upgrade backs the old file up alongside it first. The card catalog and price downloads live in your cache directory and are always safe to delete.

It is ordinary SQLite: read it with [any tool](schema/sqlite/README.md), or take versioned JSON out with `hoard export --format json` ([schema](schema/json/README.md)).

## Docs

- [Filtering](docs/filtering.md): the query language behind <kbd>/</kbd> and `--filter`
- [Recipes](docs/recipes.md): wantlists, and moving cards in bulk
- [Scripting](docs/scripting.md): `--json` documents, and piping them through `jq`
- [Adding cards](docs/adding-cards.md): the TUI add flow and the iPhone scanner
- [Compendiums](docs/compendium.md): building a database of Magic you don't own
- [Updating](docs/updating.md): how hoard tells you about a new release
- [CHANGELOG](docs/CHANGELOG.md)

Project documents: [CONTRIBUTING](CONTRIBUTING.md) · [AGENTS](AGENTS.md) · [RELEASE](RELEASE.md) · [SECURITY](SECURITY.md) · [CODE_OF_CONDUCT](CODE_OF_CONDUCT.md)

## Support

hoard is free and MIT licensed, and it stays that way. If it saved you a spreadsheet, you can [buy me a coffee](https://buymeacoffee.com/spiffcs). It keeps the macOS builds signed and notarized and the install script hosted.

## License and Legal

Code is [MIT](LICENSE), © 2026 Christopher Phillips. Card imagery in this repository remains the property of Wizards of the Coast and is **not** covered by that license; see [NOTICE](NOTICE).

hoard is unofficial Fan Content permitted under the Fan Content Policy. Not approved/endorsed by Wizards. Portions of the materials used are property of Wizards of the Coast. ©Wizards of the Coast LLC.

Card data courtesy of [Scryfall](https://scryfall.com), [MTGJSON](https://mtgjson.com) (MIT, © 2018–Present Zach Halpern) and [TCGCSV](https://tcgcsv.com). hoard is not affiliated with, endorsed by, or sponsored by any of them. All prices are daily estimates from third-party aggregators, not quotes, with absolutely no guarantee. Visit and support your LGS for final prices.
