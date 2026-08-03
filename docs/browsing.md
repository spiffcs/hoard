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
tab cards · n new binder · a add cards · R rename · d remove · : import/export · / filter · M floor
F refresh prices · v views · u undo · q quit
```

The left pane leads with **All cards** — every holding in the hoard, all
binders and decks merged into one list (read-only: edit a card where it
lives) — then your binders (the default binder, then any you created with
`hoard binder new`), then every deck ranked by value. On All cards,
same-name printings collapse into one row: ten Forests across four
printings are one ×10 line, and the set column shows the dash when the
copies span printings (a set naming one of four would be a lie). The
card detail's HELD list is where the exact printings live. The right pane shows
what is inside whichever row you have selected.

## Keys

| Key | |
|---|---|
| <kbd>tab</kbd> / <kbd>←</kbd> <kbd>→</kbd> / <kbd>h</kbd> <kbd>l</kbd> | switch pane |
| <kbd>↑</kbd> <kbd>↓</kbd> <kbd>j</kbd> <kbd>k</kbd> | move · <kbd>pgup</kbd>/<kbd>pgdn</kbd> page · <kbd>g</kbd>/<kbd>G</kbd> jump to ends |
| <kbd>enter</kbd> | card detail — printings, where it's held, price history (from every view) |
| <kbd>/</kbd> | filter (see below) · <kbd>esc</kbd> clears it · <kbd>ctrl+u</kbd> wipes the bar |
| <kbd>s</kbd> | sort by value → name → quantity |
| <kbd>v</kbd> | switch view: holdings → movers → market → watches → unpriced |
| <kbd>B</kbd> | flip the left pane: binders & decks ↔ the sets you own cards from |
| <kbd>+</kbd> <kbd>-</kbd> | change how many copies you hold |
| <kbd>d</kbd> | remove the card, or the deck — asks first |
| <kbd>u</kbd> | undo the last edit |
| <kbd>a</kbd> | add cards — the add flow opens right here, browser state intact |
| <kbd>r</kbd> | reload |
| <kbd>M</kbd> | value floor: hide cards under $5 → $10 → $25 → $50 → $100 → off (unpriced view exempt) |
| <kbd>q</kbd> | quit — asks y/n first (main views and the card detail) |
| <kbd>esc</kbd> | back out one frame — at the top it asks before quitting · <kbd>ctrl+c</kbd> quits anywhere |

Foil treatments show by name: a foil copy of a printing WotC tagged with
a treatment — ripple, surge, galaxy, halo, textured and friends — says so
in every FINISH cell and in the detail's PRICE and HELD rows, because a
ripple foil and a plain foil are different products with very different
prices. The detail's tcgplayer link for a treated foil opens a search for
the treated product rather than the plain product page, since no feed we
read carries the treated product's id.

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

The detail view draws the card itself when the terminal can: Ghostty,
Kitty and WezTerm get the real image (kitty graphics protocol); iTerm2
and any other truecolor terminal get a halfblock rendering. On a wide
terminal the card grows to as much as 40 cells, pinned at the right edge
and running down beside the frame *and* the HELD/PRICE analysis — the
text column keeps at least 96 cells, so no analysis row ever clips
against the art. When the window can no longer host that beside the
analysis, the layout goes vertical: the card keeps its size and slots
between the card details and HELD, everything full-width. A window
shorter than the card asks for shrinks it (aspect kept), and one
narrower than the art itself goes text only. Resizes re-render the art
to whatever the new size calls for, and when the overlay's sections run
past the fold, <kbd>pgup</kbd>/<kbd>pgdn</kbd> scroll to them — the line
under the rule counts what lies past each edge. Each card's scan is fetched once from
Scryfall (~100 KB) and cached in the per-user cache directory (macOS:
`~/Library/Caches/hoard/images`) — never in hoard.db, so the database
backup stays lean and losing the cache costs only a refetch. The text
never waits on the picture.

`HOARD_CARD_IMAGES=0` turns images off; `=kitty` or `=halfblock` forces
a tier past the terminal detection (say, a terminal that speaks the
kitty protocol under a name hoard doesn't know). `NO_COLOR` disables
them along with everything else, and tmux/screen show no images —
graphics passthrough through a multiplexer is not supported.
`HOARD_CELL_ASPECT=2.3` pins the terminal cell's height:width ratio the
kitty image height is computed from — the knob if a blank strip shows
under the card (ratio too high) or the card looks pinched narrow (too
low); unset, hoard uses 2.8.

## Views

<kbd>v</kbd> cycles through five views: holdings, movers, market, watches,
and unpriced — the everyday reads first, then the alerts, then the
maintenance list. All but market are instant database reads.

**Every view reads through the collection pane.** All cards is the whole
hoard; selecting a binder or deck narrows the view to what it holds, and
the header names the selection (`MOVERS · SINCE 2 Jul · RICH DECK`).
Quantities follow the scope: a card held across several containers shows
the selected container's own copy count — QTY, IMPACT, and comp values
describe that deck or binder, not every copy in the collection. On
unpriced and watches the pane greys out containers with nothing to show
and the cursor skips them; arriving at one of those views with an empty
selection snaps back to All cards and says so.

**Browsing by set.** <kbd>B</kbd> (or `BrowseBySets` in the palette) flips
the left pane from binders and decks to SETS: one row per set you own cards
from, newest release first — Alpha and Beta live at the bottom. Sets whose
printings Scryfall hasn't described yet show their code and sort last until
`UpdatePrices` runs. Selecting a set works exactly like selecting a binder:
the right pane shows that set's holdings and every analytical view narrows
to the set. The rows themselves are read-only — a set is how cards were
printed, not where they live, so edits point you back at the card's binder
or deck. The toggle lasts for the session.

**Movers hides sub-$0.20 cards by default** — bulk twitching by cents is
volume, not information. The status line says so, and the palette's
`TogglePennyFilter` shows them; `SetPennyFilter` moves the line anywhere
from $0 (gate off) to $100 for hoards whose noise starts higher. This gate
is separate from the <kbd>M</kbd> value floor, which layers on top. The
gate's state and line persist across sessions.

**CHANGE and IMPACT fade on a diverging gradient** — vivid red at the
biggest visible loss, neutral gray at zero, full green at the biggest
visible gain — each column scaled to its own extremes, with square-root
compression so mid-size moves keep readable color next to a whale.
Sorting by either column reads as one smooth sweep, and the `hoard
movers` report colors identically.

**The market view fetches only when it has nothing**: quotes already
fetched earlier the same day come back for free — the view repopulates
from the day cache on arrival, even across a restart. Arriving with no
data at all (the first visit of the day) starts the fetch itself, since an
empty table inviting a keypress is a chore, not a choice; refreshing data
that already exists stays deliberate — that is what <kbd>F</kbd> is for.
While a fetch runs the pane says so, and <kbd>esc</kbd> — or leaving with
<kbd>v</kbd> — cancels it.

```
MARKET                                           45 rows · 1,260 printings compared
ARBITRAGE  buylist pays more than last-sold
NAME           SET/NUM  FIN  TCG SOLD  BUYLIST  TO           PROFIT
Tarnished Ci…  ody/329  -        $7.81   $10.50  cardkingdom   +$2.69

BUYLIST NEAR MARKET  buylist pays at least 70% of last-sold · 1–8 of 32
NAME           SET/NUM  FIN  TCG SOLD  BUYLIST    PAYS
Thassa, Deep…  thb/71   -       $25.00   $25.00  100.0%

COMPS · SELL  vendor sale prices; spread is how much they disagree
NAME           SET/NUM  FIN  TCG SOLD     MP      CK  SPREAD
Ancient Tomb   uma/236  foil    $60.00     —   $65.00    7.7%
```

Everything anchors on tcgplayer's sales-derived market price — the one
number that describes what cards actually trade at. The three tables hold
fixed regions of the pane, each with its own headers, and each scrolls
independently when its rows overflow — the title line says where you are
(`BUYLIST NEAR MARKET … · 1–8 of 32`), which is how the 70–80% pays tail stays
reachable rather than hidden under the top of the ranking. A table emptied
by the collection filter keeps its title over a note rather than
vanishing. <kbd>]</kbd>/<kbd>[</kbd> jump straight to the next or previous
table. <kbd>enter</kbd> opens any row's card detail, and
<kbd>s</kbd>/<kbd>S</kbd> sort just the table the cursor is in, each
keeping its own column and direction. `hoard market` prints the fuller
CLI report — including the BELOW MARKET section the browser dropped for
comps room — with `--min` and `--limit`; see
[pricing.md](pricing.md#where-vendors-disagree).

Two gates decide what earns a row at all: the card needs quotes from at
least **two retail vendors** (a sheet with one vendor compares nothing),
and its low ask must clear the **$1.00 floor** (bulk wobbling by cents is
noise, not opportunity). Each table shows its ranking **50 rows per
page** — <kbd>&gt;</kbd>/<kbd>&lt;</kbd> turn the cursor's table's page,
the title says where you are (`page 2/4 of 163 (>/< turns)`), and sorting
a table snaps it back to page one so the new order's top is on screen. The
floor is the same penny-filter pair the movers view carries:
`TogglePennyFilter` shows everything with two vendors, `SetPennyFilter`
moves the line ($0 turns the gate off), and the status line names the
armed floor (`penny filter < $1.00`). Moving the line re-collects from the
day's cached quotes instantly — no refetch — and, like the movers gate,
the floor persists across sessions.

**COMPS** is the comp sheet sellers build by hand, and it has two halves —
<kbd>b</kbd> flips between them. The **sell side** (default) is the
sale-price comp: what each vendor sells the card for — tcg's last-sold
price, manapool's ask, cardkingdom's ask — with **SPREAD** measuring how
much they disagree (highest sale minus lowest, over the highest). It
grades on a heat ramp: green when the vendors agree — agreement is what
makes a price real — darkening through red as they diverge. **TCG SOLD**
is always tcgplayer's sales-derived price — TCGplayer has no separate ask
column because that one figure is both its price and its market anchor.
The **buy side** is the other side of the counter: tcg's last-sold and
the asks beside Card Kingdom's cash bid, with its own **SPREAD** — retail
minus buylist over retail, the hobby's confidence signal: 20–30% marks a liquid staple
dealers can flip, around 50% is typical, and 80–90% means the retail
price spiked and no dealer believes it yet. The color is the spread
itself: at or below zero is green (the bid meets or beats the ask — pure
value), reddening linearly toward 100% — the retailer keeping the whole
sale price. A dash means no buylist bid today (Card Kingdom runs the only
buylist in the MTGJSON feed). Each side's status line states its spread
formula. Transient receipts ("sorted by …") hold the status line only
until the cursor moves; navigating always brings back the selected row's
own summary, which leads with the selection itself — the card's name on
the right pane, the binder, deck, or set's on the left
(`Akroma's Will · 10/1346 · …`) — on every view. The comps sort cycles
every visible column, **SPREAD** first as
the default — spread ascending (tightest or most negative first), money
columns descending, then name, set/num, and finish.

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
  buylist   ▁▂▂▃▃▃▄▄▄▄▅▅▅▅▅▅▆▆▆▆▆▆▇▇▇▇▇▇▇███  $24.00
            $18.00–$24.00 · 41 checks since 2 May
  spread    ██▇▇▆▆▅▅▄▄▄▃▃▃▃▃▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂▂  29.7%  47.1% → 29.7% since 2 May · tightening

  foil      ▁▂▂▂▂▂▂▂▂▄▄▄▄▅▅▇▇▇▆▆▆▆▆▆▇▇▇▇▇▇▇█  $46.91
            $36.55–$46.91 since 29 Apr · 11 obs

COMPS
            TCG SOLD      MP      CK  CK PAYS  SPREAD
  non-foil    $34.28  $35.10  $36.99   $24.00   29.7%
  foil        $46.91       —  $52.00        —       —

LINKS
   tcgplayer.com   manapool.com   cardkingdom.com   scryfall.com
```

The **buylist** row is Card Kingdom's cash offer over time — the only
buylist in the MTGJSON feed — ingested from the same 90-day archive the price
backfill reads (its own table, its own series), and kept current by every
market-view fetch. The **spread** row is the gap between the price and the
bid as a trend: a tightening spread means the dealers increasingly believe
the retail price, the same confidence signal the MARKET view's comps table
shows for one day. **COMPS** is that table's row for this card, from
today's cached quotes — when none were fetched yet, the section says to
press <kbd>F</kbd> on the MARKET view — and when a held finish's bid beats
the sales price outright, an ARBITRAGE line says so in the section's own
numbers. (A merely decent bid earns no line: the CK PAYS column already
says it.)

**LINKS** puts the vendor pages one keypress away: <kbd>←</kbd>/<kbd>→</kbd>
move the cursor across them and <kbd>enter</kbd> opens the selected page in
your web browser — tcgplayer links the exact product page (the id comes
with the card details update-prices stores), manapool links the exact
printing by set and collector number, cardkingdom links the exact product
page too — foil holdings get the foil page — via MTGJSON's sanctioned
redirect links, learned from the same set files the id resolver already
downloads (one extra pass over your sets on the next price fetch, then
never again), and scryfall is the stored page itself. A card the resolver
has not passed yet falls back to a name search.
With links present, <kbd>enter</kbd> no longer closes the overlay;
<kbd>esc</kbd> and <kbd>backspace</kbd> do.

**HELD** answers the question no single view could before: that four copies of a
card are one in the binder and three spread across two decks — and it spans
printings, each row naming its exact set and number. The list scrolls:
<kbd>↑</kbd>/<kbd>↓</kbd> move a cursor across the held rows, and landing on
a different printing re-points the whole overlay — the art, the price and
bid sparklines, and the comps all follow, because printings of the same
play card carry different prices and different art.

The held rows edit in place — the detail is exactly where a card you don't
actually own (or own from a different set) gets noticed, and fixing it
shouldn't cost the trip back to holdings. <kbd>↑</kbd> climbs from the
links into the held list; there <kbd>←</kbd>/<kbd>→</kbd> highlight one of
the row's three fields — quantity, printing, location — and
<kbd>enter</kbd> edits the highlighted one: a new count (0 removes), a new
set code (the row re-points to that set's printing of the same card — the
fix for a name-only import that resolved to the wrong set), or another
binder's name (the copies move there, merging with any already held).
<kbd>+</kbd>/<kbd>-</kbd> still nudge the count directly and <kbd>d</kbd>
removes the row after a y/n. The rules match the holdings pane: binder
rows edit, deck rows refuse (the imported list owns them), and the
browser's <kbd>u</kbd> undoes the last change.

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
