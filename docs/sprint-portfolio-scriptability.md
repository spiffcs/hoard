# Sprint: Portfolio + Scriptability

Status document for the sprint started 2026-07-30, successor to
[sprint-binders-interop.md](sprint-binders-interop.md) (complete: binders,
export/import, destination picker, multi-card scanning, plus a hardening
interlude). Written so a fresh session — human or AI — can resume with zero
prior context. Update the status markers as phases land.

## Why this sprint

The backlog's two leading themes, chosen because they compose: **portfolio**
("your collection as an asset the competitors paywall") and **scriptability**
("the only MTG tool that composes"). Watch needs machine output and exit
codes; the valuation report is a deterministic export with prices; the value
chart needs the price history that already accumulates. The hardening
interlude already laid the groundwork: report tables are prose-free, data
assembly is separated from rendering, output ordering is deterministic, and
exit codes distinguish done from done-mostly.

Decisions made at planning: the value chart seeds itself from existing history
(labeled estimated — quantities aren't versioned, so pre-upgrade points are
"today's shelf at that day's prices"); watch v1 is price thresholds in both
directions; distribution (goreleaser/Homebrew) and backup/doctor stay in the
backlog for next time.

## Order: A → B → C → D → E → F

JSON discipline first because report and watch emit through it. Each phase is
one commit, committed by the maintainer.

## ✅ A. `--json` on every read command

**Schema philosophy (decided at planning, after surveying the ecosystem):**
there is no community-standard *collection* interchange format — MTGJSON
models cards/sets/prices (uuid-keyed), not holdings, and the de-facto
collection interchange is the proprietary CSVs hoard already speaks. So
hoard.json is deliberately a **thin holdings overlay on ecosystem
identifiers**, not a parallel universe:

- **Identifiers first**: every card reference carries `scryfallId` and, when
  known, `mtgjsonUuid` (the store already learns these) — the join keys into
  Scryfall's API and MTGJSON's AllIdentifiers/AllPrices. That is what makes
  hoard.json composable with existing tooling "for free".
- **Borrowed vocabulary**: where a field means what an MTGJSON/Scryfall field
  means, use their name and their value vocabulary (`setCode`, `number`;
  price quotes in MTGJSON's provider→retail/buylist→finish shape —
  internal/mtgjson.Quote already is one).
- **Pre-A prep commit — rename hoard's finish `normal` → `nonfoil`
  everywhere** (maintainer decision: few users, so fix the awkwardness at the
  source instead of translating at the wire). Migration **v8** rewrites
  `card_entries.finish` and `card_price_history.finish`; the whole codebase
  vocabulary follows (store validation and SQL fragments, decksource,
  collsource, export writers, tui, browse, ui display) with no legacy
  aliases — pre-rename vocabulary carried no users to break, only us
  dogfooding. The `internal/mtgjson` package
  keeps `normal`: that is MTGJSON's own price-file vocabulary, and the
  package models their wire format. Snapshots and watches move to **v9**.
- **Custom only where nothing standard exists**: containers, quantities,
  boards, totals, movers, watch state — the collection semantics no external
  schema models.
- **`schema/` management**: one `schema/json/schema-X.Y.Z.json` per version plus
  `schema-latest.json`, **never deleted and never mutated once released** —
  only new versions are added. Versioned by **SchemaVer**
  (`MODEL.REVISION.ADDITION`: MODEL = breaks all historical data, REVISION =
  breaks some, ADDITION = compatible), which fits data models better than
  semver. Schemas are **generated from the Go document structs** (an
  `internal/hoardjson` model package + a `SchemaVersion` constant govern the
  shape; `make generate-json-schema` regenerates) with drift enforced by a
  plain go test: regenerate in memory, and a mismatch against the committed
  file for the current version fails with "bump SchemaVersion". Emitted
  documents carry their `schemaVersion`. `schema/json/README.md` documents
  the versioning rules and the join recipe to MTGJSON/Scryfall (the
  hoard-field ↔ ecosystem-field mapping table) — the practical form of
  "convert hoard.json to the wider ecosystem".

Implementation:

- Global `--json` flag stripped by an `extractDBFlag`-style pre-pass (see
  main.go:~175 for the idiom and its rationale), handed to commands as a bool.
- stdout hygiene first: `fillPriceGaps` progress → stderr; movers'
  recorded-since note and arbitrage's footer are human-mode only.
- Emitters from the existing assembly seams: summary
  (CollectionTotals + []DeckSummary), unpriced ([]UnpricedRow), movers
  (exported section data from internal/report), arbitrage
  (arbitrage.Sections, kinds via Kind.String()), export ([]export.Row with
  json tags — `--json` as a fourth format).
- Golden JSON tests per command against fixture stores, schema-validated.

**Landed** (2026-07-30): one `Document` envelope (`schemaVersion` + `kind` +
one payload) in internal/hoardjson; `--json` on hoard/unpriced/movers/
arbitrage/export (everything else rejects it); schema 1.0.0 generated into
schema/json/ with descriptions from the model's doc comments, drift +
instance-validation tests in internal/hoardjson/schemagen; docs/json.md.
Decisions made while building, all documented there: `--limit` never shapes
JSON (movers/arbitrage emit the full ranking), money fields round to whole
cents, absent-means-unknown for priceUsd/mtgjsonUuid, store.Card learned
MTGJSONUUID (cardCols), UnpricedRow learned identifiers + a Containers list.

## ✅ B. `hoard report`

Dated valuation document for insurance/sale: totals, top-N holdings,
per-binder breakdown, price sources, as-of date.

- Store assembly exists pre-sorted: `CollectionTotals`, `ListBinders`,
  `OwnedByFinish` (value-DESC — top-holdings is a head-N).
- To build: newest-observation as-of query, `report.Valuation` pure renderer,
  a CSV writer for the report shape, `--json` via A.
- `hoard report [-o FILE] [--top N] [--json|--csv]`; deterministic ordering
  throughout so reports diff cleanly in git.

**Landed** (2026-07-30): `report.Valuation`/`ValuationCSV` in internal/report
(text itemizes --top N, default 10; CSV is the full list, dated per row);
store gained `PriceSources` (per-source printings/copies, classified exactly
as entryValue prices) and `LatestPriceStamp` (newest history row, else newest
priced fetch). JSON is the `report` document kind — schema **1.0.1**, the
first ADDITION bump: schema-1.0.0.json stayed immutable, 1.0.1 landed beside
it with a changelog entry.

## ✅ C. Value snapshots + browser sparkline

- Migration v9: `value_snapshots(as_of TEXT PRIMARY KEY, binder REAL,
  decks REAL, total REAL, source TEXT)`, source ∈ observed|seeded.
- Written by `RecordPrices` unconditionally — NOT inside `appendPrices`,
  whose no-changes early return would skip exactly the flat stretches a
  chart needs.
- Seeding: one snapshot per distinct history date from "today's quantities ×
  price as-of that date" (the `latestPrices` cutoff fragment joined to
  `ownedByPriceFinish` already expresses this), marked seeded, labeled
  estimated in the UI.
- Browser: `browse.Store` gains `ValueSeries()`; browse's `resample` promotes
  to internal/ui beside `Spark`; the sparkline renders in the holdings header
  and yields to the title when the terminal is narrow.

**Landed** (2026-07-30): as planned, except seeding is **Go, not SQL**
(`seedValueSnapshots`, hooked after v9 applies like the legacy transform):
one ordered pass over history with incremental totals. Two SQL formulations
(correlated MAX, then a grouped join) each ran >2 minutes on the real 55k-row
history — the planner tarpit is documented at the migration. Real-DB check:
91 seeded points in 0.4s including backup, and the newest seeded point
exactly equals the live summary's totals. Interface method is named
`ValueSnapshots()`; the header spark shows `≈` until observed points
outnumber seeded ones (seeded rows never leave the series, so presence alone
would mark it forever), and `hoard watch` becomes migration **v10**.

## ✅ D. `hoard watch`

- Same v9 migration: `watches(id, scryfall_id, display, finish, op,
  threshold, created_at, last_state)`. A table, not a prefs file — scan.json's
  best-effort error swallowing is wrong for alerts, and watches carry state.
- `watch add <name> --under N | --over N [--foil]` resolves the name once at
  creation (catalog-first searcher) and pins the printing; check time does no
  fuzzy matching and no network.
- Bare `hoard watch` checks stored prices; fires only on threshold *crossing*
  (last_state flips) so cron re-runs don't re-alert. Exit 3 = fired,
  0 = quiet; `--json` for scripting. Cron pairing:
  `hoard update-prices && hoard watch`.
- `watch list` / `watch rm` complete the surface.

**Landed** (2026-07-30): migration **v10** `watches` table with
`UNIQUE(scryfall_id, finish, op)` — re-adding a question replaces its
threshold and resets state rather than stacking duplicate alerts. Crossing
semantics: fires when the condition starts holding (including the first
check — "already under your threshold" is worth exactly one alert);
unpriced cards are skipped with state untouched, so the alert fires when a
price appears rather than manufacturing a phantom crossing. Thresholds are
strict (< / >). `watch add` resolves through `cardResolver` (the deck/import
pipeline) and upserts the card into the catalog so update-prices keeps
pricing it even when unowned — the buy-watch case. Exit 3 maps from an
`errWatchFired` sentinel handled before the error printer (an alert is a
result, not a failure). JSON is the `watch` document kind, schema **1.0.2**;
`--json` still exits 3. Live-verified on the real DB: add → list
(met/waiting/unpriced states) → first check fired exit 3 → second quiet
exit 0.

## ✅ E. Bulk paste entry

- `hoard add --file LIST` (or `-`/piped stdin): lenient text-list parse
  (skip-and-report bad lines, ignore board headers), resolve through the
  shared `cardResolver`, write through `ApplyImport` with a buffer-hash
  receipt so pastes dedupe like files.
- Extracts the `[]resolve.Request` build loop duplicated in deck.go and
  import.go (third caller).
- `--binder REF` on `hoard add` picks the destination — also closing the
  deferred URL-add `--binder`.

**Landed** (2026-07-31): `decksource.ParseLoose` (shared `parseLine` with
ParseText; headers ignored, everything board-main, bad lines skipped with
line numbers); `resolve.Requests` over a `Requester` interface that
decksource.Entry and collsource.Row implement (deck add, import, and the
paste all call it); `hoard add --file/-` with piped-stdin autodetection
(`pbpaste | hoard add` just works), `--binder` on both list and URL adds
(destination resolves before the network round-trip), `--again` over the
shared `contentHash`/`refuseReimport` helpers extracted from import.
Partial pastes add what resolved and exit 2; a re-paste of the same bytes
refuses with exit 1. Live-verified against the real DB via stdin.

## ✅→ F. Progress UI — superseded by the parity sprint

**Pivot (2026-07-31, maintainer decision):** the CLI grew ten commands the
TUI cannot reach, and the TUI grew editing/filtering the CLI cannot. The
progress UI is no longer a standalone phase: it is one pillar of the next
sprint, the **parity sprint**, because the TUI's missing big rocks
(update-prices, backfill, catalog update, import/export) are blocked on
exactly it. See "The way forward" below. The design constraints recorded
here (spinner vs bar vs checklist, determinate counts, TTY detection, one
progress contract generalizing `pricing.WithProgress` + catalog's count
callback, cancellation) carry into that sprint's opening design session
unchanged.

## Verification discipline (unchanged from last sprint)

```sh
go build ./... && go vet ./... && gofmt -l . && go test ./...
```

Plus byte-identical baseline diffs (bare/unpriced/movers/deck/catalog
status/help) against a pre binary, with intentional diffs called out per
phase: A and D touch usage text; F's piped-mode output must stay plain — that
invariance is itself a verification target.

## Risks and cut lines

- **Cut order (first → last)**: bulk paste → watch → the chart's *seeding*
  (ship the chart empty) → progress UI (slips whole to next sprint if its
  design session reveals depth). **Never cut**: `--json` and `report` — they
  are this sprint's positioning.
- JSON schema stability is a public promise once schema/ lands; field names
  and vocabularies are reviewed against MTGJSON/Scryfall conventions *before*
  first release — renames after are breaking changes logged in
  schema/CHANGELOG.md.
- v8 is the first migration with a data backfill (seeding); the
  backup-before-migration machinery from the hardening interlude is the
  safety net — verify on a real DB copy before handoff.

## The way forward (after this sprint)

1. **The parity sprint** (next; replaces phase F). Principle: every
   capability is a function in an internal package with a progress seam; the
   CLI and the TUI are both thin frontends, and a gap between them is a
   decision, not an accident — tracked in a parity table that every phase
   updates. **Opens with a plan-mode design session** covering three things
   as one conversation: the action-layer contract, the progress UI (phase
   F's constraints, recorded above), and a TUI command palette (`:`-style,
   fuzzy over every action — the scalable mirror of the CLI verb list;
   dedicated keys stay for frequent actions, the palette is the floor).
   Then: quick wins first (watches view + fired-on-open banner +
   add-watch-from-selected-card, binder new/rename/rm keys in the left
   pane, repair-finishes from the unpriced view, movers window cycling to
   match `--since`), the progress-dependent big rocks second (update-prices,
   backfill, catalog update, import/export inside the TUI).
2. **UI beautification sprint** (docs/sprint-ui-beautification.md, queued):
   deliberately *after* parity — polishing half a surface would lock the
   skew in.
3. **Distribution**: goreleaser + Homebrew tap + version wired into
   buildinfo — the "other people can use this" release; pairs with the new
   README. (Ground is favourable: pure Go, no cgo, Swift helper already
   decoupled from `go build`.)
4. **backup/doctor**: on-demand `VACUUM INTO` + integrity_check — the
   irreplaceable-price-history safety valve from the architecture audit.
5. Want lists with arbitrage-powered best-vendor pricing — portfolio's
   continuation on top of watch.
6. Condition/language columns → lossless ManaBox import, real Moxfield
   condition export.
7. Then: duplicates report, location/set-completion tracking, Dragon Shield
   import, scanner sleeve/glare fixtures, resolve/catalog unification.

**Reverse-skew backlog** (user A's ledger — the TUI can do these and the CLI
cannot; explicit so parity is honest in both directions):

- `hoard search <query>` — the browser's filter grammar (`finish:foil
  qty>2 rarity:mythic`, bare words as name search) as a read command, with
  `--json` via the holdings document.
- CLI quantity edit/remove — `hoard set <card> <qty>` / `hoard rm <card>`
  or similar: today the only non-TUI ways to shrink a holding are export →
  edit → re-import, which is not an answer.
