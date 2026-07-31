# Sprint: Binders, Interop, and the Road to Best-in-OSS

Status document for the two-week sprint started 2026-07-30. Written so a fresh
session (human or AI) can resume with zero prior context. Update the status
markers as phases land.

## Why this sprint

The mainstream paper-collection tools all have a catch: ManaBox paywalls
organization (extra binders/lists behind a subscription), Dragon Shield has no
desktop story, Delver Lens is scanner-only, and the trackers inside
Moxfield/Archidekt are second-class citizens of deckbuilders. hoard's
positioning: **a local-first, offline, no-subscription collection manager that
interops with deckbuilders instead of replacing them.**

Non-goals: deckbuilding features (Moxfield does that best — we interop),
Moxfield API sync (their API is Cloudflare-blocked; interop is file-based),
condition/language schema columns (this sprint imports drop them with a report;
adding the columns is backlog item 10).

## Sprint scope and order: C → A → B → D → E-spike

Binders (C) first because import (B) needs a target binder and the picker (D)
needs something to pick. Export (A) before import (B) because export defines
hoard's canonical CSV — which is also an import format and the round-trip test
fixture.

---

## ✅ C. Multiple named binders — DONE (committed)

**Design: no schema migration.** The `containers` table already generalized:
new binders are ordinary `kind='collection'` rows with `source='manual',
source_id='binder:<slug>'` (satisfying `UNIQUE(source, source_id)`). The
original singleton (`source_id='__collection__'`) is now simply "the default
binder", displayed as `store.LooseName` ("Binder"). Existing databases are
byte-untouched — no v7, no backup churn. `schemaVersion` is still 6.

As built:

- `internal/store/binders.go` — `ListBinders()` (default first, then by name,
  each with rolled-up counts/value as `DeckSummary`), `CreateBinder`,
  `RenameBinder`, `DeleteBinder`, `BinderByRef`, `IsDefaultBinder`,
  `binderSourceID` (slug). Default binder cannot be renamed or removed; a
  non-empty binder cannot be deleted (its entries are loose cards that exist
  nowhere else — unlike a deck, no cascade).
- `internal/store/decks.go` — `containerByRef(kind, noun, ref)` is the shared
  id / exact-name / unique-fragment resolution (DeckByRef delegates); the
  shared `containerSelect` resolves display names, so the default binder
  answers to "Binder" in refs.
- `internal/store/store.go` — `containerLabel` SQL fragment now keys on
  `source_id = '__collection__'` (not kind), so user binders keep their names.
- Scoped variants (originals delegate to the default binder — zero regression):
  `AddCardFinishTo(containerID, ...)` (holdings.go), `BinderByFinish(id)`
  (holdings.go), `SetHoldingQuantityIn(id, ...)` and `RemoveFromBinder(id, ...)`
  (edit.go). `CollectionTotals()` now sums across **all** `kind='collection'`
  rows — the summary's BINDER line means "everything not in a deck".
- `internal/browse/` — the left pane lists real binder rows from `ListBinders`
  (the synthesized fake `ID: 0` row is gone); every binder is selectable and
  editable; edits/undo are scoped to the selected binder's container id. The
  browse `Store` interface swapped `CollectionTotals`/`ListCollectionByFinish`
  for `ListBinders`/`BinderByFinish`; `Editor` uses the `*In`/`*Binder`
  variants.
- `binder.go` + main.go dispatch — `hoard binder list|new|rename|rm`.
- Tests: `internal/store/binders_test.go` (CRUD, ref resolution, collision
  refusals, scoping), `TestMultipleBindersEachGetARow` in browse.

---

## ✅ A. Export — DONE (committed)

`hoard export [--format csv|moxfield|archidekt] [--binder REF|--deck REF|--all] [-o file]`,
stdout by default (scriptable). Bare `hoard export` means `--all` — every
binder, then every deck.

As built (matches the design above with these notes):

- `internal/export/csv.go` — `Row` (normalized holding) plus `WriteCanonical`,
  `WriteMoxfield`, `WriteArchidekt` on `encoding/csv`. Canonical header is
  exposed as `export.CanonicalHeader()` for phase B's sniffer.
- Output is **deterministically sorted** (container, name, set, number,
  finish) so exports are diffable in git (backlog item 6 half-done for free).
- Moxfield/Archidekt have no container column, so identical
  `(Scryfall ID, finish)` rows are **aggregated across containers**; canonical
  keeps per-container rows. Archidekt Finish ∈ {Normal, Foil, Etched}.
- Unpriced cards emit an **empty** Price USD cell, never 0.00.
- Data comes from `BinderByFinish`/`DeckEntries`/`ListBinders`/`ListDecks` as
  planned — no new queries. Command lives in `export.go` (repo root).
- Tests are inline-want writer tests plus command tests against a temp store
  (`internal/export/csv_test.go`, `export_test.go`) — house style, no
  golden-file machinery. **Checkpoint passed: a generated file uploaded to
  Moxfield's collection import** (2026-07-30; note it is the *collection*
  importer, reached from a binder on moxfield.com/collection — deck pages only
  take pasted text lists).

## ✅ B. Import — DONE (committed)

`hoard import file.csv [--format auto|manabox|moxfield|delver|hoard]
[--binder REF | --preserve-binders] [--dry-run]`.

As built (matches the design above with these notes):

- `internal/collsource/` — `Row`/`Collection` types plus the header-sniffing
  dispatcher in csv.go, driven by a per-format `spec` table (columns looked up
  by **name**, never position, so reordered/extended exports still parse).
  Unknown headers fail loudly, naming the columns seen and suggesting
  `--format`. Dragon Shield deferred as planned.
- Resolution reuses the deck pipeline: `fetchCollection` (a package-main seam
  over `scryfall.FetchCollection`, swappable in tests) → `resolveIDs` →
  `store.CorrectFinish`. The **name fallback is a real second pass**: rows
  whose set+number landed in notFound retry as name identifiers before being
  reported unresolved (`cmdDeckAdd` still has no such pass).
- `Row.Name` is kept alongside `Row.Ident` to power that fallback; hoard's own
  Container column populates `Row.Binder` just like ManaBox's "Binder Name",
  so `--preserve-binders` also reconstructs a canonical export's organization
  (deck rows in such a file become binders of the same name — import never
  creates decks).
- Dropped-field counting is *informative-only*: a condition counts only when
  it isn't near-mint, a language only when it isn't English, a purchase price
  only when nonzero — so the report says what was actually lost.
- `--preserve-binders` shipped (not cut); creates missing binders once,
  case-insensitively, and routes empty binder names to the default binder.
- Fixtures are synthesized (docs/known formats), one per app in
  `internal/collsource/testdata/` — swap in real exports when available;
  Delver Lens headers especially vary by version.
- **Round-trip checkpoint passes** as `TestExportImportRoundTrip`
  (import_test.go): export → import into a fresh DB with `--preserve-binders`
  → identical `CollectionTotals` and binder structure. Also verified manually
  against live Scryfall with real collection rows.

## ✅ D. Add-flow destination picker — DONE

As built (matches the design with the deferred decision resolved):

- The injection is a **new `dests []tui.Destination` parameter on `tui.Run`**
  — `Destination{ID, Name, Kind}` is tui's own type, so the package still
  imports no store. add.go builds the list (binders default-first, then decks
  by value — the browse left pane's order) and the adder calls
  `AddCardFinishTo(r.ContainerID, ...)`; `ContainerID` is 0 only when no
  destinations were supplied, falling back to `AddCardFinish`.
- `stateDestPick` slots after the finish step via `toDest()`, built on the
  shared `showPicker`/`pickerKey` helpers. With ≤1 destination it never
  appears — the single-binder cascade, confirm screen, and banner are
  character-identical (existing tests pass with nil destinations, unchanged).
- Deck rows are labelled "deck · adds to the mainboard"; the pick lands on
  board 'main' via `AddCardFinishTo`.
- The session remembers the last pick (`m.dest` survives `resetForNext`), so
  the picker opens preselected and a bulk add is one enter per card; the
  confirm screen and success banner name the destination when there was a
  choice ("→ Trade").
- Tests: `TestDestinationPickerAsksHandsOffAndRemembers`,
  `TestSingleDestinationSkipsThePicker` (model_test.go).
- The `hoard add --binder REF` cut-line fallback was not needed and was not
  built; URL adds still go to the default binder (a `--binder` flag for the
  URL path is a small backlog item if wanted).

## ✅ E. Multi-card scan spike — DONE, and productionized the same day

**Verdict was GO, as a hybrid with whole-frame title lines as the primary
channel** — across five captured fixtures, rect crops only saw cards with
visible outlines (2 of 9 in a booster-sized cascade) while the whole-frame
OCR pass read every title. The spike was productionized immediately: the
helper emits `cards: []` per capture (title-line clustering via `titleLike`
text shape, crops refining by tolerant title match), and the TUI walks a
batch-confirm queue (`card k of N`, ctrl+s skips, esc abandons). The
user-facing story is in docs/scanning.md ("Scanning several cards at once");
`$HOARD_SCAN_MULTI` traces clustering decisions to stderr, iterable offline
with `--image` against captures under HOARD_SCAN_DEBUG_DIR. Final sweep hit
100% recall on all five fixtures. Original spike design follows for
reference:

### (original design)

Swift-only, in scan/hoard-scan/main.swift (~850 lines, built by build-scan.sh
via `make scan`; NOT part of `go build`):

- Today: `VNDetectRectanglesRequest` with `maximumObservations = 10` already
  finds multiple card rects but keeps **only the tallest** to anchor the
  collector-number `regionOfInterest`; one `scan` NDJSON event per capture
  carrying `name`, `candidates` (≤8 OCR lines), `collectorNumber`, `setCode`,
  `bottomLines`, `rotation`.
- Spike: loop **all** detected rects, per-rect perspective-crop + the existing
  two-pass OCR (title pass with language correction, bottom-band pass without),
  log per-rect results to **stderr only**. No protocol or Go changes.
- Output: a go/no-go memo for next sprint. Productionizing means emitting
  N cards per capture (protocol change in internal/scan/scan.go) plus a TUI
  batch-confirm queue — next sprint if the spike says go.

---

## Verification discipline (applies to every phase)

```sh
go build ./... && go vet ./... && gofmt -l . && go test ./...
```

Plus **byte-identical baseline diffs** for anything that shouldn't change
behavior:

```sh
# before starting (or from the commit before the change):
go build -o /tmp/hoard-pre .
cp <a real hoard.db copy> /tmp/base.db
for c in "" unpriced movers deck "catalog status"; do
  cp /tmp/base.db /tmp/a.db
  /tmp/hoard-pre --db /tmp/a.db $c > "/tmp/pre-${c:-bare}.txt" 2>&1
done
/tmp/hoard-pre --help > /tmp/pre-help.txt 2>&1
# after the change: rebuild as hoard-post, regenerate, diff each pair.
```

Bare `hoard` piped prints the summary table (non-TTY branch), so this works
headless. `arbitrage` output is only comparable within the same day (cached
vendor bundles) and can legitimately differ on price ties across bundle
versions.

## Risks and cut lines

- Import format drift (especially Delver Lens): sniff headers, fail loudly,
  `--format` override.
- Scryfall rate limits on big imports: `FetchCollection` already batches and
  retries 429s; reuse it, don't hand-roll.
- **Cut order (first → last)**: E spike → Delver Lens parser → Archidekt export
  writer → D picker (ship `hoard add --binder` instead) → `--preserve-binders`.
  **Never cut**: canonical CSV export, ManaBox/Moxfield import, binders — they
  are the positioning.

## Hardening interlude (2026-07-30, between B and D)

An architecture audit (Release It! / SQLite-as-application-format / DDIA /
Unix philosophy / Ousterhout) landed four commits before the picker work:

1. **Network edge**: an MTGJSON outage no longer stamps price gaps "checked"
   (it used to silence re-asks for a week); non-gzip 200s are never cached and
   poisoned day-cache entries self-heal; Scryfall's bounded retry covers 5xx;
   the bulk-data listing has a deadline; movers/arbitrage sort deterministically.
2. **Store**: forward-compat guard (+`application_id` "HORD"), the migration
   backup fires before the legacy transform, imports commit as one transaction
   (`Store.ApplyImport`), `RepairFinishes` reads inside its tx, DSN uses
   `_txlock=immediate`. Price history is documented as irreplaceable (90-day
   source window) and its `ON DELETE CASCADE` flagged as a loaded gun.
3. **Import ledger + round trip**: schema v7 `import_ledger` refuses re-runs
   by content hash (`--again` overrides); canonical CSV gained a
   `Container Kind` column and import skips deck rows, so an --all export
   restores instead of pouring decks into binders.
4. **Modules + CLI discipline**: `internal/resolve` owns the shared card
   pipeline (deck add gained the name-retry pass and offline testability);
   catalog progress and its y/N prompt moved to stderr; report tables are
   single-sourced (`report.Binders`/`FinishRepairs`, advice split from
   `Unpriced`); exit code 2 = completed-with-skips; `scryfall.Finishes` is the
   one nonfoil→normal translation table. decksource.Entry carries Name.

Deliberately not done (audit judged working-as-intended): circuit breakers,
WAL, splitting browse.Store, moving layeredSearcher, Archidekt retry.

## Post-sprint backlog

Leading themes (maintainer decision): **portfolio + scriptability** — they
compose (alerts need JSON output; the valuation report is a deterministic
export with prices).

**Portfolio — "your collection as a portfolio" (competitors paywall this):**
1. Collection value over time in the TUI: an owned-value snapshot per price
   refresh (price history + `ui.Spark` already exist), drawn in the summary
   header.
2. `hoard watch`: threshold alerts on owned/wanted cards ("ragavan < $40"),
   cron-friendly, silent exit 0 when nothing fired.
3. `hoard report`: dated valuation document (CSV + printable text) for
   insurance/sale — totals, top holdings, per-binder breakdown, price sources
   named.
4. Want lists with arbitrage-powered "cheapest vendor to buy from" (reuses
   internal/arbitrage + pricing.Quotes).

**Scriptability — "the only MTG tool that composes":**
5. `--json` on every read command (summary, unpriced, movers, arbitrage,
   export) with a documented stable schema. (Groundwork done: report tables
   are prose-free and single-sourced; export assembles data before rendering.)
6. Deterministic export ordering → version your collection in git; diffs show
   adds/removals. (Done for export, movers, and arbitrage in the hardening
   interlude.)
7. Document the SQLite schema as a public read API. (The file now carries
   application_id "HORD" and refuses newer-schema writes.)

**From the hardening audit:**
14. `hoard backup` / `hoard doctor`: on-demand `VACUUM INTO` + integrity_check,
    reusing the migration backup machinery — the irreplaceable price history
    currently has no export path.
15. Unify `refreshCards` (catalog-first resolution in catalog.go) with
    `internal/resolve` so update-prices/repair share the one pipeline too.

**Later / opportunistic:**
8. ~~Multi-card scanning productionization~~ — done post-sprint, same day:
   `cards: []` protocol, TUI batch queue with ctrl+s skip, title-primary
   clustering. See docs/scanning.md.
9. Bulk paste entry: pipe any text list into a binder (generalize
   internal/decksource/textlist.go — it already parses `4 Sol Ring (C21) 125 *F*`).
10. Condition/language columns (unlocks lossless ManaBox import and real
    Moxfield condition export).
11. Physical location tracking (box/page) and set-completion progress from the
    catalog.
12. Duplicates report ("9 Sol Rings across 7 containers" — `HoldingsOf`
    already answers this per card).
13. Dragon Shield import parser; goreleaser + Homebrew tap (the scanner stays
    an optional macOS component).

## Competitive context (why these features)

- **ManaBox**: the consensus mobile app; scanning + multi-vendor prices, but
  extra binders/lists are subscription-gated and there's no desktop/CLI. Our
  binders + import-from-ManaBox is the migration funnel.
- **Moxfield/Archidekt**: where decks live; their collection trackers are
  secondary. We export their import formats rather than compete.
- **Dragon Shield**: free scanning, nested folders, no desktop version.
- **Delver Lens**: the bulk-entry speed king (multi-card fanned scans) —
  the target of spike E.
