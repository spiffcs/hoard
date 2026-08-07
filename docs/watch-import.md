# The bulk watch list

A price watch fires when a card crosses a threshold. `hoard watch add` stands one
at a time; `hoard watch import` stands a whole file at once.

```sh
hoard watch import watches.csv
hoard watch import watches.json
```

It takes exactly one path — there is no stdin form, so a generator writes a file
first.

This is the one file format hoard reads that scripts are meant to generate — a
deck-price tracker, a spreadsheet, whatever already knows what you want alerted
on. Collections go through [CSV](csv.md) instead.

## Two dialects, sniffed by content

The parser looks at the first non-space byte: `[` means JSON, anything else is
CSV. The file extension is not consulted, so either dialect works under any name.

### JSON

An array of objects. Field names are the same vocabulary the `--json` output
uses, so a script that reads hoard speaks its input format without a second
mapping:

```json
[
  {"name": "Sol Ring", "direction": "under", "thresholdUsd": 1.5, "setCode": "c21", "number": "263"},
  {"name": "Sheoldred, the Apocalypse", "direction": "under", "thresholdUsd": 40, "finish": "foil"},
  {"name": "Orcish Bowmasters", "direction": "over", "thresholdUsd": 25}
]
```

Unknown keys are ignored, so a generator can keep its own fields in the file.

### CSV

One header row, cells looked up **by header name, never by position**, so a
reordered or extended file still parses. A UTF-8 byte-order mark and ragged rows
are tolerated. Column names match the canonical collection export where the two
overlap.

```
Name,Direction,Threshold,Finish,Set,Collector Number
Sol Ring,under,1.50,,c21,263
"Sheoldred, the Apocalypse",under,40,foil,,
Orcish Bowmasters,over,25,,,
```

## The fields

| JSON | CSV | required | meaning |
|---|---|---|---|
| `name` | `Name` | **yes** | the card name |
| `direction` | `Direction` | **yes** | `under` or `over` — nothing else |
| `thresholdUsd` | `Threshold` | **yes** | a positive dollar amount; the CSV tolerates a leading `$` |
| `finish` | `Finish` | no | `nonfoil`, `foil` or `etched`; defaults to nonfoil |
| `setCode` | `Set` | no | pins the watch to one printing |
| `number` | `Collector Number` | no | with the set, names the printing exactly |
| `scryfallId` | `Scryfall ID` | no | the strongest identifier; skips resolution |

A watch is unique on `(printing, finish, direction)`. Importing a file that names
a watch already standing adjusts its threshold rather than adding a duplicate, so
re-importing an updated list is safe.

## Resolution is strict, on purpose

Rows resolve through the same one-pass pipeline collection import uses, with
**no fuzzy matching** — an alert about the wrong printing is worse than no alert.
Give a `scryfallId`, or a `setCode` and `number`, when a name has many printings.

Rows that cannot be resolved are skipped and listed; the rest still stand. Two
adjustments are reported rather than performed silently:

- **Refinished** — the row asked for a finish that printing has no price for, so
  the watch was moved to one that does. An etched-only printing is priced as
  foil, and a finish the card does not come in could never cross anything.
- **Unresolved** — the row named no printing hoard could identify.

Every imported card joins the catalog even if you own no copy, so
`update-prices` keeps its price fresh and the watch can actually fire.

## Checking them

```sh
hoard update-prices && hoard watch
```

`hoard watch` exits **3** when something crossed, so a cron can branch on it —
`--json` does not change that. See [json.md](json.md) for the `watch` document
and a worked cron example.

## Related

- [docs/json.md](json.md) — the `--json` output contract.
- [docs/csv.md](csv.md) — importing and exporting a collection.
- [docs/pricing.md](pricing.md) — where the prices a watch reads come from.
