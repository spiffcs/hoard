# JSON format v2 — card characteristics in the holdings document

A design, not an implementation. It answers one question: how does hoard's JSON
carry what a card *is*, so an agent handed a document can compute a curve, a
rarity breakdown or a text search without a network fetch.

Every size in here was measured against the owner's real collection, not
estimated. Method is in the last section so any number can be re-run.

Written against `7883a51`, alongside `docs/specs/json-agent-surface.md`
(untracked, main worktree), whose "Feature request 1" this answers.

## The decision, up front

Carry **all sixteen** printing characteristics on the **holdings row only**, as
a new optional `facts` object beside the existing `card` object. Do not put them
on the shared `Card` type. Do not add a `cards` catalog block.

```json
{
  "card": { "name": "Sol Ring", "scryfallId": "…", "setCode": "c21", … },
  "facts": {
    "rarity": "uncommon",
    "typeLine": "Artifact",
    "cmc": 1,
    "manaCost": "{1}",
    "oracleText": "{T}: Add {C}{C}.",
    "setName": "Commander 2021",
    "releasedAt": "2021-04-23",
    "artist": "Mike Bierek",
    "layout": "normal",
    "flavorText": "…",
    "promoTypes": ["surgefoil"],
    "tcgplayerId": 235854
  },
  "count": 3,
  "container": "Binder",
  "containerKind": "binder",
  "board": "main",
  "priceUsd": 1.55
}
```

`schemaVersion` goes to **1.1.6** — an ADDITION, not a break. See
[schemaVersion](#schemaversion-116-and-the-rule-going-forward) for why a MODEL
bump would actively harm.

| | today | **all 16 in `facts`** | A: on `Card` | C: `cards` block |
|---|---|---|---|---|
| holdings document | 959,393 | **2,035,780** (2.12×) | 1,984,508 (2.07×) | 2,023,354 (2.11×) |
| `schema --kind holdings` | 3,881 | **6,727** | ~6,400 | ~6,900 |
| other kinds' schemas | — | **byte-identical** | +2,100 each × 5 | byte-identical |
| `movers` document | 598,882 | **byte-identical** | ~1.16 MB | byte-identical |
| `hoard` (merge) document | 8.5 MB | **byte-identical** | grows unless split | grows unless split |
| merge content hash | — | **unmoved by this change** | moves | moves |
| SchemaVer | 1.1.5 | **1.1.6 ADDITION** | 1.1.6 ADDITION | 1.1.6, or 2.0.0 if rows go lean |

The shape is decided by blast radius, not by bytes; the field *set* is decided
by the asymmetry below. Confirmed by measurement, not assumption: with all
sixteen in `facts`, `unpriced` (3,627), `movers` (4,416), `market` (6,622),
`report` (7,484), `watch` (3,921) and `summary` (2,727) all return **exactly**
their current schemas.

## Why all sixteen, and not a curated subset

An earlier draft of this document cut six fields to save 322,247 bytes. That was
wrong, and the reasoning it used — cost per field against a question I could
name — was the wrong test. Three things make the inclusive answer correct:

**The costs are not symmetric.** Adding a field now is nearly free: nothing has
been released, and the holdings document is write-only (measured below —
`merge.go:140` is the only reader in the tree and `collsource` has no JSON
branch), so no consumer has to be migrated. Adding one *later* costs a
schemaVersion bump and a re-export for anyone who cached. Inclusion is the
reversible direction. A cut spends a one-way option to buy back bytes.

**Every field is `omitempty`, so cost is proportional to coverage.** This
inverts the case against the sparse fields entirely. `printedName` is present on
**0%** of what the owner holds and therefore costs **literally zero bytes**
today — while being exactly what a collector needs the first time he buys a
Japanese card. Cutting a field that costs nothing is a strictly losing trade.
`loyalty` is the same argument at 1.2% coverage and 520 bytes.

**Size was never the binding constraint.** The document was already past
promptable at 959,393 bytes, before any of this. What makes the surface usable
is the narrowing that already exists (`--binder`, `--deck`) and the streaming
mode in Feature request 2 — not a smaller field list. Trading query power
against a number that is already over the threshold buys nothing.

So the test is no longer "can I name the question this answers". It is: **does
this field fail on grounds other than bytes** — unstable, inconsistently
derived, or genuinely duplicated by a field already present? Applied to all
sixteen, **none of them does**, and each has a plausible query behind it:
`artist` (which artists do I own most of), `promoTypes` (variant identification,
which moves prices), `layout` (split and transform cards in deck analysis),
`flavorText` (thematic search), `tcgplayerId` (a marketplace join key).

Two fields survived the test with a caveat worth recording rather than acting
on. `layout` is nearly — but not quite — implied by `typeLine`: only 12 of
1,666 owned printings are non-`normal` (5 transform, 4 modal_dfc, 2 leveler, 1
split) and `typeLine` reveals 10 of them by carrying `//`, so it earns its place
on the two it does not. `promoTypes` overlaps `finish`, which hoard already
derives from it via `FoilTreatment`, but it is strictly more information than
the derived value, not a restatement of it.

### The one real counterargument

All sixteen makes the document **2,035,780 bytes** — 2.12× today, roughly
510K tokens. `oracleText` alone is 302,607 of that (28% of the payload added),
and the six fields restored here add 322,247.

That is a real cost and it is stated here rather than buried: if this document
is ever meant to be pasted somewhere whole, sixteen fields make it worse than
ten, which was already worse than a baseline that did not fit. The answer is
that the whole-document paste is not a workflow any field list rescues. The
workflows that do work are unaffected: `hoard schema --kind holdings` stays at
6,727 bytes, and one 100-card deck via `export --deck` measures 121,645 bytes —
about 30K tokens, comfortably promptable, against 95,620 at ten fields and
55,944 today.

## Reproducing the investigation first

`json-agent-surface.md`'s numbers hold. Re-measured on the owner's database:

| claim | measured | verdict |
|---|---|---|
| holdings 937 KB | 959,393 bytes | ✓ |
| movers 599 KB | 598,882 bytes (`--since 30d`) | ✓ |
| report 4.9 KB | 4,866 bytes | ✓ |
| `schema --kind holdings` 3.9 KB | 3,881 bytes | ✓ |
| full schema 22 KB | 21,988 bytes | ✓ |
| value by rarity | rare $2,900.46 · mythic $1,079.37 · uncommon $682.21 · common $394.87 | ✓ exact |

Two corrections, neither fatal to its argument:

**"100% coverage (1,670/1,670) for every field below" is the wrong shape of
claim.** What is 100% is the *source*: every printing has a stored Scryfall
document (`raw_json` non-null on 1,670/1,670). The derived columns are present
only where the card has such a value, which is the correct behaviour and not
100%:

    rarity 100%   typeLine 100%   cmc 100%   setName 100%   releasedAt 100%
    artist 100%   layout 100%     oracleText 99.9%   tcgplayerId 99.9%
    manaCost 79.7%   flavorText 56.1%   power 43.2%   toughness 43.2%
    promoTypes 25.7%   loyalty 1.2%   printedName 0%

(over the 1,666 owned printings). `printedName` is present on **nothing** the
owner holds — it only exists on non-English printings. A field that is always
absent is schema surface with no data behind it.

**The row and printing counts have drifted slightly.** The document now holds
1,768 rows over 1,666 distinct printings, against the doc's 1,769/1,670. The
1,670 is the catalog size (`cards` has 1,670 rows); 1,666 of them are owned.
This matters because the whole size argument turns on the ratio between them.

## The tension, measured: repetition is 6%, and 6% is not the problem

The premise for a `cards` block is that repeating printing data across rows is
wasteful. Measured, the waste is small:

    1,768 rows ÷ 1,666 distinct printings = 1.0612

Six percent. Meanwhile a catalog block is not free — it pays a fixed structural
cost of one object and one repeated `scryfallId` per printing. Measured by
adding a field that is 0% populated (`printedName`), so the only thing being
measured is the structure:

    empty cards block over 1,666 printings = +129,969 bytes

That overhead has to be recovered out of a 6% saving, and on this collection it
is not. Every single field is cheaper inline than in a block:

| field | inline (bytes added) | in a `cards` block | inline ÷ block |
|---|---|---|---|
| oracleText | 302,607 | 417,875 | 0.72 |
| flavorText | 113,309 | 234,451 | 0.48 |
| setName | 86,741 | 208,500 | 0.42 |
| typeLine | 74,960 | 197,688 | 0.38 |
| releasedAt | 67,184 | 189,945 | 0.35 |
| artist | 65,411 | 188,251 | 0.35 |
| tcgplayerId | 58,003 | 181,280 | 0.32 |
| layout | 53,068 | 176,645 | 0.30 |
| rarity | 52,792 | 176,343 | 0.30 |
| manaCost | 44,706 | 170,750 | 0.26 |
| cmc | 35,365 | 159,962 | 0.22 |
| promoTypes | 32,456 | 156,905 | 0.21 |
| toughness | 20,461 | 148,684 | 0.14 |
| power | 17,532 | 145,799 | 0.12 |
| loyalty | 520 | 130,449 | 0.00 |
| printedName | 0 | 129,969 | 0.00 |

Costs are additive, and that is worth checking rather than assuming: the sixteen
per-field figures sum to 1,025,115, and all sixteen measured together in the
same shape add exactly 1,025,115 (959,393 + 1,025,115 = 1,984,508, the measured
inline document).

**Where the block would start winning — and it moved when the field set grew.**
Replicating real rows over the same 1,666 printings to raise the duplication
ratio, then comparing per-row against the most aggressive block form (catalog
plus rows stripped to a bare reference):

At **ten** fields, inline won today and the crossover sat just above the
collection:

| rows ÷ printings | rows | inline | catalog + lean rows | winner |
|---|---|---|---|---|
| 1.0612 (today) | 1,768 | 1,662,261 | 1,688,409 | inline by 26,148 |
| 1.0732 | 1,788 | 1,682,214 | 1,693,914 | inline by 11,700 |
| **1.0852** | 1,808 | 1,700,583 | 1,699,405 | **block** by 1,178 |
| 1.2 | 2,068 | 1,944,416 | 1,770,881 | block by 173,535 |

At **sixteen**, the crossover has moved *below* the collection's ratio — the
block's fixed 129,969 bytes of structure are now recovered by deduplicating a
much larger payload:

| rows ÷ printings | rows | `facts` per row | catalog + lean rows | winner |
|---|---|---|---|---|
| 1.0612 (today) | 1,768 | 2,035,780 | 1,976,096 | **block by 59,684** |
| 1.0972 | 1,828 | 2,104,617 | 1,992,581 | block by 112,036 |

This is a real change and it is recorded rather than smoothed over: **carrying
more fields makes the catalog block look better, not worse.** It does not change
the recommendation, for two reasons that are not about bytes.

First, that 59,684-byte win belongs to the *breaking* variant. The like-for-like
non-breaking comparison — per-row against a catalog with rows left intact — is
1,984,508 against 2,023,354, still 38,846 in favour of keeping the data on the
row. The block only gets ahead by stripping `name`, `setCode`, `number`,
`mtgjsonUuid`, `lang` and `colorIdentity` out of every row, which is a MODEL
bump and locks out every installed build.

Second, a catalog forecloses the streaming mode in Feature request 2. A
whole-document structure cannot be expressed one-holding-per-line without either
repeating it or forcing two passes; `facts` on the row survives NDJSON intact.

So the honest summary is that size never decides this. At ten fields it was
close, at sixteen it tilts toward the block, and in both cases the decision is
made by blast radius, the MODEL boundary, and NDJSON compatibility. The
crossover is now below 1.0612 and worth re-deriving only if the MODEL cost is
ever being paid for some other reason anyway.

## Why B wins, and what A and C lose on

### A — put the fields on `Card`

`Card` is a shared value type. It is embedded in `Holding`, `UnpricedRow`,
`PriceChange`, `Comp`, `Opportunity`, `ReportHolding`, `FiredWatch` and `Watch`.
A field added there lands in all eight kinds at once, and the generator emits
one `$defs/Card` that every narrowed schema pulls in.

A is the smallest document (1,984,508 with all sixteen) and the smallest
holdings schema. It loses on blast radius, measured:

- **`movers` nearly doubles** — 598,882 → 1,163,051 at ten fields (+564,169),
  more at sixteen. Nobody asked for that.
- **Five other kinds' schemas grow with it** — at ten fields, `unpriced` 3,627 →
  5,101, `movers` 4,416 → 5,890, `market` 6,622 → 8,096, `report` 7,484 →
  8,958, `watch` 3,921 → 5,395; roughly 2,100 each at sixteen.
- **Six store paths, not one.** The card data reaching `movers`, `market`,
  `report`, `watch` and `unpriced` comes from five different queries. Either all
  five grow, or the fields are silently absent in those kinds.

That last branch is the disqualifying one. Leaving them absent ships *exactly
the defect the same investigation filed as Bug 1*: a schema that tells a
consumer a field may be there when the encoder never writes it. Fixing hoard's
one instance of that while creating five more is not a trade worth making.

### C — a `cards` catalog block beside `rows`

C is the better data model and it is the one hoard already uses elsewhere: the
`hoard` merge kind carries `printings[]` as a catalog and refers into it from
holdings rows, and `planMerge` resolves every row through that catalog rather
than through the row's own fields. So this is a proven shape in this codebase.

It still loses, though by less at sixteen fields than at ten:

- **Not smaller, like for like** — 2,023,354 with rows unchanged, against
  1,984,508 for the same sixteen carried per row with no wrapper. (Compare
  wrapper-to-wrapper and it is 2,023,354 against 2,035,780, a 0.6% gap — noise.)
  The repetition it removes (6%) still does not pay for the structure it adds
  (129,969 bytes).
- **Getting it smaller costs a MODEL break.** The only variant that wins
  (1,976,096) requires stripping `name`, `setCode`, `number`, `mtgjsonUuid`,
  `lang` and `colorIdentity` out of every row. Removed fields are a MODEL bump —
  2.0.0 — and `read.go` gates on MODEL, so every older hoard would refuse the
  documents outright.
- **Forecloses NDJSON.** A whole-document catalog cannot be written one holding
  per line.
- **Largest schema** — `--kind holdings` 5,906 against 5,668 at ten fields.
- **Two ways to say the same thing.** Rows keep `card.name` while the catalog
  also has it; a consumer has to be told which is authoritative.

C becomes right if the MODEL boundary is being crossed for some other reason, or
if a second per-printing consumer appears. Neither is true now.

### B — a `facts` object on the holdings row

B repeats printing data per row exactly as A does. It is not a better *model*
than A; it is the same model with a blast radius that stops at the payload that
asked for it. That is the whole case for it, and it is enough:

- `unpriced`, `movers`, `market`, `report`, `watch` and `summary` schemas come
  back **byte-identical** — measured, not assumed.
- One converter (`FromExportRows`) and one store column list (`cardCols`).
- The merge document does not move (below).

The honest cost: `.facts.typeLine` is a slightly worse jq path than
`.card.typeLine`, and the extra nesting is most of the 51,272 bytes B pays over
A. Both are real. Neither is worth 564,169 bytes of `movers` and five schemas.

**On the name.** `facts` is deliberately not a rules term. The Magic
comprehensive rules define "characteristics" narrowly (name, mana cost, type,
power, toughness, loyalty, rules text) — it excludes rarity, set name and
release date, which are properties of a *printing*. This set spans both, so
naming it `characteristics` would misuse a term of art. `card` says which
printing; `facts` says what is known about it.

## The sixteen fields, costed

Cost is per field, measured one at a time against the 959,393-byte baseline, and
additive (see above). Coverage is over the 1,666 owned printings, and because
every field is `omitempty`, **cost tracks coverage** — which is why the bottom
of this table is nearly free.

| field | bytes | coverage | what it is for |
|---|---|---|---|
| `oracleText` | 302,607 | 99.9% | text search — "which cards mention graveyard recursion" |
| `flavorText` | 113,309 | 56.1% | thematic search; the only prose axis a collection has |
| `setName` | 86,741 | 100% | makes a by-set breakdown readable without a code table |
| `typeLine` | 74,960 | 100% | "how many creatures", land counts, tribal |
| `releasedAt` | 67,184 | 100% | "what is my oldest printing"; set codes do not sort |
| `artist` | 65,411 | 100% | "which artists do I own most of"; proofs and signings |
| `tcgplayerId` | 58,003 | 99.9% | marketplace join key — see the placement note below |
| `layout` | 53,068 | 100% | split/transform handling in deck analysis |
| `rarity` | 52,792 | 100% | rare-to-common ratio, value by rarity |
| `manaCost` | 44,706 | 79.7% | colour pips — `cmc` gives none |
| `cmc` | 35,365 | 100% | the curve |
| `promoTypes` | 32,456 | 25.7% | variant identification, which moves prices |
| `toughness` | 20,461 | 43.2% | creature stats |
| `power` | 17,532 | 43.2% | creature stats |
| `loyalty` | 520 | 1.2% | planeswalker stats — 520 bytes for the whole collection |
| `printedName` | **0** | **0%** | the name as printed on a non-English card — free today |

The field bytes sum to **1,025,115**. The document adds **1,076,387** — the
extra 51,272 is the `facts` wrapper itself (one more object and one more
indent level on 1,768 rows), giving a 2,035,780-byte document. That wrapper is
the price of the blast-radius property argued above, and it is charged once, not
per field.

`oracleText` is 28% of the payload on its own and is the field most worth
questioning. It stays because dropping it is the one cut that breaks the
surface's defining property: the reason `hoard schema --kind holdings` is worth
anything is that a model can be handed the contract and analyse the data
locally, *without card data leaving the machine*. A document that requires a
Scryfall bulk download to answer "which of my cards mention graveyard
recursion" has given that up. 302 KB on disk is cheaper than a network
dependency.

### Placement: `tcgplayerId` is an identifier, and still belongs in `facts`

It is a fair objection — `card` already carries `scryfallId` and `mtgjsonUuid`,
so an identifier looks like it belongs beside them. It should still go in
`facts`, for three reasons, two of which are measured.

**The blast-radius argument applies to it exactly as to everything else.**
`Card` is shared by eight kinds. Moving one field there to satisfy a taxonomy
grows five other kinds' schemas and documents for a field none of them needs.
Nothing about this field is special enough to pay that.

**`card`'s identifiers are hoard's own join vocabulary; this one is not.**
`schema/json/README.md` names exactly what `card` commits to joining against —
`scryfallId`, `mtgjsonUuid`, `setCode`, `number`, `finish`, `lang`, `condition`.
Those are stored columns hoard resolves and merges on. `tcgplayer_id` is a
VIRTUAL generated column over `raw_json`, like the other fifteen. That is the
line `facts` actually draws, and it is testable rather than aesthetic: **`facts`
is what hoard derives from the printing's Scryfall document; `card` is the
identity hoard itself stores and joins on.**

**It is finish-ambiguous, and `card` is not.** A holdings row is a
printing-*and-finish*; `scryfallId` + `finish` names a copy exactly. A TCGplayer
product id is printing-level, and TCGplayer mints a separate product for etched.
Placing a finish-ambiguous id beside finish-exact ones would be actively
misleading. In `facts` — defined as printing-level throughout — it is simply
accurate.

Measured while checking this, and worth recording because it removes a
follow-up rather than creating one: the store has a separate
`tcg_etched_product_id` column, but it is **non-empty for zero owned
printings** (all 174 non-null values are the empty string), and neither of the
two etched holdings has one. There is no live alternative id to choose between.
`tcgplayer_id` and the stored `tcg_product_id` both cover 1,664 of 1,666 and
**never disagree**. So one id, printing-level, documented as such.

That also settles a seventeenth-field question before it is asked: adding
`tcgplayerEtchedId` would carry no data, and it is finish-specific, so it would
break the printing-level invariant that keeps `facts` coherent (and keeps a
catalog block possible if the ratio ever crosses). If a future refresh
populates it, it belongs beside `finish` on the row, not in `facts` — a
separate decision with its own version bump.

## schemaVersion: 1.1.6, and the rule going forward

**This is an ADDITION.** `schema/json/README.md` defines ADDITION as "a new
optional field or document kind", and the changelog already contains this exact
change once: **1.1.1 added `card.colorIdentity`** to the holdings, unpriced and
movers card objects and versioned it as an ADDITION. `facts` is optional, its
absence means "hoard has not stored this printing's document", and nothing is
renamed, removed or redefined. `1.1.5 → 1.1.6`.

**Breaking is allowed here but would do damage.** `read.go` compares only the
MODEL component and refuses a document whose MODEL is higher than the build's.
A MODEL bump for a purely additive change means every hoard already in a user's
hands refuses merge documents in which every field it reads is unchanged. The
cost is real and the benefit is nil. MODEL is a compatibility kill switch, not a
version number for the size of a release.

There is one genuine wrinkle worth stating rather than hiding: the generated
schema sets `additionalProperties: false`, so a consumer validating a 1.1.6
document against the pinned 1.1.5 schema *will* fail on the unknown `facts`
key. That is inherent to every ADDITION this project has already shipped
(1.0.1, 1.0.2, 1.1.1, 1.1.2, 1.1.3, 1.1.4, 1.1.5), and the rules already handle
it: the versioned schema files are immutable, so a consumer holding an old
document can always fetch the schema it was written against. No change needed.

**The rule going forward**, proposed as three lines under `## Rules` in
`schema/json/README.md`, because the recurring case is not covered explicitly:

- A new card characteristic, optional and with absence meaning *unknown*, is an
  **ADDITION**.
- Changing what an existing *absence* means — a field that used to be omitted
  now emitting a value — is a **REVISION**. Existing consumers keep working but
  their reading of the data changes.
- **MODEL** stays reserved for renames, removals and meaning changes, and is
  the only component enforced at read time. Bumping it locks out older builds;
  do not spend it on a release that is merely large.

**Coordination with the `colorIdentity` lane.** That fix makes 338 rows emit
`[]` where they previously emitted nothing, while leaving the generated schema
byte-identical. A byte-identical schema is not the same as an unchanged
document: a consumer that recorded "339 rows have unknown colour identity" gets
different data out of the same hoard. By the rule above that is a REVISION. If
both land in one release, the combined version is **1.2.0** and this document's
fields fold into that changelog entry; if the fix ships judged as no bump at
all, this is **1.1.6**. Either way these fields never force MODEL.

## What happens to `import` and `merge`

### `import` — nothing, because it cannot read these documents

Measured, not assumed: `internal/collsource` parses CSV only (ManaBox,
Moxfield, Delver, hoard's own canonical CSV), dispatched by header row.
`internal/command/import.go` contains no JSON branch at all. `--format json`
output has **no reader anywhere in the tool**.

The only consumer of any hoardjson document is `hoardjson.Read` /
`ReadHoard`, called from exactly one place — `internal/action/merge.go:140` —
and only for the `hoard` kind.

So the holdings document is write-only, and its shape is constrained by no
round trip. That is a large part of why this change is cheap.

### `merge` — the hash guard survives, and this design keeps it still

The guard is `internal/action/merge.go:125-135`: the document bytes are
SHA-256'd (`action.ContentHash`) and `RefuseReimport` rejects a source already
in the ledger, because merging twice doubles every quantity. The model's doc
comment is right that the document must stay content-only — a path or a
timestamp would change the hash on every run and silently disable it.

Derived card characteristics are content, and deterministic given the same
source database, so nothing about them breaks the guard's mechanism. The hazard
is narrower and one-shot: **any change to the emitted bytes moves every hash**,
so a source merged under the old format no longer matches its ledger row and a
re-merge would double it.

This design avoids contributing to that entirely, because **the `hoard` kind
must not carry `facts` at all**. It already carries strictly more: every
`Printing` embeds the verbatim Scryfall document in `raw` — measured at
**8,482,662 bytes across 1,670 printings, 100% non-null**. Every one of the
sixteen fields is already in there, authoritative, and `planMerge` reads
identity straight out of it. Emitting derived copies beside it would add ~700 KB
of redundancy to an 8.5 MB document *and* move every ledger hash, for nothing.

The implementation consequence: `FromSnapshot` embeds the holdings via
`FromExportRows` (`convert.go:166`), the same call `export` uses. It must clear
`Facts` on the rows it embeds, with a comment naming the hash guard as the
reason. With `Facts` unset and `omitempty`, the hoard document is byte-identical
to today and every ledger row stays valid.

Note this hazard is already live this release regardless: the `colorIdentity`
fix moves the hash on the owner's real database (`82840d63…` → `1f475e33…`,
measured by that lane). It is bounded — the owner's `import_ledger` holds 23
rows and none is a `.db` — but it should be one release note, not two. To
inspect the exact bytes `ContentHash` consumes, run `merge --dry-run -o FILE`.

## Generation and `--kind` slicing

**The shape survives generation, and slicing still works.** Both were measured
by building each variant and running the real `hoard schema`.

`sliceSchema` keeps the envelope, the named kind's payload, and the transitive
closure of `$defs` those reach. Under B, `CardFacts` is reachable only from
`Holding`, so:

| `--kind` | today | 10 fields | **all 16** |
|---|---|---|---|
| holdings | 3,881 | 5,668 | **6,727** |
| unpriced | 3,627 | 3,627 | **3,627** |
| movers | 4,416 | 4,416 | **4,416** |
| market | 6,622 | 6,622 | **6,622** |
| report | 7,484 | 7,484 | **7,484** |
| watch | 3,921 | 3,921 | **3,921** |
| summary | 2,727 | 2,727 | **2,727** |
| hoard | 8,131 | 9,918 | 10,977 |
| full | 21,988 | 23,775 | 24,834 |

Six kinds coming back byte-identical *is* the proof that `reachableDefs` walks
correctly and the slice stays closed — and it holds at sixteen fields exactly as
at ten, because `CardFacts` is reachable only from `Holding`.

**`--kind holdings` at 6,727 bytes settles the size objection.** That is the
number that mattered: the whole point of this surface is that a model can be
handed the contract without any card data leaving the machine, and 6.7 KB is
still trivially promptable. Sixteen fields cost 1,059 bytes more than ten and
2,846 more than today. Nothing here approaches the 20 KB that would have damaged
the use case.

Two generation constraints this shape has to respect:

- **`schemagen` makes fields without `omitempty` required.** Every added field
  must be `omitempty`, and `cmc` must be `*float64` rather than `float64` — a
  bare zero would be indistinguishable from "no mana value stored", and a
  required `cmc` would be a MODEL break. (This is the same trap the
  `colorIdentity` lane hit: "drop `omitempty`" would have made that field
  required.)
- **Doc comments cost about as much as fields.** `AddGoComments` pipes every
  comment into `description`. Measured on `--kind holdings`: the same ten
  fields cost 5,355 with one-line comments and 5,960 with three-line ones — 605
  bytes of pure prose — while going from ten terse fields to sixteen costs 680
  (about 113 bytes per field). Ten verbose fields (5,960) and sixteen terse
  ones (6,035) land within 75 bytes of each other. Keep field comments to one
  line and put the rationale on the type or the converter; all schema figures
  above use one-line comments.

`hoard` growing to 10,977 while its documents never carry `facts` is the one
loose thread: the schema will declare a field that kind does not emit. It is
legal (optional and absent) but it is the shape of Bug 1, so the `CardFacts`
doc comment must say so outright — "absent in a `hoard` document, where
`printings[].raw` carries the same data verbatim" — which is generated into the
schema description where a consumer will actually see it.

## Scope: the other kinds

They share `Card`, and under this design **none of them changes**: not
`report.topHoldings`, not `movers.changes`, not `unpriced`, not `market`, not
`watch`. Documents and schemas alike, byte-identical.

This is a deliberate narrowing and worth naming as such. Rarity on
`report.topHoldings` or `movers.changes` would be genuinely useful. It is left
out because the holdings document is the complete itemized list — every other
kind is a derived view an agent can compute from it — and because the cost was
measured at +564,169 bytes on `movers` alone even at ten fields, plus five
schemas and five store queries. If those kinds are wanted later, the same
`CardFacts` type extends to them one kind at a time, each its own ADDITION.

## Implementation sketch

Cheaper than it looks, because the store already reads most of this. `cardCols`
(`internal/store/store.go:260`) **already selects `mana_cost` and
`promo_types`** and `store.Card` already has a `ManaCost *string` field — the
export path loads the mana cost out of SQLite today and discards it before the
JSON.

All sixteen candidates are VIRTUAL generated columns over `raw_json`: no extra
storage, no extra fetch, no extra round trip, and `cards_trait_filter` already
indexes several.

1. `internal/store/store.go` — extend `cardCols` with the remaining fourteen
   columns and `store.Card` with the matching fields. Feeds all five queries
   that use it, including both export paths (`BinderByFinish`,
   `DeckEntries`). Note `promo_types` is already selected but reaches
   `store.Card` only as the derived `Treatment`; the raw list is what `facts`
   needs.
2. `internal/export/csv.go` — add the fields to `export.Row`. Follows the
   established "rides along for the JSON emission; no CSV writer carries it"
   pattern already used by `MTGJSONUUID`, `ColorIdentity` and `Lang`. **The
   canonical CSV column set must not change** — it is a compatibility promise
   shared with the import sniffer.
3. `internal/action/read.go` — populate the new fields in `binderRows` and
   `deckRows`.
4. `internal/hoardjson/hoardjson.go` — add `CardFacts` and
   `Holding.Facts *CardFacts`, one-line comments, everything `omitempty`,
   `Cmc *float64`.
5. `internal/hoardjson/convert.go` — populate in `FromExportRows`; **clear it in
   `FromSnapshot`**, commented with the hash guard as the reason.
6. `make generate-json-schema` after bumping `SchemaVersion`, plus the
   `schema/json/README.md` changelog entry and the three versioning rules.

Tests worth having: a holdings row for a printing with no stored document emits
no `facts` key at all (not an empty object); a `hoard` document is byte-identical
to the 1.1.5 output for the same source, so the ledger hash is provably
unmoved; `--kind` for the five untouched kinds returns byte-identical schemas.

## The size problem this does not solve

The document goes from 959,393 to 2,035,780 bytes — roughly 510K tokens. It was
already past a promptable size at 959 KB, and no field selection fixes that;
cutting the six sparsest fields would have saved 322,247 bytes off a document
that is over the line either way.

Two things are true and should not be conflated. If the workflow is *paste the
data into a prompt*, the document is already too big and this makes it worse. If
the workflow is the one `hoard schema` exists for — **paste the 6,727-byte
contract, leave the data on disk, let the agent query it with jq or Python** —
then doubling a local file is close to free, and the fields are what make the
queries answerable at all. This design optimises the second, because the first
is not reachable by any field choice.

The narrowing that already exists is the mitigation: one 100-card deck via
`export --deck … --format json` measures 55,944 bytes today and 121,645 under
this design — about 30K tokens, comfortably promptable.

The real fix for the size axis is the investigation's Feature request 2 (an
NDJSON mode, streamable and filterable without a full parse). It is orthogonal
to this and out of scope here. Worth noting the two compose: `facts` on the row
survives one-holding-per-line intact, whereas a `cards` catalog block does not —
a catalog is a whole-document structure and cannot be expressed in a line-
oriented format without either repeating it or forcing two passes. **If NDJSON
is coming, that is an independent reason not to build option C.**

## What could not be resolved

- **No hardware or downstream validation.** Nothing here was tried against a
  real agent doing a real analysis. Including all sixteen is now argued from the
  asymmetry of the costs rather than from a per-field use case, which is a
  stronger argument precisely because it does not depend on predicting the
  queries — but it is still not an observation.
- **The crossover ratios were reached by replicating real rows**, which raises
  duplication without changing the distribution of card sizes. A collection
  genuinely at 1.2 might sit somewhere slightly different. At sixteen fields the
  crossover has moved below the collection's own 1.0612, so the catalog block
  is no longer rejected on size — only on the MODEL break and NDJSON.
- **`unpriced` measured 89 bytes** against the investigation's 410 B, so
  something has been priced since. Immaterial here, noted so it is not read as
  a regression.
- **`oracleText` at 302,607 bytes remains the single largest line item** — 28%
  of the payload added. It is kept, and the reasoning is in the field table, but
  it is the one field whose inclusion is worth revisiting against a real usage
  observation rather than an argument.
- **Coverage figures are one collection's.** `printedName` costs zero bytes
  *here* because the owner holds no non-English printings; on a collection that
  does, it costs real bytes. The asymmetry argument for including it does not
  depend on that, but the byte figure does.

## Method

Measured 2026-08-10 on the worktree `design-json-fields` off `7883a51`, against
a **copy** of the owner's database (`hoard.db`, 25,489,408 bytes) placed in a
scratch directory. The original was opened read-only for SQL and never by a
hoard command.

Measured in two rounds. The first costed a curated ten-field set; the second,
after the owner pushed back on cutting anything pre-release, re-ran every figure
for all sixteen. Both rounds' numbers are kept where they are informative,
labelled as such — the ten-field figures are what show that the catalog block's
economics depend on how much payload there is to deduplicate.

- Baseline documents came from a binary built from this worktree, run as
  `hoard --db <copy> …`, so they are what hoard actually emits.
- Document variants were built by a Python script that re-encodes the real
  baseline with fields added. It is a faithful stand-in for `hoardjson.Write`:
  re-encoding the untouched baseline reproduces the original **byte for byte**
  (959,393 = 959,393), so indentation, escaping and key order all match.
- Schema figures came from patching a **scratch copy of the repository** — not
  this worktree — into each candidate shape, running the real generator
  (`go run ./schema/json/generate`), building, and running the real
  `hoard schema --kind X`. No file in `internal/hoardjson/` was edited in any
  repository worktree at any point, so there was no possibility of colliding
  with the `colorIdentity` lane working in the same package.
- Field coverage, layout counts and the value-by-rarity reconciliation came
  from SQL against the copy opened `?mode=ro`.
