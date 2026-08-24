<p align="center">
    <img src="docs/assets/hoard-logo.png" width="250" alt="The hoard logo: an ornate red and gold treasure chest, a gold dragon crest on its lid">
</p>

# hoard

[![Validations](https://github.com/spiffcs/hoard/actions/workflows/validations.yaml/badge.svg)](https://github.com/spiffcs/hoard/actions/workflows/validations.yaml)
[![Release](https://img.shields.io/github/v/release/spiffcs/hoard?sort=semver)](https://github.com/spiffcs/hoard/releases)
[![License](https://img.shields.io/github/license/spiffcs/hoard)](LICENSE)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/spiffcs/hoard/badge)](https://scorecard.dev/viewer/?uri=github.com/spiffcs/hoard)

A TUI collection tracker for Magic: The Gathering.

Your cards and decks live in a single SQLite file on your machine, priced daily
from [MTGJSON](mtgjson.com) and [TCGCSV](https://tcgcsv.com/). Metadata and card information come from [Scryfall](https://scryfall.com/).

<p align="center">
    <img src="docs/assets/demo.gif" width="100%" alt="A valuation report, then the browser: card detail with price history, the movers, watches and market screens, and a rarity filter">
</p>

## Install

```sh
curl -sSfL https://tools.aithirne.com/hoard/install.sh | sh -s -- -b "$HOME/.local/bin"
```
`/usr/local/bin` is root-owned on stock macOS, so installing there needs `sudo`
```sh
curl -sSfL https://tools.aithirne.com/hoard/install.sh | sudo sh -s -- -b /usr/local/bin
```

`tools.aithirne.com` is from the maintainer (see [triage](https://github.com/spiffcs/triage) as well).
The script it serves is [`install.sh`](install.sh) from this repository. The release workflow uploads
that exact file so you can read it here before running it, or check the two
match without taking anyone's word for it:

```sh
diff <(curl -sSfL https://tools.aithirne.com/hoard/install.sh) \
     <(curl -sSfL https://raw.githubusercontent.com/spiffcs/hoard/main/install.sh)
```

It downloads from this repository's releases and verifies the SHA-256. You can add `-v`
to verify the Sigstore signature too if you have cosign. A trailing tag pins the
version instead of taking the latest.

```sh
curl -sSfL https://tools.aithirne.com/hoard/install.sh | sh -s -- -v -b "$HOME/.local/bin" v0.1.0
```

#### Golang/Github Specific
`go install github.com/spiffcs/hoard/cmd/hoard@latest` (Go 1.26+), or take an
archive from the [releases page](https://github.com/spiffcs/hoard/releases). The
macOS binaries are signed and notarized and should open without a Gatekeeper
prompt; [RELEASE.md](RELEASE.md) covers verifying them.

If you want to look at the TUI before importing anything? `hoard demo` opens the browser on a small sample collection that is a database of its own your main hoard DB is never opened or clobbered.

## Bring Your Collection In

Most people arrive with an export from somewhere else. `hoard import` reads
ManaBox, Moxfield and Delver Lens CSVs. It's also backwards compatible with 
hoard's own format. Import sniffs the header, so the format usually needs no naming:

```console
$ hoard import manabox-export.csv
✓ Imported 1,284 cards (manabox format): 1,284 rows resolved.
```

Decks can be imported from an Archidekt URL, or from a pasted list:
```console
$ hoard deck add https://archidekt.com/decks/1234567/my-deck
$ pbpaste | hoard deck add --file - --name "Modern Burn"
```

Archidekt is the only site with a URL importer — Moxfield's API is behind
Cloudflare, and most others have none. Everywhere else, export the deck to text
and pipe it in: the plain `1 Sol Ring` / `1x Sol Ring (C21) 125` shape that
Moxfield, MTGGoldfish, TappedOut, EDHREC and Scryfall all export is read
directly, foil markers (`*F*`) and `SB:` sideboard lines included.

Archidekt's and Deckstats' text exports keep their sectioning: Archidekt's
`[Category]` blocks put the commander in the command zone and leave anything
marked `{noDeck}` on the maybeboard, and Deckstats' `//Commander` headers do the
same. Both are read as exported, categories and all.

Loose cards can go in one at a time by Scryfall link:
```console
$ hoard add https://scryfall.com/card/uma/7/ulamog-the-infinite-gyre
✓ Added 1× Ulamog, the Infinite Gyre (uma/7) as nonfoil into Binder · $37.35
```

`--dry-run` works on all three: resolve and report, write nothing. On an import,
`--preserve-binders` recreates the file's own binders instead of putting
everything in one.

## Try It

Run `hoard` with no arguments to see the browser, or `hoard help` for every command.
With nothing imported yet, `hoard demo` shows the same browser with sample data;
`hoard demo --reset` starts that sample over. When you're in the main browser you can use `:` to
view the main command palette.

The browser does not have to be pointed at cards you own. The [compendium
generator](internal/compendium/gen/README.md) builds a database from a
slice of Magic. The example shows every mythic and rare since 2020 but feel free to make your own

A handful of sets priced and backfilled helps people browse and chart it with the same screens. 
Helps when building/pricing decks for new formats

```sh
task compendium -- -rarity mythic,rare -since 2020 mythics-rare.db
HOARD_DB=mythics-rare.db hoard
```

To build a whole format instead of a date range, `task compendium -- -format
premodern premodern.db` does Premodern in one command — the [generator
docs](internal/compendium/gen/README.md#a-format-not-a-date-range) cover that and
importing decklists to price against it.

<p align="center">
    <img src="docs/assets/browse.png" width="100%" alt="The browser: the sets you own cards from listed by value on the left, every card on the right with its set, finish, quantity and price, card names colored by color identity">
</p>

## Filtering

Press <kbd>/</kbd> in the browser to narrow what you are looking at, and give
the same query to `hoard export --filter` to narrow an export. One vocabulary,
both places.

```console
$ hoard export --binder Binder --filter 'price<1 rarity:common'
```

| Key | Matches | Example |
|---|---|---|
| *(bare word)* | card name, anywhere in it | `sol ring`, `"lion's eye"` |
| `name` | card name, anywhere in it | `name:ulamog` |
| `set` | set code | `set:uma` |
| `finish` | `nonfoil`, `foil` or `etched` | `finish:foil` |
| `board` | `main`, `side`, `commander` or `maybe` | `board:side` |
| `qty` | copies held | `qty>=4` |
| `price` | price per copy, USD | `price<1` |
| `value` | copies × price, USD | `value>=20` |
| `cmc` | mana value | `cmc<=2` |
| `rarity` | the whole rarity | `rarity:mythic` |
| `type`, `t` | type line | `t:creature` |
| `artist` | artist | `artist:guay` |
| `layout` | Scryfall layout | `layout:transform` |
| `setname` | full set name | `setname:"modern horizons"` |
| `color`, `c` | colour identity letters | `c:wu` |

Terms are ANDed, so repeating a key makes a range: `price>=5 price<20`. The
comparisons `>` `>=` `<` `<=` work on the numeric keys; everything else takes
`:` or `=`. Quote anything with a space in it.

Text matching ignores case and matches anywhere in the field — except `rarity`,
`finish` and `board`, which must match whole. `c:wu` asks for a colour identity
containing both W and U, not one that is exactly WU. An unpriced card matches no
`price` term: absent is not free.

The last seven keys read the card documents hoard stores, so they match nothing
until `hoard update-prices` has filled the catalog. A few do not apply on the
movers, watches and market screens; the browser says so when you type one.

## A wantlist

There is no wantlist screen, and there does not need to be one: a binder that
does not count toward your collection *is* a wantlist.

```console
$ hoard binder new Want
Created binder #2 "Want"
$ hoard binder exclude Want
Binder "Want" is no longer counted toward your collection
```

Cards you are hunting go in it the same way anything else goes into a binder.
The binder has to exist first — `--binder` names one, it does not create one.

```console
$ hoard add https://scryfall.com/card/uma/7/ulamog-the-infinite-gyre --binder Want
✓ Added 1× Ulamog, the Infinite Gyre (uma/7) as nonfoil into Want · $36.32
$ pbpaste | hoard add --file - --binder Want
```

Excluding a binder changes the accounting, not the card. Everything in `Want`
is still priced by `hoard update-prices`, still turns up in `hoard movers`, and
can still carry a watch — so the list of cards you don't own yet is also the
list hoard tells you about when one gets cheaper:

```console
$ hoard watch add "Ulamog, the Infinite Gyre" --under 30
Watching Ulamog, the Infinite Gyre (uma/7) nonfoil: under $30.00.
```

What exclusion buys you is that none of it counts as yours. `hoard report`
leaves the binder out of the total, so a wantlist can never inflate what your
collection is worth, while `hoard binder list` still totals it up so you can
see what finishing the list would cost:

```console
$ hoard binder list
ID  NAME    CARDS   VALUE
 1  Binder      0   $0.00
 2  Want *      1  $36.32
* not counted toward your collection
```

When you actually get one, move it out of `Want` and into wherever it lives.
That happens in the browser: select the card, <kbd>enter</kbd> for its detail,
<kbd>up</kbd> to drop into the row for the copy you hold, then <kbd>right</kbd>
along that row to its last field — the binder it is in. <kbd>enter</kbd> there
asks which binder to move it to. It counts toward your collection from the
moment it lands in a counted binder; <kbd>esc</kbd> back out to the browser and
<kbd>u</kbd> undoes the move.

Nothing is special about the name — `Want` is just a binder, so run as many as
you sort by: a `Want` list, a `Trade` binder of things you'd part with, a
`Maybe` pile. Decks take the same treatment with `hoard deck exclude`, which is
how a proxied or borrowed list stays out of your total. `hoard binder include
Want` puts any of it back.

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

Commands that do not honour `--json` **refuse** it rather than ignoring it.

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
estimates from third-party aggregators, not quotes, with absolutely no guarantee.
Visit and support your LGS for final prices.
