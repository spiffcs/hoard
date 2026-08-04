# Scanner coverage: what scans well, and what will not

Which Magic sets the hands-free scanner handles, which it struggles with, and
why. Derived from the catalog's 107,214 paper printings across 986 sets, and
from the live-session evidence recorded in `docs/scanner-tuning.md`.

The short version: **of 662 sets with ten or more cards, 64 have a known
problem.** The other ~600 should scan and auto-commit cleanly, because the
printed collector number identifies the printing and the scanner reads it well.

## How the scanner decides

Worth understanding before the tables, because every limitation below follows
from it. Two separate questions have to be answered for a card to auto-commit:

1. **Which card is this?** Answered by OCR of the title, backed by fuzzy
   matching against the catalog. This works nearly always — even on borderless
   frames and 1993 cards.
2. **Which *printing* is this?** Answered by the collector number, the set
   code, and the copyright year. This is where cards fail.

Almost everything below is a failure of the second question, not the first. A
card that lands in review has usually been *identified* correctly; the scanner
simply cannot prove which of its printings you are holding, and says so rather
than guessing. Auto-committing the wrong printing is the failure this project
treats as most expensive, since a wrong set is invisible until valuation.

## Group 1 — pre-1998: no collector number printed (32 sets)

The largest group, and the one most likely to matter, because these are the
sets people scan for value. Before 1998 Magic simply did not print a collector
number. The whole bottom line is two centred rows:

    Illus. Dameon Willich
    © 1995 Wizards of the Coast, Inc. All rights reserved.

No number, no set code, no language code. The scanner reads the *name* fine —
what it cannot do is tell a 4th Edition printing from a Revised one.

The copyright year helps when it is legible: it pins **24%** of these outright
and cuts the rest from a median of 12 candidate printings to 3. But on a desk
photo at 1080p that line is roughly six pixels tall and usually unreadable —
measured across a live session, most old cards yielded no year at all.

| year | sets |
| --- | --- |
| 1993 | `lea` `leb` `2ed` `arn` `ced` `cei` |
| 1994 | `3ed` `sum` `fbb` `fem` `leg` `atq` `drk` |
| 1995 | `4ed` `4bb` `bchr` `chr` `ice` `hml` `ren` `rin` |
| 1996 | `mir` `all` `itp` `mgb` `rqs` |
| 1997 | `tmp` `vis` `wth` `5ed` `por` `pvan` |

**What to expect:** the card is identified and lands in review with a short
candidate list for you to confirm. Cards with a single printing still commit
normally.

**Where it is worst:** Alpha vs Beta (both 1993, both black-bordered) and
Revised vs Summer Magic (both 1994, both white) are *genuinely* indistinguishable
from the card face — same art, same layout, same artist. No amount of work
fixes those; the information is not on the card. They are an explicit non-goal.

**How much of this is even winnable.** The 47% and 24% above are shares of all
6,411 printings, and a large part of that denominator is unwinnable by
anything: 1,802 of them (28%) are inside the two non-goal families above, and
another 330 are World Championship decks from group 2. Against the ~4,279 that
remain, the year alone pins **36%** and year plus border pins **71%**.

**Border colour is now read, and it doubles the year's reach.** It pins 47%
rather than 24% overall, and the win is lopsided in a useful direction:

| set | year alone | year + border |
| --- | --- | --- |
| `4ed` Fourth Edition | **0%** | **95%** |
| `chr` Chronicles | 0% | 90% |
| `wth` Weatherlight | 81% | 93% |
| `vis` Visions | 72% | 87% |

Fourth Edition is one of the most-scanned sets here and gets nothing from the
year, because `4ed` (white, 1995) and `4bb` (black, 1995) share their year,
art and artist.

The reader anchors on text it recognizes by content — the copyright line and
the illustrator credit — reconstructs the card's height from it, and samples
the border on the full-resolution frame, so it never depends on the perspective
crop containing the border. It scores that sample against the range of tones
the card prints its own footer with, which is what makes it survive a desk
lamp: a white border is brighter than the card's own paper, a black one darker
than its own ink, and both endpoints move with the light.

It declines rather than guesses. Across 231 corpus images, 19 desk photographs
and a live session of white-bordered bulk cards it has **never once called a
white border black or a black one white**. It speaks for **84%** of pre-1998
white and black cards on clean scans, and read 7 of the 9 live frames that
held a legible card.

Its one real blind spot is gold and silver, which read as white — a silver
border is light grey, and no photometry separates it. That costs nothing here
because a printing whose border the reader cannot recognise is never ruled out;
it affects only World Championship and Un-set cards, groups 2 and 3. See
`docs/scanner-tuning.md` for the first attempt, why it was removed, and the
four things the pixels contradicted.

**The next lever is the expansion symbol**, and the groundwork is measured.
Verified against corpus images rather than recollection: 4th Edition's type line
has an empty right margin, Ice Age prints a snowflake there — core sets carry no
symbol, expansions do. The symbol sits at card-space **(0.877, 0.578)**, about
0.03 of card height across, and pre-Exodus symbols are monochrome black since
rarity colour only arrived in 1998.

Presence alone breaks confusions border cannot: `4bb` against `ice` (both black,
both 1995 — 72 printings), `itp` against `rqs` (131), and Portal against the
Tempest block (118). Symbol *identity* is equivalent to set identity, which pins
83% of pre-1998 printings — hands-free, and without asking the user anything.

None of it is built. The plan, the measurements behind it, and the reason it is
currently blocked — the patch cannot be located on a live 8th Edition frame —
are in [scanner-symbol-plan.md](scanner-symbol-plan.md).

**What border does with its evidence, for now:** it orders the review queue and nothing
else — it never removes a printing, raises a rank, or commits. Unlike a year or
a number, a wrong border matches *something* rather than nothing, so it cannot
fail closed on its own and does not yet get to decide alone. Every read is
logged on the resolve line with the set it promoted, so a session of real cards
measures its accuracy before it is trusted further.

## Group 2 — gold border: numbers that are not numbers (11 sets)

World Championship decks number their cards with the player's initials —
`jn12`, `gn12a` — which matches no numeric pattern. Measured against clean
scans, the collector number parses **0% of the time in every era**.

`ptc` `wc97` `wc98` `wc99` `wc00` `wc01` `wc02` `wc03` `wc04` `punk` `pssc`

**What to expect:** identified by name, then review. Arguably correct to skip
rather than guess, but it is currently an accident rather than a decision.

## Group 3 — silver border: Un-set layouts (7 sets)

Un-sets break frame conventions deliberately — that is the joke — so title
parsing degrades. Measured at **62–75%** by name against clean scans, versus
near-100% elsewhere.

`ugl` `unh` `ust` `tust` `und` `ulst` `hho`

**What to expect:** more misreads than usual, and more review. These are the
one group where question 1 (*which card*) genuinely fails.

## Group 4 — indistinguishable by design (14 sets)

Sets where the same card appears more than once with the same number, year and
border, so no printed evidence can separate them. Almost entirely tokens,
oversized commanders and promo printings.

`pmps` `p09` `p10` `ocmd` `oc13` `tktk` `pm15` `ugin` `tema` `pxtc` `tvow`
`tsnc` `t30a` `tonc`

**What to expect:** review. Low practical impact — these are mostly tokens and
promos where the exact printing matters little.

## Everything else

The remaining ~600 sets are expected to scan and auto-commit. Modern frames
print a collector number and set code in the bottom band, the scanner reads
them reliably, and a set-and-number match is the strongest evidence in the
ranking ladder. Live sessions of modern cards routinely finish with everything
committed and nothing in review.

Two modern caveats worth knowing, neither set-specific:

- **Foils on pre-8th-Edition frames** record as nonfoil. The old-frame foil
  marker is a printed starburst, not text, and no reliable detector for it
  exists yet. Modern foils are detected correctly from the set/language line.
- **Borderless and full-art cards** are slower to capture, not less accurate.
  Their art runs to the edge, so the card outline is hard for the detector to
  hold; the name still reads.

## Validating this

The structural analysis above covers every set. Confirming it with images is
possible via `scan/corpus/` (`fetch.sh` then `sweep.sh`), which samples the
catalog and scores the helper against known answers.

**Read that harness's limits before trusting it here.** The corpus is built
from clean digital scans, where the card fills the frame — so there is no
card-against-desk edge, the detector finds nothing to crop, and anything
depending on the card's *outline* is unmeasurable. That format once reported
borderless cards at 14% by name when they read fine on real photographs.

So the corpus can validate groups 2, 3 and 4 — those are parser problems, and
the parser is exactly what clean scans exercise.

For group 1 it goes half way, and the halves are worth keeping straight.
Because the card fills a clean scan, the true card rect is known exactly, which
makes it the right place to *fit* the border reader's layout constants and the
only place with enough labelled borders to score it (`./scan/corpus/border.sh`).
What it cannot show is how any of that survives a desk lamp — a colour gate
that looked perfect there was wrong on the first real photograph it met. So the
constants are fitted on the corpus, confirmed on `scan/fixtures/`, and the
accuracy that decides whether border may ever commit comes from live sessions,
which is where the rest of group 1's evidence in `docs/scanner-tuning.md` comes
from too.
