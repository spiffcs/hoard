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

## ⬜ B. `hoard report`

Dated valuation document for insurance/sale: totals, top-N holdings,
per-binder breakdown, price sources, as-of date.

- Store assembly exists pre-sorted: `CollectionTotals`, `ListBinders`,
  `OwnedByFinish` (value-DESC — top-holdings is a head-N).
- To build: newest-observation as-of query, `report.Valuation` pure renderer,
  a CSV writer for the report shape, `--json` via A.
- `hoard report [-o FILE] [--top N] [--json|--csv]`; deterministic ordering
  throughout so reports diff cleanly in git.

## ⬜ C. Value snapshots + browser sparkline

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

## ⬜ D. `hoard watch`

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

## ⬜ E. Bulk paste entry

- `hoard add --file LIST` (or `-`/piped stdin): lenient text-list parse
  (skip-and-report bad lines, ignore board headers), resolve through the
  shared `cardResolver`, write through `ApplyImport` with a buffer-hash
  receipt so pastes dedupe like files.
- Extracts the `[]resolve.Request` build loop duplicated in deck.go and
  import.go (third caller).
- `--binder REF` on `hoard add` picks the destination — also closing the
  deferred URL-add `--binder`.

## ⬜ F. Progress UI for long-running commands

`update-prices`, `backfill-prices`, `catalog update`, first-of-day arbitrage,
and large imports can run many seconds with ad-hoc stderr lines or silence —
a quiet stretch reads as a hang.

**Pick this up with a dedicated plan-mode design session before any code.**
The UI angles need deliberate comparison: spinner vs progress bar vs step
checklist; determinate (bytes, chunks-of-75, card counts are mostly knowable)
vs indeterminate; TTY detection so piped runs keep plain stderr lines and
`--json` streams stay clean; whether `pricing.WithProgress` and
`catalog.Update`'s count callback generalize into one progress contract; how
cancellation is communicated. Ingredients: the browse TUI's spinner idiom,
`ui.Env` capability detection, the WithProgress inversion pattern.

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

1. **Distribution**: goreleaser + Homebrew tap + version wired into
   buildinfo — the "other people can use this" release; pairs with the new
   README. (Ground is favourable: pure Go, no cgo, Swift helper already
   decoupled from `go build`.)
2. **backup/doctor**: on-demand `VACUUM INTO` + integrity_check — the
   irreplaceable-price-history safety valve from the architecture audit.
3. Want lists with arbitrage-powered best-vendor pricing — portfolio's
   continuation on top of watch.
4. Condition/language columns → lossless ManaBox import, real Moxfield
   condition export.
5. Then: duplicates report, location/set-completion tracking, Dragon Shield
   import, scanner sleeve/glare fixtures, resolve/catalog unification.
