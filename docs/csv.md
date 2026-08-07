# CSV: importing and exporting a collection

CSV is hoard's interchange format — the only one hoard both writes and reads.

```sh
hoard export > holdings.csv          # the canonical format
hoard export --format moxfield       # Moxfield's collection-import columns
hoard export --format archidekt      # Archidekt's
hoard import holdings.csv            # reads any format below, sniffed
```

`hoard export --json` is **not** importable. The JSON documents are an output
contract for scripts ([json.md](json.md)); CSV is the one hoard reads back.

## What round-trips, and what does not

Export writes your whole collection — binders *and* deck contents. Import stands
up the binder rows and **skips the deck rows**, saying so in its summary:

```
Imported 356 cards (hoard format): 194 rows resolved.
  356 into Binder
  Skipped 1399 deck rows: decks come back via 'hoard deck add', not as loose cards.
```

That is deliberate, not a gap. A deck's cards would be counted twice the moment
the deck itself was re-imported, so decks come back through `hoard deck add` and
the CSV carries them for other tools to read rather than for hoard to swallow.

So: **a canonical export is a complete picture of a collection, and importing it
restores the loose cards.** If you are moving a whole hoard between machines,
copy `hoard.db` — it is a plain SQLite file
([data-model.md](data-model.md)) — rather than round-tripping CSV.

---

## The canonical format

```
Count,Name,Set,Collector Number,Finish,Scryfall ID,Container,Container Kind,Board,Price USD
```

These column names are a compatibility promise. The importer recognizes hoard's
own files by them, so renaming one breaks files already on disk.

| column | notes |
|---|---|
| `Count` | copies of this printing, in this finish, in this container, on this board |
| `Name` | English name; display only, never used to resolve when a Scryfall ID is present |
| `Set` | Scryfall set code |
| `Collector Number` | as printed, including any `★`/`†`/`Φ` variation marker |
| `Finish` | `nonfoil`, `foil` or `etched` |
| `Scryfall ID` | the identity. Everything else is a convenience |
| `Container` | binder or deck name |
| `Container Kind` | `binder` or `deck` |
| `Board` | `main`, `commander`, `side` or `maybe` |
| `Price USD` | the value at export time, **empty when unpriced** — never `0.00` |

Rows are ordered container → name → set → number → finish, deterministically, so
two exports of the same collection diff cleanly.

`Container Kind` arrived after the first release. A file without it is read as
all-binder rows, which is what those older files were.

---

## Importing

The format is recognized from the header row — cells are looked up **by name,
never by position**, so a reordered or extended export still parses. A UTF-8
byte-order mark and ragged rows are tolerated. Force a parser with `--format` if
the sniff fails.

| format | recognized by | quantity | set | number | finish | Scryfall ID | container |
|---|---|---|---|---|---|---|---|
| `hoard` | `Scryfall ID` + `Container` + `Board` | `Count` | `Set` | `Collector Number` | `Finish` | yes | `Container` |
| `manabox` | `Binder Name` + `Scryfall ID` | `Quantity` | `Set code` | `Collector number` | `Foil` | yes | `Binder Name` |
| `moxfield` | `Tradelist Count` + `Edition` | `Count` | `Edition` | `Collector Number` | `Foil` | — | — |
| `delver` | `Card number` | `Quantity` | `Set code` | `Card number` | `Foil` | yes | — |

Only quantity and name must exist; the rest degrade. Without a Scryfall ID,
hoard resolves set + collector number, and falls back to the name.

Delver Lens columns vary by version and export settings, so the spec above is its
common default. An unrecognized dialect fails the sniff and says so rather than
guessing — a wrong guess silently mangles quantities and finishes.

**Finish is normalized, and unknown values read as `nonfoil`.** ManaBox's
`normal`, an empty cell, `foil`, `etched`, `foil-etched` and `etched foil` all
map to the three values hoard stores. Anything unrecognized becomes `nonfoil`
deliberately: an invented foil would claim a price that may not exist.

### What an import drops, and why it tells you

Three columns carry real information hoard cannot store: **condition**,
**language** and **purchase price**. Rather than discard them silently, the
importer counts every cell that actually says something — a condition other than
near mint, a language other than English, a nonzero price — and reports the
totals when the import finishes.

The canonical hoard format has none of these columns, which is what makes it
lossless. A test asserts that: importing hoard's own export drops nothing.

Imports are recorded in `import_ledger` by content hash, so importing the same
content twice is refused rather than silently doubling every quantity:

```
error: this content was already imported on 2026-08-06T20:55:30Z (356 cards);
re-running would double every quantity.
Use --again to add them anyway
```

The hash is of the content, not the path, so renaming the file does not get past
it. `--again` is the override when you really do mean to add a second copy.

---

## Exporting to other tools

**Moxfield** — `Count, Name, Edition, Condition, Language, Foil, Collector Number`.
`Condition` is written as `Near Mint` because hoard does not track it and
Moxfield requires the column. `Language` is the printing's own, falling back to
English where hoard has not stored the card's document. The `Foil` column is
empty for nonfoil, else the finish name.

**Archidekt** — `Quantity, Name, Finish, Edition Code, Collector Number, Scryfall ID`.
Their importer tolerates missing optionals, so this is the minimal exact set.
Finish is capitalized to `Normal` / `Foil` / `Etched`.

Neither has a container column, so rows that differ only by binder or board are
merged into one line. The canonical format keeps them separate.

---

## Decklists are a different thing

`hoard deck` reads the universal text decklist format, not CSV:

```
2 Sol Ring
1 Sol Ring (C21) 1 *F*
```

with the usual section headers. That is for deck contents; the formats on this
page are for collections.

## Related

- [docs/json.md](json.md) — the `--json` output contract for scripts.
- [docs/data-model.md](data-model.md) — what hoard stores and how a printing is
  identified.
- [docs/watch-import.md](watch-import.md) — the bulk price-watch list, the one
  other file hoard reads.
