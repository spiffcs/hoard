# Prices and history

How hoard values a collection: where prices come from, how history accumulates,
and what to do when a card stubbornly counts as $0.00.

## Refreshing prices

```sh
hoard update-prices
```

Scryfall recalculates its price data roughly once a day, so `update-prices` is
worth running about that often. Running it several times in a day re-fetches the
entire catalog to arrive at the same numbers: it is the only command that talks
to Scryfall about every card you own, in batches of 75. With the
[local catalog](#the-local-catalog) built, most of those lookups never touch the
network at all.

Every refresh records the prices it sees, and ends by saying what changed since
the last one:

```
Updated prices for 1,256 of 1,256 cards.

NAME                     SET/NUM  FINISH    WAS       NOW  CHANGE  QTY   IMPACT
RISERS
  Breena, the Demagogue  c21/1    foil    $3.00  →  $4.32  +44.0%   ×2   +$2.64
  Sol Ring               c21/1    -       $1.00  →  $1.10  +10.0%  ×40   +$4.00

SINKERS
  Flame Discharge        neo/142  -       $0.25  →  $0.10  -60.0%   ×1   -$0.15

2 printings moved since the last refresh. Net change: +$6.49
```

The list is ordered by **impact** — the move multiplied by how many copies you
hold, across every binder and deck — not by the change in sticker price. Forty
commons that each gained a dime moved the hoard more than one mythic that gained
a dollar, and sorting on the per-card figure buries that.

Only prices that actually changed are stored, so the history stays small — most
of a collection does not move on a given day. A card added between refreshes gets
its first observation on the next one, as a baseline rather than as a rise from
nothing.

## Movers

`hoard movers` asks the same question over a longer window:

```sh
hoard movers                 # since 30 days ago
hoard movers --since 7d      # or 2w, or 48h
hoard movers --limit 25      # more than the default 10 per section
```

The window names a date, not a period, because prices are observed when a refresh
runs rather than continuously: `--since 7d` compares today's price against the
last one recorded *on or before* a week ago. On a hoard refreshed every few
weeks, that baseline may itself be older than the window, and the footer says so
when history does not reach back as far as the question.

## Backfilling history you don't have yet

History begins the first time you run `update-prices`, so a new hoard answers
`movers --since 30d` with a day of data and a footer apologising for it. MTGJSON
publishes the last ninety days, which closes that gap in one download:

```sh
hoard backfill-prices
```

Run it once. It is deliberately not part of `update-prices`: the ninety-day
archive is around 150 MB against the 5 MB of the daily file, and the download
cache is cleared nightly, so folding it in would risk re-fetching the lot on any
day you refresh prices. Running it again is harmless and inserts nothing — the
import stops at the day your own history begins.

Backfilled prices come from **TCGplayer**, which is the source Scryfall's USD
figures come from, so the imported series joins up with your own instead of
stepping at the seam. Where a real observation and an imported one land on the
same day, the real one stands. The same pass also imports **Card Kingdom's
buylist bids** into their own table — the bid history behind the card detail's
buylist sparkline and spread trend — bounded the same way against the bid
series' own first observation.

Two things it cannot reach: printings MTGJSON has no id for, and printings
TCGplayer never priced — the same gap `unpriced` reports. Those simply have no
history before you started recording, and the command says how many there were,
because `movers` would otherwise just quietly list fewer cards.

Ninety days is the ceiling. `--since 6m` will still tell you the history only
goes back so far.

## When Scryfall has no price

Scryfall's USD prices come from TCGplayer alone, so a printing TCGplayer has no
record of is simply unpriced there. This is not rare. The Modern Horizons 3
Commander ripple foils have no USD foil price at all, which left one deck valued
at $134.72 instead of $459.54.

When `update-prices` finds cards with no price for a finish you actually own, it
consults [MTGJSON](https://mtgjson.com/), which aggregates several vendors:

```
Updated prices for 1,256 of 1,256 cards.
  111 cards have no price for a finish you own; checking MTGJSON...
  filled 96 from cardkingdom, manapool, tcgplayer.
  15 still unpriced anywhere.
```

Nothing is downloaded unless there is a gap to fill, and the files are cached for
the day in your cache directory. A Scryfall price always wins; these only ever
stand in where there is none. Because they come from a different shelf than the
rest of hoard's numbers, the browser marks them with `*` and its status line
names the vendors:

```
NAME           SET/NUM  FINISH  QTY  PRICE   VALUE
Planar Nexus   m3c/80   foil    ×1   $37.99  $37.99*
──────────────────────────────────────────────────
1/26 · sorted by value · * estimated from cardkingdom via MTGJSON
```

Cardmarket is deliberately not used: it quotes euros, and a second currency
inside a dollar total would be misleading.

The per-set id resolution that powers this also harvests **Card Kingdom's
product links** from the same set files — the sanctioned mtgjson.com redirects,
one per finish — which is what lets the card detail's cardkingdom link open the
exact product page instead of a name search. One extra pass over your sets the
first time, then stamped and never fetched again.

## Cards that cannot be priced

`hoard unpriced` shows every card counting as $0.00 and which deck each one is
dragging down, because a card valued at nothing otherwise looks the same as a
card genuinely worth nothing:

```
NAME                  SET/NUM  FINISH  COPIES  HELD IN
Ambush Commander      evg/1    -            1  Duel Decks Anthology: Elves vs. …
Angelic Protector     tpr/2    -            1  Duel Decks Anthology: Divine vs.…

19 copies across 15 cards count as $0.00.
Try: hoard repair-finishes, then hoard update-prices
```

Often the cause is not a missing price but a wrong finish. A decklist with no
`*F*` marker imports as non-foil, and plenty of printings are foil-only: precon
commanders and Duel Decks reprints among them. That entry then asks for a price
that cannot exist, so no amount of refreshing will ever fill it.

`hoard repair-finishes` finds those and corrects them, using the list of finishes
Scryfall already publishes for each printing:

```
NAME                        SET/NUM  QTY  WAS     NOW   IN
Ambush Commander            evg/1      1  nonfoil  foil  Duel Decks Anthology: Elves v…
Licia, Sanguine Tribune     c17/40     1  nonfoil  foil  Vampiric Bloodlust (Commander…

Corrected 12 entries. Run hoard update-prices to value them.
```

It only acts where there is a single right answer, meaning the printing comes in
exactly one finish. Anything genuinely ambiguous is reported and left alone. It
is safe to re-run: a second pass finds nothing.

## Where vendors disagree

A valuation reports one price per card as though it were the truth. It isn't: the
same card is quoted by three US vendors at once, on both sides of the counter,
and they disagree more than a single figure suggests. Everything anchors on
**TCGplayer's sales-derived market price** — the one number that describes what
cards actually trade at. `hoard market` shows where the vendors depart from it,
in four blocks:

```
ARBITRAGE  CK buylist pays more than TCG last-sold
Tarnished Citadel  ody/329  -    $7.81 last sold  $10.50 cardkingdom  +$2.69

BUYLIST NEAR MARKET  CK buylist pays at least 70% of TCG last-sold
Thassa, Deep-Dw…   thb/71   -   $25.00 last sold  $25.00 cardkingdom  100.0%

BELOW MARKET  a marketplace is asking far under tcg's last-sold price
Glimmerpost        som/223  foil  $1.10 manapool     $3.99 last sold  -72.4%

COMPS  a list comparing vendor prices
Ancient Tomb   uma/236  foil   —   $65.00 tcgplayer  $60.00  $60.00  $42.00  30.0%
```

The first section is the only unambiguous one: a dealer's cash bid above what
the card actually sells for is free money, though in practice a couple of
dollars a card. The second is where the buylist pays as much as — or nearly as
much as — the current sale price: cards you could turn into cash near full
value, against a median card whose bid fetches about half. In the browser
that table flips with <kbd>b</kbd> to a **lowball band** — the bids paying
under 50% of the last-sold price, worst first; see
[browsing.md](browsing.md). The band is browser-only, and `hoard market`
prints the near-market table alone. The third is
where to buy: real asks at least 25% under the last-sold price. The other
direction — a lone listing far above the sales price — is scalper noise and is
deliberately not a section. **COMPS** is the per-card sheet: each vendor's ask,
the lowest of them, the buylist bid, and the **spread** (retail minus buylist
over retail) — the hobby's confidence signal, tight meaning the price is real.

`--min` sets the floor on what you would pay (default `$1`), because a 900%
spread between $0.20 and $1.99 is arithmetic rather than an opportunity.
`--limit` sets rows per section. Everything here is one day's vendor prices.
The browser's MARKET view shows the same analysis (minus BELOW MARKET, whose
space serves a two-sided comps table there), filtered to whichever collection
is selected; see [browsing.md](browsing.md#views).

## The local catalog

hoard keeps a copy of Scryfall's card data on disk, so lookups that used to be
API calls are queries against a file. It is built from Scryfall's published bulk
bundle — the same thing their rate-limit message points you at — and refreshed
only when what they publish is newer than what you have.

```sh
hoard catalog          # what's stored, how old, whether Scryfall has newer
hoard catalog status   # the same, spelled out
hoard catalog update   # rebuild now
```

About 107,000 paper printings: a ~77 MB download that builds to ~57 MB on disk in
under ten seconds. The CLI asks before downloading it rather than spending your
bandwidth uninvited, and declining is fine — a stale catalog still answers, and
an absent one just means the API path hoard always used. The browser is the one
exception: opening it with no catalog built auto-starts the download as an
ordinary cancellable operation, because its whole value is fast lookups in the
add flow.

On a real collection the difference is stark. `update-prices` used to make 21
batched requests and could be rate-limited part-way, losing everything already
fetched; with a catalog it served 1,568 of 1,573 cards locally in 1.3 seconds,
and the prices matched the API's exactly on all 1,573.

Card lookups use it too. Name completion and printing search become indexed
queries — instant, and working with no network. Camera scanning resolves against
it as well, which turned out to be *more* accurate than the API rather than
less: on a set of real card names with OCR-style corruptions, the local matcher
scored 30/30 against Scryfall's 26/30. Scryfall returns nothing for `Sol Rlng`,
`S0l Ring`, `Stonef0rge Mystlc` or `Anclent Tornb`, while the local matcher reads
all four — and it refuses `option`, which the API resolves to the card `Opt`.

It is a **cache, never an authority**. Anything it does not have falls through to
the Scryfall API, so a card printed since the last build still adds normally
(digital-only printings, which the bundle excludes on purpose, come from the API
the same way).

The catalog lives in your cache directory — `~/Library/Caches/hoard/catalog` on
macOS — and never inside `hoard.db`. Everything in it is a download away and your
collection is not, so they are kept apart: deleting the catalog is always a safe
fix, and the pre-migration backups of `hoard.db` stay small.

For why hoard keeps its own price schema rather than adopting MTGJSON's, see
[mtgjson-storage.md](mtgjson-storage.md).

Treated foils (ripple, surge, …) are priced correctly by construction:
Scryfall models the treatment as a tag on the printing whose foil finish
is the treated copy, usually with no Scryfall foil price — so the foil
value rides MTGJSON's foil bucket, which tracks the treated product's
market. The FINISH columns name the treatment so the number and the
physical card read as the same thing.

**Finish and treatment are different axes, in every source.** Scryfall
publishes exactly three finishes — `nonfoil`, `foil`, `etched` — across
all 107,000 paper printings, and MTGJSON publishes the same three. A
treatment is a `promo_types` tag on the printing, never a finish of its
own, so hoard stores the same three and derives the treatment word from
the tag. Any tag ending in `foil` is a treatment, which is how WotC names
them, plus a short list for the ones that don't (`textured`, `embossed`,
`gilded`, `neonink`, …). That rule replaced a hand-written allowlist that
had already drifted from the source: it keyed `texturedfoil`, a tag
Scryfall does not publish, so textured foils read as plain foils.

**Which vendor is selling which product.** A treated printing is one
Scryfall id, so all three vendors file their price under the same key —
but they do not all sell the same product under it, and a comp sheet that
assumes they do reports a data defect as vendor disagreement (observed
live: an 85% "spread" on `m3c/403`, between a $4.61 ripple and a $26.69
something else). MTGJSON's set files answer the question directly, and
hoard now stores what they say:

| Vendor | Identifiers | Treated foil |
|---|---|---|
| TCGplayer | `tcgplayerProductId` + `tcgplayerAlternativeFoilProductId` | splits it into its own listing — the tcgcsv overlay above fetches that listing |
| Card Kingdom | `cardKingdomId` + `cardKingdomFoilId`, no alternative-foil variant | does not split, so the one foil product *is* the treated foil |
| Manapool | none published | cannot be tied to a product at all |

Where TCGplayer splits, the feed carries no tcgplayer foil series for
that printing whatsoever (verified across all 312 in a live hoard), so
the overlay is the only foil price there is and the hole-filling merge is
enough. Where it does not split, the ordinary foil listing is itself the
treated foil. Either way the figure describes the card held — which is
what lets the comps table drop Manapool's, and only Manapool's, on a
treated foil. See [browsing.md](browsing.md#views).

Etched is a real bucket in the price feed, not a synonym for foil: every
vendor prices the etched product separately, on both sides of the
counter. A comp for an etched copy reads that bucket, falling back to the
foil one only where a vendor publishes no etched series.

**Where treated-foil TCGplayer prices come from.** TCGplayer sells each
treatment as a separate product — "Akroma's Will (Ripple Foil)" is its
own listing beside the regular card — and MTGJSON's price feed ingests
only the base product, so its tcgplayer bucket has no foil series for
these cards at all. The split product's id, however, rides the same
MTGJSON set files hoard already reads (`tcgplayerAlternativeFoilProductId`,
with the etched id as fallback); hoard stores it per printing and fetches
the product's market price from TCGplayer's public catalog via
[tcgcsv.com](https://tcgcsv.com) — free, keyless, day-cached beside the
MTGJSON cache. The fetched figure merges into the card's record before
any vendor selection, so the provider order genuinely holds for treated
foils: TCG SOLD fills in, spreads and verdicts compute, and the effective
foil price anchors on TCGplayer like every plain card's does. If tcgcsv
is unreachable the overlay simply doesn't happen — the other vendors'
answers stand, exactly as before.

The archive backfill resolves each finish through the same vendor order
the live prices use: TCGplayer's series leads, and a finish TCGplayer
never lists takes the next vendor's series — without that fallback the
Collector's Edition decks sat one observation deep and never appeared in
movers. Treated foils get their TCGplayer series from tcgcsv's daily
archives: up to 90 days, ~4 MB per day, each fetched once and its
extractions cached, decoded entirely in Go (the archives are
PPMd-compressed 7z; hoard registers a lenient PPMd decoder because the
archives carry seven property bytes where the canonical shape is five).

## What hoard downloads, and when

The price sources are volunteer- and community-run, so the network
contract is deliberately small, and the caches enforce it rather than
politeness alone:

- **Scryfall**: card documents in paced chunks (150 ms between requests,
  rate-limit responses waited out), only when refreshing or resolving.
- **MTGJSON**: one ~5 MB `AllPricesToday` per day (every quotes or price
  read after that re-parses the cached file — pressing <kbd>F</kbd>
  repeatedly costs zero requests); the ~150 MB `AllPrices` archive only
  during a backfill, at most once a day behind the receipt ledger; per-set
  identifier files once per set ever.
- **tcgcsv**: the group list and each needed group's prices once per day;
  one ~4 MB daily archive per backfill day, extracted once and cached
  permanently — the first backfill sweeps ~90 of them, after which steady
  state is one per day.

Every request carries hoard's User-Agent, and all three clients pace
their download starts (no two closer than 250 ms), so even the one-time
archive sweep never reads as scraper traffic. Nothing retries in a loop:
a failed download surfaces as an error and waits for you.
