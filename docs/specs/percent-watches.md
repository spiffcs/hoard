# Percent watches

Status: **design, nothing built.** Measured against the owner's real database
on 2026-08-10 (103 days of history, 74,119 observations, 1,965 printings).

A watch today can only say a dollar amount. `store.WatchStatus.Met()`
(`internal/store/watch.go:52`) compares `*w.PriceUSD` against `w.Threshold`
and nothing else. This document designs the second thing a watch can say —
"tell me when this moves 10%" — and rejects the version of that feature most
people would build first.

---

## 1. What prompted it

Someone built a ±10% alert by exporting holdings, computing thresholds in
`jq`, and bulk-importing two watches per printing: an `over` at price×1.10 and
an `under` at price×0.90. Those sixteen rows are in the database now:

```
id  card                      finish   op     threshold   price
12  Ancient Tomb              foil     over     149.86   136.24
13  Ancient Tomb              foil     under    122.62   136.24
16  Barrowgoyf                foil     over      69.06    62.78
17  Barrowgoyf                foil     under     56.50    62.78
10  Demonic Tutor             nonfoil  over      85.68    77.89
…
```

The arithmetic is right and the feature is wrong. **The baseline is frozen at
creation.** If Ancient Tomb drifts from $136.24 to $150 the `over` fires once
and is then permanently met; the `under` at $122.62 is now asking about a
price 18% below the market and will not be heard from again. The user asked
for "tell me when this moves 10%". The tool answered "tell me when this
crosses $149.86". Those are different sentences, and the difference only
shows up weeks later, as silence.

The originating report called the anchor "either pinned at creation or
trailing", as if that were an implementation detail. It is the entire
feature. This document picks one, with numbers.

---

## 2. What was measured

Read-only against `~/Library/Application Support/hoard/hoard.db`
(`?mode=ro`). Simulation scripts were run outside the repo; nothing in
`internal/` was modified.

### 2.1 The shape of the data

| | |
|---|---|
| Observations | 74,119 |
| Distinct printing+finish series | 2,898 |
| Span | 2026-04-30 → 2026-08-10 (103 days) |
| Sources | tcgplayer 67,143 · scryfall 6,346 · cardkingdom 330 · manapool 300 |

**`card_price_history` is a change log, not a sample series.** `RecordPrices`
writes a row only when the effective price differs from the last one
recorded. Measured: 808 of 68,062 consecutive same-source pairs repeat a
price — 1.2%, and those are source-switch artifacts. This matters more than
anything else in this document, because it means:

> `MAX(price_usd)` over a date range **is** the true running high of every
> price the card was ever observed at. There is no sampling error to correct
> for and no state to maintain. A derived anchor is exact.

It also corrects a reading I made first and had wrong. A card with only 8
observations is not under-observed — those 8 series span an average of 82 of
the 103 days. **Few rows means a stable price, not missing data.**

### 2.2 How volatile the collection actually is

Fraction of consecutive observations (i.e. of *price changes*) that moved 10%
or more, held printings only:

| price band | ≥10% moves | of changes | rate |
|---|---:|---:|---:|
| <$1 | 2,339 | 20,987 | **11.1%** |
| $1–5 | 654 | 15,568 | 4.2% |
| $5–20 | 98 | 10,298 | 1.0% |
| $20–100 | 5 | 2,436 | 0.2% |
| $100+ | 0 | 84 | 0.0% |

A 10% move means something completely different at $0.50 than at $136. And
the collection is overwhelmingly cheap: of 1,871 priced held printings,
**1,304 are under $1** and exactly **one** is over $100. Every watch the
owner has actually set is on the $30+ tail.

### 2.3 Source flips are a phantom-move generator

Effective price is `COALESCE(c.price_usd, a.price_usd)` — scryfall preferred,
then the alt table. Following that rule through history: **1,897 consecutive
pairs switch source, and 210 of them register as a ≥10% move.** That is 6% of
all ≥10% moves being an artifact of which vendor answered, not of the market.
Restricted to tcgplayer alone, source flips are 0 by construction and the
≥10% rate barely changes (6.3% vs 6.4%) — so the flips add noise without
adding signal.

### 2.4 Pinned, simulated on the owner's own nine watched printings

Anchor frozen at the first observation, ±10% band, 90 days, crossing
semantics (`last_state`) applied:

| card | fin | anchor | over@ | under@ | fires | already-met for |
|---|---|---:|---:|---:|---:|---|
| Ancient Tomb | foil | 122.46 | 134.71 | 110.21 | 0 | — |
| Barrowgoyf | foil | 68.19 | 75.01 | 61.37 | 0 | — |
| Demonic Tutor | nonfoil | 66.64 | 73.30 | 59.98 | 1 | **100%** of the rest |
| Prismatic Vista | nonfoil | 38.90 | 42.79 | 35.01 | 1 | 62% of the rest |
| Stoneforge Mystic | nonfoil | 28.58 | 31.44 | 25.72 | 3 | 58% of the rest |
| Talon Gates of Madara | foil | 132.21 | 145.43 | 118.99 | 0 | — |
| Tezzeret, Cruel Captain | foil | 78.26 | 86.09 | 70.43 | 1 | **100%** of the rest |
| Urborg, Tomb of Yawgmoth | nonfoil | 57.48 | 63.23 | 51.73 | 0 | — |
| Warren Soultrader | foil | 46.78 | 51.46 | 42.10 | 1 | **100%** of the rest |

Tezzeret fired on 2026-06-02 and then sat already-met for all 37 remaining
observations. It stopped being a watch on day 22 of 90 and nothing said so.

Across the whole held collection (1,220 series with ≥20 observations), pinned
"+10% from creation": **72% fired at least once**, median 10 observations to
first fire, and after firing the watch sat already-met for a **median 84%** of
the remaining window. That is the reported bug, reproduced from inside the
proposed feature.

### 2.5 Trailing, same nine printings

Anchor is the running extreme; fires when the price is 10% off it; re-anchors
on fire:

```
Prismatic Vista    nonfoil   2026-07-11   high  38.43 ->  34.57   (-10%)
Urborg, Tomb …     nonfoil   2026-05-30   high  61.54 ->  55.31   (-10%)
Tezzeret, Cruel …  foil      2026-06-02   low   78.26 ->  86.56   (+11%)
Warren Soultrader  foil      2026-05-21   low   46.54 ->  51.96   (+12%)
Warren Soultrader  foil      2026-06-18   low   51.96 ->  57.25   (+10%)
Ancient Tomb, Barrowgoyf, Demonic Tutor, Stoneforge, Talon Gates — silent.
```

**Five alerts, nine cards, ninety days.** Every one of them is a sentence a
person would want to read, including the second Warren Soultrader leg — a
further 10% off the new floor, which is exactly the follow-up a pinned watch
can never give. Four cards said nothing, correctly.

### 2.6 Alert volume, and why the dollar floor is not optional

Trailing 10% drop applied to every held series, 103 days, 1,120 series:

| threshold | $ floor | alerts | per series | **per week** |
|---|---:|---:|---:|---:|
| 10% | — | 3,336 | 2.98 | **227** |
| 10% | $1.00 | 286 | 0.26 | 19.5 |
| 10% | $5.00 | 24 | 0.02 | 1.6 |
| 20% | — | 1,085 | 0.97 | 73.8 |
| 20% | $1.00 | 152 | 0.14 | 10.3 |
| 20% | $5.00 | 18 | 0.02 | 1.2 |

227 alerts a week is not an alert system. Nearly all of it is sub-$1 cards
where 10% is two cents. **A minimum absolute move is the difference between a
usable feature and an unusable one**, and it has to be per-watch, because $1
is far too coarse for a $3 card and far too fine for a $136 one.

### 2.7 The anchor's lookback window

Anchor = highest price in the trailing *W* days:

| window | alerts/week (no floor) | alerts/week ($1 floor) | fires on the owner's 9 cards |
|---|---:|---:|---:|
| 7d | 155 | 9.0 | **0 drop, 0 rise** |
| 14d | 175 | 14.1 | **0 drop, 0 rise** |
| 30d | 178 | 20.6 | 3 drop, 2 rise |
| 90d | 171 | 22.9 | 4 drop, 6 rise |
| since creation | 171 | 22.9 | 4 drop, 6 rise |

Short windows fail the actual use case: a 7-day high on a slow-moving $60
card is barely distinguishable from today's price, so a real quarter-scale
move never looks like one. **90d and since-creation are numerically identical
here and that is an artifact — the data is only 103 days deep.** They will
diverge; this dataset cannot say by how much.

---

## 3. The decision

**Trailing only. Pinned is not shipped in any form.**

Two reasons, in order of weight:

1. **Pinned is the bug.** §2.4 reproduces the reported failure exactly, with
   the anchor moved inside the tool. Shipping it would convert a user's
   workaround into a supported feature and inherit its defect, which is worse
   than the status quo because it carries hoard's endorsement.
2. **Pinned is already expressible.** `hoard watch add X --over 149.86` is
   the pinned percent watch. A `--pct` flag that means pinned is a unit
   conversion wearing a feature's name. It would save the user a
   multiplication and cost them the belief that the alert stays meaningful.

The two directions are named **`drop`** and **`rise`**, not `under`/`over`.
`under` names a place; `drop` names a movement. They are different questions
and giving them different words is what stops `hoard watch list` from having
to explain that "over 10" sometimes means dollars.

> **A note on scope.** The originating report asked for "percent watches".
> This design delivers percent watches whose baseline moves. If the owner
> specifically wants a frozen baseline expressed in percent — a target sell
> price stated relatively — that is a real want, but it should be built as
> sugar over the *existing* absolute watch: resolve the price at `add` time,
> store dollars, and print "set from $136.24 +10%" so the row explains where
> the number came from. That is a two-line change to `watchAdd`, needs no
> schema, and is listed as rejected-but-cheap in §11.

---

## 4. Schema

### 4.1 The conflict key already works

`watchUpsertSQL` conflicts on `(scryfall_id, finish, op)`. Because `drop` and
`rise` are **new values of `op`**, not a new dimension, a percent watch
coexists with absolute ones on the same printing for free:

```
Ancient Tomb / foil / over  $149.86     ← absolute
Ancient Tomb / foil / drop  10%         ← percent, different op, no conflict
```

And two `drop` watches on the same printing at different percentages are
still impossible, which is the existing doctrine ("two alerts for one
question would fire twice") applied without amendment.

**No `kind` column.** A `kind` column plus the existing `op` would make
`(sid, finish, 'under', absolute)` and `(sid, finish, 'under', percent)`
distinct rows — two watches on one printing both saying "under", one in
dollars and one in percent. That is the ambiguity the vocabulary split
avoids. `kind` is derivable from `op` and would only ever be a second, less
reliable copy of it.

### 4.2 Migration v28

Three `ALTER TABLE ADD COLUMN`s, all with defaults, no table rebuild:

```sql
-- v28: percent watches. A watch can now name a movement rather than a
-- price. The direction vocabulary extends (drop|rise beside under|over)
-- rather than a kind column being added, because op already keys the
-- uniqueness constraint and a second discriminator would let one printing
-- carry two watches that both read "under".
ALTER TABLE watches ADD COLUMN pct REAL NOT NULL DEFAULT 0;
ALTER TABLE watches ADD COLUMN min_move REAL NOT NULL DEFAULT 0;
ALTER TABLE watches ADD COLUMN window_days INTEGER NOT NULL DEFAULT 30;
ALTER TABLE watches ADD COLUMN last_fired_at TEXT NOT NULL DEFAULT '';
```

Four, counting `last_fired_at`, whose job is §7.

**What it does to the sixteen existing rows: nothing observable.** They keep
`op` in `('under','over')`, `pct` reads 0, and `Met()` takes the absolute
branch exactly as before. There is no data migration and no rewrite of
`threshold`. The columns are inert on absolute watches and the absolute
columns are inert on percent ones:

| op | `threshold` | `pct` | `min_move` | `window_days` |
|---|---|---|---|---|
| `under`/`over` | the dollar line | 0, unused | 0, unused | 30, unused |
| `drop`/`rise` | 0, unused | 0.10 = 10% | dollars, 0 = none | lookback |

`threshold` was deliberately **not** reused to carry the percent. It is read
as money in six places (`ui.Money(w.Threshold)` in `watchList`,
`watchRemove`, `WatchByRef`'s ambiguity message, `watchCheck`'s alert line,
and `hoardjson.Watch`/`FiredWatch`); a polymorphic column would print
"$0.10" for a 10% watch in every one of them, and the bug would be silent
because $0.10 is a plausible threshold.

`pct` is stored as a fraction (0.10), not as 10. One representation, decided
once, at the boundary: the CLI parses `10%` and the store never sees a
percent sign. Storing 10 invites the factor-of-100 error at every read.

### 4.3 The rejected schema, costed

Storing the anchor as a column (`anchor REAL`, `anchor_at TEXT`) is the
obvious design and it is the one to avoid. It costs:

- **A write path in `update-prices`.** Every price refresh would have to
  re-examine every watch to ratchet anchors. `RecordPrices` currently knows
  nothing about watches and touching it to serve alerts couples the two.
- **Correctness across a gap.** A stored anchor is frozen at the last run.
  Derived, the anchor reads whatever history exists whenever it is asked.
- **Drift with no detector.** If the ratchet is ever missed — a crash between
  the price write and the anchor write, a `merge` that brings watches but not
  history — the anchor is silently wrong forever, and nothing in the row says
  so. A derived anchor cannot drift; it is a function of the history table.

Given §2.1 — history holds every distinct price transition — the stored
anchor buys nothing a query does not already give exactly.

---

## 5. `Met()` becomes a branch

`Met()` today is a pure function of the row plus the current price. A percent
watch needs the anchor, which is a query. Rather than give `WatchStatus` a
database handle, the anchor is **joined in `watchStatusQuery` as a nullable
field**, so `Met()` stays pure and every existing caller (`ListWatches`,
`CheckWatches`, `WouldFire`, `watchList`, the TUI banner) keeps working
untouched.

```go
// Anchor is the extreme price the watch measures movement from — the
// highest price for a drop, the lowest for a rise — over the watch's
// window. Nil for an absolute watch, and nil for a percent watch on a
// printing with no history to anchor against, which answers neither
// direction.
Anchor *float64

// Met reports whether the watch's condition currently holds.
func (w WatchStatus) Met() bool {
	if w.PriceUSD == nil {
		return false
	}
	switch w.Op {
	case "under":
		return *w.PriceUSD < w.Threshold
	case "over":
		return *w.PriceUSD > w.Threshold
	case "drop":
		if w.Anchor == nil {
			return false
		}
		return *w.PriceUSD < *w.Anchor*(1-w.Pct) &&
			*w.Anchor-*w.PriceUSD >= w.MinMove
	case "rise":
		if w.Anchor == nil {
			return false
		}
		return *w.PriceUSD > *w.Anchor*(1+w.Pct) &&
			*w.PriceUSD-*w.Anchor >= w.MinMove
	}
	return false
}
```

The default returning `false` rather than panicking matters: a database
written by a newer hoard can carry an `op` this binary does not know, and a
watch that stays quiet is a better failure than one that crashes the cron.

`validateWatch` extends its op set to the four words. Note that this makes
the existing error message wrong (`"watch op must be under or over"`) — it
must name all four, or a typo'd `--drop` reports a misleading reason.

### 5.1 The anchor query

Added to `watchStatusQuery` as a correlated subquery. It reads only the
finish the watch names, and only the source-consistent series:

The window's lower bound is **computed in Go and bound as a parameter**, the
way `Movers` already does it (`internal/command/movers.go:61` passes
`cutoff.Format(time.RFC3339)` into a `WHERE as_of <= ?`). This is not a style
preference — see the measured trap below.

```sql
-- Anchor: the extreme observed price the movement is measured from.
-- Bounded below by three things at once — the watch's own creation (a
-- watch cannot measure a move that predates it), its lookback window (a
-- high from last spring is not "the high"), and the moment it last fired,
-- which is what re-anchors a trailing watch without storing an anchor.
-- ? is the window cutoff as RFC3339, computed by the caller.
(SELECT CASE WHEN w.op = 'drop' THEN MAX(h.price_usd) ELSE MIN(h.price_usd) END
   FROM card_price_history h
  WHERE h.scryfall_id = w.scryfall_id
    AND h.finish      = w.finish
    AND h.as_of >= ?
    AND h.as_of >= w.created_at
    AND h.as_of >= w.last_fired_at)
```

Four notes, each load-bearing:

- **`h.finish = w.finish`** is not decoration. A foil watch anchored on the
  nonfoil series would have compared Ancient Tomb's $136 foil against a
  nonfoil high and fired immediately. (I made this mistake once while reading
  the existing watches and it produced a table that looked like every foil
  watch was already met.)
- **Do not write the cutoff as `datetime('now', '-N days')`.** `as_of` is
  RFC3339 (`2026-08-10T21:14:12Z`); `datetime()` returns
  `2026-07-11 22:02:38` — space separator, no `Z`. These are compared as
  strings. Measured on the real database: the `datetime()` bound admits
  **24,345** rows where the correct RFC3339 bound admits **23,684**. The
  651-row difference is the cutoff day itself, where `'T'` (0x54) sorts above
  `' '` (0x20) and pulls in observations from before the window opens. It is
  not a catastrophic failure — the month digits differ first in the common
  case, which is exactly why it would survive a casual test — it is one day
  of boundary slop that silently widens every window. `strftime('%Y-%m-%dT%H:%M:%SZ',
  'now', '-N days')` would also be correct; binding from Go matches the
  existing code and keeps "what is now" testable.
- **`w.last_fired_at` defaults to `''`**, which is less than every timestamp,
  so the never-fired case needs no special handling. Same for a watch whose
  `created_at` predates its window: the tightest of the three bounds wins by
  construction.
- **The subquery does not filter by source.** It should. Per §2.3, 6% of
  ≥10% moves are vendor switches. See §12 — this is the largest thing I could
  not resolve.

### 5.2 Window default

`window_days` defaults to 30, matching `hoard movers --since 30d`. The flag
spells it the same way (`--since 30d`) and reuses `parseWindow`
(`internal/command/movers.go:103`), which already accepts `7d`, `2w`, `48h`;
the command converts to days at the boundary so the store keeps an integer
and `parseWindow` does not have to move packages.

30 rather than 90 because §2.7 cannot distinguish them on 103 days of data,
and 30 degrades more gracefully: a stale high inside a 30-day window ages
out, while `since-creation` turns a trailing watch back into a pinned one
over a long enough horizon — the exact failure this design exists to avoid.

---

## 6. When the anchor moves

**Never, because it is not stored.** It is recomputed on every read, from
`card_price_history`, which `update-prices` already maintains. This answers
the "every price refresh or only on a check?" question by dissolving it.

**Across a gap where hoard was not run for a month:** the anchor is computed
from the observations that exist. `update-prices` records the price *now*; it
does not reconstruct the interval. `BackfillPrices` looks like it would help
and does not — it is bounded to "the era with no live history of its own" and
stops where a series' first live observation begins
(`internal/store/history.go:351`), so it fills a series' prehistory, never a
hole in its middle.

Measured, on Prismatic Vista with a synthetic 31-day blackout: the real
series produces **1** alert, the gapped series produces **0**. The peak that
the drop was measured from was inside the blackout, so the drop was never
visible. **A blackout is a permanent hole and alerts inside it are lost.**

This is not a defect of the derived design — a stored anchor loses the same
alert, and additionally comes back with a stale anchor afterwards. It is a
property of a tool that only knows what it observed, and it belongs in the
docs next to `hoard movers`' existing "Prices have only been recorded
since …" hint. `hoard watch` should print the same hint when a percent watch
has fewer than two observations in its window.

---

## 7. The fire-once problem

`last_state` gives crossing semantics: fire when met now and not met before.
For a trailing watch that is **not enough**, and the measurement shows why.

Prismatic Vista, 30-day window, 10% drop, crossing semantics only:

```
2026-07-11   high 38.43 -> 34.57   (-10.0%)   fires
2026-07-12   bounced back over the line       un-mets
2026-07-13   high 38.43 -> 34.41   (-10.5%)   fires again
```

Two alerts for one continuous slide, because the anchor did not move when the
alert did. Across the nine watched printings, crossing-only produces 3 drop
alerts where the correct answer is 2.

**The fix is `last_fired_at`, and it is already in the anchor query.** Firing
sets `last_fired_at = now`; the anchor's lower bound then becomes the fire
moment, so the anchor collapses to approximately the price that fired, and
the condition immediately reads unmet. The watch is re-armed at the new
level and will fire again only on a *further* 10% move — which is the second
Warren Soultrader alert in §2.5, and is wanted.

So: **re-anchoring on fire is the notification design, and it costs one
timestamp.** `last_state` is kept and still written, unchanged, because
`CheckWatches` uses it uniformly and it stays meaningful for absolute
watches; on a percent watch it will simply read `unmet` after every fire.

`WouldFire` — the TUI banner — must **not** write `last_fired_at`, for the
same reason it does not consume `last_state` today: a glance at the browser
is not an acknowledgment. Its comment already states the doctrine and the
new field falls under it.

---

## 8. Input surfaces

> **Assumption, stated because it may be wrong.** Another lane is editing
> `internal/command/watch.go` and `internal/action/watch_import.go` right now
> to add stdin support and relax the both-bounds guard. I have read both at
> `03422f8` and have edited neither. I assume their result is: `watch import`
> accepts `-` for stdin, and `watchAdd`'s
> `if (under > 0) == (over > 0)` exclusivity check is loosened to permit
> `--under` and `--over` together. Everything below composes with that; if
> the guard becomes something other than "at least one bound, any
> combination", §8.1's flag arithmetic needs re-reading, not redesigning.

### 8.1 `watch add`

```
hoard watch add Ancient Tomb --foil --drop 10%
hoard watch add Ancient Tomb --foil --rise 10% --min-move 5 --since 60d
hoard watch add Ancient Tomb --foil --drop 10% --rise 10%     # two rows, one command
```

- `--drop` / `--rise` take a percentage. **The `%` is accepted and so is a
  bare number**, but a bare number is read as a percent, not a fraction:
  `--drop 10` is 10%, never 1000%. The parser refuses values `>= 100` for a
  drop with a message naming the confusion, because a 100% drop is worthless
  and a user who typed `0.10` meaning 10% must be caught, not silently given
  a 0.1% hair-trigger. That specific slip is the one this feature will
  generate most.
- `--min-move` is dollars, default 0. `watch add` should **warn** (not
  refuse) when a percent watch is set on a printing under $1 with no
  `--min-move`, quoting §2.6: at 10% with no floor those printings generate
  ~11% of their price changes as alerts.
- `--since` reuses `movers`' vocabulary and default.
- The existing exclusivity rule extends by kind rather than by count: `--drop`
  and `--under` on one command is a usage error. They are two different
  questions about one printing and each deserves its own `watch add` line, so
  that `watch rm` can remove one without the other.

### 8.2 `watch import`

`watchsource` looks columns up by header name and never by position
(`internal/watchsource/csv.go`), so new columns are additive by construction
and an old file keeps parsing.

CSV gains three optional columns beside the existing `Direction` /
`Threshold`:

| column | meaning |
|---|---|
| `Direction` | now one of `under`, `over`, `drop`, `rise` |
| `Threshold` | dollars — **required for `under`/`over`, must be empty for `drop`/`rise`** |
| `Percent` | required for `drop`/`rise`, must be empty otherwise |
| `Min Move` | optional dollars, default 0 |
| `Since` | optional, `30d` form, default `30d` |

`normDirection` extends to four words. Its comment — "There is no default and
no inference: a watch fires money decisions, so the file must say which way" —
is the reason `Threshold` and `Percent` are mutually exclusive **and
enforced** rather than one silently winning. A file that fills both is
refused by line number, the way an over-long CSV row already is.

JSON entries gain `percent`, `minMoveUsd`, `sinceDays` alongside the existing
`thresholdUsd`, with the same exclusivity rule.

`store.WatchInput` grows `Pct`, `MinMove`, `WindowDays`. `AddWatches`'
existence probe and its `(scryfall_id, finish, op)` upsert are unchanged —
§4.1 is why.

---

## 9. The JSON document

`schemaVersion` goes **1.0.0 → 1.0.1**. ADDITION, not REVISION: every new
field is optional, no existing field changes meaning, and a 1.0.0 consumer
reading a 1.0.1 document sees exactly what it saw before. MODEL stays 1 — the
kill switch `read.go` enforces is untouched, and nothing here breaks a reader.

The one field whose *meaning* widens is `op`'s enum, from `enum=under,over`
to four values. That is worth a second look and I claim it is still an
ADDITION on the emit side: a consumer that switches on `op` and has no `drop`
case was already obliged to handle an unknown value, and the alternative
reading — that widening an enum is a REVISION — would make every future
`Kind` addition a REVISION too, which the existing `Kind` enum's history
contradicts.

**Every added field carries `omitempty`**, because `schemagen` makes a field
without it REQUIRED, and a required `percent` would make every absolute
watch's document invalid against its own schema. This is the same trap
recorded for `colorIdentity`.

```go
// Watch is one standing price alert on one printing and finish.
//
// Op names both the comparison and its units: under/over are dollar lines
// and read Threshold; drop/rise are movements and read Percent. Exactly
// one of the two is meaningful, which is why both are omitempty — a
// document that carried both would not say which one the alert obeys.
type Watch struct {
	Card      Card    `json:"card"`
	Op        string  `json:"op" jsonschema:"enum=under,enum=over,enum=drop,enum=rise"`
	Threshold float64 `json:"threshold,omitempty"`
	// Percent is the movement that fires the alert, as a fraction: 0.1 is
	// a ten percent move. A fraction rather than 10, because the document
	// is read by scripts and a percent sign's worth of ambiguity in a
	// number that multiplies prices is not worth the readability.
	Percent    float64 `json:"percent,omitempty"`
	MinMoveUsd float64 `json:"minMoveUsd,omitempty"`
	SinceDays  int     `json:"sinceDays,omitempty"`
	Display    string  `json:"display"`
	CreatedAt  string  `json:"createdAt,omitempty"`
}
```

`Threshold` gaining `omitempty` is a change to an existing field. It is safe
in exactly one direction and needs stating: a `threshold` of 0 is not a
meaningful absolute watch (`parseThreshold` requires positive), so `omitempty`
can only ever elide a value that was already impossible.

`FiredWatch` needs more, because §10 is a real gap:

```go
// FiredWatch is one alert. An absolute watch crossed a line and reports
// it; a percent watch reports a movement, which a threshold column cannot
// express — AnchorUsd is the price the move is measured from and
// AnchorAt is when that price was observed, so a reader can say "down 11%
// from its 3 July high" without re-querying the history.
type FiredWatch struct {
	Card         Card     `json:"card"`
	Op           string   `json:"op" jsonschema:"enum=under,enum=over,enum=drop,enum=rise"`
	ThresholdUsd float64  `json:"thresholdUsd,omitempty"`
	PriceUsd     float64  `json:"priceUsd"`
	Percent      float64  `json:"percent,omitempty"`
	AnchorUsd    *float64 `json:"anchorUsd,omitempty"`
	AnchorAt     string   `json:"anchorAt,omitempty"`
	// MovedPct is the movement actually observed, signed: -0.113 is down
	// 11.3%. It is derivable from PriceUsd and AnchorUsd and is included
	// anyway, because the alert's whole content is this number and a
	// consumer should not have to recompute the thing being reported.
	MovedPct float64 `json:"movedPct,omitempty"`
}
```

`AnchorAt` requires the anchor query to return the observation's `as_of`
alongside its price — an `ORDER BY price_usd DESC LIMIT 1` shape rather than
`MAX()`. Cheap, and it is what makes the output in §10 possible.

The `hoard` merge document carries `Watches []Watch` and therefore carries
percent watches with no further change. Its existing doctrine — "It carries no
last-fired state … a merged watch evaluates fresh" — holds and gets *better*
here: `last_fired_at` is deliberately not in the document, so a merged percent
watch anchors from the receiving hoard's own history, which is the only
history it can honestly speak about.

---

## 10. What `hoard watch` prints

A $ threshold column cannot express a movement. Today:

```
Ancient Tomb (2XM 234) foil is $136.24, crossed over $149.86
```

For a percent watch the sentence has to name the anchor, the movement, and
when the anchor was set — otherwise "down 10%" is unfalsifiable from the
alert alone:

```
Prismatic Vista (MH1 244) is $34.57, down 10.0% from its $38.43 high of 24 Jun
Warren Soultrader (DSK 97) foil is $57.25, up 10.2% from its $51.96 low of 21 May
```

`down`/`up` rather than `drop`/`rise` in the rendered sentence: the op is the
watch's name for the rule, the prose is the report of what happened. The
percentage is bolded, not the price — the price is context and the movement
is the news, which inverts the emphasis the absolute alert uses on purpose.

`watch list`'s **WATCH** column already renders `fmt.Sprintf("%s %s", w.Op,
ui.Money(w.Threshold))`. It becomes op-dependent:

```
ID  NAME               SET/NUM   FINISH  WATCH            ANCHOR   PRICE   STATE
12  Ancient Tomb       2XM 234   foil    over $149.86          —  $136.24  waiting
20  Ancient Tomb       2XM 234   foil    drop 10% ≥$5     $136.24  $136.24  waiting
21  Prismatic Vista    MH1 244   —       rise 10%          $33.82   $34.57  waiting
```

A new **ANCHOR** column, `Priority` set so it is the first thing dropped on a
narrow terminal (it is the least important of the three numbers), holding `—`
for absolute watches. The alternative — folding the anchor into the WATCH
cell as `drop 10% from $136.24` — was rejected because that column is already
the widest variable-content cell in the table and `ui.Table`'s flex budget
goes to NAME, which is the column a user scans.

The `--min-move` suffix (`≥$5`) is shown only when nonzero. A watch whose
floor is silently suppressing alerts must say so on the row, or the feature's
one confusing behaviour ("why didn't it fire, it moved 12%") has no visible
cause.

---

## 11. Rejected, with costs

**Pinned percent (`--pct` anchored at creation).** §2.4: 72% of series fire
within a median of 10 observations and then sit already-met for a median 84%
of the remaining window. It is the reported bug with a flag on it. Cost of
shipping it: the feature looks correct for about three weeks and then goes
quiet, which is indistinguishable from "nothing moved" — the worst possible
failure mode for an alert.

**Pinned percent as sugar over an absolute watch.** *Not rejected on merit —
deferred.* `--over +10%` resolving to dollars at `add` time, storing
$149.86, and printing "set from $136.24 +10%" is honest, needs no schema, no
migration and no `Met()` branch, and is maybe fifteen lines in `watchAdd`. It
is a strictly better version of what the `jq` pipeline did. It is not in this
design because it is a *different feature* — a relative way to say an
absolute number — and bundling it would blur the distinction §3 exists to
draw. Worth building separately, after, when the two cannot be confused.

**Storing the anchor.** §4.3. Costs a write path in `update-prices`,
correctness across gaps, and an undetectable drift mode. Buys nothing,
because §2.1 makes the derived value exact.

**A `kind` column.** §4.1. Costs a discriminator that permits two watches per
printing both reading "under", one in dollars and one in percent, and makes
the `(sid, finish, op)` uniqueness constraint no longer mean what its comment
says. Buys the ability to have a percent and an absolute watch in the same
direction, which is a question nobody asks.

**Reusing `threshold` to carry the percent.** §4.2. Costs six call sites that
would render "$0.10" for a 10% watch, every one of them plausible enough to
survive review. Buys one column.

**A 7-day or 14-day window.** §2.7: fires 0 times on all nine of the owner's
watched printings across 90 days. A short window makes the anchor track the
price, and an anchor that tracks the price cannot measure movement.

**No dollar floor.** §2.6: 227 alerts/week collection-wide. The feature would
be turned off within a day.

**Percent on the whole collection by default.** Not proposed by anyone, but
worth pre-refusing: 1,304 of 1,871 priced held printings are under $1, and
`watch import` over a full holdings export is exactly how this feature gets
into that state. It is the shape of the originating incident.

---

## 12. What I could not resolve

**1. Which price series the anchor should read — the largest open question.**
The anchor query in §5.1 reads all sources for the finish. §2.3 measured 1,897
source flips in history, 210 of which look like ≥10% moves. That means **up to
6% of percent alerts would be reporting a vendor change as a price change**,
and they would be indistinguishable in the output from real ones. Three
options, none measured against the others:
   - Anchor on the same source as the current effective price. Correct in
     principle; the effective price's source is not currently exposed to the
     watch query, and `card_prices_alt` has `source_usd`/`source_usd_foil`
     columns that would need joining through.
   - Anchor on tcgplayer only. 90.6% of history, 0 flips by construction,
     and §2.2 shows the volatility profile is essentially unchanged. Simple,
     and wrong for any printing tcgplayer does not price.
   - Ignore it and accept ~6% phantom alerts. Cheapest; I do not recommend it,
     but I cannot rule it out without knowing how often the *effective* source
     flips for the specific printings people watch. On the owner's nine, the
     dense series are all tcgplayer and scryfall appears 3–6 times each — so
     the exposure is real but I did not isolate it per-watch.

**2. Whether 30 days is right.** §2.7 shows 90d and since-creation are
numerically identical on this data and that is purely because the history is
103 days deep. The choice of 30 is argued from degradation behaviour, not
measured. It wants re-testing at a year of history; the column exists so the
default can move without a migration.

**3. What a percent watch should do on an unpriced or newly-added printing.**
Talon Gates of Madara has 5 observations spanning 7 days. Its anchor is
computed from those 5 points, and a "high" derived from a week of a card's
first month is not a high. `Met()` returning false when `Anchor` is nil covers
*no* history; it does not cover *thin* history. A minimum-observations guard
(3? 5?) is probably right and I have no measurement to set the number.

**4. Whether `min_move` should default to 0 or to something.** 0 is the
honest default — it changes no behaviour nobody asked for. But §2.6 says 0 is
the unusable setting, and a default that is measurably unusable is a strange
thing to ship. A price-proportional default (say, `max(0, 0.25 × price)`) was
not simulated and might dominate both.

**5. Whether the crossing/`last_fired_at` interaction is right when a watch
is edited.** `watchUpsertSQL` resets `last_state` on re-add, on the reasoning
that a new threshold has not been checked yet. Re-adding a percent watch
should presumably also reset `last_fired_at`, since the anchor's lower bound
would otherwise be a fire that belonged to the previous rule. I believe it
should reset; I have not thought about it hard enough to be sure it is not
the mechanism that lets someone accidentally re-fire an old alert by
adjusting a percentage.

**6. I did not prototype any of this in Go.** Every number here comes from
simulating the semantics over the real price series in a scratch directory
outside the repo. No file in `internal/` was modified — this document is the
only change in the worktree — and nothing was run that could write to the
owner's database; every query used `file:…hoard.db?mode=ro`.

The anchor subquery in §5.1 has **not** been executed as written. What was
executed against the real database is its date-bound arithmetic, which is
where the one bug found so far lived (§5.1, second note). `Met()`,
`watchStatusQuery` and the migration are unexercised text. The first thing an
implementer should do is run the §5.1 subquery against a copy of the real
database and check the anchors it produces against the five alerts in §2.5 —
if it does not reproduce those five and only those five, this design is
wrong somewhere I did not look.

---

## 13. As built

Status: **built and green**, on `percent-watches`, uncommitted. Schema v28,
`schemaVersion` 1.0.1. This section records what was decided, what it cost,
and the places the implementation departs from §5.1 — each because running the
query against the real database found something the design had not.

### 13.1 What the anchor reads

**The anchor reads whichever source hoard's effective price came from**, for
both ends of the movement — §12.1's option 1. That was not the first answer.

It shipped first as a constant, `store.AnchorSource = "tcgplayer"`, on §2.3's
reasoning: 90.6% of recorded history, no source flips by construction, and a
vendor change reported as a price change is the one failure an alert cannot
survive. Three measurements retired it, and the sequence is more useful than
the conclusion.

**Restricting only the anchor is worse than not restricting it.** hoard's
effective price is `COALESCE(scryfall, alt)` and Scryfall now prices 1,871 of
1,967 printings. A tcgplayer anchor compared against that leaves **6.1% of held
series already more than ten percent apart on the vendor gap alone** — firing
on the first check and every check after. That is §2.3's 6% converted from
occasional to structural. Whatever the anchor reads, the price it is compared
against has to come from the same series.

**A named vendor is a fact about the past.** The feed changed over in late
July, and by the most recent week tcgplayer had stopped being written at all:

| week | tcgplayer | scryfall | cardkingdom |
|---|---:|---:|---:|
| 2026-W29 | 4,945 | — | — |
| 2026-W30 | 3,899 | 2,829 | 306 |
| 2026-W31 | 2,237 | 1,319 | — |
| 2026-W32 | **0** | 2,198 | — |

The constant was anchoring to a series nothing writes any more.

**The fixed vendor was itself manufacturing the staleness it had to be worked
around.** Held series with no observation in the last thirty days: **1 of 2,898
across all sources, 336 of 2,884 restricted to tcgplayer.** The 13.4% blind
spot that forced §13.3's carry-forward was 96% created by the restriction.

Following the effective price cannot go stale that way, cannot disagree with
the price hoard displays, and needs no vendor named anywhere. The cost is
narrower than the fixed vendor's: a printing whose source has just changed and
whose price has not moved since has no rows in its new series yet, because
history records transitions. That is 2 of 1,968 held series, it resolves at the
first real move, and `watch add` says so at the time.

**The shape that made this cheap is worth keeping.** The decision lived in one
named constant, so reversing it was an expression and a test rather than a
rewrite. A decision made from a measurement of the past should be stored where
it can be found again.

### 13.2 §2.5, reproduced and then re-measured

Replaying §5.1's semantics over a read-only copy of `hoard.db`, trailing 10%,
re-anchoring on fire. First under the vendor rule as originally decided:

| window | source | alerts on the nine |
|---|---|---:|
| 90d | all | 10 |
| 90d | tcgplayer | 7 |
| 30d | all | 7 |
| **30d** | **tcgplayer** | **5** |

The 30d/tcgplayer run is §2.5 exactly — same five cards, same dates, same
anchors, same prices, and the same five silent. **§2.5's heading is wrong**: it
says "ninety days", and at ninety the same rule fires seven times. The numbers
in §2.5 are the 30-day run.

Under the shipped rule — effective-source anchor, guard on the printing's
record — the replay gives **five alerts on the same five cards**, with the same
five silent, and three of them at different moments:

```
Urborg, Tomb …      2026-05-30   high  61.54 ->  55.31   (-10.1%)   = §2.5
Prismatic Vista     2026-07-11   high  38.43 ->  34.57   (-10.0%)   = §2.5
Warren Soultrader   2026-05-30   low   46.54 ->  54.03   (+16.1%)   §2.5: 05-21, +12%
Tezzeret, Cruel …   2026-06-12   low   78.27 ->  87.99   (+12.4%)   §2.5: 06-02, +11%
Warren Soultrader   2026-07-03   low   54.03 ->  59.83   (+10.7%)   §2.5: 06-18, +10%
```

The three that moved all moved *later*, and to the same boundary: the record
begins 2026-04-30, so no watch can claim a thirty-day window before 05-30, and
the guard holds them until it can. That is the dataset's own start showing
through, not the vendor rule — §2.5 was computed without any history guard at
all.

### 13.3 The other three open questions

**2. Thin history — guard the record's reach, not its row count, and not the
anchored slice's reach either.** §12.3 reads Talon Gates of Madara's five
observations as thin. Measured: that series is **97.9 days old**. It has five
rows because its price has not moved since 12 May — §2.1's own insight ("few
rows means a stable price") applied to the card §12.3 cited for the opposite. A
count of five would have muted **63% of held series**, nearly all of them the
best-known prices in the collection. Collection-wide, no held series is younger
than 14 days.

So a percent watch does not fire while **the printing's record**, across every
source, is younger than the window it claims to summarise. It takes its
threshold from the watch's own `--since` rather than a number chosen by hand,
and mutes **5 of 1,968** held series.

The word *printing's* is worth 83% of the collection. Guarding on the anchored
slice instead — the vendor currently answering — muted **1,652 of 1,968** the
day the feed changed over, because the new vendor's slice was eleven days old.
That would also have been guarding the wrong direction: a window truncated to a
young slice puts the anchor *nearer* the current price, making both a drop and
a rise harder to reach, not easier. §2.7 measured exactly that, where seven-
and fourteen-day windows fire zero times on the owner's nine printings. A short
slice fails closed on its own. The case that genuinely needs a guard is a new
printing whose first recorded price may be a preorder spike, and the printing's
own reach is what detects it.

`watch list` shows a guarded row as **waiting on history** rather than
`waiting`, so a watch that cannot yet fire does not look like one that might.

**3. `min_move` defaults to $0.25.** Simulated over 1,959 held series, 103
days, trailing 10% drop:

| rule | alerts | per week | binds below | where they land |
|---|---:|---:|---:|---|
| none | 1,706 | 115.9 | — | `<$1`:1037 `$1-5`:505 `$5-20`:149 `$20-100`:15 |
| **$0.25** | **585** | **39.8** | **$2.50** | `$1-5`:378 `$5-20`:149 `<$1`:43 `$20-100`:15 |
| $0.50 | 362 | 24.6 | $5.00 | `$1-5`:186 `$5-20`:149 `$20-100`:15 |
| $1.00 | 183 | 12.4 | $10.00 | `$5-20`:108 `$1-5`:53 `$20-100`:15 |
| $5.00 | 10 | 0.7 | $50.00 | `$1-5`:5 `$20-100`:4 |
| 0.25 × anchor | 304 | 20.7 | *n/a* | `<$1`:192 `$1-5`:103 `$5-20`:9 |

$0.25 removes 994 of the 1,037 sub-$1 alerts and costs **nothing** above
$2.50 — the `$5-20` and `$20-100` bands fire 149 and 15 alerts with it and
without it. What each rule actually demands of a 10% watch:

| rule | $3 card | $136 card | $0.50 card |
|---|---:|---:|---:|
| $0.25 | **10.0%** | **10.0%** | 50.0% |
| $1.00 | 33.3% | 10.0% | 200.0% |
| $5.00 | 166.7% | 10.0% | 1000.0% |
| 0.25 × anchor | 25.0% | 25.0% | 25.0% |

$1 and $5 buy quiet by breaking the feature: they silently convert a 10% watch
on a $3 card into a 33% or 167% one, so the card a user deliberately chose is
the card that stops answering.

**The price-proportional default §12.4 proposed is degenerate**, and this is
the numeric proof: a floor of `k × anchor` makes the condition `move ≥ k`, so
it is a second percentage wearing a dollar sign. Simulated, `0.25 × anchor`
fired 304 alerts against 293 for a plain 25% watch with no floor, and it
demands 25% at every price alike — so it cannot tell a $3 card from a $136 one,
which is the only thing it was proposed to do.

**4. Window stays 30 days.** Not spent on. §13.2 notes it is also what
reproduces §2.5.

### 13.4 The window carries the price in effect into itself

§2.1 establishes that `MAX(price_usd)` is the exact running high of every price
*inside* the range. The other half of "history is a change log" is that a
printing whose price has not moved writes nothing at all, so the range can be
empty of a series that is perfectly well known.

Under §5.1 as written those watches have a `NULL` anchor and can never fire.
Worse than never: when the price finally falls, `RecordPrices` writes one row,
that row is the only thing in the window, `MAX` equals the fallen price, and
the alert is **lost rather than delayed**. So the window opens at the last
observation at or before its lower bound.

Under the original tcgplayer rule this was load-bearing — 262 of 1,959 held
series (13.4%) had nothing in a thirty-day window, Talon Gates among them.
Anchoring on the effective source drops that to 1 of 2,898. **The carry-forward
is kept anyway.** "An empty window is not an unknown series" is true whatever
the anchor reads, a lost alert is worse than a delayed one, and a correctness
property is in a better place when nothing depends on it.

The window cutoff is computed in SQL from a bound `now`, as
`strftime('%Y-%m-%dT%H:%M:%SZ', ?, '-' || window_days || ' days')`, because
`window_days` is per row and one bound parameter cannot carry it. The format is
explicit and the trap §5.1 measured is covered by a test that fails when it is
spelled `datetime()`.

### 13.5 A negative control that does not fail is not a control

Four mechanisms have tests that assert the correct behaviour, and each was
verified by breaking the mechanism and watching the test fail: the history
guard, the re-anchor, the RFC 3339 bound, and `RecordPrices` not writing
unchanged prices. A fifth covers the carry-forward.

The fourth is the one worth recording. Its first version **passed with
`RecordPrices` fully broken**. `appendPrices` deliberately collapses two
refreshes inside one second onto the primary key — the later price is the truer
one — so a test that fast watched the spurious write overwrite itself and
reported green. It was measuring the collision, not the skip. It now walks the
stored rows back a day between refreshes.

The general form: a test that has never been seen to fail is evidence about
nothing, and green from a run that could not have gone red is not verification.
It is worth breaking the mechanism on purpose once, at the time the guard is
written, while it is still obvious what breaking it should look like.

### 13.6 Input surfaces

`watch add` takes `--drop` and `--rise` as percentages, with `--min-move` and
`--since`; a movement and a dollar line in one command is a usage error.
`watch import` takes `Percent`, `Min Move` and `Since` beside `Threshold`, in
both dialects, with the two size cells mutually exclusive and refused by line
number. Columns are read by name, so a watch file written before movements
existed parses unchanged.

The two dialects spell a movement differently and it is deliberate: the CSV's
`Percent` is a percentage, because a person types it and `store.ParsePercent`
exists to catch the one who types `0.1` meaning ten percent; the JSON's
`percent` is a fraction, because that is what `hoardjson.Watch.Percent` means
and a watch list hoard emitted has to read back unchanged.
