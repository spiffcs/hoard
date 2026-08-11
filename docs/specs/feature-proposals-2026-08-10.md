# Feature proposals — 2026-08-10

Two surveys, run in parallel against a copy of a real 1,968-printing collection:
one asking what a human collector wants, one asking what an AI agent or script
wants. Both exercised the tool rather than reading it — the human lane drove the
TUI through a pty harness, the machine lane answered real questions using only
`--json` and recorded where it got stuck.

This is the synthesis. Proposals are ranked by *evidence that hoard already
almost does it*, because that has been the shape of this project's best
features: percent watches existed as a jq pipeline before they were a feature.

Every claim below was verified against the code or the database. Where a survey
asserted something I could not check, it says so.

## The strongest signal: both lanes independently proposed the same thing

They surveyed different audiences with different methods and both led with the
portfolio series. That convergence is worth more than either report.

The machine lane asked *"has my collection gained or lost value this month?"*
and could only answer by summing `movers[].impactUsd` — **+$21.78** — while the
`value_snapshots` table says **+$2,202.63**. Both numbers are right and they
measure different things: `movers` deliberately excludes cards first seen after
the cutoff, so it reports price movement on cards you already held, while the
snapshot delta also includes acquisitions. An agent given only `movers` answers
confidently and wrongly.

The human lane found the same hole from the other side: `report` gives one dated
total with nothing to compare it to.

## 1. A portfolio series — `hoard history` and a `history` document kind

**Who and when.** Sunday morning, three months of `update-prices` behind you,
one question: is this going up? And an agent asked the same thing, which today
it cannot answer honestly.

**What already exists.** `store.ValueSnapshots()` returns the whole series,
oldest first — 177 dated points over 103 days, already split into binder, decks
and total. **Verified: its only callers are tests.** The method is finished,
tested, and wired to nothing.

`ui.Spark` and `ui.Resample` already draw exactly this shape — the card detail
overlay uses them for six per-card sparklines with trend verdicts.

**The honesty problem is already solved too, and this is the detail that makes
it worth building properly.** `ValuePoint` carries `Seeded bool`, whose comment
says *"charts should say so"* — 91 of 177 points are reconstructed at migration
time from *today's* quantities at that day's prices, not observed. More than
half the line is counterfactual. The provenance flag exists; nothing can reach
it, so neither the data nor its warning label reaches a consumer.

**Cost.** One file in `internal/command/` yields the CLI verb, its `--json`,
and the browser palette entry — the registry gives all three from one
declaration. No migration, no network, no new data. A fifth browse view is
optional and costs more.

**Open.** Command or view or both. Whether the seeded/observed boundary is drawn
differently in the chart — I think it must be, given the ratio.

## 2. Acquisition cost — hoard already reads it and throws it away

**Who and when.** You buy a $40 card. Six months later hoard says $52. It cannot
tell you that you made $12, because every valuation is a bare current-market
number with no cost basis.

**What already exists, and this is the striking part.** The importers *already
parse the purchase-price column*, test it for meaning, and count it as a loss.
`internal/collsource/csv.go:171-172`:

    if informativePrice(get(rec, sp.price)) {
        out.Dropped["purchase price"]++

Run live by the survey: `Dropped purchase price on 2 rows: hoard could not carry
it.` ManaBox, Moxfield and Delver all publish it. **Users are handing hoard
their cost basis at the door and hoard counts it as something it lost.**

`value_snapshots` supplies the market side of the comparison for free.

**Cost — the honest one on this list.** This needs a schema migration. Columns
on `card_entries` are simpler but wrong: the unique key is
`(container, card, finish, condition, board)`, so two buys of the same card at
different prices collapse into one row. A separate `acquisitions` table is
correct and more work. Plus `--price` on the add path and a column read in the
importers. No new network source, no licensing question — this is the user's own
data.

**Open, and it is the whole design.** Lot-level tracking (FIFO, per-purchase) or
an average cost per holding. Also: disposals are the natural partner — cost basis
without a sale record only ever yields unrealised gain — and building either
alone means migrating twice.

## 3. Deck format and legality

**Who and when.** You import a Commander precon; hoard files 100 cards and never
learns it is a Commander deck. You cannot ask which of your decks are Modern, or
whether this one is legal, or why it has two Sol Rings in a singleton format.

**What already exists.** `containers.format` — **the column is already there and
NULL on all 22 decks.** `legalities` sits in `raw_json` for all 1,968 cards, 23
formats each, with **zero Go references anywhere in the repo**. `color_identity`
is already a generated column, so the Commander identity rule is a join rather
than a fetch. The survey ran the check by hand against one precon: 88 entries,
all `commander: legal`.

**Cost.** No migration — the column exists. Optionally one `ALTER TABLE` adding
a generated `legalities` column, which in this schema is genuinely that cheap.
`--format` on `deck add`, a legality check, a column in the deck list. Entirely
offline.

**Open.** Whether format is inferred (a 100-card singleton deck is almost
certainly Commander) or always declared. Inference is a nice touch and this
codebase generally refuses to guess silently.

## 4. `--fields` projection on `export`

**Who and when.** An agent asked for a mana curve. It needed **129,902 bytes of
a 2,401,968-byte document** — an 18× reduction — and had no way to ask for less.

**What already exists.** The document is complete; the problem is that it is
all-or-nothing. `--deck` and `--binder` narrow by rows, not by fields.

**Cost.** Small, and unblocked: the flag audit already ruled projection a
legitimate flag rather than filter-language-in-disguise — *"a filter language
cannot express it"* — so this does not reopen the `--min-price` decision.

**Note on the sibling idea.** NDJSON has been proposed twice on size grounds and
the survey **measured it away**: compact JSON is 1,643,508 bytes, NDJSON is
1,643,442 — a 66-byte saving — and jq reaches the first row of the full document
in 0.03s. The honest case for NDJSON is workflow, not size: `head -n 200` bounds
a context window without a JSON parser. Worth building for that reason or not at
all. The 46% indentation overhead is a bigger size lever than either.

## 5. A receipt document for mutations

**Who and when.** An agent imports a deck and gets prose back. Forcing a partial
today produces genuinely well-designed output — the count, the failing names,
and exit 2 — but only as indented English under a heading:

    Would import deck "PartialTest" (text): 1 cards resolved.
      2 cards could not be resolved and were skipped:
        - Notarealcard Xyzzy

**What already exists.** The information is already deliberately surfaced. Exit
2 already carries a real contract. Only the encoding forces scraping.

**Cost.** Moderate — every write path would need one. `add`, `import`,
`deck add`, `merge`, `watch add` all return prose only today.

**Open.** Whether this is one kind or one per verb, and whether it composes with
`--dry-run` as a rehearsal receipt.

## 6. Mark the printings you already own, in the picker

**Who and when.** You pull a second Sol Ring from a precon. The picker offers
~150 printings newest-first and nothing says which four you already hold, so a
mis-pick creates a duplicate printing instead of incrementing a holding.

**What already exists.** `cascadeDelegate.Render` already decorates rows with
colour pips and a dim description; the store already knows every held
`scryfall_id`.

**Cost.** Very small. One lookup passed into the cascade. No new data.

**Open.** Annotate only, or sort owned-first. Re-sorting fights the deliberate
newest-first order.

## 7. "What would this deck cost me" on `deck add --dry-run`

**Who and when.** A friend sends an Archidekt link and you want to know what you
would have to buy. Today `--dry-run` prints `4 cards resolved.` — having just
resolved every Scryfall id, with your whole collection in the same process.

**What already exists.** The entire resolution pipeline, plus `oracle_id` in
`raw_json`, so "I own this card in a different printing" is answerable. The
survey ran it: 8 oracle ids held across multiple printings, Sol Ring in four.
Prices are already on `cards`.

**Cost.** Small — a join at report time. No migration, no network. This is the
closest thing on the list to the percent-watches pattern: a determined user
could assemble it from `export --format json` today.

**Open.** Match on printing or on `oracle_id`. For "what must I buy" it is
clearly `oracle_id`, which would be its first read anywhere.

## 8. A totals line under any filter

**Who and when.** "What are my Reserved List cards worth?" "What is my
Modern-legal value?" The grammar filters rows; nothing rolls them up.

**What already exists.** `store.TraitFilter`, `MatchingCardIDs` and the
`cards_trait_filter` index. Adding a trait key is a `case` in
`browse/filter.go` plus a field. `reserved` and `edhrec_rank` would want cheap
generated columns.

**Cost.** Small per key — and the survey's own read is that the higher-value
half is the totals line rather than more keys, since the filtered set is already
in hand.

## 9. Smaller machine-surface gaps

Each small, each with a named consumer:

- **The holdings document carries no timestamp.** `report` has `asOf`; holdings,
  market, unpriced and watch have nothing. An agent storing exports to diff has
  no anchor for when one was taken. Output is byte-deterministic — two runs are
  `cmp`-identical — which is what makes diffing viable, so the timestamp should
  be the price observation time rather than emit time or it destroys that.
  **Not** on the `hoard` kind, which is hashed.
- **`watch list --json` does not exist**, and `hoard watch --json` gives only
  `{checked, fired}`. Standing state — waiting/met, current price, threshold,
  anchor — is terminal-only. `store.WouldFire()` is a documented read-only
  preview *with no CLI caller at all*, so an agent's only way to learn about a
  pending alert is to consume it and destroy cron's.
- **Container provenance costs 15 MB.** `source`, `sourceUrl`, `format` per
  container exist only in the `hoard` kind, of which 65% is Scryfall blobs. And
  obtaining it means copying your database elsewhere and pretending to merge the
  copy into yourself, because `merge --dry-run` refuses to describe your own DB.
- **`catalog status --json` is refused**, so an agent cannot check whether card
  data is stale before trusting `detail`.
- **`guessed` has no JSON**, though `unpriced` — the same kind of audit queue —
  does.

## Bugs found, not fixed

Recorded here because the surveys found them; none is dispatched by this
document except the first, which is already in flight.

2. **`export --deck A --deck B` silently takes the last one.** No error, no
   warning, a plausible smaller document. Same class as the `--format csv
   --json` bug the flag audit found, and it bites scripts hardest.
3. **`import --format json` is not rejected**; it falls through to the CSV
   parser and reports `bare " in non-quoted-field`. Compare `schema --kind
   bogus`, which lists the valid set.
4. **The detail overlay invents an etched printing.** Bitterblossom `uma/85`
   renders a COMPS row for etched, identical to foil, on a card whose
   `finishes` are `["nonfoil","foil"]`. Only 7 cards in the whole database
   actually have an etched finish. Consistent with the documented
   feeds-fold-etched-into-foil behaviour leaking into a surface that presents it
   as a distinct product. Root cause not traced.
5. **`movers --since` reads backwards.** 30d reports 1,659 printings moved; 103d
   reports 474. Correct — a card needs a price at or before the cutoff to be
   comparable — but the sentence claims movement where it is reporting coverage.
   A legibility fix.
6. **`guessed` never retires an entry.** 114 rows, the same card at different
   timestamps, presented as a worklist that can only grow.

## Rejected, with reasons

- **Richer vendor data** — sold comps, more buylists, live listings. Not legally
  or practically available: TCGplayer grants no new API access, and the quotes
  hoard has arrive via a volunteer mirror the licensing spec says must not
  become load-bearing. Features over data already cached are fine; features that
  *depend* on new feeds are not.
- **Condition affecting valuation.** Condition is in better shape than assumed —
  stored, editable in the browser, shown in the detail overlay. What it does not
  do is revalue, and that is deliberate because the feeds publish one price per
  printing. Applying multipliers would present a derived number as a market
  fact, which is the thing the licensing doc is careful about. A condition
  *breakdown* adds information without inventing prices; that is the version
  worth building.
- **Extracting the filter grammar.** Weaker now than when the flag audit
  deferred it: with card `detail` at 100% coverage, jq answers trait questions
  locally.

## Two corrections to the framing I gave the surveys

- I said nothing surfaces price history as a series. Wrong: the card detail
  overlay draws six sparklines with min–max, check counts and trend verdicts.
  The gap is one level up — the *portfolio* has no series.
- I described the scanner as reachable from the CLI. There is no `hoard scan`
  verb; the scanner exists only inside the add cascade.
