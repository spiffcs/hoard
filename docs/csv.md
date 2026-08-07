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
Count,Name,Set,Collector Number,Finish,Condition,Scryfall ID,Container,Container Kind,Board,Price USD
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
| `Condition` | `nm`, `lp`, `mp`, `hp`, `dmg` — **empty when unassessed** |
| `Scryfall ID` | the identity. Everything else is a convenience |
| `Container` | binder or deck name |
| `Container Kind` | `binder` or `deck` |
| `Board` | `main`, `commander`, `side` or `maybe` |
| `Price USD` | the value at export time, **empty when unpriced** — never `0.00` |

Rows are ordered container → name → set → number → finish → condition,
deterministically, so two exports of the same collection diff cleanly.

`Container Kind` arrived after the first release. A file without it is read as
all-binder rows, which is what those older files were. `Condition` arrived with
schema v23 on the same terms: the sniff does not key on it, cells are read by
name, and a file written before it imports as unassessed.

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

### Condition: what is accepted, and what it becomes

hoard stores six values. Five are conditions — MTGJSON's, which are
TCGplayer's — and the sixth means nobody has said:

```
unknown · nm · lp · mp · hp · dmg
```

Two different condition scales arrive in exports, and they are not the same scale.
Everything below is translated on the way in:

| your file says | hoard stores | |
|---|---|---|
| `Near Mint`, `NM`, `NM-Mint`, `Mint`, `MT`, `M` | `nm` | Mint folds down — neither MTGJSON nor TCGplayer has anything above near mint |
| `Lightly Played`, `LP` | `lp` | |
| `Good (Lightly Played)`, `Good/Lightly Played`, `SP` | `lp` | Moxfield's own long and short forms — it abbreviates this one `SP`, not `LP` |
| `Slightly Played` | `lp` | Cardsphere's name for the same one |
| `Excellent`, `EX`, `Good`, `GD`, `G` | `lp` | Cardmarket values, folded |
| `Light Played` | `lp` | **see the ambiguity below** |
| `Moderately Played`, `MP` | `mp` | |
| `Played`, `PL` | `mp` | Cardmarket's bare "Played" |
| `Heavily Played`, `HP` | `hp` | |
| `Damaged`, `DMG`, `D` | `dmg` | |
| `Poor`, `PO` | `dmg` | |
| *(blank)* | `unknown` | the ordinary case, not a loss |
| anything else — `Pristine`, `BGS 10`, `graded 9.5` | `unknown` | **not** a guess; see [graded-cards.md](graded-cards.md) |

Case and underscores do not matter: `near_mint`, `Near Mint` and `NEAR MINT` are
the same value. That is what makes ManaBox's `near_mint` and Moxfield's
`Near Mint` land together.

**The one real ambiguity.** Cardmarket's `Light Played` sits a step *below*
TCGplayer's `Lightly Played`, and the two strings are nearly identical. Both fold
to `lp` — the commoner reading. This is a deliberate choice, and a cheap one:
condition does not affect value in hoard, so a value that lands one step generous
mislabels a card but cannot misprice it.

**Anything hoard cannot place becomes `unknown`, never a guess.** A professional
grade (`BGS 10` — a different concept, see [graded-cards.md](graded-cards.md))
or a vocabulary hoard does not know is not silently rounded to
near mint — it is recorded as unsaid and reported, so you can see it happened.

A folded value is **stored, not reported**: the card keeps a condition, which is
what was at risk. Only an unplaceable one is counted as dropped — see below.

### What an import drops, and why it tells you

**Condition is stored**, including where a seven-value scale folds onto hoard's
five: the card keeps a condition, which is the thing that was at risk of being
lost. Only a value hoard could not place at all — a professional grade, or a
vocabulary it does not know — is counted and reported:

```
Imported 4 cards (manabox format): 3 rows resolved.
  4 into Trade (new binder)
  Dropped condition on 1 rows: hoard could not carry it.
```

Two columns still carry information hoard has nowhere to put: **language** on a
non-English row, and **purchase price**. Rather than discard them silently, the
importer counts every cell that actually says something and reports the totals
when the import finishes.

The canonical hoard format round-trips its conditions exactly — `hoard export`
then `hoard import` reproduces every bucket — and a test asserts it drops
nothing.

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
`Condition` is the holding's own, in Moxfield's vocabulary (`Good (Lightly
Played)` for `lp`, `Played` for `mp`), so the file re-imports into hoard
cleanly. An unassessed row sends `Near Mint`: the column is required, and that
is what this export claimed for every row before conditions were stored.
`Language` is the printing's own, falling back to
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
