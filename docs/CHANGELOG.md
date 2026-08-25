# Changelog

Notable changes to hoard, newest first.

The per-release notes on the [releases page](https://github.com/spiffcs/hoard/releases)
are the canonical record and carry the full commit list plus the signature
verification block. This file is the readable history.

The root `CHANGELOG.md` is a build artifact — `task changelog` regenerates it
for goreleaser on every release and it is gitignored. This is the tracked one.

## v0.3.0 — 2026-08-25

Databases carry over untouched — the schema is still v33, so there is no
migration and no backup on first launch. `--json` documents move to schema
1.1.4, which adds the container id to holdings rows.

### Breaking

* `hoard compendium --legal` is removed fully. Use `--format`, which took the same
  format names. Scryfall records legality per card rather than per printing, so
  `--format` now keeps every printing of a legal card, later reprints included —
  those are legal to play and are frequently the cheapest copy. Pass `--era` for
  the period-correct set list instead.

### Added

* `hoard compendium --era` narrows a format to its own era: the 29 sets from
  Fourth Edition through Scourge for `premodern`, and everything released before
  Commander 2011 for `predh`. Formats without an era refuse the flag rather than
  ignoring it.
* `hoard export --filter` narrows an export with the browser's filter language,
  so `hoard export --binder Binder --json --filter 'price<1' | hoard move --to Bulk`
  is a bulk move.
* `hoard move` files a piped holdings document into a binder. Deck rows are
  skipped and counted, so a decklist is never touched.
* Browse: price what finishing a set would cost, and split a set into what you
  hold and what you don't.
* Browse: name a run of rises or falls on the card detail.
* Browse: vendor prices are fetched in the background at startup.
* Leave a deck out of your totals with `hoard deck exclude`, the way a binder
  already could, and `hoard deck include` counts it again. `x` in the browser
  toggles either. Decks count by default; the summary marks an excluded one
  with `*`.
* Browse: the collection pane holds its place and the selected card previews
  below it, so the sidebar no longer scrolls away to make room for art.
* Browse: `ToggleRawOnly` from the command palette adds and removes
  `finish:nonfoil`, hiding every foil, etched and treated printing.
* Browse: a price refresh names the day its data is from.
* The terminal's cell aspect is measured at startup, so card art is drawn in
  proportion on terminals whose cells are not the shape hoard assumed.
* Holdings JSON rows carry the id of their container.
* [Adding cards](adding-cards.md) documents the TUI add flow and the iPhone
  scanner end to end.

### Changed

* The add flow's confirm screen always names the binder a card is going into,
  not only when there was more than one to choose from.
* A deck's cards can be edited in place in the browser, rather than only through
  a refresh of the whole decklist.
* The filter language moved out of `browse` into its own package.
* Compendium databases no longer refuse commands that write. Treat one as
  read-only by habit; building into a file that already exists is still refused.
* The Go toolchain floor is pinned to 1.26.7, which clears four reachable
  standard-library advisories.
* `spf13/pflag` 1.0.9 → 1.0.10 and `charmbracelet/x/ansi` 0.11.7 → 0.11.8.

### Fixed

* Browse: a sort tie breaks on the printing rather than only the name.
* Browse: the card detail refreshes when a set view resets — the printing's
  document is refetched and a thin price history is backfilled rather than the
  previous card's detail being left on screen.
* Deck list imports keep their sideboard rows.
* Scryfall calls are paced across every endpoint together rather than per
  endpoint class alone, and a rate-limited request backs off for as long as
  Scryfall asks.
* `hoard report` names a price stamp's own day.

## v0.2.0 — 2026-08-23

Databases upgrade from schema v30 to v33 on first launch; the old file is backed
up beside it before migrating.

### Added

* Dip & Momentum joins the view cycle (`v`): printings sitting at the floor of
  their recent range, and printings climbing without a down day.
* Deck folders — group decks one level deep, nested in the sidebar with copies
  and value rolled up. `hoard folder new|list|rename|rm` and `hoard deck move`.
* Fold a folder away with `space`; the sidebar remembers what you folded between
  launches.
* Move a deck into a folder with `m`, and hoard offers to create the folder if
  the name is new.
* Leave a binder out of your totals with `hoard binder exclude`, or `x` in the
  browser.
* Select a run of rows with `shift+↑` / `shift+↓`; the header reports copies,
  value and row count.
* Rename a deck with `hoard deck rename`, and `R` renames binders, decks and
  folders.
* The compendium generator builds a browsable database of printings you do not
  own, priced and backfilled.

### Fixed

* Cycling views no longer loses the collection selected in the sidebar.
* The sidebar's VALUE column no longer shifts when folders fold and unfold.
* `schema/sqlite/schema-latest.sql` is executable SQL again rather than being
  emitted with truncated trigger bodies.
* A TCGplayer low price below half the mid is no longer trusted as an ask;
  Direct is preferred where present.
* MTGJSON's `normal` finish is translated to `nonfoil` where the payload is
  decoded rather than in three places downstream.

### Changed

* Finishes are a compiler-checked type and constrained in the schema with
  `CHECK`, so an unknown finish cannot reach the database from any path.

## v0.1.1 — 2026-08-13

### Fixed

* Foreign strings are sanitised and decompression is bounded.
* The install script is fixed.

### Changed

* gosec and CodeQL added to CI.

## v0.1.0 — 2026-08-13

The first release. Roughly 190 changes went into it; the
[release notes](https://github.com/spiffcs/hoard/releases/tag/v0.1.0) carry the
full list. In outline it brought:

* The terminal browser of binders, decks, card detail, images, colour identity,
  container filters, and the command palette.
* Pricing from MTGJSON and TCGplayer CSV, with price history, bid history,
  spreads, comps, and a market screen that refuses a price its own asks
  contradict.
* Watches, including percentage moves anchored to a trailing high, and bulk
  import.
* Importing from ManaBox, Moxfield and Delver Lens; deck import from Archidekt
  and from pasted lists; bulk paste entry from stdin.
* `--json` documents with a published, versioned schema.
* The iPhone scan link TLS with TOFU pinning, an art index with pHash, and the
  add-flow scan queue.
