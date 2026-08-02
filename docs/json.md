# Scripting hoard: `--json`

The read commands emit a machine-readable document instead of a table when
`--json` is given (anywhere on the line, like `--db`):

```sh
hoard --json                 # the summary: binder, decks, totals
hoard unpriced --json        # what counts as $0.00, and where it's held
hoard movers --json          # every price change in the window
hoard arbitrage --json       # the full vendor-disagreement ranking
hoard report --json          # the dated valuation: totals, binders, sources
hoard watch --json           # one watch check: what just crossed a threshold
hoard export --json          # holdings, same data as the canonical CSV
```

`export --format json` is the same as `export --json`. Every other command
rejects `--json` rather than silently printing a table at a script.

## The contract

- **One document on stdout, nothing else.** Progress and narration go to
  stderr; pipe stdout straight into `jq`.
- **Envelope**: every document is `{schemaVersion, kind, <kind>: …}` — the
  payload field is named by `kind`. Documents validate against
  `schema/json/schema-<schemaVersion>.json` (see `schema/json/README.md` for
  the versioning and compatibility rules; short version: field names are a
  promise, breaking changes bump the MODEL number).
- **Deterministic**: the same data produces the same bytes, so outputs diff
  cleanly in git. Ordering is part of the shape — holdings in the canonical
  export order, decks by value, movers by absolute impact, opportunities
  grouped arbitrage → liquid → below-market.
- **Everything, not the display's top-N**: `movers --json` and
  `arbitrage --json` emit the full result; `--limit` only shapes the human
  tables. Selection flags (`movers --since`, `arbitrage --min`,
  `export --binder`) still apply — they change the question, not the width of
  the answer.
- **Absent means unknown**: an unpriced card has no `priceUsd` field at all —
  never `0`, never `null`. A card with no MTGJSON mapping has no
  `mtgjsonUuid`.
- **Money is whole cents**: every `*Usd` field is rounded to two decimals at
  the boundary, so summed totals don't leak binary-float noise into diffs.
  Ratios (`belowMarket`, `liquidity`) are unrounded fractions.
- **Card references travel with identifiers**: every card carries
  `scryfallId` (+ `mtgjsonUuid` when mapped) plus `setCode`/`number`, so a
  document joins directly against Scryfall bulk data or MTGJSON AllPrintings
  without name matching.

## Examples

Total value, and each deck's share:

```sh
hoard --json | jq '.summary.total.valueUsd'
hoard --json | jq -r '.summary.decks[] | "\(.valueUsd)\t\(.name)"'
```

What fell the most since last month, as a TSV:

```sh
hoard movers --since 30d --json |
  jq -r '.movers.changes[] | select(.impactUsd < 0) |
         [.card.name, .oldUsd, .newUsd, .impactUsd] | @tsv'
```

Cards a shop pays more for than the cheapest retail (true arbitrage):

```sh
hoard arbitrage --json |
  jq -r '.arbitrage.opportunities[] | select(.kind == "arbitrage") |
         "\(.card.name): buy \(.buyUsd) at \(.buyFrom), sell \(.sellUsd) to \(.sellTo)"'
```

Everything in one binder as Scryfall ids, e.g. to feed another tool:

```sh
hoard export --binder Trade --json | jq -r '.holdings.rows[].card.scryfallId'
```

The valuation's headline figures, and how much of the total is estimated:

```sh
hoard report --json | jq '{asOf: .report.asOf, total: .report.total.valueUsd,
                           sources: .report.sources}'
```

A cron that pushes an alert when a watch fires (`hoard watch` exits 3 on a
crossing, so the `||` branch runs exactly then — remember `--json` still
exits 3):

```sh
hoard update-prices && hoard watch --json > /tmp/watch.json ||
  notify "$(jq -r '.watch.fired[] |
    "\(.card.name) is \(.priceUsd) — \(.op) \(.thresholdUsd)"' /tmp/watch.json)"
```

Exit codes are unchanged by `--json`: 0 success, 1 error, 2 finished with
skips.
