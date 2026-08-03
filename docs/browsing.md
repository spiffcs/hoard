# Browsing

Running `hoard` with no arguments opens the browser: containers on the left, the
cards inside the selected one on the right.

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

The left pane leads with **All cards** — every holding in the hoard, all
binders and decks merged into one list (read-only: edit a card where it
lives) — then your binders (the default binder, then any you created with
`hoard binder new`), then every deck ranked by value. The right pane shows
what is inside whichever row you have selected.

## Keys

| Key | |
|---|---|
| <kbd>tab</kbd> / <kbd>←</kbd> <kbd>→</kbd> / <kbd>h</kbd> <kbd>l</kbd> | switch pane |
| <kbd>↑</kbd> <kbd>↓</kbd> <kbd>j</kbd> <kbd>k</kbd> | move · <kbd>pgup</kbd>/<kbd>pgdn</kbd> page · <kbd>g</kbd>/<kbd>G</kbd> jump to ends |
| <kbd>enter</kbd> | card detail — printings, where it's held, price history (from every view) |
| <kbd>/</kbd> | filter (see below) · <kbd>esc</kbd> clears it · <kbd>ctrl+u</kbd> wipes the bar |
| <kbd>s</kbd> | sort by value → name → quantity |
| <kbd>v</kbd> | switch view: holdings → movers → unpriced → watches → market |
| <kbd>+</kbd> <kbd>-</kbd> | change how many copies you hold |
| <kbd>d</kbd> | remove the card, or the deck — asks first |
| <kbd>u</kbd> | undo the last edit |
| <kbd>a</kbd> | add cards — the add flow opens right here, browser state intact |
| <kbd>r</kbd> | reload |
| <kbd>M</kbd> | value floor: hide cards under $5 → $10 → $25 → off (unpriced view exempt) |
| <kbd>q</kbd> | quit — asks y/n first (main views only) |
| <kbd>esc</kbd> | back out one frame — at the top it asks before quitting · <kbd>ctrl+c</kbd> quits anywhere |

Binder cards are editable in place (<kbd>+</kbd>, <kbd>-</kbd>, <kbd>d</kbd>,
<kbd>u</kbd>) — every binder, not just the default. Deck cards are deliberately
read-only: a deck is owned by the list it was imported from, so editing it here
would drift from that source until the next `deck add` overwrote the change
without saying so.

Pressing <kbd>a</kbd> opens the same interactive add flow `hoard add` runs —
type a name or <kbd>ctrl+o</kbd> to scan with your iPhone — inside the
browser: it takes over the screen while it runs. <kbd>ctrl+d</kbd> finishes
the session and drops you back exactly where you were, cursor, filter and
undo intact, with the new cards already in your binder, a one-line receipt
on the status line, and a price fetch already running so the new rows fill
in without asking. <kbd>esc</kbd> also leaves, but through a gate: it
states that confirmed cards are saved and anything mid-pick is not, and
asks for <kbd>y</kbd> then <kbd>enter</kbd> before letting go. A long
operation keeps running behind the cascade, and the full scan receipt
still prints to the terminal scrollback when you quit the browser.

## Card images

The detail view draws the card itself to the right of its text when the
terminal can: Ghostty, Kitty and WezTerm get the real image (kitty
graphics protocol); iTerm2 and any other truecolor terminal get a
halfblock rendering. Each card's scan is fetched once from Scryfall
(~100 KB) and cached in the per-user cache directory (macOS:
`~/Library/Caches/hoard/images`) — never in hoard.db, so the database
backup stays lean and losing the cache costs only a refetch. The text
never waits on the picture, and a terminal too narrow for both shows
text only.

`HOARD_CARD_IMAGES=0` turns images off; `=kitty` or `=halfblock` forces
a tier past the terminal detection (say, a terminal that speaks the
kitty protocol under a name hoard doesn't know). `NO_COLOR` disables
them along with everything else, and tmux/screen show no images —
graphics passthrough through a multiplexer is not supported.

## Views

<kbd>v</kbd> cycles through five views: holdings, movers, unpriced, watches,
and market. All but the last are instant database reads.

**Every view reads through the collection pane.** All cards is the whole
hoard; selecting a binder or deck narrows the view to what it holds, and
the header names the selection (`MOVERS · SINCE 2 Jul · RICH DECK`). On
unpriced and watches the pane greys out containers with nothing to show
and the cursor skips them; arriving at one of those views with an empty
selection snaps back to All cards and says so.

**The market view waits to be asked**: it needs today's vendor quotes from
MTGJSON, so cycling to it
says `press F to fetch` rather than starting a download because you passed
through. Quotes already fetched earlier the same day come back for free —
the view repopulates from the day cache on arrival, even across a restart,
with `F` still re-asking for fresh numbers. While it runs the pane says so,
and <kbd>esc</kbd> — or leaving with <kbd>v</kbd> — cancels it.

```
MARKET                                           45 rows · 1,260 printings compared
ARBITRAGE  buylist pays more than last-sold
NAME           SET/NUM  FIN  TCG SOLD  BUYLIST  TO           PROFIT
Tarnished Ci…  ody/329  -        $7.81   $10.50  cardkingdom   +$2.69

EASY TO SELL  buylist pays at least 70% of last-sold · 1–8 of 32
NAME           SET/NUM  FIN  TCG SOLD  BUYLIST    PAYS
Thassa, Deep…  thb/71   -       $25.00   $25.00  100.0%

COMPS · SELL  vendor sale prices
NAME           SET/NUM  FIN  TCG SOLD     MP      CK  BUYLIST  SPREAD
Ancient Tomb   uma/236  foil    $60.00     —   $65.00   $42.00   30.0%
```

Everything anchors on tcgplayer's sales-derived market price — the one
number that describes what cards actually trade at. The three tables hold
fixed regions of the pane, each with its own headers, and each scrolls
independently when its rows overflow — the title line says where you are
(`EASY TO SELL … · 1–8 of 32`), which is how the 70–80% pays tail stays
reachable rather than hidden under the top of the ranking. A table emptied
by the collection filter keeps its title over a note rather than
vanishing. <kbd>]</kbd>/<kbd>[</kbd> jump straight to the next or previous
table. <kbd>enter</kbd> opens any row's card detail, and
<kbd>s</kbd>/<kbd>S</kbd> sort just the table the cursor is in, each
keeping its own column and direction. `hoard market` prints the fuller
CLI report — including the BELOW MARKET section the browser dropped for
comps room — with `--min` and `--limit`; see
[pricing.md](pricing.md#where-vendors-disagree).

**COMPS** is the comp sheet sellers build by hand, and it has two halves —
<kbd>b</kbd> flips between them. The **sell side** (default) is the comp
proper: each point of sale's number for the card side by side — tcg's
last-sold price, manapool's ask, cardkingdom's ask — with the cash bid as
the floor. **TCG SOLD** is always tcgplayer's sales-derived price, and
the table's note says so — TCGplayer has no separate ask column because
that one figure is both its price and its market anchor. The **buy side**
answers the opposite question, the cheapest copy to acquire: the same
asks, the lowest of them, and who asks it. **SPREAD** is retail minus
buylist over retail, the hobby's confidence signal: 20–30% marks a liquid
staple dealers can flip, around 50% is typical, and 80–90% means the
retail price spiked and no dealer believes it yet — the column grades
green as it tightens. A dash means no buylist bid today (Card Kingdom
runs the only buylist in the MTGJSON feed). The comps sort cycles
value → spread → market → low → buylist → name; spread sorts tightest
first.

## Filtering

<kbd>/</kbd> opens a filter bar. The pane narrows as you type; bare words match
the card name.

```
bitter                 sol ring              set:mh3
rarity:mythic          t:creature            t:"legendary creature"
color:B                color:WU              cmc>=3        cmc<=2
finish:foil            board:side            qty>1         price>20
```

Terms are ANDed, so `rarity:mythic finish:foil qty>1` is all three at once.
There is no `OR`: every question this answers narrows.

The keys are `name set finish board qty price value` for what you hold, and
`rarity type artist layout setname color cmc` for the card itself. The second
group needs card details stored — see [Card details](#card-details) — and the bar
says so if they are missing rather than reporting no matches.

Two details that are easy to trip over. `rarity` matches exactly, because
"common" is a substring of "uncommon" and a substring match would return the
opposite of what you asked. And a card no source can price fails `price<1`
rather than counting as zero: unpriced is not cheap.

## Card details

Pressing <kbd>enter</kbd> on a card opens what hoard knows about it:

```
Solitude
Creature — Elemental Incarnation  {3}{W}{W}
Modern Horizons 2 · mh2/32 · mythic
Evan Shipard · 2021-06-18

Flash
Lifelink
When this creature enters, exile up to one other target creature. That creature's
controller gains life equal to its power.

HELD
  ×4  -       Binder

PRICE
  non-foil  ▇▇█▇▇▇▇▄▃▆▅▄▄▄▄▃▄▄▄▂▁▁▁▁▁▁▁▁▁▁▁▁  $34.28
            $33.34–$41.08 since 29 Apr · 79 obs
  bid       ▁▂▂▃▃▃▄▄▄▄▅▅▅▅▅▅▆▆▆▆▆▆▇▇▇▇▇▇▇███  $24.00
            $18.00–$24.00 · 41 checks since 2 May
  spread    ██▇▇▆▆▅▅▄▄▄▃▃▃▃▃▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂  29.7%  47.1% → 29.7% since 2 May · tightening
  foil      ▁▂▂▂▂▂▂▂▂▄▄▄▄▅▅▇▇▇▆▆▆▆▆▆▇▇▇▇▇▇▇█  $46.91
            $36.55–$46.91 since 29 Apr · 11 obs

COMPS
  non-foil  tcg last sold $34.28 · mp asks $35.10 · ck asks $36.99 · ck pays $24.00 · spread 29.7%
```

The **bid** row is Card Kingdom's cash offer over time — the only buylist
in the MTGJSON feed — ingested from the same 90-day archive the price
backfill reads (its own table, its own series), and kept current by every
market-view fetch. The **spread** row is the gap between the price and the
bid as a trend: a tightening spread means the dealers increasingly believe
the retail price, the same confidence signal the MARKET view's comps table
shows for one day. **COMPS** is that table's row for this card, from
today's cached quotes — when none were fetched yet, the section says to
press <kbd>F</kbd> on the MARKET view — and when a held finish qualifies
for ARBITRAGE or EASY TO SELL, a verdict line says so in the section's own
numbers.

**HELD** answers the question no single view could before: that four copies of a
card are one in the binder and three spread across two decks.

The sparklines are scaled to each series' own low and high, not to zero — against
a zero baseline a card that drifted between $34.00 and $34.50 would look
identical to one that never moved. The numbers underneath carry the magnitude,
which is why they are there. Points are spaced by **date**, so a quiet month
occupies a month of the line rather than one step.

Card details come from Scryfall and are stored on refresh, so a hoard upgraded
from an older version has none until you run `hoard update-prices`. That fills
them for every card you own in one pass — the same request that refreshes
prices, so it costs nothing extra. Until it runs, the detail pane says so
instead of showing blanks, and trait filters find nothing and explain why.

## Piped output

Redirected or piped, plain `hoard` prints the totals as a table instead of
opening the TUI — no colour, no truncation, no escape sequences — so
`hoard | grep` and `hoard > totals.txt` still work. The table is grouped into
two sections and ranked by value, with a bar showing each deck's share of the
grand total:

```
COLLECTION                                           100  $1,901.70  ████▉
DECKS · 22                                         1,878  $1,987.58  █████

  Vampiric Bloodlust (Commander 2017)                100    $198.12  ▌
  Draconic Domination (Commander 2017 Precon)        100    $172.41  ▍
  Tricky Terrain Collector's Edition (Modern Hori…   100    $164.59  ▍
  …
  Duel Decks Anthology: Jace vs. Chandra (Chandra)    60     $15.05  ▏

TOTAL                                              1,978  $3,889.28
```

The two section bars tile the column exactly, so they double as the scale for the
deck bars beneath them. A blank bar means a deck is worth $0.00, usually because
its prices haven't been fetched yet; run `hoard update-prices`.

Set `COLUMNS` to override the detected terminal width.
