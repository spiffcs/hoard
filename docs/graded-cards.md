# Graded cards

**Status: proposal, nothing built.** Written down while the condition work is
fresh, so a later session does not have to rediscover the shape. Schema v23 laid
the groundwork by giving a holding a condition and its own id; this is what comes
next if graded cards are worth modelling.

## The problem

A graded card is not a card in a condition. It is a card sealed in a slab by a
third party who has assigned it a number, and that number is the thing people
buy and sell. "PSA 10" is not an adjective applied to a Black Lotus — it is,
commercially, a different object from a raw one, often by an order of magnitude.

hoard currently reads a grade and throws it away. `normCondition` maps `BGS 10`,
`PSA 9.5` and `Pristine` to `unknown`, on the principle that a grade hoard cannot
place is not one it should invent. That is correct and also unsatisfying: the
file said something specific and hoard recorded nothing.

## Why not just more condition values

The obvious move is to extend the condition vocabulary — `psa10`, `bgs95`, and so
on. It does not survive contact with the numbers.

There are four graders in common use (PSA, BGS, CGC, SGC), each with roughly
twenty grade steps, several with half grades, and BGS additionally publishing
four subgrades per card plus a Black Label for a straight-10 sweep. The product
is on the order of a hundred values before subgrades and thousands after. Against
that, a collection holds a handful of graded cards — most hoards hold none.

So the condition column would grow by two orders of magnitude to describe a
fraction of a percent of rows. That is the wrong trade, and it also loses the
structure: `psa10` and `bgs10` sort as unrelated strings, when what a reader
wants is "everything a 10", "everything PSA", "everything above 9".

## The shape

`condition` gains one more value, `graded`, which means *see the grade table*.
The detail lives beside it, one row per holding:

```sql
CREATE TABLE card_entry_grades (
    entry_id  INTEGER PRIMARY KEY REFERENCES card_entries(id) ON DELETE CASCADE,
    grader    TEXT NOT NULL,   -- psa | bgs | cgc | sgc
    grade     TEXT NOT NULL,   -- '10', '9.5' — the grader's own notation
    ordinal   REAL NOT NULL,   -- 10.0, 9.5 — for comparing and sorting
    cert      TEXT,            -- the slab's certification number, when known
    subgrades TEXT             -- JSON, for BGS's four; NULL otherwise
);
```

Keyed on `entry_id`, so it is one-to-one with the holding and every graded row
carries exactly one grade. This is the payoff from v23's surrogate key: without
it there would be nothing stable to point at.

`grade` and `ordinal` are both stored deliberately. `grade` is what the slab
says and what a person expects to read back; `ordinal` is what a query sorts and
filters on, because `'10'` and `'9.5'` compare wrongly as text and grader
notations are not all numeric (SGC's Gold Label, BGS's Black Label).

## Quantity, and when a bucket stops making sense

A holding is a counted bucket, and grading is the one axis where that starts to
strain. Two PSA 10 copies are genuinely two objects with two different
certification numbers.

The proposal keeps the bucket and makes `cert` optional:

- **No cert recorded** — `quantity` may exceed 1, meaning that many
  identically-graded slabs. Fine for a valuation, useless for provenance.
- **Cert recorded** — `quantity` must be 1, because a certification number
  names one slab. Enforce it, or the column is a lie.

Anyone wanting per-slab tracking wants the second, and should get an error rather
than a silent merge if they try to add a second copy under one cert.

## Pricing: further out of reach than condition

Condition does not change value in hoard because no source it reads publishes a
per-condition price. Graded is worse: the price of a slab depends on the grader
and the number, and those figures live in auction records and dedicated graded
marketplaces, none of which hoard touches.

So a graded holding would carry the raw card's price, exactly as an ungraded one
does, and be visibly wrong for anything valuable. That is the strongest argument
for *not* building this until there is a price source to go with it — a hoard
that displays "PSA 10 Black Lotus · $8,400" (the raw price) is more misleading
than one that says nothing.

**Recommendation: do not build the grade table until a graded price source is
identified.** The modelling is easy; the number beside it is the hard part, and
shipping the first without the second makes the totals worse rather than better.

## The vocabulary: Go or a table?

The user's sketch called for reference tables of graders and grades. Worth
weighing against house style, which currently keeps every vocabulary in Go —
`validFinish`, `validCondition`, the board and container-kind constants — and has
no lookup tables at all.

- **Go constants**, consistent with everything else, and a grader list that
  changes once a decade does not need a table.
- **Reference tables**, which earn their place if displays want to join for a
  grader's full name, or if `ordinal` should live next to the grade rather than
  be recomputed.

Suggested compromise: keep validation in Go (`validGrade(grader, grade)`), and
store `ordinal` on the row so no join is needed to sort. Add tables only if a
concrete screen wants them.

Either way the grader's scale must be pinned from that grader's own published
documentation when this is built. The rough shapes above — PSA and CGC with half
grades, BGS with subgrades and a Black Label, SGC with a Gold Label — are from
memory and are exactly the kind of thing that is wrong in a detail that matters.

## Import

`normCondition` would gain a parse rather than a lookup, since a grade is
structured text and not a word from a fixed list:

```
"PSA 10"      -> graded, psa, 10,  ordinal 10.0
"BGS 9.5"     -> graded, bgs, 9.5, ordinal 9.5
"CGC 10"      -> graded, cgc, 10,  ordinal 10.0
"graded 9.5"  -> graded, "",  9.5, ordinal 9.5   -- grader unknown
"Pristine"    -> ambiguous; see below
```

Two traps worth writing down now:

- **`Pristine` is not one thing.** CGC uses it for a 10, BGS for a straight-10
  sweep. Alone, without a grader, it names no single grade and should stay
  `unknown` rather than be guessed at.
- **A bare number is not a grade.** A condition column reading `10` could be a
  quantity, a set number, or a spreadsheet artefact. Require a grader token, or
  the literal word `graded`, before treating anything as a grade.

Everything hoard cannot parse keeps today's behaviour: `unknown`, counted and
reported.

## What it touches when built

Much less than v23 did, because v23 did the expensive part.

- One migration: the new table, plus `graded` in `validCondition`.
- `normCondition` grows a parser and a second return value.
- The canonical CSV needs a column, or a graded export loses its grade.
- The browse editor needs a way to enter one — the condition prompt would take
  `psa 10` as well as `nm`.
- The JSON model gains an optional `card.grade` object; a schema ADDITION.

`entryValue` and the aggregates need no change at all, since a graded holding
prices as its raw card until there is a source that says otherwise.

## Related

- [docs/data-model.md](data-model.md) — the condition column and what `unknown`
  means.
- [docs/csv.md](csv.md) — the condition values imports accept today, and what
  each becomes.
