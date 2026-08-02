# Sprint: TUI completion — seamless add + parity ledger

**Status: IMPLEMENTED (all phases landed 2026-07-31; A3's live-terminal
smoke of the embedded cascade — add flow, receipt on exit, camera path —
still to be run by a human)**

Written so a fresh session can resume any phase with zero prior context.
Update the phase table as work lands.

## Why

Browse's "Add cards" quits the TUI, runs the add cascade as a second
bubbletea program, and rebuilds browse from scratch — flicker, lost
cursor/filter/undo state, and adds are refused while an op runs.
docs/dogfood-notes.md deferred "seamless add" as a design decision pairing
with the remaining parity ledger. Decisions of record (user-confirmed):

- **Embed the cascade as a child model** inside browse (full-screen
  takeover, the detail-view precedent) — not a nested-program handoff.
- Scope includes the four ledger items: **deck add by URL, TUI confirm
  modal, import + export prompts, report view**.
- Receipt: **status one-liner when the cascade closes + the full per-card
  receipt prints to scrollback when browse itself exits** (the record must
  outlive the alt screen).
- Ops keep running during an add (the op-blocks-add guard dies with the
  handoff).

## Phase table

| Phase | What | Status |
|-------|------|--------|
| A1 | tui `Child` facade (no behavior change) | ✅ 2026-07-31 |
| A2 | browse child mode, handoff kept as fallback | ✅ 2026-07-31 |
| A3 | wire main, delete the handoff | ✅ 2026-07-31 (manual smoke pending) |
| B1 | confirm bridge (`Deps.Confirm` → modal) | ✅ 2026-07-31 |
| B2 | text overlay + report view | ✅ 2026-07-31 |
| B3 | rich op outcome + deck add by URL | ✅ 2026-07-31 |
| B4 | import (file prompt, re-import confirm, result overlay) | ✅ 2026-07-31 |
| B5 | export (path/format prompts, overwrite confirm) | ✅ 2026-07-31 |
| B6 | bonus add-by-URL + docs pass | ✅ 2026-07-31 |

Order: A1 → A2 → A3 → B1 → B2 → B3 → B4 → B5 → B6. B1/B2 are independent
of Part A and can interleave. B4 needs B2+B3; B5 needs B2's idioms only.

Gates per phase: `go build ./... && go vet ./... && gofmt -l . &&
go test ./...` + the six CLI baselines byte-identical
(bare/unpriced/movers/deck/catalog/help). One commit per phase.

## Part A — embed the add cascade

Verified ground the design rests on: the cascade never uses altscreen or
mouse (`tui.Run` builds `tea.NewProgram(m)` plain), its View is short and
unpadded, WindowSize handling is three lines, and it has exactly two
`tea.Quit` sites (ctrl+c; esc at the name prompt when not reviewing).
bubbles v1.0.0's spinner ID-rejects foreign TickMsgs, so both models can
share tick delivery. The scan session's goroutines are cleaned up only by
`tui.Run`'s post-exit close or the model's own `closeSession` — an embedded
child must get explicit teardown on every exit path.

### A1 — tui `Child` facade

New `internal/tui/child.go`: exported `Child` wrapping the unexported
`model` — `NewChild(ctx, Searcher, Adder, Scanner, initialName, dests)`,
`Init/View`, `Update(msg) (Child, tea.Cmd)` (returns `Child`, swallows
`tea.Quit` once done), `Done() bool`, `Summary()`, `Err()`, and
`(*Child).Close()` — forced teardown: `closeSession` plus the
close-prompt-discard accounting (`resolveGen++`, count queued+resolving,
record a "discarded" summary entry) so a forced quit leaves an honest
receipt. `model` gains `done bool`; both quit sites set it first (the
ctrl+c site also closes the session before quitting — safer standalone
too; `Run`'s post-exit safety net stays). Invariant comment at the field:
every future `tea.Quit` site must set `done` first. `tui.Run` and
model_test.go: zero churn.

### A2 — browse child mode (fallback kept)

- `WithAddCascade(newChild func() (tui.Child, error))` Option; Model
  fields `newAddChild`, `addChild *tui.Child`, `addSummary tui.Summary`.
  Browse imports internal/tui for types only; construction stays in main.
  Absent option → status "adding is unavailable in this build".
- `modeAddChild` in mode.go: precedence confirm > **addChild** > prompt >
  palette > filter > detail > text.
- Update restructure: named cases stay browse's (WindowSize → self AND
  forward; arbitrage/op msgs → self, ops keep running behind the child;
  spinner.TickMsg → both models); the default case forwards everything
  else through a single `forwardToChild` funnel that also notices
  `Done()` → `closeAddChild()`.
- `handleAddChildKey`: ctrl+c belongs to browse (teardown child → quit;
  with an op running, stage the quit-mid-op confirm whose onYes tears down
  the child, cancels the op, quits — rendered as one line under the child
  view). Every other key is forwarded; esc walks the cascade's own chain.
- Sizing: full frame; synthesize a WindowSizeMsg synchronously on open
  (the child must never render its 80×22 default); forward real resizes.
- `closeAddChild`: fold `Summary().Entries` into `m.addSummary`, status
  one-liner ("added 3 — 2 auto, 1 reviewed"), then refresh() +
  loadValueSeries() + loadView() — on close only; the panes are invisible
  during the cascade.
- command.go `add`: `openAddCascade` when injected, else the existing
  wantAdd+Quit body including its op guard (correct for the handoff path).

### A3 — wire main, delete the handoff

`storeAdder(st) tui.Adder` extracted from addByName; main's cmdBrowse loop
becomes a single `browse.Run(..., WithAddCascade(closure))` +
`printScanSummary(sum)`. The closure captures cmdBrowse's existing catalog
handle (collapsing add.go's separate one) and re-reads `destinations(st)`
per invocation so new binders appear. `browse.Run` returns
`(tui.Summary, error)`; `wantAdd` dies; the "taking turns is the whole
design" doc comment gets rewritten; the command fallback + op guard are
deleted. Manual smoke required (add flow, receipt on exit, camera path).

### Part A risks

A future `tea.Quit` without `done` quits all of browse (invariant comment
+ the `Child.Update` swallow is the single choke point). Shared catalog
handle vs a concurrent catalog-update op — verify during A3 (searcher is
per-invocation, bounding exposure). Same-frame status collision — cosmetic.

## Part B — parity ledger

### B1 — confirm bridge

`Deps.Confirm` is a synchronous blocking call on the op goroutine (sole
caller: `EnsureCatalog`, the catalog-download questions); browse's confirm
is async Elm state. Bridge: new `internal/browse/opconfirm.go` with
exported `ConfirmRequest{Question string; Reply chan<- bool}` and
`WithConfirm(<-chan ConfirmRequest)`. Pump `awaitConfirm` armed from
`Init()` (currently `return nil`), re-armed per delivery, selects on
ctx.Done. **No generation guard** — an asker is by definition still
blocked, never stale; answering is mandatory. main creates a cap-1 ask
channel and cap-1 Reply per question; `deps.Confirm` sends, waits,
ctx-guarded both ways.

`pendingConfirm` gains `onNo func(*Model)` (nil for existing users) and
`help string`. `handleConfirmKey`: non-y runs onNo then cancels; **ctrl+c
runs onNo AND cancelOp before quitting** (fixes the blocked-worker trap;
strict improvement for the existing quit-mid-op confirm too). Collision
with the single `m.confirm` slot: park the op request in `m.deferredAsk`,
stage it when the current confirm resolves — at most one can exist.
helpLine renders `m.confirm.help`, retiring the hardcoded "y remove"
(sprint-parity.md's recorded debt: fix when a second confirm user exists).

### B2 — text overlay + report

New `internal/browse/textview.go`: `textView{title, lines, offset}`,
`m.text`, `modeText` below detail. Scroll up/down/pgup/pgdn/g/G,
esc/enter close, q/ctrl+c quit, `:` closes-then-opens palette, footer
"line 12–40 of 96". Report command (`report.view`, palette-only, rank 0):
`ReportFunc(top, width int) ([]string, error)` / `WithReport`; main's
closure runs `Deps.Valuation(10)` + `report.Valuation(ui.Env{Width,
Color: true, Clamp: true}, d)` split on newlines. Top fixed at 10.

### B3 — rich op outcome + deck add by URL

`opDoneMsg.summary` → `outcome opOutcome{summary, report []string,
confirm *pendingConfirm}`; `startOpReport` sibling of `startOp`; exported
`OpReport{Summary, Report []string, AlreadyImported string}` for main's
closures. `onOpDone` sequences refresh → open overlay if report → stage
confirm if set. Follow-up confirms are built browse-side in wrapper
closures where args are in scope — they ride the done message, never the
progress channel (progress events are droppable narration).

Deck add: `DeckAddFunc(ctx, p, url) (OpReport, error)` /
`WithDeckAddByURL`; palette entry "Add a deck by URL…" (holdings rank 1);
prompt validates http/https + host (decksource.Fetch's provider errors do
the rest); main's closure runs `decksource.Fetch` then `action.DeckAdd`.
No cursor move to the new deck in v1.

### B4 — import

`internal/action/ledger.go`: `RefuseReimport` returns a typed
`*AlreadyImportedError{When, Cards}` whose `Error()` reproduces the
current string byte-exactly (root import tests lock it). main maps via
`errors.As` → `OpReport.AlreadyImported` → browse stages "import it
again?" whose onYes re-runs `startImport(path, true)`. `ErrPartial` maps
to success-with-notes ("· N skipped", statusErr false — the work
committed). `ImportFunc(ctx, p, path string, again bool)` /
`WithImportFile`; path prompt validates exists+regular; `expandPath`
handles `~/`. Result overlay auto-opens, mirroring the CLI's blocks.
No dry-run-first flow in v1 (future refinement).

### B5 — export

`ExportFunc(binderRef, deckRef, format, path string) (string, error)` /
`WithExport` — fast local, runs in the prompt commit, no op. Chained
prompts: path (prefilled `~/hoard-<container-slug>.csv`) → format
(prefilled from extension; csv/json/moxfield/archidekt). Parent dir must
exist; existing file → overwrite confirm. Entries: "Export this
container…" (holdings, refs by ID) and "Export everything…". main reuses
the CLI writer map + `writeHoldingsJSON` (same package).

### B6 — bonus + docs

Optional `add.url` over `action.AddByURL` (single prompt, qty 1, nonfoil,
default binder). docs/parity.md full pass: flip the "next sprint" rows,
fix the stale backfill row (shipped in parity P4/P5), ensure-catalog row →
confirm modal, note the ErrPartial→status mapping. docs/sprints.md row,
docs/browsing.md, dogfood-notes item-8 disposition.

## Verification

Per phase: the four Go gates + six baselines byte-identical (only B4
touches CLI-reachable code and its message is byte-locked). A3: manual
smoke in a live terminal. B1: `-race`. After B4/B5: live import of a real
CSV, export of a binder, deck-URL fetch against the scratchpad DB copy.

## Cross-cutting

Every `ConfirmRequest` answered exactly once on every exit path
(y / other key / ctrl+c / deferred-then-staged). `onOpDone` is the single
consumer of outcomes (refresh → overlay → confirm, in that order).
Name/mode claims: Part A owns `modeAddChild` (above prompt) +
`WithAddCascade`; Part B owns `modeText` (below detail) +
`WithDeckAddByURL`/`WithImportFile`/`WithExport`/`WithReport`/
`WithConfirm`.
