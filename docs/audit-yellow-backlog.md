# Yellow backlog — 2026-08-07 pre-launch audit

Risk items from the full-code audit (Go CLI/TUI + both Swift apps). None are launch
blockers on their own; each is a real defect or a latent one kept safe by luck.
Red items live in the launch-fix plan; Green (feature gaps) seed the roadmap.
Line numbers are as of commit 4d6212f and will drift — verify before editing.

Format per item: **where** → what's wrong → how it fails → fix direction.

## Status — worked 2026-08-07, same day

**Fixed and green** (full Go suite ×3 platforms, vet, gofmt; 174 Swift tests; both
apps build): A1–A11 and the scan-CI item; B1–B6, B8–B11 and the artindex guards;
C3–C7, C9, C10. Schema moved v25 → v27 (v26 finish_guesses FK, v27 trait-filter
index pinned via INDEXED BY). A12 turned out already fixed at HEAD (the SELECT
already runs inside the merge's immediate transaction).

**Deliberately deferred**, with reasons:
- **A14 (go.mod generator deps)** — won't fix: generator mains don't link into
  the installed CLI; a tools submodule buys nothing but ceremony.
- **C1 (Vision + rasterisation on the capture queue)** and **C2 (configure()
  on main)** — hot-path restructures; the loop currently runs 491ms median
  against a 700ms budget, so these need live measurement before and after, not
  a blind rewrite.
- **C8 (English-only OCR, hand-fitted sparkle/leftU/trigger geometry)** —
  recognition-quality work; belongs to the approved ML sprint and the tuning
  ledger's live-session rule.
- **B7's full production enablement of art-match** — the channel is
  structurally tied to HOARD_SCAN_DEBUG_DIR (stills only land there); enabling
  it for real means requesting stills without a debug dir, which is ML-sprint
  scope. The safe halves (probe semaphore, decisive-verdict floor, phash
  guard) are done.
- **B10's View() mutation through the detail pointer** — structural; safe
  today, wants the render path made read-only as its own change.
- **C10's ScanKitTests target** — the false comments were corrected; creating
  the target and covering RemoteController is real new work, post-launch.
- **PeerBrowser's run-loop busy-pump** — one-shot CLI path, cosmetic.

Scanner-behavior items (B2, B6, and the Red-phase resolver changes) still owe a
live pile session before they're trusted — the tuning ledger's standing rule.

---

## A. Go — core / data layer

### A1. Backup pruning sorts versions lexicographically and deletes the wrong file
`internal/store/migrate.go:885-892`. The comment claims the names "sort in the order
they were written", but `dest` is built at `migrate.go:836` as `%s.bak-v%d-%s` with
**no zero-padding on the version**. Lexically `bak-v10 < bak-v23 < bak-v9`.
With backups v9, v10, v23 on disk the prune deletes **v10** (newer) and keeps v9.
The safety net for "the migration two versions ago broke something" silently keeps
the wrong files. Fix: parse the version number out and sort numerically, or embed a
zero-padded version in the name.

### A2. `finish_guesses` can never be cleared from browse; guesses stored under container 0
Two halves:
- `add.go:207` prints "fix a wrong one in browse (enter → finish), which clears it
  here" — but `grep FinishGuess internal/browse` returns nothing. Browse's finish
  editor goes through `store.MoveEntryFinish` (`internal/store/edit.go:130`), which
  never touches `finish_guesses`. Only caller of `ClearFinishGuess` is
  `add.go:179,184` (same-session scan correction). `hoard guessed` is therefore a
  monotonically growing queue with no exit, and the on-screen instruction is false.
- `add.go:198` → `internal/store/guesses.go:22` stores `res.ContainerID` verbatim;
  `0` is the "default binder" sentinel, not a container id. Schema
  (`internal/store/migrate.go:523-529`) has `container_id INTEGER NOT NULL` with
  **no FK and no cascade**, so nothing rejects it and deleting a container orphans
  its guess rows forever.
Fix: wire `ClearFinishGuess` into browse's finish edit; resolve the sentinel to the
real default-binder id before recording; add the FK (needs a migration).

### A3. tcgcsv archive extractions written non-atomically, then cached as authoritative
`internal/tcgcsv/tcgcsv.go:302,305` — two bare `os.WriteFile` calls, no
temp-and-rename (contrast `internal/mtgjson/mtgjson.go:254-278` and
`internal/pricing/quotescache.go:90-99`, which do it right). `ArchivePrices` treats
file presence as "already extracted" (`tcgcsv.go:265-273`) and swallows parse
failures (`if prices, err := foilMarket(b); err == nil`). Ctrl-C or disk-full
mid-write leaves truncated JSON; every future backfill silently skips that
group-day and never re-downloads — a permanent invisible hole in treated-foil
history (ripple/surge foils, the most expensive cards) for up to 100 days.
Fix: temp+rename like the two good citations; treat unparseable cache files as
absent (delete and re-fetch).

### A4. MTGJSON day-cache prune races concurrent downloads
`internal/mtgjson/mtgjson.go:322-334` — prune removes every file not prefixed with
today's date, and runs from `fetch` (line 252) *before* `os.CreateTemp(o.CacheDir,
"dl-*")` at line 254. `dl-*`/`quotes-*` temps don't carry the date prefix. Two
overlapping fetches (e.g. `Prices` + `Quotes` via `treatedExtra` + `remap`): the
second prune unlinks the first's in-flight temp; its `os.Rename` at line 279 fails
ENOENT, aborting a 150 MB download with a confusing error.
Fix: exclude `dl-*`/`quotes-*` from the prune, or date-prefix the temp names.

### A5. Troll-listing guard missing from `market.Assess`
`internal/mtgjson/mtgjson.go:1046-1065` (`bestUSD`) and
`internal/market/comps.go:275-306` (`dropTrollListings`) both clamp at
`listingOutlierRatio = 20`. `internal/market/market.go:167-192` (`Assess`) does
not — it reads raw quotes from `TodayQuotesWith` (no filtering,
`mtgjson.go:520-560`). `Profit() = SellAt - Market` (`market.go:66`) with one
polluted vendor figure fabricates an ARBITRAGE row telling the user to sell at a
price nobody pays; the comps table in the same command filters correctly, so the
two tables disagree. Fix: apply the same clamp in `Assess` (share the helper).

### A6. Unguarded `binders[0]` on the add/import write paths
`internal/action/add.go:33`, `internal/action/import.go:149,170` — return
`binders[0]` with no length check. Holds today because `ListBinders` calls
`collectionID()` first (a row always exists), but the invariant is enforced three
call-frames away. Any future filter on `ListBinders` (hidden binders, corrupt row,
partial migration) turns `hoard add`/`import` into an index-out-of-range panic.
Fix: guard and return a real error.

### A7. Migrations rely on driver-dependent multi-statement `Exec`
`internal/store/migrate.go:808` — `tx.Exec(m.Stmts)` assumes `modernc.org/sqlite`
executes every statement in the string. v23 (`migrate.go:483-504`) renames
`card_entries` aside, recreates, copies, drops — all in that one call. A driver
change that stops at the first statement would rename the holdings table away,
create an empty one, and stamp `user_version = 23`: total silent collection loss.
Fix: explicit statement splitter, or at minimum a post-migration row-count
assertion on destructive migrations.

### A8. PPMd decoder takes attacker-influenced sizes from archive headers unchecked
`internal/tcgcsv/ppmd.go:26` — `ppmd.NewH7zReader(readers[0], int(order),
int(memory), int(uncompressedSize))` with `memory`/`uncompressedSize` straight from
the archive header, no bounds check; the registration deliberately relaxes
upstream's property validation. Deps are `stangelandcl/ppmd v0.1.1` and
`bodgit/sevenzip v1.6.5` (`go.mod:10,13`) — v0, single-maintainer — parsing
untrusted 4 MB archives from a volunteer mirror, on 3 concurrent goroutines
(`internal/pricing/tcgcsv.go:~190`). A hostile/corrupt archive can request an
arbitrary allocation. Fix: sanity-clamp `order`/`memory`/`uncompressedSize` before
constructing the reader.

### A9. Hot-path table scans
- Browse filter: `internal/browse/model.go:761` → `internal/store/filter.go:105-107`
  runs `SELECT ... WHERE lower(type_line) LIKE '%x%'` over **virtual generated
  columns** (`migrate.go:217-245`) — every row re-parses ~5.4 KB of `raw_json`,
  no usable index, re-run on every filter keystroke (`model.go:1344,1353,1357`).
  ~5,000 printings → visible stutter. Fix: STORED columns + indexes, or cache the
  parsed fields.
- `internal/pricing/pricing.go:206-220` (`resolve`) runs four whole-table scans
  (`KnownMTGJSONUUIDs`, `KnownCardKingdomLinks`, `TCGAltProducts`,
  `KnownVendorProductIDs`) and is called twice per `Prices`/`Quotes`/`History`
  (`tcgcsv.go:51` and `pricing.go:104`) — eight scans per price op. Fix: memoize
  per-call or thread one resolve through.

### A10. Deck/CSV parsing tolerance is inconsistent and sniffing is weak
- `internal/decksource/textlist.go:~73` — `ParseText` aborts the whole file on one
  unparseable line; `ParseLoose` (same file, ~128) collects `skipped` and
  continues. `lineRE` (line 16) handles no `SB:` prefix, no `.dek`, no
  tab-separated exports, set codes `[A-Za-z0-9]+` only. One odd comment line and a
  99-card `hoard deck add --file` refuses entirely. Fix: align on the tolerant
  policy with a skipped-lines report.
- `internal/collsource/csv.go:57-60` — Delver Lens sniffed on the single generic
  column `"Card number"`; any other tool's CSV with that column silently maps
  wrong quantity/finish columns. Fix: require 2-3 distinctive columns.
- `internal/collsource/csv.go:~232` — `parseQty` trims a leading *and* trailing
  `x` in one pass; `"x2x"` parses as 2 instead of erroring. Cosmetic.

### A11. Concurrent catalog updates clobber each other
`internal/catalog/build.go:236-241` — fixed temp name `fileName+".building"`, no
lock, unconditional `os.Remove` of whatever is there. Browse's catalog-update op +
a second terminal's `hoard catalog update`: one deletes the other's 77 MB
in-progress build → corrupt-database error after minutes of download. Same shape in
`Open` (`catalog.go:160-163`), which removes the live catalog on schema mismatch.
Fix: `os.CreateTemp` unique names + a lock file.

### A12. `store.RepairFinishes` cross-process hazard is documented and unmitigated
`internal/store/finishes.go:70-135` — its own comment admits another hoard process
between the SELECT and the merge "would silently lose or invent copies".
`_txlock=immediate` protects within one transaction but the acknowledged hazard is
left open. Fix: take the write transaction before the SELECT.

### A13. User-Agent carries no contact URL
`internal/buildinfo/buildinfo.go:8` — `"hoard/0.1"`. docs/data-licensing.md §8 P1.7
recommends `hoard/0.1 (+https://github.com/spiffcs/hoard)`. Given the rate-limit
history, Scryfall's first contact will otherwise be a block, not an email.
Cheapest insurance on this list. (Version part is fixed by the Red-plan `hoard
version` work; add the URL there.)

### A14. Generator-only deps in the main module; scanner has no CI
- `go.mod:11-12` — `invopop/jsonschema` + `santhosh-tekuri/jsonschema/v6` are
  direct requires used only by generators/tests; pulled into every `go install`.
  Fix: tools submodule or build tag.
- `.github/workflows/scan.yml` is manual-only and its own header says the cost:
  "nothing checks the goldens automatically any more." The feature with the most
  surface area and least test automation has no CI gate. Fix: run the golden check
  on PRs touching `scan/` at minimum.

### A15. `report.go` ignores the output file's Close error
`report.go:39,43` — `defer f.Close()` discards the error; disk-full on final flush
→ truncated valuation file, exit 0. `export.go:70-73` does it right; copy that.
(The `os.Create`-overwrites-anything half of this is in the Red plan.)

---

## B. Go — TUI / browse

### B1. `artMatchMsg` double-decrements the in-flight resolve counter
`internal/tui/model.go:606-613` routes `artMatchMsg` into `onResolveDone`, whose
first act is `if m.resolving > 0 { m.resolving-- }` (`:1517`) — but the art channel
fires from the queue path (`:2052`) *after* the original resolve already
decremented. `m.resolving` under-counts: `afterCard` (`:2443`) ends the walk early,
the real resolve lands with `walking == false` and sits in `m.review` unwalked, and
the close-prompt "N unsaved scans will be dropped" warnings lie.
Fix: don't route art results through the resolve-counter path.

### B2. `upgradeQueued` transplants a foil hint across physical copies
`internal/tui/model.go:2303-2316` — matched on canonical *name* only; when a
higher-rank read replaces a queued entry it inherits the old `finishHint`. Two
different copies (one foil, one not) queued in the window → confirm writes foil for
a copy nothing read a marker on. The comment justifies same-card re-reads; the code
never checks it is one. Fix: carry capture identity (seq) and only inherit within
the same physical sighting.

### B3. `ctrl+d`/`ctrl+s` with the add-palette drawer open → unanswerable quit gate
`internal/tui/model.go:675-683` — these branches run *before* the
`m.addPalette != nil` intercept. `finishAdding` sets `stateLeaveConfirm` but leaves
the palette non-nil; next key goes to the invisible drawer, so `y` types into the
query while the red "quit add session? y/n" renders on top. Recoverable only via
esc or ctrl+c. Fix: close/route around the palette before switching state.

### B4. Op confirms can be invisible (text takeover) and can block the worker
- `internal/browse/textview.go:67-90` renders no status-line slot, but `mode()`
  ranks `modeConfirm` above `modeText` (`mode.go:47`). Start `UpdatePrices`, open
  `ValuationReport` (no running-op guard, `command.go:279`): the catalog confirm
  sets `m.confirm`, every key is eaten answering a question that isn't on screen,
  and the op goroutine blocks on `reply <- bool` (`opconfirm.go:99-105`). Same
  hole for `modePrompt` over text.
- `stageConfirmRequest`'s reply channel capacity ≥ 1 is a doc comment
  (`opconfirm.go:31`), not enforced; and quitting via ctrl+c at
  `model.go:1043-1048` with `m.deferredAsk != nil` leaves the worker hung past
  program exit.
Fix: render the status line in text takeover; enforce buffered reply channels;
drain/answer deferred asks on quit.

### B5. Every `reloadDetail()` return value is discarded — COMPS can pend forever
`internal/browse/edit.go:248,323,431,512,640,677` drop the returned `tea.Cmd`.
When the detail moves to a printing the memo has no answer for, `loadPrinting` sets
`compsPending = true` (`detail.go:291`) and only the dropped command would clear
it. Worst: `repointHeldSet` (`edit.go:512-530`) discards the comps command and then
loads a third printing — the section shows "reading today's vendor quotes…"
forever. Fix: return/batch the commands.

### B6. `onPrints` narrowing commits a misread collector number without asking
`internal/tui/model.go:1114-1131` — number-matching rows narrow the picker; when
one row survives, `m.chosen` is set and `advanceAfterPrint()` commits with no
picker shown. The in-code comment accepts it: "a misread digit that happens to name
a different real printing of the same card now commits silently." The `ctrl+a`
hatch only exists when the picker is shown. `numberTailMatches`
(`autoscan.go:1267`) repairs only 1 of 4 observed misread classes
(`autoscan.go:1247-1250`). Fix: require a second corroborating signal (set code or
year) before skipping the picker on a number-narrowed single row.

### B7. Art-identification channel is dead in production, unbounded when alive
`internal/tui/artmatch.go:70` requires `HOARD_SCAN_DEBUG_DIR`; `probePath()`
(`:99-110`) requires `bin/cardkit-probe` relative to the **cwd**. Real installs get
a nil matcher — the shipped feature never fires. When it does run (dev), `:151`
spawns one probe process per queued card with no semaphore. Also
`internal/artindex/index.go:97`: with one entry in the index the runner-up distance
stays at the seeded 65, so `artDecisive` is trivially true — any image within 10
bits "decisively" matches. And `internal/artindex/phash.go:109` divides by
`b.Dy()` with no guard — a degenerate (<2 px) crop panics.
Fix: install-path resolution + env-independent enable; probe semaphore; require
`Count() >= 2` for decisive; guard empty crops.

### B8. Scan-session telemetry log written from two goroutines, closed under one
`internal/scan/session_darwin.go:44-60,126-130,227` — `s.log` is a bare `*os.File`
with no mutex: `logWriter.Write` runs on the helper's stderr pipe goroutine,
`Session.Note` on the Bubble Tea goroutine (`model.go:1504-1508`), and `pump`'s
defer closes the file while either may be mid-write. Race-detector territory;
worst case writes to a closed fd (swallowed). Fix: mutex or a single writer
goroutine fed by a channel.

### B9. Nudge timers are uncancellable `time.Sleep` goroutines
`internal/tui/autoscan.go:2121-2124`, `model.go:2042-2045` — every processed
capture arms a 5.5-44 s sleeping goroutine; a 200-card session leaves hundreds
alive, unabortable on close. Fix: `time.After` in a select with a done channel, or
timers tracked and stopped.

### B10. Latent panics / invariants held by call-order luck
- `internal/browse/market.go:594-600` + `comps.go:70-84` — `[3]int` /
  `[3]marketSection` indexed by `market.Kind`; `KindLowball = 3` is out of range
  and `KindBelowMarket = 2` collides with `compsSection = 2`. Safe only via the
  filter at `market.go:560` and re-tag at `:549`; `market_test.go:215` asserts the
  fragility. Any stray Kind ≥ 3 reaching render = index-out-of-range.
- `internal/browse/comps.go:155-156` — buy sort has 8 columns, sell has 7;
  `compsSortIdx` is safe only because the side-flip resets it to 0
  (`command.go:531`). Nothing enforces the pairing.
- `internal/browse/market.go:622` — `deriveMarketPages` slices
  `marketAllRows[start:start+totals[k]]` assuming contiguous Kind grouping; held
  by `sortArbRows` alone, but also called from `sortCompRows` (`comps.go:196`).
  A caller-order change shows the wrong section's rows.
- `internal/browse/view.go:171-188` — `View()` (value receiver) mutates
  `m.detail.scroll*` through the shared pointer; render path is not read-only.
- `internal/tui/model.go:~845` — `stateAbandonConfirm` dereferences `*m.current`
  with no nil guard; safe today across five handlers by inspection, not
  construction.
- `internal/tui/model.go:897-901` — spinner tick forwarded to the child without
  the `Done()` check that `forwardToChild` performs; latent stuck-child if `done`
  is ever set off a key path.
Fix: map-by-Kind instead of fixed arrays; assert/clamp indices; make View
read-only; add nil guards.

### B11. Hard-coded layout + render churn
- `internal/browse/image.go:34,39,136` — `artColsMax = 40`,
  `artMinTextCols = 96`, `kittyCellAspect = 2.8` ("owner's tuned value for
  Ghostty"); art layout is effectively wide-terminal-only, tunable only via
  `HOARD_CELL_ASPECT`. Degrades without crashing (verified to width/height 1).
- `internal/browse/view.go:301-308` — `helpRows()` rebuilds and wraps five views'
  help text on every frame, called from seven layout functions per render.
- `internal/tui/model.go:2360`, `tui.go:170` — `m.tally` and `summary.Entries`
  grow unbounded (only 10 render); fine at pile scale.
- `internal/browse/detail.go:518` — stat box pads with `len()` (bytes) while
  everything else measures `lipgloss.Width`; misaligns non-ASCII stats.

---

## C. Swift — scanner apps

### C1. Vision + full-res rasterisation inline on the capture queue
`scan/hoard-scan-ios/Sources/Capture/TriggerRunner.swift:255` — the served
`request(buffer)` closure (`CameraSession.swift:133-138`) builds a `CIImage`,
`.oriented(.right)`, and `createCGImage` at 4032×3024 inline on
`hoard-scan.trigger`; `TriggerRects.swift:36` runs `VNDetectRectanglesRequest`
synchronously; `sceneSignature` runs up to `1 + boxes.count` times per sample.
`alwaysDiscardsLateVideoFrames = true` converts backlog into silently unseen
samples. Fix: rasterise/OCR off the trigger queue; keep the queue to cheap
signature math.

### C2. Camera `configure()` blocks the main thread at launch
`CameraSession.swift:230-286` — `beginConfiguration`, input construction,
`lockForConfiguration` + `activeFormat`, `addOutput`, `commitConfiguration` all
`@MainActor`; only `startRunning` is off-thread (`:283`). Hundreds of ms of UI
hitch on first Scan-tab appearance. Fix: move configuration to a session queue.

### C3. Phone never notices a clean Mac disconnect; no scene-phase handling
- `LinkController.swift:205` handles only `.failed`; `RemoteController.shutdown()`
  cancels (`:255`) → `.cancelled` on the phone is unhandled: `connected` stays
  true (green header over a dead link), `session` isn't released, and
  `markVerified` (`:110 guard session == nil`) refuses the next Mac until a
  second connection happens to `adopt`. Stale `PeerSession` links leak.
- No `scenePhase` observer anywhere; `link.start()` runs once
  (`HoardScanApp.swift:66`). iOS tears down `NWListener` on suspend; nothing
  restarts it on foreground — `PairingView.swift:82-85` documents the symptom to
  the user instead. `isIdleTimerDisabled = true` (`SessionView.swift:148`) is
  never restored, so the screen never sleeps even off the Scan tab.
Fix: handle `.cancelled`; restart the listener on `.active`; scope the idle-timer
override to the live session.

### C4. Still frames sent with no backpressure; preview leg unobserved
- `LinkController.swift:356-359` — 4-8 MB stills queued via `send` (not
  `sendDroppable`) with no limit on a slow link; also hops a non-`Sendable`
  `PeerSession` off the MainActor (compiles only under Swift 5 language mode,
  `Package.swift:89`) and the completion mutates `PeerLink.state` from a foreign
  queue (`PeerLink.swift:208`).
- Neither `RemoteController.wire` (`:85-110`) nor `LinkController.adopt`
  (`:197-227`) sets `onState` on the preview connection — if the preview leg dies
  while control survives, stills vanish with no error.
- `PeerLink.swift:163-168` — `previewInFlight` is a cross-thread Bool; if it
  sticks true, `sendDroppable` returns false forever (currently masked by the
  dead mirror path, which is a Red item).
Fix: droppable/limited still queue; observe preview state; make the flag atomic.

### C5. Wire inputs unvalidated at the edges
- `ScanCommand.swift:70-71` — `tune` accepts any positive ints;
  `tune 2000000000 1e9` sets the trigger interval to ~30 years and the throttle at
  `TriggerRunner.swift:245` sits in front of the manual-capture service, so
  *manual* capture stalls too. Also reachable by accident:
  `HOARD_SCAN_AUTO_INTERVAL` forwarded unvalidated
  (`RemoteController.swift:198-203`); the `HOARD_SCAN_AUTO_STABLE`-only default
  (0.1 s) is 3× the phone's tuned 0.033. Fix: clamp to sane ranges at decode.
- `RemoteController.swift:156-157` — NDJSON payloads written to stdout verbatim;
  a payload containing `\n` splits into multiple lines for the Go line parser
  (FrameCodecTests explicitly pins that payloads may contain newlines). Fix:
  escape or length-prefix on the stdout bridge.

### C6. Audio dies silently after any interruption
`Sounds.swift:134-140` — session activated and engine started once in `init`,
`working = true` forever; no `AVAudioSession.interruptionNotification` or
`routeChangeNotification` observer. After a call or Bluetooth change the engine is
stopped but `isWorking` still reports healthy, so the "sound is broken" warning
(`SessionView.swift:209`) — built for exactly this — never shows. Minor cousin:
`SettingsView.swift:118` auditions on every `onChange(of: voice)`.
Fix: observe interruptions/route changes, restart the engine, reflect real state.

### C7. Camera control errors silently swallowed; torch state lies
`CameraSession.swift:196,206` — empty `catch {}` on EV bias.
`:401-508` — every control is `guard (try? lockForConfiguration()) != nil else
{ return }`; a busy device means focus/exposure/torch silently no-op.
`:513-515` — `setTorch` publishes `torchLevel = level` even when
`setTorchModeOn` threw. Also `LinkController.swift:259-261` drops unparseable
commands with no error event while `RemoteController.swift:239-241` emits one for
the same condition — the two ends disagree. Fix: log + surface failures; publish
torch state only on success; align unknown-verb policy.

### C8. Magic-number geometry / OCR limits
- `Read.swift:486` — `recognitionLanguages = ["en-US"]` hard-coded while
  `Collector.swift:376` maintains 13 `knownLanguages` and `Wire.swift:63` ships a
  `language` field. Non-English cards get English-only OCR.
- `CardLayout.swift:153-154` — sparkle anchors fitted on exactly two frame eras;
  `:149-152` admits nothing between them was ever measured. `:75-129` — `leftU`
  hand-fitted with a documented wrong landmark for 2003 old-frame printings
  (`Sparkle.swift:139-143`), unfixed, worked around by non-use.
- `Read.swift:379-394` — footer band/head/symbol rects assume a standard frame;
  no split/flip/planeswalker handling.
- `Trigger.swift:97-131` — `cardChanged = 12.0`, `movedFaceMax = 20.0`
  ("interpolated rather than measured"), `sceneDetail = 12.0` fitted on 1080p
  buffers, now fed 4032×3024 (header at `TriggerRunner.swift:6-11`).
Fix direction: pass the collector's language hint into Vision; re-fit thresholds
at the real buffer size; the era table waits on the symbol-reader sprint.

### C9. Session log is exposed via the Files app
`Info.plist` sets `UIFileSharingEnabled` + `LSSupportsOpeningDocumentsInPlace`;
`SessionLog` records every card name and price read. Anyone with the unlocked
phone can browse the collection log. Also `SessionLog.write`
(`SessionLog.swift:33-42`) does synchronous open/seek/write/close per line from
any thread (MainActor on the capture hot path via `LinkController.trace`), with
no lock — interleaved writes possible. Fix: turn off file sharing (or move the
log out of Documents); single serial writer.

### C10. Misc correctness debt
- `PeerBrowser.browse` (`PeerEnds.swift:239,247-251`) — writes `found[name]` on
  its queue, reads `found.values` on the caller thread right after an async
  `cancel()`, and busy-pumps the run loop for the full duration.
- `FrameReader` (`FrameCodec.swift:120`) — O(n) front-removal compaction per
  frame on the leg that carries 8 MB stills.
- `Sparkle.swift:425` (`sparkleTemplateDecimated`) — indexes
  `source[j * cols + i]` with no count validation; both shipped templates are
  exactly 1768, but the template is generated source
  (`SparkleTemplateData.swift:3-6`) — a wrong-length regen is a runtime
  index-out-of-range in the read path, not a build error. Fix: precondition on
  `template.count == rows*cols`.
- `TierSettings.swift:53-66` / `Sounds.swift:159` — a stale voice id in
  UserDefaults silently mutes that tier.
- `FooterPatterns.swift:13,16,19` — the codebase's only `try!`s (literal
  regexes); safe, but worth converting for the lint story.
- No `ScanKitTests` target despite two source comments claiming one
  (`main.swift:5`, `CLI.swift:47`); `RemoteController`, `PriceHUD`,
  `PhotoDecode`, `PreviewHost`, `CameraSession`, `TriggerRunner`,
  `LinkController`, `Sounds`, `PairingStore`, `PixelReader` bounds all have zero
  automated coverage — which is how the dead mirror path (Red) survived.
