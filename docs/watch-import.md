# Importing watches in bulk

`hoard watch import <file>` — or `ImportWatchList` in the browser's palette
on the watches view — stands many price watches at once from a file. The
format is designed to be *generated*: take any list of card names, have a
script or another tool emit one of the two shapes below, and import it.

```sh
hoard watch import watches.csv
hoard watch import watches.json
```

The format is sniffed from content, not the file name: a document whose
first non-whitespace byte is `[` is the JSON array, everything else is CSV.

## The CSV shape

One header row; cells are looked up by header name, case-insensitively, in
any column order. Extra columns are ignored and a UTF-8 BOM is tolerated.
Column names match hoard's canonical collection export where they overlap.

| Column | Required | Meaning |
|---|---|---|
| `Name` | yes | The card's exact name |
| `Direction` | yes | `under` or `over` — no default, ever |
| `Threshold` | yes | A positive dollar amount; a leading `$` is fine |
| `Finish` | no | `nonfoil` (default), `foil` (also `true`/`yes`/`1`), or `etched` |
| `Set` | no | Set code — with `Collector Number`, pins one printing |
| `Collector Number` | no | See `Set` |
| `Scryfall ID` | no | Pins a printing exactly; beats every other identifier |

```csv
Name,Direction,Threshold,Finish,Set,Collector Number
Sol Ring,under,1.50,,c21,263
"Sheoldred, the Apocalypse",under,40,foil,,
Orcish Bowmasters,over,25,,,
```

## The JSON shape

A bare top-level array of objects — no envelope. Field names reuse the
vocabulary of hoard's `--json` output documents (see `docs/json.md`).
Unknown keys are ignored, so a generator may keep private fields inline.

| Field | Required | Meaning |
|---|---|---|
| `name` | yes | The card's exact name |
| `direction` | yes | `"under"` or `"over"` |
| `thresholdUsd` | yes | A positive number |
| `finish` | no | `"nonfoil"` (default), `"foil"`, or `"etched"` |
| `setCode` + `number` | no | Pin one printing |
| `scryfallId` | no | Pin a printing exactly; beats every other identifier |

```json
[
  {"name": "Sol Ring", "direction": "under", "thresholdUsd": 1.5, "setCode": "c21", "number": "263"},
  {"name": "Sheoldred, the Apocalypse", "direction": "under", "thresholdUsd": 40, "finish": "foil"}
]
```

## Semantics

- **Identifier precedence**: `scryfallId` beats `setCode`+`number` beats
  `name`. A set+number pair Scryfall does not know retries by name; the
  retry's printing is whichever Scryfall picks.
- **Names never fuzzy-match.** An alert about the wrong printing is worse
  than no alert, so an unresolvable row is skipped and reported — the rest
  of the file still imports, and the CLI exits 2 ("done, mostly") instead
  of 0. A malformed file (bad direction, missing threshold) imports
  nothing at all: the whole batch is one transaction.
- **Re-importing adjusts, never stacks.** A watch is unique per card,
  finish and direction; importing a row that matches a standing watch
  replaces its threshold and re-arms the alert (the new line counts as
  never checked). Editing the file and re-running it is the intended way
  to manage a watch list. Duplicate rows in one file: the last wins.
- **Watches store a price finish** — `nonfoil` or `foil`. An `etched`
  row (or an etched-only printing) watches the foil price, and a finish
  the printing does not come in is corrected and reported.
- **Every imported card joins the catalog**, owned or not, so
  `hoard update-prices` keeps its price fresh — watching a card you are
  hoping to buy is the `under` case entirely.
- Checks read stored prices only; the cron pairing remains
  `hoard update-prices && hoard watch`.

## Generating a file from a name list

The minimal generator is one line per name with a shared threshold rule.
For example, watching every card on a list for a 30% dip below its current
TCGplayer price only needs `name`, `direction: "under"`, and a computed
`thresholdUsd` — the identifiers are optional precision, not a requirement.
