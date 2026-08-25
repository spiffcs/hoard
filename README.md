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

Data is stored as a single SQLite file on your machine. The cards are priced daily from [MTGJSON](https://mtgjson.com/) and [TCGCSV](https://tcgcsv.com/).

Metadata and card information come from [Scryfall](https://scryfall.com/).

<p align="center">
    <img src="docs/assets/demo.gif" width="100%" alt="The browser on the sample collection: a card's price history with a six-check fall named, what a set is still missing and what finishing it would cost, binders and decks, the command palette, then the movers and market screens">
</p>

<p align="center">
    <sub>Recorded from <code>hoard demo</code> a sample collection that ships with hoard, not a real one.</sub>
</p>

## Install

```sh
curl -sSfL https://tools.aithirne.com/hoard/install.sh | sh -s -- -b "$HOME/.local/bin"
```

Make sure `$HOME/.local/bin` is on your path!

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

### Golang/Github Specific

`go install github.com/spiffcs/hoard/cmd/hoard@latest` (Go 1.26+), or take an archive from the [releases page](https://github.com/spiffcs/hoard/releases). The macOS binaries are signed and notarized and should open without a Gatekeeper prompt; [RELEASE.md](RELEASE.md) covers verifying them.

Do you want to look at the TUI before importing anything? `hoard demo` opens the browser on a small sample collection. This is a demo database of its own. Your main hoard DB is never opened or clobbered.

## Bringing Your Collection In

Most people arrive with an export from somewhere else. `hoard import` reads ManaBox, Moxfield and Delver Lens CSVs. If you have some kind of format that's incompatible file an issue and I'll work with you to see how your case can be imported.

Import is also backwards compatible with hoard's own format.

```console
$ hoard import manabox-export.csv
✓ Imported 1,284 cards (manabox format): 1,284 rows resolved.
```

Decks can be imported from an Archidekt URL, or from a pasted list:

```console
$ hoard deck add https://archidekt.com/decks/1234567/my-deck
$ pbpaste | hoard deck add --file - --name "Modern Burn"
```

Archidekt is the only site with a URL importer. Moxfield's API is behind Cloudflare, and most others have none. Try exporting the deck to text and pipe it in: the plain `1 Sol Ring` / `1x Sol Ring (C21) 125` shape that Moxfield, MTGGoldfish, TappedOut, EDHREC and Scryfall all export is read directly, foil markers (`*F*`) and `SB:` sideboard lines will be included.

Loose cards can go in one at a time by Scryfall link:

```console
$ hoard add https://scryfall.com/card/uma/7/ulamog-the-infinite-gyre
✓ Added 1× Ulamog, the Infinite Gyre (uma/7) as nonfoil into Binder · $37.35
```

`--dry-run` works on all three: resolve and report, write nothing. On an import, `--preserve-binders` recreates the file's own binders instead of putting everything in one.

Loose cards also have a full add flow in the TUI. [Adding cards](docs/adding-cards.md) walks through typing a card in, and through building, pairing and scanning with the iPhone helper.

NOTE: the iPhone helper is experimental and needs more attention and effort before I try to publish it to the App Store.

## Try It

Run `hoard` with no arguments to see the browser, or `hoard help` for every command. With nothing imported yet, `hoard demo` can show the same browser with sample data; `hoard demo --reset` starts that sample over. When you're in the main browser you can use `:` to view the main command palette.

The browser also does not have to be pointed at cards you own. [`hoard compendium`](docs/compendium.md) builds a database from a specified slice of Magic (can be all Magic, go nuts). The example shows every mythic and rare since 2020 but feel free to make your own.

A handful of sets priced and backfilled with price data can help people when building/pricing decks for new formats

```sh
hoard compendium --rarity mythic,rare --since 2020 mythics-rare.db
hoard --db mythics-rare.db
```

To build a whole format instead of a date range, `hoard compendium --format premodern premodern.db` does Premodern in one command. `--format` takes any format Scryfall records legality for. This includes things like `legacy`, `modern`, `pauper`, `commander`. Legality is recorded per card rather than per printing, so that build keeps later reprints of legal cards too — often the cheapest copy. Add `--era` for the period-correct set list instead. See [compendium docs](docs/compendium.md) for more information.

<p align="center">
    <img src="docs/assets/browse.png" width="100%" alt="The browser: the sets you own cards from listed by value on the left, every card on the right with its set, finish, quantity and price, card names colored by color identity">
</p>

## Filtering

Press <kbd>/</kbd> in the browser to narrow what you are looking at. This command line gives you the same query as `hoard export --filter`

```console
$ hoard export --binder Binder --filter 'price<1 rarity:common'
```

| Key             | Matches                                | Example                       |
| --------------- | -------------------------------------- | ----------------------------- |
| *(bare word)*   | card name, anywhere in it              | `sol ring`, `"lion's eye"`    |
| `name`          | card name, anywhere in it              | `name:ulamog`                 |
| `set`           | set code                               | `set:uma`                     |
| `finish`        | `nonfoil`, `foil` or `etched`          | `finish:foil`                 |
| `board`         | `main`, `side`, `commander` or `maybe` | `board:side`                  |
| `qty`           | copies held                            | `qty>=4`                      |
| `price`         | price per copy, USD                    | `price<1`                     |
| `value`         | copies × price, USD                    | `value>=20`                   |
| `cmc`           | mana value                             | `cmc<=2`                      |
| `rarity`        | the whole rarity                       | `rarity:mythic`               |
| `type`, `t`     | type line                              | `t:creature`                  |
| `artist`        | artist                                 | `artist:guay`                 |
| `layout`        | Scryfall layout                        | `layout:transform`            |
| `setname`       | full set name                          | `setname:"modern horizons"`   |
| `color`, `c`    | colour identity letters                | `c:wu`                        |

Terms are ANDed, so repeating a key makes a range: `price>=5 price<20`. The comparisons `>` `>=` `<` `<=` work on the numeric keys; everything else takes `:` or `=`. Quote anything with a space in it.

Text matching ignores case and matches anywhere in the field. An exception is `rarity`, `finish` and `board`, which must match whole. `c:wu` asks for a colour identity containing both W and U, not one that is exactly WU.

## A Wantlist

There is no wantlist screen, but a user can set a binder that does not count toward your collection (using the x key in the TUI or through the CLI below) as a wantlist.

```console
$ hoard binder new want
Created binder #2 "want"
$ hoard binder exclude want
Binder "want" is no longer counted toward your collection
```

Cards you are hunting go in it the same way anything else goes into a binder.

```console
$ hoard add https://scryfall.com/card/uma/7/ulamog-the-infinite-gyre --binder want
✓ Added 1× Ulamog, the Infinite Gyre (uma/7) as nonfoil into want · $36.32
$ pbpaste | hoard add --file - --binder want
```

Excluding a binder changes the accounting. Everything in our example `want` is still priced by `hoard update-prices` and it will still turn up in `hoard movers`. You can add watches to cards in excluded binders as well to trigger on when one hits a certain price.

```console
$ hoard watch add "Ulamog, the Infinite Gyre" --under 30
Watching Ulamog, the Infinite Gyre (uma/7) nonfoil: under $30.00.
```

What exclusion gives you is that none of it counts as yours. `hoard report` leaves the binder out of the total, so a wantlist can never inflate what your collection is worth, while `hoard binder list` still totals it up so you can see what finishing the list would cost:

```console
$ hoard binder list
ID  NAME    CARDS   VALUE
 1  Binder      0   $0.00
 2  Want *      1  $36.32
* not counted toward your collection
```

When you actually get a card on the list it's as easy as moving it out of `want` and into which ever binder you have stored to card in.

That happens in the browser: select the card, <kbd>enter</kbd> for its detail, <kbd>up</kbd> to drop into the row for the copy you hold, then <kbd>right</kbd> along that row to its last field which is the binder it is in. <kbd>enter</kbd> there asks which binder to move it to. It counts toward your collection from the moment it lands in a counted binder; <kbd>esc</kbd> back out to the browser and <kbd>u</kbd> undoes the move.

### Moving Bulk Cards

For one card the above is the quickest way to move it. For a lot of them at once, `hoard move` takes a holdings document on stdin and files every card in it into one binder:

```console
$ hoard export --binder want --json | hoard move --to Binder
```

## Your data

| OS    | Default location                                                          |
| ----- | ------------------------------------------------------------------------- |
| macOS | `~/Library/Application Support/hoard/hoard.db`                            |
| Linux | `$XDG_DATA_HOME/hoard/hoard.db` (else `~/.local/share/hoard/hoard.db`)    |

Override with `--db PATH` or `$HOARD_DB`. A schema upgrade backs the old file up alongside it first. The card catalog and price downloads live in your cache directory and are always safe to delete.

It is ordinary SQLite: read it with [any tool](schema/sqlite/README.md), or take versioned JSON out with `hoard export --format json` ([schema](schema/json/README.md)).

## Scripting it

`--json` emits a versioned document instead of a table. `hoard schema` prints the JSON Schema it validates against, and the schema files are published under [`schema/json/`](schema/json/README.md) so a parser can pin a version.

Commands that do not honour `--json` **refuse** it rather than ignoring it.

Documents compose. `hoard export` chooses holdings and `hoard move` acts on them, so a bulk move is a pipe. Filter terms are ANDed, so `set:` cannot name two sets at once — export each one and splice the rows together to sweep both into bulk in a single move (fish syntax; in bash the `begin`/`end` pair is `{ ...; }`):

```fish
begin
    hoard export --binder Binder --json --filter 'price<1 set:cmd'
    hoard export --binder Binder --json --filter 'price<1 set:isd'
end | jq -s '.[0].holdings.rows = ([.[].holdings.rows] | add) | .[0]' \
    | hoard move --to Bulk --dry-run
```

```
Would move 250 copies of 250 printings into "Bulk" · $87.77
```

`--dry-run` reports without writing, and the move asks before it writes unless you pass `--yes`. Because the middle of that pipe is a published document, `jq` narrows anything [`--filter`](#filtering) cannot:

```console
$ hoard export --binder Binder --json \
  | jq '.holdings.rows |= map(select(.detail.artist == "Rebecca Guay"))' \
  | hoard move --to bulk
```

Only binder cards move. Piping `hoard export --all` is safe: rows belonging to decks are skipped and counted, so a decklist is never touched.

## More Documents

- [CONTRIBUTING.md](CONTRIBUTING.md)
- [AGENTS.md](AGENTS.md)
- [RELEASE.md](RELEASE.md)
- [SECURITY.md](SECURITY.md)
- [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)

## Support

hoard is free and MIT licensed, and it stays that way. If it saved you a spreadsheet, you can [buy me a coffee](https://buymeacoffee.com/spiffcs) — it keeps the macOS builds signed and notarized and the install script hosted.

## License and Legal

Code is [MIT](LICENSE) — © 2026 Christopher Phillips. Card imagery in this repository remains the property of Wizards of the Coast and is **not** covered by that license; see [NOTICE](NOTICE).

hoard is unofficial Fan Content permitted under the Fan Content Policy. Not approved/endorsed by Wizards. Portions of the materials used are property of Wizards of the Coast. ©Wizards of the Coast LLC.

Card data courtesy of [Scryfall](https://scryfall.com), [MTGJSON](https://mtgjson.com) (MIT, © 2018–Present Zach Halpern) and [TCGCSV](https://tcgcsv.com). hoard is not affiliated with, endorsed by, or sponsored by any of them. All prices are daily estimates from third-party aggregators, not quotes, with absolutely no guarantee. Visit and support your LGS for final prices.
