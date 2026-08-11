# Live refresh in the browser, measured

Written 2026-08-10 against `17231c8`, on the `live-refresh-design` worktree.
Nothing here is a change; it is a measurement and a recommendation.

## The question

> "When I'm adding cards, if I have the hoard read view up in another session,
> then I should see the cards appear in the TUI as they are added. We should
> make sure any implementation or plan of this does not sacrifice performance
> or overly burden the program. If it's not worth implementing this then don't
> do it."

## The answer, up front

**Build it, in the scoped form below.** Every regime that was measured shows
the writer at baseline latency, including the burst case that a naive version
makes unusable.

The decisive numbers. One process adding ten cards a second, another watching,
against a copy of the real database. The first four rows are one measurement
session, so they compare directly:

| Current hoard (2,062 entries) | writer p50 | writer max | writes over 10ms |
|---|---|---|---|
| Adding, nobody watching (baseline) | 4.4ms | 6.6ms | 0 / 60 |
| Watcher refreshes on every change | 4.9ms | **63.0ms** | **18 / 60** |
| Watcher refreshes continuously | 38.5ms | 40.4ms | **60 / 60** |
| Watcher, **750ms quiescence gate** | 4.8ms | 6.6ms | **0 / 60** |

| 10× hoard (22,682 entries) | writer p50 | writer max | writes over 10ms |
|---|---|---|---|
| Watcher refreshes on every change | 2.7ms | **462.9ms** | **29 / 60** |
| Watcher, **750ms quiescence gate** | 4.8ms | 7.6ms | **0 / 60** |

Note the second row: the naive version is **already** bad at the hoard's
current size under a burst — 18 of 60 adds stalled, one for 63ms. This is not a
problem that waits for growth.

The feature that matters is not the poll. It is the **quiescence gate**: a
change arms a timer rather than triggering a read, and a further change re-arms
it, so a burst of adds costs one refresh at the end instead of one apiece. That
single rule is what turns the fifth row of that table into the sixth.

Two things fall out of the same measurements and are part of the recommendation:

- **The `r` key is already broken** and should be fixed whether or not any of
  this ships. Its help text says "keeping your place"; it demonstrably does not
  (§5).
- **Only holdings refreshes.** Movers costs 351ms today and 456ms at 10×, and
  market needs the network. Refreshing everything on every change is the wrong
  design and the numbers say so by a wide margin (§3.2).

## 1. Method, so the numbers are reproducible

The owner's database was **copied** to a scratch directory and every run used a
fresh copy; the original at `~/Library/Application Support/hoard/hoard.db` was
read once, by `cp`, and never opened by a test.

Copy at time of writing: **1,967 cards, 2,062 entries, 74,119 price-history
rows, 23 containers, 16 watches, 30MB, `journal_mode=delete`, schema v27.**

Measurement needed the real query paths, which live behind unexported fields,
so a **temporary** file `internal/store/zz_livebench_test.go` was added, run,
and **deleted**; likewise `internal/browse/zz_statesurvival_test.go` for §5.
Both are gone and `git status` is clean. The harness opened databases through
`store.Open`, so every run carried the real DSN — `foreign_keys(1)`,
`busy_timeout(5000)`, `_txlock=immediate`, `SetMaxOpenConns(1)`.

One precision about the journal mode, since it is the premise of §4: the DSN at
`store.go:425` does **not** set `journal_mode` or `synchronous`. It relies on
SQLite's defaults, which are `DELETE` and `FULL` — which is what the comment
above it means by "durability is the default and deliberately so". Confirmed on
the file itself rather than inferred: `PRAGMA journal_mode` on the owner's
database returns `delete`.

Writer and reader ran as **two separate processes** against the same file, which
is the situation being designed for. One "write" is `AddCardFinishTo` — the
upsert-printing plus entry-insert transaction that one scanned card lands as.

Growth was simulated by duplicating every card and entry 5× and 10× into fresh
copies (12,372 and 22,682 entries; 85MB and 141MB), so the scaling claims are
measured rather than extrapolated.

## 2. `PRAGMA data_version` — verified, not assumed

The premise was that `data_version` changes when another connection commits and
that reading it is a header read. Both hold:

```
data_version  v0=1  after_own_read=1  after_own_write=1  after_other_write=2
```

A second process's commit moves it. This connection's own reads and its own
writes do not — which matters, because it means browse's own edits never
trigger a self-refresh loop.

Its cost is **6µs, and flat**: 0.006ms on the 30MB database and 0.005ms on the
141MB one. It does not scale with the hoard, which is the property the whole
design leans on.

Nothing in the tree uses it today; that was confirmed by grep, along with the
absence of any `fsnotify` dependency.

## 3. What a refresh actually costs

### 3.1 Per read, alone, no contention

| Read | 2,062 entries | 12,372 | 22,682 |
|---|---|---|---|
| `PRAGMA data_version` | **0.006ms** | 0.006ms | **0.005ms** |
| `ListWatches` | 0.18ms | — | 0.18ms |
| `ListBinders` | 1.17ms | 10.1ms | 21.3ms |
| `EntryKeys` | 1.75ms | 11.4ms | 23.4ms |
| `ListDecks` | 2.67ms | — | 37.2ms |
| `Unpriced` | 2.78ms | — | 87.4ms |
| **`AllByFinish`** | **35.2ms** | **217.6ms** | **404.3ms** |
| `Movers` (7d) | 350.6ms | — | 456.2ms |

A holdings refresh is `ListBinders` + `ListDecks` + `AllByFinish` + `EntryKeys`
— measured end to end at **41ms** today and **526ms** at 10×.

`AllByFinish` dominates and is linear in entries (~17.5µs each). Its plan is
`SCAN card_entries`, an index probe into `cards` per row, and temp b-trees for
both the `GROUP BY` and the `ORDER BY`. The probe is expensive because `cards`
carries 9MB of `raw_json` across 1,967 rows, so walking it touches a lot of
pages. That is a standing optimisation opportunity — it would shrink every
collision window in this document — but it is not part of this design.

### 3.2 This is what decides which views refresh

`internal/browse/views.go:197` says of the analytical loads: "All are plain
database reads and return in milliseconds." True in units, misleading in
magnitude — `Movers` is a **third of a second** today and **nearly half a
second** at 10×, the single longest lock hold in the program.

So:

- **Holdings + containers: refresh.** 41ms today. This is the view the request
  is about.
- **Movers: does not auto-refresh.** 351ms→456ms, and adding a card only moves
  its QTY/IMPACT columns. Not worth the most expensive read in the program.
  `dataGen` already invalidates `moversCache`, so the next `W` or `r` pays for
  fresh rows at a moment the user chose.
- **Market: never.** It is the one view that needs the network
  (`model.go:~350`), it is fetched on request, and it is abandonable mid-flight.
  Automatic refetching would mean automatic vendor traffic.
- **Watches: does not auto-refresh.** `ListWatches` is nearly free (0.18ms) but
  the screen's third table is `Unpriced` at 87ms/10×, and the two load together
  by design. A watch's status changes on price movement, not on an add.
- **The detail overlay: never, while it is open.** See §6.3.

### 3.3 The frame cost of the tick itself

A poll tick wakes the Elm loop, and bubbletea calls `View()` after every
message. That is the poll's real cost, not the 6µs pragma:

```
View() per frame  = 199µs   (120×40 terminal)
inert Update()    =   7µs
```

≈212µs per tick. At 250ms that is **0.085% of one core** on an idle browser.
Acceptable, and worth stating rather than waving at.

## 4. Contention, which is the question the design turns on

Under `journal_mode=DELETE` a reader holds SHARED for the whole query and a
committing writer needs EXCLUSIVE, so **a writer waits for whatever read is
in flight**. Every number below is one process adding while another watches.

A note on variance before the numbers: absolute latencies moved between
measurement sessions (a baseline p50 of 2.0ms in one, 4.4ms in another) with
machine load and page-cache state. **Only compare rows measured together**, and
the tables below say which those are. The conclusions survive the variance;
individual milliseconds do not.

**Detection alone is free.** A reader polling `data_version` every 250ms and
never refreshing saw 25 changes and cost the writer nothing measurable — this
pair measured together:

```
baseline (no reader)   p50 2.04ms  p95 2.79ms  max  3.93ms   over-10ms 0/60
poll only, no refresh  p50 2.15ms  p95 4.34ms  max  4.78ms   over-10ms 0/60
```

**Refreshing is where the cost is,** and the collision cost tracks the
refresh duration almost exactly — this group measured together:

```
baseline (no reader)                  p50 4.44ms  max  6.63ms  over-10ms  0/60
refresh on every change, 250ms poll   p50 4.87ms  max 63.05ms  over-10ms 18/60
                                                  p90 40.14ms
refresh continuously, no gate         p50 38.50ms max 40.36ms  over-10ms 60/60
```

Continuous refresh makes *every single write* wait, at a median 8.7× baseline.
Gated on a 250ms poll it still stalls nearly a third of them, and the stalls
cluster at ~40ms — the duration of `AllByFinish`, which is the tell that this is
lock contention and not noise.

**The poll interval is not the lever.** Stretching it changes how many writes
can collide, not what a collision costs (measured together, in the first
session):

```
250ms poll   p95 10.3ms   max 36.2ms   (21 refreshes)
1000ms poll  p95 36.1ms   max 58.2ms   ( 6 refreshes)
2000ms poll  p95  5.1ms   max 21.4ms   ( 4 refreshes)
```

The p95 does not fall as the interval grows — it wanders. That is the shape of
a flat truth: whenever a refresh happens, a write landing inside it waits for
the whole query. Tuning the interval is tuning the *odds* of a stall, never its
size. Picking a number this way is picking noise.

**Cadence is the lever.** At a human scanning pace — one card every 2s — the
refresh finishes inside the gap and nothing collides, even at 10×:

```
1×,  one add / 2s, refresh every change   p50 2.90ms  max  4.33ms  over-10ms 0/15
10×, one add / 2s, refresh every change   p50 5.26ms  max 16.74ms  over-10ms 1/15
```

**Bursts are the danger,** and hoard has plenty of them — a decklist import,
`hoard add --file`, a merge, an update-prices run. Ten writes a second at 10×:

```
10×, burst, refresh every change   p50 2.71ms  p90 439.6ms  max 462.9ms
                                   mean 109.6ms   over-10ms 29/60
```

Half the writes stalled, mean latency 55× baseline. **This is the case that
would make the feature feel like a bug**, and it arrives on its own as the
hoard grows.

**The quiescence gate removes it.** A version bump arms a 750ms timer; a
further bump re-arms it; the refresh runs only once the stream goes still:

```
1×,  burst, 750ms gate   p50 4.82ms  max 6.55ms  over-10ms 0/60  (25 → 1 refresh)
     — against that session's baseline of p50 4.44ms / max 6.63ms / 0 of 60

10×, burst, 750ms gate   p50 4.81ms  p95 6.34ms  max 7.61ms
                         over-10ms 0/60   (26 changes → 1 refresh)
10×, one add / 2s, gated p50 4.39ms  max 6.89ms  over-10ms 0/15
```

At the current size the gated watcher is **statistically indistinguishable from
nobody watching at all** — 4.82ms against a 4.44ms baseline, identical maxima,
zero stalls on either side. At 10× under the same burst it is still zero, with
26 changes collapsed into a single read. That is the design.

The reader pays a little, which is the correct direction: its refresh went from
41ms alone to a p50 of 57ms under a burst writer, and its poll from 6µs to
0.2–0.46ms. The user adding cards is not made to wait for the user watching.

## 5. State preservation — what the shipped `r` really does

The prediction was that state preservation, not detection, is where a naive
implementation feels worse than no feature. Correct, and there is already
evidence in the tree: **hoard has two reload functions that disagree.**

- `edit.go:762 refresh()` — the edit path. Saves and restores the card cursor,
  the container cursor and the page; re-derives and re-clamps.
- `model.go:1501 reload()` — bound to **`r`** (`command.go:138`), described in
  the palette as *"Re-read everything from disk, keeping your place."* It calls
  `loadCards()`, which ends with `m.cardsPage = 0`, `m.cursor[paneCards] = 0`,
  `m.offset[paneCards] = 0`.

Measured on the same state, view = holdings:

```
cursor=2   after refresh()=2   after reload()=0
```

```
cardCursor       2 -> 0     ✗ lost
cardOffset       1 -> 0     ✗ lost
cardsPage        0 -> 0       (not exercised)
containerCursor  0 -> 0     ✓
focus            1 -> 1     ✓
floorIdx         1 -> 1     ✓
filterText   "sol" -> "sol" ✓  (6 rows before, 6 after)
watchSecOffset [3 3 3] -> [3 3 3]  ✓
marketSecOffset[4 0 0] -> [4 0 0]  ✓
moversPage       2 -> 2     ✓
detail overlay: still open, NOT re-read — stale
```

So the shipped manual refresh loses your place in exactly the way this feature
must not, and its help text says the opposite. **Fix that first**, as its own
change: it is small, it is a documented-behavior bug, and it is worth doing even
if everything else here is declined.

The watches screen's per-section offsets and the market's survive, because
`loadCards` only resets the holdings pane. They were checked with the view on
holdings; a watches-view reload should get the same probe before anyone relies
on it.

### 5.1 Index is the wrong thing to preserve

`refresh()` restores the cursor **index**, which is right for an edit — the
user changed the row that is there and it stays roughly there. It is **wrong for
an insert**. Holdings sort by value descending; a newly added card lands
wherever its value puts it, and every row below shifts down. Restoring index 2
would leave the cursor on a *different card* than the one the user was looking
at, silently.

A live refresh must preserve the cursor **by row identity** —
`ScryfallID|Finish|Condition`, the key that schema v23 made a holding distinct
by — and fall back to the clamped old index only when that row is gone.

**When the selected row disappears** (someone in the other session moved the
last copy to a deck): the honest behavior is to keep the index, clamp it, and
say so on the status line. Silently jumping to row 0 is the failure mode
`showView` had, and silently keeping a cursor on a row that no longer exists is
worse. The user's *place* survives; the user's *selection* cannot, and should
not pretend to.

## 6. The design

### 6.1 Detect

A `tea.Tick` at **500ms**, following the pattern already at `image.go:76`,
reading `PRAGMA data_version` through the existing store handle.

500ms rather than 250ms because the measurements say the interval buys nothing
below the gate's own latency — the gate is 750ms, so a 250ms poll only doubles
the frame cost (§3.3) to sharpen a number the user cannot perceive. Perceived
latency becomes poll + gate ≈ 0.75–1.25s after the last card lands. Both halves
are named constants next to the measurement that chose them.

The poll runs only in `modeBrowse` and only when `m.op == nil`. `mode()`
(`mode.go:42`) is already the single source of truth for who owns the keyboard,
so this is one condition rather than seven.

### 6.2 Gate

A change **arms** a timer; it does not read. A further change re-arms it. The
refresh runs when the stream has been still for **750ms**. This is the whole
feature — §4 is the argument for it.

### 6.3 Apply

Refresh holdings and containers only (§3.2), in the `refresh()` body, with
these differences:

- cursor restored **by row identity**, not index (§5.1);
- page follows the row it was tracking;
- the status line reports the delta: `+3 cards · $41.20` — the container pane's
  copies and value totals will move too, so the change is legible in two places;
- **if a takeover owns the keyboard, defer.** Detail overlay, text view, prompt,
  confirm, palette, filter bar, add cascade: hold the pending flag and apply on
  return to `modeBrowse`. This costs one boolean and removes an entire class of
  "it moved while I was reading it" bugs, including the stale-detail problem
  §5 measured.

### 6.4 The growth guard

At 10× the hoard, a holdings refresh is 526ms. The gate keeps that off the
writer, but it still occupies the single connection (`SetMaxOpenConns(1)`), so a
keypress needing a query waits behind it.

**Time each refresh; if one exceeds a budget (250ms is the natural line — it is
where a keypress stops feeling immediate), stop auto-refreshing for the session
and fall back to the notice: `+12 cards · press r`.** The feature then
self-limits as the hoard grows, with no knob, no config key, and no user left
wondering why their browser got sticky. It also converts the strongest argument
against the feature into a runtime behavior.

### 6.5 Opt-in: none

**Always on. No flag, no config key.**

- Measured cost when nothing is happening: 212µs every 500ms — 0.04% of a core.
- Measured cost to the writer: zero, in every regime (§4).
- `hoard` with no arguments *is* the browser (`command/browse.go`); there is no
  `browse` subcommand to hang a local flag on, so `--live` would become hoard's
  **third global persistent flag**, beside `--db` and `--json`, for a behavior
  that costs nothing to leave on.
- `docs/specs/cli-flag-audit.md` declined `--min-price` because it encoded a
  concept the codebase expresses better elsewhere. A `--live` flag is a worse
  case: it encodes a concept nobody needs to express.
- If a toggle is ever wanted, the browse-native precedent is the `settings`
  table (`views.go:85 loadPennyFilters`), reachable from the command palette —
  not the CLI surface.

The growth guard (§6.4) is the escape hatch, and it fires on measurement rather
than on the user having predicted the problem.

### 6.6 What it costs to build

Roughly 150 lines in `internal/browse` plus tests: a tick message, two
constants, an armed-timestamp field, an identity-keyed cursor restore, and one
deferred-apply boolean. No new dependency. No schema change. No change to
`internal/store` — the poll is a `PRAGMA` through the existing handle.

## 7. Rejected, with what they cost

**Switch to WAL.** WAL would let the reader and writer proceed without blocking
each other, which is the cleanest fix to §4. It is rejected here for three
reasons, in order. First, it is unnecessary: the quiescence gate already
delivers zero stalled writes in the worst case measured, so WAL would buy
nothing this design needs. Second, `store.go:425` is explicit that
`journal_mode=DELETE` with `synchronous=FULL` is deliberate — "This file is the
irreplaceable half, and every commit paying a real fsync is the choice" — and
warns that "WAL quietly weakens it to `NORMAL` semantics"; switching would mean
setting `synchronous=FULL` explicitly and then measuring what that costs, since
WAL+FULL is not WAL's usual performance story. Third, it changes the durability
posture of the owner's irreplaceable data to make a *convenience* feature
easier. If WAL is ever wanted it should be its own decision, argued on
durability, with its own measurements — not smuggled in as a detail of this.

**Refresh on every change, no gate.** Rejected by 18/60 stalled writes at the
current hoard size, and 29/60 with a 439ms p90 at 10× (§4). It also grows
*worse* over time, which is the wrong direction for a defect.

**Refresh everything — movers, watches, market.** Rejected at 351ms today and
456ms at 10× for movers alone, and at "makes network requests nobody asked for"
for market.

**Poll and notify only — never auto-apply.** Cheapest safe option: the poll is
free, the notice is a status-line string, and the user presses `r` when ready.
Genuinely tempting, and it is what §6.4 degrades *into*. It is not the
recommendation because it does not answer what was asked — "I should see the
cards appear" is not "I should be told cards appeared" — and because the
measurements say the *gated* automatic version costs the writer nothing at all
(4.82ms against a 4.44ms baseline, §4). It would be the right answer if the
gate did not work. It does.

**Manual `r` only, i.e. do nothing new.** Costs zero and is a defensible answer.
Two things argue against it. The `r` key does not currently keep your place
(§5), so the "cheap alternative already exists" claim is not true as shipped.
And `r` requires knowing something changed, which is the exact information the
user does not have while looking at the other terminal.

**`fsnotify` on the database file.** Rejected without measurement:
`data_version` is a header read that answers the question exactly, while file
watching would fire on journal churn, needs a new dependency, and behaves
differently per platform.

## 8. The strongest case for not building this

Stated plainly, because it is real.

The feature only matters when two sessions are open at once. Browse already has
an **embedded add cascade** — the `a` key — and `addchild.go:91` calls
`m.refresh()` when it closes, so adding cards *from inside the browser* already
updates both panes correctly, with state preserved, at zero contention risk.
If the two-terminal habit is a workaround for not knowing `a` exists, the right
fix is one line of documentation rather than 150 lines of polling.

Against that: the browser is a full-frame takeover during the cascade, so the
panes are invisible while adding — "watch the cards appear" is a thing the
embedded path structurally cannot do. Two terminals is not obviously the wrong
workflow. And the measured cost of serving it is zero.

## 9. What I could not resolve

- **Run-to-run variance is larger than I would like.** The writer's baseline p50
  measured 2.0ms in one session and 4.4ms in another, and the same
  refresh-on-every-change configuration produced a p95 of 10.3ms once and
  63.0ms later. Every conclusion here rests on comparisons *within* a session,
  and the effects are large enough to survive that (0/60 versus 18/60 stalled
  writes is not a variance artefact) — but no single millisecond figure in this
  document should be quoted as a constant. Nothing was done to quiesce the
  machine; a serious performance gate would need repeated interleaved runs.
- **Every number is from one machine** (macOS 15, APFS). A 2ms fsync'd commit
  under `synchronous=FULL` is fast; macOS's default barrier fsync is not a full
  device flush. On a filesystem where a commit costs 10–20ms, the writer's
  baseline moves and the collision arithmetic changes — the *ordering* of the
  conclusions should hold, but the margins would narrow.
- **The 10× database is synthetic**, built by duplicating rows. Real growth
  would bring more distinct cards and a differently shaped `card_entries`. The
  linearity of `AllByFinish` is solid; the exact 526ms is indicative.
- **The state-survival probe ran with the view on holdings.** The watches
  screen's three-table state and the movers page survived a reload in that
  configuration, but they were not exercised *as the active view*. Before
  relying on it, run the same probe with `m.view` set to each.
- **No live run.** Nothing here was validated against the real browser with a
  real scanner session — the writer was `AddCardFinishTo` in a loop, not the
  add cascade end to end. A real add also does a Scryfall lookup per card,
  which would space writes further apart and make contention rarer than
  measured, so the numbers are conservative; but "conservative" is an argument,
  not an observation.
- **The 250ms refresh budget in §6.4 is reasoned, not measured.** It comes from
  the usual keypress-perception threshold, not from anything in this document.
  Someone should sit in front of a 10× hoard and find out where it actually
  starts to feel bad.
- **Whether `AllByFinish` should simply be made faster** was not pursued. Its
  35ms is mostly page-walking a `cards` table weighed down by 9MB of
  `raw_json`. Cutting that would shrink every window in this document, and it
  would help `r`, the analytical views and every CLI read too. It is the higher-
  leverage change, and it is a different piece of work.
