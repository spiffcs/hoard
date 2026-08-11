# hoard — friction found while building an agent-orchestration demo

Session date: 2026-08-10. Binary built from `main` at `5114608`.

All findings below were **reproduced by running the binary**, not read out of docs or
comments. Every command ran against a copy of the real collection
(`cp ~/Library/Application Support/hoard/hoard.db → /tmp/.../demo.db`, `HOARD_DB` pointed
at the copy). The real database was byte-identical before and after —
`shasum` = `ea3c011eef15549127461d3e6a3433fdce224ad2` both times.

Sorted roughly by how much each one would hurt someone wiring hoard into an agent loop.

---

## 1. `watch add <name>` watches a printing you don't own

**Severity: highest — it silently produces a wrong answer.**

I own Bitterblossom as `uma/85` at $34.80. Adding a watch by bare name:

```
$ hoard watch add Bitterblossom --over 1
Watching Bitterblossom (2x2/69) nonfoil: over $1.00.

$ hoard watch list | grep -i bitterblossom
Bitterblossom  2x2/69  -  over $1.00  $32.97  met

$ hoard export --format csv | grep Bitterblossom
4,Bitterblossom,uma,85,nonfoil,,547872bd-...,Binder,binder,main,34.80
```

It picked `2x2/69` ($32.97) — a printing that is **not in the collection**. The holding is
`uma/85` ($34.80). Nothing in the output flags the substitution; it reads like a success.

Why it matters: the entire premise of a price watch is "tell me when *my* card moves."
An alert keyed to a printing you don't hold is worse than no alert — you'd act on it. In
an agent loop that fans out `watch add` over a list of names, every single watch could be
pointed at the wrong printing and the transcript would look clean.

Suggested fix: when the named card **is held**, prefer the held printing and say so. When
it isn't held, keep current behaviour but print the disambiguation explicitly
(`"Bitterblossom has 9 printings; watching 2x2/69. You hold uma/85 — did you mean that?"`).
The information to do this is already in the store.

---

## 2. `binder list` has no `--json`, but binder ids are the scripting handle

`export`/`import`/`add` all take `--binder` as "id, name, or unique fragment". So an agent
needs to know what binders exist. There is no machine path to that list:

```
$ hoard binder --json
error: hoard binder has no JSON output
```

Current `--json` coverage, measured:

| supports `--json` | does not |
|---|---|
| `report`, `movers`, `market`, `watch`, `unpriced`, `export`, `import`, `merge` | `binder`, `catalog`, `guessed` |

Names happen to work as `--binder` values, so this isn't blocking — but the *discovery*
step forces an agent to either hardcode a name or scrape a human-formatted table. That's
the one place in an otherwise clean machine interface where you have to parse columns.

`guessed` is the same shape of gap and arguably worse: it's a worklist whose whole purpose
is `--checked <id>`, and the ids are only obtainable from the text table.

Suggested fix: `binder list --json` and `guessed --json`. Both are small payloads.

---

## 3. `watch list` refuses `--json`, so watch *state* is unreadable to a machine

```
$ hoard watch list --json
error: hoard watch list has no JSON output
```

`hoard watch --json` emits only `{"checked": 17, "fired": []}`. So an agent can learn
*that* nothing fired, but cannot read the standing watches, their thresholds, or their
`waiting`/`met`/`fired` state — all of which the text table shows.

This bites specifically because of finding #4 below: since firing latches, an agent that
misses one `exit 3` has no way to query "what is currently met?" It has lost the event
permanently.

Suggested fix: give `watch list` the same envelope the other commands use, including the
`state` field.

---

## 4. Watch firing latches, and nothing says so

Verified by running it three times in a row against unchanged prices:

```
$ hoard watch   # → exit 3, "1 fired."
$ hoard watch   # → exit 0
$ hoard watch   # → exit 0
$ hoard watch list | grep -i bitterblossom
Bitterblossom  2x2/69  -  over $1.00  $32.97  met      ← state moved to "met"
```

This is **good behaviour** — edge-triggered alerts are exactly right for cron, and it's
better than most hand-rolled price alerters. The problem is purely that it's undiscoverable.
`hoard watch --help` says `exit 3 = fired` and stops there. Anyone writing the obvious
supervisor loop will assume level-triggered semantics, see `exit 0`, and conclude the
threshold is no longer crossed — when in fact it is still crossed and was simply already
reported.

Suggested fix: one line in `watch --help`, e.g. *"A watch fires once per crossing and then
holds at `met`; it re-arms when the price crosses back."* (I did not verify the re-arm
condition — worth confirming what actually resets `met`, and documenting that too.)

---

## 5. `import --binder` validates the binder name *after* ~4.6 s of resolving

```
$ time hoard import binder.csv --binder NoSuchBinder
  resolving cards...
  resolving cards: 75/678 cards
error: no binder matching "NoSuchBinder"
  4.628 total
```

A typo costs five seconds and, worse, shows a progress counter climbing first — so the
operator's mental model is "it's working" right up until it isn't. The destination is known
before any card is touched.

Two smaller things in the same area:

- The error doesn't name the fix. It could say *"…no binder matching "X". Create it with
  `hoard binder new X`, or see `hoard binder list`."*
- `--binder` on an unknown name refuses outright, which is defensible, but it's worth a
  deliberate decision rather than an accident — `--preserve-binders` will happily materialise
  binders from the file, so "import may create binders" is already true in one path.

Suggested fix: resolve `BinderRef` at the top of the import action, before the resolve
loop. The ref is currently passed through to the action at `internal/command/import.go:123-126`
and validated somewhere downstream of resolution.

---

## 6. Pluralization: "1 lines", "1 cards"

```
$ printf '4 Lightning Bolt\n1 NotARealCardXyz\n' | hoard deck add --file - --name P1 --dry-run
  1 cards could not be resolved and were skipped:
    - NotARealCardXyz
error: 1 lines would not resolve: some items were skipped
```

Both strings are unconditional `%d`:

- `internal/action/add.go:287` — `"%d lines would not resolve: %w"`
- `"%d cards could not be resolved and were skipped:"` appears **seven times**:
  `internal/command/deck.go:150`, `add.go:207`, `import.go:168`, `watch.go:232`,
  and `browse.go:78,280,411`

Cosmetic, but it lands on the *error* path, which is the path a demo audience reads most
carefully. Seven duplicated copies of the same sentence also suggests a shared helper is
overdue — something like `ui.Plural(n, "card", "cards")` used at all seven sites.

---

## 7. The column header `ID` means two unrelated things

- `internal/report/tables.go:22` — binder tables: `ID`, right-aligned, dim. This is the
  binder's integer row id. `hoard binder list` prints ` 1  Binder  915  $3,797.19`.
- `internal/report/movers.go:63` and `internal/report/report.go:91` — `ID`, left-aligned,
  pips-styled. This is **colour identity**: `G`, `C`, `WR`, `B`.

So in `hoard movers` the `ID` column reads `G` / `C` / `WR`, while in `hoard binder list`
the `ID` column reads `1`. The code comment at `movers.go:61-62` is self-aware about it —
*"Identity pips beside the name … meaning-bearing ornament, not data"* — which is precisely
why it shouldn't share a header with an actual identifier. The collision is sharpest because
`guessed --checked <id>` makes "id" a real, typeable thing elsewhere in the CLI.

Suggested fix: rename the pips column to `COLOR`, `WUBRG`, or blank it entirely (it's
`Priority: 7`, the first column dropped on a narrow terminal, so a blank header costs
nothing).

---

## 8. `movers --json` drops the percentage the text view computes

Text: `Cryptolith Rite  G  soi/200  -  $6.31 → $13.54  +114.6%  ×3  +$21.69`

JSON: `{"card":{…},"copies":3,"oldUsd":6.31,"newUsd":13.54,"impactUsd":21.69,"source":"scryfall"}`

`impactUsd` is derived and included; percent change is derived and omitted. Every consumer
now recomputes `(newUsd-oldUsd)/oldUsd`, and each one re-decides what to do when
`oldUsd == 0` — which is a real case, since `unpriced` exists as a whole command.

Suggested fix: include `pctChange` (null when `oldUsd` is 0), so the divide-by-zero policy
is decided once, in hoard, rather than in every caller.

---

## 9. Import-guard timestamp is the only non-human date in the CLI

```
error: this content was already imported on 2026-08-11T03:11:07Z (915 cards);
       re-running would double every quantity. Use --again to add them anyway
```

Everything else in the tool renders dates as `10 Aug 2026` / `Built 10 Aug 12:22`. This one
is raw RFC 3339 in UTC — so on a US clock it reads as **tomorrow**, which is momentarily
alarming in a live demo. (The guard itself is excellent; see the closing section.)

Suggested fix: render it in the same local human format as the rest, or show both.

---

## 10. Smaller notes

- **`unpriced` is a dead demo on a healthy collection.** It prints *"Every card you own has
  a price."* Correct and pleasant, but there's nothing to show. If it's meant to be
  demoable, a `--explain` or a summary of *how* prices were sourced would give it a body.
- **`export --json` and `export --format json` are byte-identical** (verified with `diff`).
  Two spellings for one thing. Not a bug — and the conflicting case is handled properly
  (`error: --json conflicts with --format csv`) — but worth knowing it's redundant.
- **CSV round trip is card-exact, value-approximate.** 915 cards out, 915 cards in, but
  `$3,797.19` → `$3,797.53`. Prices are re-derived from the local catalog on import rather
  than carried from the CSV's `Price USD` column. That's probably the right call; it just
  isn't stated anywhere, and a 34-cent drift is the kind of thing that makes someone
  distrust a round trip they should trust.

---

## What is genuinely good, and worth keeping

Recording this so the list above isn't read as a verdict on the tool. These are the things
I'd point at in a design review:

1. **`hoard schema --kind <k>`** — 4.4 KB for `movers` against 27 KB for the full schema,
   with help text that names the use case exactly: *"handed to a model or a validator, it is
   enough to write a correct query against hoard's JSON without any card data leaving the
   machine."* This is the correct architecture for LLM integration and most tools get it
   backwards by shipping the data into the context instead of the contract.
2. **The exit-code tri-state**, verified: `0` ok, `1` error, `2` partial success, `3` watch
   fired. `2` is the rare one. Most CLIs report "imported, 3 rows skipped" with status `0`,
   and that is exactly how automated pipelines lose data without anyone noticing.
3. **Content-hashed import idempotency.** Keyed on file *content*, not filename, so a
   retrying agent or two racing processes cannot silently double every quantity. `--again`
   is the explicit override. For unattended automation this is the single most valuable
   property in the tool.
4. **`--json` is refused, not ignored** (`internal/cli/cli.go:117`). A command that can't
   emit JSON says so and exits non-zero, rather than printing a table into something
   expecting a parser.
5. **`merge --dry-run` states what it will *not* carry** — price history, dated valuations,
   price-gap records. Tools that enumerate their own omissions are rare.
6. **`stdin` refuses to invent an identifier**: *"reading a decklist from stdin needs
   `--name`: a pipe carries no file name to call the deck."*
7. **Catalog and collection are separate stores**, so a throwaway `--db` still resolves card
   names fully offline. This is what made a zero-risk, zero-network demo possible at all.

---

## Reproduction environment

```bash
go build -o hoard ./cmd/hoard
cp ~/Library/Application\ Support/hoard/hoard.db /tmp/hoard-demo/demo.db
export HOARD_DB=/tmp/hoard-demo/demo.db
```

Catalog at time of testing: 107,338 cards, 60 MB, built 10 Aug 12:22 from Scryfall's
10 Aug 05:05 bundle. Demo collection: 2,794 copies, $6,773.45, 915 loose + 1,879 across
22 decks, 16 pre-existing watches.
