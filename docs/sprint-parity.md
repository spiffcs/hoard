# Sprint: Parity — action layer, progress, palette

**Status: IN PROGRESS** (started 2026-07-31). Designed the same day in a
plan-mode session (two design passes: action-layer contract, TUI
interaction model). Written so a fresh session — human or AI — can execute
with zero prior context. Update the status markers as phases land. See
[sprints.md](sprints.md) for where this sits among the other sprints.

## Why this sprint

The CLI grew ~10 commands the TUI cannot reach (update-prices, backfill,
catalog, report, watch, import/export, repair-finishes) while the TUI grew
editing/filtering the CLI lacks. Cause: capabilities are implemented inside
`cmd*` functions (flag parsing + orchestration + printing fused), so TUI
support is bespoke per feature. This sprint makes parity structural: every
capability becomes a function in an internal package with a uniform progress
seam; the CLI and TUI are thin frontends. The beautification sprint lands
after, on a complete TUI — polishing half a surface would lock the skew in.

## Decisions already made (maintainer-confirmed at planning)

- Palette opens on `:` with an unadvertised `ctrl+p` alias.
- The fired-watch banner is a **read-only preview** (new `store.WouldFire()`,
  no state written) — cron's `hoard watch` stays the consumer of record; the
  banner repeats each TUI open until a real check runs. A glance is not an
  acknowledgment.
- The mtgjson 60s zero-bytes idle timeout ships as its **own commit**
  (deliberate behavior change: stalls become errors instead of hanging).
- TUI op wiring this sprint = **update-prices, repair-finishes, catalog
  update** (the daily-loop trio). Backfill/import/export get action
  functions but stay CLI-only until a later sprint.

## Architecture (settled)

- **`internal/progress`** (new, stdlib-only leaf): `Event{Step string; Done,
  Total int64; Unit Unit; Note string}`, `Fn func(Event)` (nil = silent;
  synchronous on the worker goroutine; must never block),
  `Throttled(fn, interval)`, `Mailbox` (latest-value cap-1 channel bridge:
  producer side never blocks — replaces the pending event). **Invariant:
  events are droppable narration; anything load-bearing is in the returned
  result struct. Errors/results are return values only.**
- **`internal/action`** (new): one func per capability,
  `func Xxx(ctx, d Deps, p progress.Fn[, opts]) (XxxResult, error)`.
  `Deps{Store, Catalog, CacheDir, Confirm func(string) bool, Resolver
  *resolve.Resolver}`. Sits above all domain packages; only `main` (and the
  closures main hands to browse) import it. The `confirmFn` and
  `cardResolver` package vars die; Deps fields replace them (test seams move
  with them).
- **Browse keeps zero network deps**: ops are injected via `Option` closures
  (the `WithArbitrage` precedent), all shaped
  `func(ctx, p progress.Fn) (T, error)`. `newQuietFetcher` is deleted once
  arbitrage rides the layer.
- **CLI renderer**: `ui.Printer` (new `internal/ui/progress.go`; internal/ui
  stays bubbletea-free). At a TTY: one updating line per step using `ui.Bar`
  (`  refreshing cards ▰▰▰▱ 1,250/4,800`), Notes as transient lines. Piped:
  plain lines only, no \r/ANSI — a locked verification target.
- **TUI bridge**: `startOp` in browse — per-op `context.WithCancel` +
  generation counter (the arbGen precedent) + Mailbox pump re-armed per
  event (`progressMsg{gen, ev}` / `opDoneMsg{gen, result, err}`). A running
  op is background state (`m.op *opState`), NOT an input mode — browsing
  stays interactive during ops.

## Phases

### ✅ P0 — Groundwork (S)

`internal/progress` (+tests: Mailbox replace semantics; Throttled passes
Step transitions and Notes immediately); `ui.Printer` (+golden tests, TTY
and piped modes); `signal.NotifyContext(os.Interrupt, SIGTERM)` in
`main.run` (main.go:154 today), second ^C restores default handling,
`context.Canceled` → "interrupted" on stderr + exit 130. No other output
changes.

**Landed** (2026-07-31): as specified. Mailbox carries a `Done()` channel +
idempotent `Close()` so the TUI pump can't leak a goroutine after an op
ends; `Fn.Emit` centralizes the nil check; `ui.NewPrinter(w, tty)` takes
the TTY-ness explicitly (main knows; tests pass a Builder). Race-detector
clean. Live check: SIGINT mid catalog download → "interrupted", exit 130,
live catalog untouched (temp-file build). All six baselines identical.

### ⬜ P1 — Action layer, CLI side (L; several commits)

First: `docs/parity.md` — the capability table (capability × action func ×
CLI form × TUI surface × steps/units × confirm × result type × exit codes ×
status), with the rule at the top: a capability lands as an action function
first; frontends only render. Doubles as the migration checklist.

Migrations in order — **stdout locked by golden tests per command BEFORE
each migration** (fixture DB + stubbed Deps.Resolver.Fetch):

1. **catalog** — `action.CatalogUpdate/EnsureCatalog/CatalogStatus`; change
   `catalog.Update(ctx, progress func(int))` → `(ctx, p progress.Fn)` (two
   call sites); countingReader over compressed bytes (Total =
   `Catalog.DownloadSize`, already known up front), card count rides as a
   Note. `Deps.Confirm` replaces the `confirmFn` package var.
2. **update-prices** (the exemplar) — `refreshCards` (root catalog.go:60)
   moves into action; new `scryfall.FetchCollectionProgress(ctx, ids,
   onChunk)` (old func delegates with nil); retry/Retry-After waits emit
   Notes — visible for the first time (they can stall 90s today, silently).
   Steps: checking catalog → (downloading catalog) → refreshing cards
   (determinate: Done starts at fromCatalog, Total = len(ids)) → saving →
   filling price gaps → recording history. **Preserve the stale-catalog
   subtlety** (root pricing.go:79-82, `priceSource` vs `cat`) with a
   dedicated test: stale catalog + declined confirm ⇒ Scryfall fetch for
   all ids, `CatalogUsed == false`. Result struct carries everything main
   prints; stdout byte-identical.
3. **gap fill** — `action.FillGaps` wrapping pricing.FillGaps
   (WithProgress prose → Notes); `newFetcher`/`fillPriceGaps` leave main.
4. **backfill** — `action.BackfillPrices`; mtgjson gains
   `Options{CacheDir, Progress func(done, total int64)}` + a counting
   reader; **separate commit**: the 60s zero-bytes idle timeout. Deliberate
   fix folded in: the "Fetching 90 days…" header moves from stdout to
   progress/stderr (closes the documented convention inconsistency).
5. **import** — `action.ImportCollection`; result struct from import.go's
   locals (copies/perBinder/created/skipped/refinished/dropped/unresolved);
   `errPartial` → `action.ErrPartial` (main keeps the exit-2 mapping).
   Resolver two-pass progress: Total may grow when the name-retry pass adds
   identifiers — renderers re-read Total on every event.
6. **deck add / bulk add / add-by-URL** — share the resolver closure.
7. **repair-finishes** — reuses the refreshing-cards machinery.
8. **arbitrage** — `action.Arbitrage`; browse's `ArbitrageFunc` gains the
   `p` parameter; delete `newQuietFetcher`.
9. **fast ops** (watch check, report, export, movers, binder ops) —
   mechanical extraction for table completeness; `p` mostly unused.
   Cuttable to a later sprint.

### ⬜ P2 — TUI mode refactor (M; behavior-preserving, lands alone)

`internal/browse/mode.go`: derived `inputMode` enum — precedence confirm →
prompt → palette → filter → detail → browse — computed from the existing
state fields (no new booleans, no state stack); `handleKey`, `View`,
`statusLine` all switch on `m.mode()` so input ownership and rendering
cannot disagree. Generalize `pendingRemoval` → `pendingConfirm{prompt
string; onYes func(*Model) tea.Cmd}` (y runs, anything else cancels —
unchanged semantics). New reusable `prompt` struct (label/text/err/
validate/commit; filter-bar-style status-line input; esc cancels, enter
commits, ctrl+u wipes). Status-line precedence becomes: confirm → prompt →
filter → transient status → op progress → arbitrage → emptyNote → position
line — implemented as one ordered function. **model_test.go (1,660 lines)
must be green with zero behavioral diffs before anything stacks on this;
it is the sprint's schedule risk.**

### ⬜ P3 — Palette + command registry (M)

`internal/browse` gains the registry (name the type `command` to avoid
reader confusion with the action package, which browse does NOT import) +
`palette.go`. Registry entry: `{id, title, aliases, key, note func(*Model)
string, where func(*Model) bool, run func(*Model) tea.Cmd}`; built once in
`New`. `handleBrowseKey` consults registry-bound keys first, then the
navigation switch (cursor movement is not a "command"). Existing
`a/d/u/v/s/S/r` migrate into the registry — single source, so the palette's
key-hint column and real bindings cannot drift. `subjectCard()` helper
resolves the row under the cursor OR the open detail card (palette must
work from detail: "watch this card" while reading it). Palette UI:
`:`/`ctrl+p`, bottom-anchored full-width drawer (input line + ≤8 fuzzy
matches + help; panes shrink — `visibleRows()` subtracts drawer height so
the pane cursor stays visible; clamp match rows ≥3 on short terminals).
Fuzzy via sahilm/fuzzy (promote to direct dep), matched runes bolded
(bold/dim only — no color, beautification comes later). `where` hides
inapplicable actions; finer refusals stay in `run` as status messages (the
`editable()` idiom — no second copy of guards). Enter closes the palette,
then runs; argumented actions open a P2 prompt.

### ⬜ P4 — Op layer in TUI + the daily-loop trio (M)

`internal/browse/opstate.go`: `opState{id, title, gen, cancel, mail, last}`
+ `startOp`; spinner tick guard generalizes to
`!m.arbLoading && m.op == nil`. Rendering: status-line slot
`⠋ updating prices · ▓▓▓░░ 412/980 · <note>` (ui.Bar ~12 cells;
indeterminate steps show step text instead of a bar) plus an always-visible
header badge `· ⠋ updating prices` (scan-session idiom) so a transient
status never makes a long op look dead. Semantics: browsing stays live; a
second op is refused with a status (palette rows show a `running…` note);
the `a` add-cascade handoff is refused while an op runs (a stranded
goroutine would write into a dead program); `q`/ctrl+c stage a
`pendingConfirm` whose onYes cancels and quits. esc chain:
arbitrage-loading → clear-filter → cancel-op (two escapes to cancel a rare
deliberate act beats eating the everyday esc; the palette carries an
explicit `Cancel: <op>` entry). Completion: `refresh()` (cursor-preserving
— NOT `reload`), reload the value series, `loadView()`, status summary
`prices updated · 980 printings · 3m12s`; cancelled →
`cancelled · 412 of 980 done` from the last event. New browse Options
wired from main via action closures: `WithUpdatePrices`,
`WithRepairFinishes`, `WithCatalogUpdate`. `f` key (unpriced view) +
palette entries trigger repair.

### ⬜ P5 — Quick wins (M; independent once P2–P4 exist)

- **Watches view**: in the `v` cycle before arbitrage (holdings → movers →
  unpriced → watches → arbitrage — the network view keeps the last slot).
  Columns NAME/SET-NUM/FINISH/WATCH/PRICE/STATE (met bold, unpriced dim);
  wired into the per-view sort machinery. `enter` opens detail, `d`
  removes (confirm; undo = re-AddWatch), `w` edits the threshold
  (prefilled — AddWatch's upsert makes edit = re-add). Store interface:
  `ListWatches` on Store; `AddWatch`/`RemoveWatch` on Editor.
- **Fired banner**: `store.WouldFire()` (ListWatches filtered
  `Met() && LastState != "met"`, no writes) called in `New`; bold status
  `2 watches met their threshold — Ragavan under $50, Bolt under $35 · v
  to view`; dismissed by any status-clearing key; repeats each open until
  cron consumes — by design, noted in help.
- **`w` add-watch-from-subject**: prompt
  `watch <name> (<finish>) · now $46.20 — threshold: ▏`; a bare number
  infers direction from the current price (below → under, above → over);
  also accepts `under 40`/`over 40`/`<40`/`>40`; a card with no price
  refuses bare numbers; etched refused with "watches support nonfoil and
  foil"; no undo entry (re-`w` overwrites; `d` in the watches view
  removes).
- **Binder management** (containers pane): `n` new (prompt), `R` rename
  (prefilled; default binder and decks refused with friendly messages),
  `d` on a binder row = confirmed delete (the store's non-empty refusal
  flows to the status line). Add `IsDefault` to `store.DeckSummary` (do
  not rely on index 0). Undo pairs: create↔delete, rename↔rename-back.
  Editor gains the binder create/rename/delete methods (verify exact store
  names in internal/store/binders.go).
- **Movers window `W`**: `moversWindow` const → `m.moversDays` cycling
  7 → 30 → 90; `loadView` re-query (milliseconds); header already names
  the date; palette gets direct jumps (`Movers: last 7 days`…).

## Verification

- Per phase: `go build ./... && go vet ./... && gofmt -l . && go test
  ./...` + the six baselines (bare/unpriced/movers/deck/catalog/help) vs a
  HEAD-built binary. Intentional diffs called out per phase; **piped-mode
  stdout byte-identical per migrated command, locked by golden tests
  written before each migration**.
- P0: kill -INT during a fixture download → exit 130, transactions intact.
- P1 real-DB checks (scratchpad copy): update-prices shows the
  refreshing-cards bar; catalog update shows the byte bar; the idle
  timeout exercised against a stalled httptest server.
- P2: model_test.go green, zero behavioral diffs.
- P3–P5: model_test-driven (palette open/filter/run; op progress via a fake
  injected op; watches view rows; banner; prompts) + live TUI eyeball by
  the maintainer (palette feel, drawer height, op status during a real
  update-prices).

## Risks

model_test churn (P2 lands alone) · status-line contention (the ordered
precedence function is the contract) · op-goroutine vs quit/add handoff
(the refusal guards are load-bearing) · stale async messages (every msg
carries gen, arbGen precedent) · stderr shape changes vs scripts (the
documented convention says stderr is narration, not contract) · idle
timeout is a new failure mode (own commit, bisectable) · the stale-catalog
price-source subtlety (dedicated test) · resolve Total growth across the
retry pass (renderers re-read Total) · palette drawer on short terminals
(clamp ≥3 rows).
