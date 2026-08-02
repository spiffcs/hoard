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

The left pane lists your binders first (the default binder, then any you created
with `hoard binder new`), followed by every deck ranked by value. The right pane
shows what is inside whichever container you have selected.

## Keys

| Key | |
|---|---|
| <kbd>tab</kbd> / <kbd>←</kbd> <kbd>→</kbd> / <kbd>h</kbd> <kbd>l</kbd> | switch pane |
| <kbd>↑</kbd> <kbd>↓</kbd> <kbd>j</kbd> <kbd>k</kbd> | move · <kbd>pgup</kbd>/<kbd>pgdn</kbd> page · <kbd>g</kbd>/<kbd>G</kbd> jump to ends |
| <kbd>enter</kbd> | card detail — printings, where it's held, price history |
| <kbd>/</kbd> | filter (see below) · <kbd>esc</kbd> clears it · <kbd>ctrl+u</kbd> wipes the bar |
| <kbd>s</kbd> | sort by value → name → quantity |
| <kbd>v</kbd> | switch view: holdings → movers → unpriced → watches → market |
| <kbd>+</kbd> <kbd>-</kbd> | change how many copies you hold |
| <kbd>d</kbd> | remove the card, or the deck — asks first |
| <kbd>u</kbd> | undo the last edit |
| <kbd>a</kbd> | add cards — the add flow opens right here, browser state intact |
| <kbd>r</kbd> | reload |
| <kbd>M</kbd> | value mask: hide cards under $5 → $10 → $25 → off (unpriced view exempt) |
| <kbd>q</kbd> | quit — asks y/n first (main views only) |
| <kbd>esc</kbd> | back out one frame — at the top it asks before quitting · <kbd>ctrl+c</kbd> quits anywhere |

Binder cards are editable in place (<kbd>+</kbd>, <kbd>-</kbd>, <kbd>d</kbd>,
<kbd>u</kbd>) — every binder, not just the default. Deck cards are deliberately
read-only: a deck is owned by the list it was imported from, so editing it here
would drift from that source until the next `deck add` overwrote the change
without saying so.

Pressing <kbd>a</kbd> opens the same interactive add flow `hoard add` runs —
type a name or <kbd>ctrl+o</kbd> to scan with your iPhone — inside the
browser: it takes over the screen while it runs and <kbd>esc</kbd> drops you
back exactly where you were, cursor, filter and undo intact, with the new
cards already in your binder and a one-line receipt on the status line. A
long operation (a price update, a backfill) keeps running behind it. The
full scan receipt still prints to the terminal scrollback when you quit the
browser, so the record of unattended writes outlives the alternate screen.

## Views

<kbd>v</kbd> cycles through five views: holdings, movers, unpriced, watches,
and market. All but the last are instant database reads. **The market view
waits to be asked**: it needs today's vendor quotes from MTGJSON, so cycling to it
says `press F to fetch` rather than starting a download because you passed
through. Quotes already fetched earlier the same day come back for free —
the view repopulates from the day cache on arrival, even across a restart,
with `F` still re-asking for fresh numbers. While it runs the pane says so,
and <kbd>esc</kbd> — or leaving with <kbd>v</kbd> — cancels it.

```
MARKET                                           45 rows · 1,260 printings compared
ARBITRAGE  a buylist pays more than the card last sold for
NAME           SET/NUM  FIN  LAST SOLD  BUYLIST  TO           PROFIT
Tarnished Ci…  ody/329  -        $7.81   $10.50  cardkingdom   +$2.69

EASY TO SELL  a buylist pays at least 70% of the last-sold price
NAME           SET/NUM  FIN  LAST SOLD  BUYLIST  TO           PAYS
Thassa, Deep…  thb/71   -       $25.00   $25.00  cardkingdom  100.0%

BELOW MARKET  asking well under the last-sold price
NAME           SET/NUM  FIN     ASK  AT        LAST SOLD  BELOW
Glimmerpost    som/223  foil  $1.10  manapool      $3.99  -72.4%
```

Everything anchors on tcgplayer's sales-derived market price — the one
number that describes what cards actually trade at. The three tables stack
in one scrolling pane, each with its own headers; <kbd>enter</kbd> opens any
row's card detail, and <kbd>s</kbd>/<kbd>S</kbd> sort just the table the
cursor is in, each keeping its own column and direction. `hoard market`
prints the same three tables with `--min` and `--limit`; see
[pricing.md](pricing.md#where-vendors-disagree).

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
  foil      ▁▂▂▂▂▂▂▂▂▄▄▄▄▅▅▇▇▇▆▆▆▆▆▆▇▇▇▇▇▇▇█  $46.91
            $36.55–$46.91 since 29 Apr · 11 obs
```

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
