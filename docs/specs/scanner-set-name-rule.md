# Using the printed set *name* to resolve a printing

**Status:** proposed, nothing built. Written 2026-08-09 from a live pile.

**One sentence:** the card prints its set name in the collector band, the
resolver only reads the set *code*, and on promo printings the code is a generic
marker that identifies nothing — so a card that says exactly what it is still
resolves as ambiguous.

---

## 1. The card that showed it

A Standard Showdown Island, scanned 2026-08-09. The band read cleanly —
`bandAnchored: true`, no OCR complaint:

```
002/005 P Standard Showdown
PRM*EN • REBECCA GUAY
TN & C 2017 Wizards of the Coast
```

Telemetry:

```
~ resolve "Island" rank=number-ambiguous match=exact set=PRM num=2 prints=828
~ outcome "Island" queued: printing unverified: 828 printings, and the front one is not from 2017
~ outcome "Island": no better read within 1000ms, review it
```

The review offered `PSS4 #2 · MKM Standard Showdown · 2024-02-10`. The card is
`pss2`, XLN Standard Showdown, 2017.

**The queue was correct.** `printingUnverified` (`internal/tui/autoscan.go:960`)
refused to auto-commit because the front-ranked printing released in 2024 and
the band said 2017 — the guard that distinguishes guessing without evidence from
committing against it. Nothing here is a bug. The question is why a card that
printed its own set name needed a human at all.

## 2. Why the existing year rule could not fire

There is already a year rule: `soleIndexInYear` at `autoscan.go:1666` pins a
printing when the copyright year narrows the number's matches to **exactly one**.
It returned -1 here, and the measurement says why:

| Signal | Candidates |
| --- | --- |
| `Island`, any printing | 828 |
| `Island` #2 | **20** |
| `Island` #2, released 2017 | **2** ← year alone is not enough |
| `Island` #2, set name contains "Standard Showdown" | 3 |
| `Island` #2, 2017 **and** "Standard Showdown" | **1** ← exact |

The two 2017 survivors:

```
g17   2017 Gift Pack            2017-10-20   ["instore","giftbox"]
pss2  XLN Standard Showdown     2017-09-29   ["standardshowdown","instore"]
```

Two promo products from the same year, both containing basic-land Island #2.
The year is a real signal and it is simply not sharp enough on its own. The
markings path (`markPinned`, `autoscan.go:1684`) could not help either: both are
foil, both are black-bordered.

**The signal that would have settled it was on the card and thrown away.**
`P Standard Showdown` names the product. `pss2`'s `set_name` is
`XLN Standard Showdown`. Matching those two narrows 20 → 1 with the year, and
20 → 3 without it.

## 3. What the resolver reads today

`exactSet` is matched from `scan.Card.SetCode`. On this card that is `PRM` — the
generic promo marker Wizards prints on promotional cards. It is not a Scryfall
set code, it matches nothing, and it is the same three letters on promos from
every year and every product. So for a large family of cards the *strongest*
identifier printed on the card is guaranteed to be useless, and the second
strongest — the product name, printed right beside it — is never consulted.

## 4. The rule

> When the collector number matches several printings and the card's band
> carries text that matches a candidate's set name, keep the candidates whose
> set name matches. If exactly one survives — alone or combined with the
> copyright year — treat it as pinned.

Deliberately the same shape as the two narrowing passes already there
(`yearPinned`, `markPinned`): a filter over `matchIdxs` that only pins on a sole
survivor. It should slot beside them at `autoscan.go:1666-1696`, not replace
them — the year stays the first filter, and this runs when the year leaves more
than one.

**Matching has to be fuzzy in one direction only.** The card prints
`P Standard Showdown`; Scryfall says `XLN Standard Showdown`. The card's text is
a *substring-ish* of the real name with a product prefix swapped in, so the test
is "does the candidate's set name contain the distinctive run of words the band
printed", not equality. Normalise case, drop the leading `P`/`Promo` token, and
require a match of at least two words so that `Standard` alone cannot pin
anything.

## 5. What it costs — the real work is plumbing, not the rule

**The text does not currently reach the resolver.**

- `scan.Event.BottomLines` exists (`internal/scan/scan.go:55`) and carries
  exactly the string needed. Its own comment says it is "the raw text of the
  bottom band, for debugging a bad read", and **nothing outside a test reads
  it** — verified by grep.
- `scan.Card` — which is what `queueItem.raw` holds and what the resolver sees —
  has **no** bottom-line field at all. Its 21 fields are listed at
  `internal/scan/scan.go:185`.

So there are two implementations, and they are not equally cheap:

1. **Event-level, no phone change.** Use `Event.BottomLines` for the
   single-card case. Correct for a pile, which is one card per capture, and
   wrong for a fanned spread where one capture yields several `Cards` and one
   set of bottom lines. Cheap, and honest if the fan case is excluded explicitly
   rather than by accident.
2. **Per-card, needs a phone change.** Add the band text (or a parsed set-name
   field) to each block the phone emits, so a spread resolves each card by its
   own band. Correct in general, and it is a wire change — `ScanWire/Wire.swift`
   and `scan.Card` both move, and the port's cross-check vectors do not cover
   this field.

Start with (1) and say so in the code, or the fan case becomes a silent wrong
answer later.

## 6. Rank, and where it lands

A set name plus a number is evidence of the same strength as a set *code* plus a
number, so the natural home is at or just below `scanMatchSetAndNumber`
(`autoscan.go:48`). That matters beyond ordering: `numberVerified()`
(`autoscan.go:68`) gates the auto-commit, and a rank placed below
`scanMatchNumberOnly` would narrow the candidates correctly and still queue the
card, which is most of the work for none of the benefit.

**This is a decision, not a detail.** Promoting promo printings into
auto-commit territory on a fuzzy string match is exactly the kind of change the
foil and border work has been burned by before. It deserves its own corpus run,
not a confident guess.

## 7. How to know it worked

- The Island above auto-commits as `pss2`, not `pss4`, and not to review.
- `make scan-check` still passes — the fixture goldens must not move, because
  this rule adds a pin where there was none rather than changing an existing
  answer.
- A count, before and after, of cards queued as `printing unverified` across a
  corpus run. The number should fall; if it does not, the rule is not firing and
  the string matching is the first suspect.
- A deliberate negative: a card whose band names a set it is not from must still
  fail to pin rather than pin wrongly.

## 8. Scope

This is one card in one pile. Whether it is worth building depends on how many
promo printings go through real piles — Standard Showdown, Gift Pack, prerelease
and buy-a-box promos all print `PRM` and all name their product on the card, so
the family is larger than one set. **Count them in a session log before
building**: the same telemetry that produced this note (`~ outcome … printing
unverified`) is what would size it.

Related: `docs/specs/scanner-limits.md` for what the reader does and does not
get off a card, and `docs/specs/scanner-tuning.md` for the rule that tuning
changes are only real after a live pile.
