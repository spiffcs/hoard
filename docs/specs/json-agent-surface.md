# The JSON surface as an agent input — 2026-08-10

Exercised against a real collection (1,670 distinct printings, 2,417 copies,
$6,299.73, 22 decks) by trying to *be* the agent: emit each document kind, then
answer the questions a collector actually asks and see which ones the data
supports.

Findings are split into what works (the demo material), one bug, and two
feature requests. Line numbers are as of `73721e4`.

## What works, and is worth demonstrating

**The envelope is self-describing.** Every document opens with
`schemaVersion` + `kind`, so a consumer knows what it is holding before it
parses anything.

**`hoard schema --kind holdings` is 3.9 KB.** The full schema is 22 KB, but the
narrowed form is small enough to paste into a prompt alongside the data — the
envelope, that kind's payload, and only the definitions they reach. This is the
strongest part of the story: the tool ships its own contract, so an agent can be
handed the shape without any card data leaving the machine.

**The numbers reconcile across kinds.** Summing `priceUsd * count` over
`export --format json` gives $6,299.73, matching `report --json`'s
`total.valueUsd` exactly. Independent code paths, same answer.

**Identifiers are the ecosystem's own.** Every row carries `scryfallId` and
`mtgjsonUuid`, so a document joins directly against Scryfall bulk data or
MTGJSON AllPrintings without a name-matching step.

Sizes on this collection: `holdings` 937 KB · `movers` 599 KB · `report` 4.9 KB
· `unpriced` 410 B · `watch` 102 B.

Questions answerable from `export --format json` alone, verified by running
them: total value, copies, foil share (24.2%), value by container, top holdings,
biggest movers, sets by weight, and which cards appear in more than one
container (186 here).

## Bug 1 — `colorIdentity` contradicts the schema it ships

`internal/hoardjson/hoardjson.go:87`

```go
ColorIdentity []string `json:"colorIdentity,omitempty"`
```

The doc comment three lines above it defines two distinct meanings:

> WUBRG letters, **empty for a colorless card**, **absent when the identity is
> not known to hoard**

`omitempty` omits an empty slice, so a known-colorless card is emitted as
*absent* — which the schema defines as "not known". The two states are
indistinguishable in the output, and the shipped `hoard schema` tells a consumer
they are different.

Measured: **339 of 1,769 rows** are missing the key. All are genuinely colorless
— Aether Vial, Ancient Tomb, Batterskull, Blast Zone, Blinkmoth Nexus, City of
Brass. The database has it right (`color_identity` is `'[]'` for Sol Ring and
Wastes, `'["B"]'` for Swamp); the loss happens at encode time.

An agent trusting the schema will classify 359 copies of colorless cards as
"identity unknown" and either drop them from a color breakdown or report them as
a data-quality problem that does not exist.

**Fix: `*[]string`, and NOT "drop `omitempty`"** — that first suggestion was
wrong and is corrected here so nobody acts on it. `schemagen/gen.go:39` states
the rule: *"fields without omitempty are required"*. Removing the tag would
make `colorIdentity` required on every card object — a breaking model bump —
and a nil slice would then emit `null`, failing the field's own
`"type": "array"` constraint. A pointer keeps the field optional, and reflects
byte-identically, so the generated schema does not move at all.

Also worth knowing before touching this file: `AddGoComments` pipes doc
comments into `description`, so **extending the field's comment alone moves two
schema files**. Rationale about the Go representation belongs on the converter,
not on the field.

Note the
neighbouring fields are NOT affected and should not be changed — `Condition` and
`Lang` are `omitempty` strings whose doc comments give absence a single meaning
("nobody has assessed it", "not stored"), so zero value and absence agree. This
is the one field where they disagree. The pointer-typed `omitempty` fields
(`*float64` prices, the `*Summary`/`*Holdings` payload slots) are correct by
construction.

**Regression test:** a holding of a colorless printing must serialize
`"colorIdentity": []`, and the round trip must keep it distinct from a card
whose identity was never fetched.

**Consequence, measured 2026-08-10: the merge hash moves.** `merge.go:130`
hashes `res.Document`, which embeds the holdings verbatim, so emitting `[]`
where the key was absent changes the hashed bytes:

    before  82840d63c3d01f761709d121a09c3bc1275fe1334fa1cb5eaaff3b9c433b3339
    after   1f475e332d6292c486fca45cfc45f03c26997bc9d22d163ee52dfbaec035f17f
    13046162 -> 13057316 bytes (+11154, one line per affected row)

The documents are otherwise deepequal. So a source database merged under an
older binary hashes differently under the new one: `RefuseReimport` finds no
ledger row, and re-merging an *unchanged* source is permitted, doubling every
quantity. One-time, at this version boundary, for any source holding a
colorless card — which is all of them.

Two things bound it. `merge.go`'s own comment already concedes the guard is
this fragile ("edit the candidate at all and the hash moves… `--dry-run` before
a re-merge is the only real check"), and the owner's real database has 23
ledger rows, **none of them a `.db`** — no merge has ever been recorded there,
so today's blast radius is zero.

There is no way to fix the encoding bug without moving those bytes, short of
hashing a canonicalized form. That lives in `internal/action` and is separate
work; the alternative is a release note.

**Getting the hashed bytes:** `merge --dry-run -o FILE` writes `res.Document`
itself, which is exactly what `ContentHash` consumes — so a hash question here
can be measured rather than argued.

## Feature request 1 — the export carries no card characteristics

This is the big one for the stated use case, and it is cheap.

`export --format json` emits 13 fields per row: `name`, `scryfallId`,
`mtgjsonUuid`, `setCode`, `number`, `finish`, `lang`, `colorIdentity`, `count`,
`container`, `containerKind`, `board`, `priceUsd`.

None of them says what a card *is*. So these all fail:

- "How many creatures do I own?"
- "What is my mana curve?"
- "What is my rare-to-common ratio, and where is the value concentrated?"
- "Which of my cards mention graveyard recursion?"
- "What is my oldest printing?"

Meanwhile the store already holds all of it, as VIRTUAL generated columns over
`raw_json` — zero extra storage and no extra fetch.

**Correction (2026-08-10): "100% coverage" below was the wrong shape of claim.**
What is ~100% is the *source*: `raw_json` is non-null on every enriched card.
The derived columns are present only where a card actually has such a value, so
coverage varies enormously — `rarity`/`type_line`/`cmc`/`oracle_text` track
`raw_json`, but `power`/`toughness` are 42.9%, `flavor_text` 55.8%, `loyalty`
1.2%, and `printed_name` is **0%** — present on nothing in this collection.
That matters for the design: a field's cost is proportional to its coverage,
and a field at 0% is pure schema surface.

    rarity          type_line       cmc             mana_cost
    oracle_text     set_name        released_at     artist
    layout          power           toughness       loyalty
    flavor_text     promo_types     printed_name    tcgplayer_id

Answered from the same database in one query, to show what the export is
leaving behind:

| question | answer |
|---|---|
| by type | Land 818, Creature 765, Artifact 222, Sorcery 191, Instant 165, Enchantment 119 |
| by rarity | common 928, rare 750, uncommon 566, mythic 173 |
| curve (nonland) | 1:177 2:359 3:356 4:255 5:182 6:128 7:72 |
| value by rarity | rare $2,900 · mythic $1,079 · uncommon $682 · common $395 |

That last row is the kind of insight the demo wants — "half your value is in
rares, and your commons are 38% of your copies" — and it is unreachable through
the JSON surface today.

**Direction, to decide deliberately:** these are card-level attributes, not
holding-level, so adding all of them to every row inflates a 937 KB document
substantially through repetition. Options, in preference order:

1. **`--fields` on `export`** — opt in to the attributes a question needs, so
   the default document stays small and an agent asks for the curve fields only
   when computing a curve.
2. **A `cards` block beside `rows`** — each distinct printing once, keyed by
   `scryfallId`, with `rows` referring into it. Removes the repetition entirely
   and is the natural shape for a document that already has 1,769 rows over
   1,670 printings.
3. Add the seven high-value fields inline and accept the size.

**Superseded 2026-08-10 — option 2's premise was measured and does not hold.**
See `docs/specs/json-format-v2.md`. The repetition this section assumed is only
6% (1,769 rows over 1,667 distinct printings, a ratio of 1.0612), while an
empty catalog block costs a fixed ~130 KB of structure. So the block is
*larger* than inlining, not smaller — 1,735,667 bytes against 1,662,261. The
crossover is at a ratio of about 1.079 and this collection sits below it.

Size therefore could not decide it, and the design was settled on blast radius
instead: a `detail` object beside `card` on the holdings row, rather than fields
on the shared `Card` type, because `Card` is reused by `movers`, `report`,
`market`, `unpriced` and `watch` — putting them there would nearly double
`movers` and grow five schemas, or leave the fields silently absent in five
places, which is five fresh instances of Bug 1.

A further argument against the catalog block, independent of size: `detail` on
the row survives an NDJSON mode intact, and a catalog block cannot.

## Feature request 2 — size, for collections larger than this one

937 KB at 1,670 printings is roughly 250K tokens — already past a comfortable
prompt, and this is a small collection. A 10,000-card collection lands near
6 MB.

The document is also a single JSON object, so a consumer must parse all of it
before seeing the first row.

**Direction:** an NDJSON mode (one holding per line) would let an agent stream
and filter without a full parse, and pairs naturally with `--fields`. Worth
noting `import` already reads line-oriented formats, so this is not a foreign
shape for the tool.

## Not a bug, recorded so it is not re-investigated

- `hoard export --format json` is the `holdings` kind. The `hoard` kind is the
  DB→DB merge document and is reached through `merge`, not `export`.
- `--json` and `--format json` coexist deliberately; the one ambiguous
  combination is a clean usage error.
- `priceUsd` is per copy. Totals need `priceUsd * count` — confirmed against
  `report`.
- Prices are the printing's price whatever the copy's condition, and
  `Condition` is documented as never affecting a value in these documents.
