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

## ⬜ A. Export (days 5–6) — NEXT UP

`hoard export [--format csv|moxfield|archidekt] [--binder REF|--deck REF|--all] [-o file]`,
stdout by default (scriptable).

- **Canonical CSV** (also the re-import format):
  `Count,Name,Set,Collector Number,Finish,Scryfall ID,Container,Board,Price USD`.
  Scryfall ID makes re-import exact; Set+CN keeps it spreadsheet-friendly.
- **Moxfield writer** (their collection-import shape):
  `Count,Name,Edition,Condition,Language,Foil,Collector Number` — Condition
  hardcoded "Near Mint", Language "English" (hoard lacks both), Foil column ∈
  {"", foil, etched}.
- **Archidekt writer**:
  `Quantity,Name,Finish,Edition Code,Collector Number,Scryfall ID` — their
  importer tolerates missing optionals.
- New `internal/export/csv.go` on `encoding/csv`. Data comes from
  `store.BinderByFinish` / `store.DeckEntries` / `store.ListBinders` /
  `store.ListDecks` — no new queries expected.
- Golden-file tests against a fixture DB. **Checkpoint: a generated file
  actually uploads to Moxfield** (manual verification).

## ⬜ B. Import (days 7–9)

`hoard import file.csv [--format auto|manabox|moxfield|delver|hoard] [--binder REF] [--dry-run]`.

- New `internal/collsource/csv.go`: **header-sniffing dispatcher**. Formats:
  ManaBox (has "Binder Name", ..., "Scryfall ID" columns), Moxfield
  ("Count,Tradelist Count,...,Edition"), Delver Lens (varies by version — fail
  loudly on unknowns with `--format` override), and hoard's own canonical CSV.
  Dragon Shield deferred (cut line). Normalize every format to
  `{Qty, Name, Set, Number, Finish, ScryfallID}`.
- **Resolution reuses the deck-import pipeline verbatim**: rows with Scryfall
  IDs (ManaBox/Delver/hoard) go direct; others build set+number
  `scryfall.Identifier`s for `scryfall.FetchCollection`
  (internal/scryfall/scryfall.go — batches 75/request, 429 retry built in) with
  name fallback, then `resolveIDs` (deck.go) and `store.CorrectFinish`
  (internal/store/finishes.go) for illegal finish/printing combos — exactly how
  `cmdDeckAdd` works today.
- Lossy fields (condition, language, purchase price) are **dropped with a
  summary line** ("dropped condition on 212 rows"). Non-English rows resolve to
  the printing the set/number names.
- `--dry-run` prints the resolution report, writes nothing. Writes go through
  `AddCardFinishTo` into the chosen binder (default binder if none named).
- Optional `--preserve-binders`: fan ManaBox's "Binder Name" column into real
  binders via `CreateBinder` (cut-line candidate).
- One fixture per real app export committed to testdata.
- **Checkpoint: round-trip test — export → import into a fresh DB → identical
  totals.**

## ⬜ D. Add-flow destination picker (day 10 am)

- `tui.Result` (internal/tui/tui.go) gains `ContainerID int64`.
- New `stateDestPick` in internal/tui/model.go built with the existing
  `showPicker`/`pickerKey` generic helpers, slotted after finish pick **only
  when more than one destination exists** — the single-binder flow stays
  byte-identical. Offer binders AND decks (the original ask included adding to
  decks from the add flow); deck adds go to board 'main'.
- The adder in add.go calls `AddCardFinishTo(r.ContainerID, ...)`.
- The container list is injected into `tui.Run` (a new source param or an
  extension of the Adder contract — decide at implementation; keep tui free of
  store imports, it currently only knows scryfall.Card).
- Remember the last pick for the session (bulk adds go to the same place).
- Cut-line fallback if TUI work runs long: `hoard add --binder REF` flag
  delivers the capability without TUI changes.

## ⬜ E. Multi-card scan spike (day 10 pm, timeboxed)

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
   export) with a documented stable schema.
6. Deterministic export ordering → version your collection in git; diffs show
   adds/removals.
7. Document the SQLite schema as a public read API.

**Later / opportunistic:**
8. Multi-card scanning productionization (if the E spike says go).
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
