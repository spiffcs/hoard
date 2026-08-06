# What the scanner cannot do

Where the hands-free scanner fails, which sets it fails on, and how much of
that is winnable. Three sources of evidence, and they answer different
questions:

- **The catalog** — 107,214 paper printings across 986 sets — says which sets
  are structurally unscannable, whatever the optics do.
- **`scan/corpus`** — 231 labelled images, stratified by frame era and border
  colour — scores the parser. Regenerate with `make cardkit-score` (about 23
  seconds), or `make cardkit-score ARGS=--misses` to see every failing card.
- **Live sessions**, recorded in [scanner-tuning.md](scanner-tuning.md), are the
  only place anything involving lighting, motion or card-finding is real.

**Read this before trusting a number, and before chasing a bug.** Several of
the figures below are not the scanner being wrong; they are a harness asking a
question the scanner was never trying to answer. Those are called out.

For what queues on a *live pile* rather than in the corpus, and the work that
would recover it, see [scanner-review-cases.md](scanner-review-cases.md).

The short version: **of 662 sets with ten or more cards, 64 have a known
problem.** The other ~600 should scan and auto-commit cleanly, because the
printed collector number identifies the printing and the scanner reads it well.

## How the scanner decides

Worth understanding before anything else, because every limitation below
follows from it. Two separate questions have to be answered for a card to
auto-commit:

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

---

# Part 1 — Which sets fail

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

### Border colour, and why it doubles the year's reach

It pins 47% rather than 24% overall, and the win is lopsided in a useful
direction:

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
the illustrator credit, lines whose identity is proven by what they *say* —
reconstructs the card's height from it, and samples the border on the
full-resolution frame, so it never depends on the perspective crop containing
the border. It scores that sample against the range of tones the card prints
its own footer with, which is what makes it survive a desk lamp: a white border
is brighter than the card's own paper, a black one darker than its own ink, and
both endpoints move with the light.

It declines rather than guesses. Across 231 corpus images, 19 desk photographs
and a live session of white-bordered bulk cards it has **never once called a
white border black or a black one white**. It speaks for **84%** of pre-1998
white and black cards on clean scans, and read 7 of the 9 live frames that
held a legible card.

Not reading the crop at all is what moved the corpus numbers: coverage fell
from 37% of cards to 24% and accuracy rose from 60% to 90%, so **wrong answers
went from 32 to 5**. That is the trade worth having, because a wrong border
commits a wrong printing while an abstain only queues the card.

Its one real blind spot is gold and silver, which read as white — a silver
border is light grey, and no photometry separates it. That costs nothing here
because a printing whose border the reader cannot recognise is never ruled out;
it affects only World Championship and Un-set cards, groups 2 and 3. See
[scanner-tuning.md](scanner-tuning.md) for the first attempt, why it was
removed, and the four things the pixels contradicted.

**What border does with its evidence, for now:** it orders the review queue and
nothing else — it never removes a printing, raises a rank, or commits. Unlike a
year or a number, a wrong border matches *something* rather than nothing, so it
cannot fail closed on its own and does not yet get to decide alone. Every read
is logged on the resolve line with the set it promoted, so a session of real
cards measures its accuracy before it is trusted further.

### The next lever is the expansion symbol

The groundwork is measured. Verified against corpus images rather than
recollection: 4th Edition's type line has an empty right margin, Ice Age prints
a snowflake there — core sets carry no symbol, expansions do. The symbol sits at
card-space **(0.877, 0.578)**, about 0.03 of card height across, and pre-Exodus
symbols are monochrome black since rarity colour only arrived in 1998.

Presence alone breaks confusions border cannot: `4bb` against `ice` (both black,
both 1995 — 72 printings), `itp` against `rqs` (131), and Portal against the
Tempest block (118). Symbol *identity* is equivalent to set identity, which pins
83% of pre-1998 printings — hands-free, and without asking the user anything.

None of it is built. The plan, the measurements behind it, and the reason it is
currently blocked — the patch cannot be located on a live 8th Edition frame —
are in [scanner-symbol-plan.md](scanner-symbol-plan.md).

## Group 2 — gold border: numbers that are not numbers (11 sets)

`ptc` `wc97` `wc98` `wc99` `wc00` `wc01` `wc02` `wc03` `wc04` `punk` `pssc`

World Championship decks number their cards with the player's initials —
`jn12`, `gn12a` — which matches no numeric pattern. Their footer prints a
**sideboard marker** (`SB`, `GB`) where a collector number would be, and they
carry no number at all. Measured against clean scans, the collector number
parses **0% of the time in every era**.

The parser refuses them deliberately. Folding digit lookalikes would turn `SB`
into `58` and `GB` into `68`, inventing a collector number on cards that print
none — measured at 18% of the pre-1998 gold stratum before the guard existed.
**An invented printing is the worst thing this pipeline can produce**, and
refusing a real number occasionally is the right trade.

**What to expect:** identified by name, then review. Gold-bordered cards will
not auto-commit on a number; they can still commit on year plus border.

`punk` and `pssc` fail a second, unrelated way — they are Planechase planes and
oversized promos whose content is printed landscape, which breaks the title
read too. See [rotated content](#2015gold-0-name-0-number--rotated-content).

## Group 3 — silver border: Un-set layouts (7 sets)

`ugl` `unh` `ust` `tust` `und` `ulst` `hho`

Un-sets break frame conventions deliberately — that is the joke — so title
parsing degrades. Measured at **62–75%** by name against clean scans, versus
near-100% elsewhere.

**What to expect:** more misreads than usual, and more review. These are the
one group where question 1 (*which card*) genuinely fails.

## Group 4 — indistinguishable by design (14 sets)

`pmps` `p09` `p10` `ocmd` `oc13` `tktk` `pm15` `ugin` `tema` `pxtc` `tvow`
`tsnc` `t30a` `tonc`

Sets where the same card appears more than once with the same number, year and
border, so no printed evidence can separate them. Almost entirely tokens,
oversized commanders and promo printings.

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
  Measured cost in [what no harness reaches](#what-no-harness-reaches).
- **Borderless and full-art cards** are slower to capture, not less accurate.
  Their art runs to the edge, so the card outline is hard for the detector to
  hold; the name still reads.

## Foreign-language printings, across every group

The scanner reads them. Fuzzy matching then resolves against **English** names,
so a correctly-read Italian card will not find its printing and queues. That is
a genuine gap and a real product decision — it is just not an OCR one, and
**reading foreign cards is planned as its own sprint** rather than treated as a
defect. It also distorts the corpus scores badly enough to need its own
handling; see [the headline](#headline).

---

# Part 2 — What the corpus measures

The corpus is Scryfall scans in which the card fills the frame. That is the
right place to fit parser rules and the **wrong** place to judge anything that
depends on finding the card, because segmentation has no background to lock
onto. `scan/corpus/border.sh` says so in its own header, and
[what the corpus cannot show](#what-the-corpus-cannot-show) says which
conclusions it may carry.

## Headline

`make cardkit-score`, over the 214 English printings:

| | |
|---|---|
| name | **87%** |
| collector number | **78%** |
| border colour, when it answers | **90%** on the corpus (48 of 53) |

**The 17 non-English printings are scored apart and reported on their own row**
(18% name, 71% number). The manifest carries a `lang` column for exactly this,
added by `scan/corpus/lang.py`. Their images are Italian, Spanish, Japanese and
Chinese cards while the manifest holds the *English* name, so a perfect read of
"Miniera a Cielo Aperto" counted as a failure to read "Strip Mine".

That one change moved the headline from 82% to 87%, and `pre1998/black` from
58% to 72% — not by improving anything, but by no longer asking the scanner a
question it was never trying to answer.

Their 71% number score is the interesting part: the collector row is digits and
reads fine regardless of language. It is only the *name* we cannot use.

## By stratum

English printings only. `n` varies because non-English cards have been lifted
out of their strata.

| era | border | n | name | number |
|---|---|---|---|---|
| pre1998 | white | 39 | 95% | 100% |
| pre1998 | gold | 40 | 90% | 100% |
| pre1998 | black | 29 | 72% | 97% |
| 1998-2002 | white | 8 | 100% | 88% |
| 1998-2002 | black | 8 | 100% | 63% |
| 1998-2002 | silver | 8 | 88% | 75% |
| 1998-2002 | gold | 8 | 100% | **0%** |
| 2003-2014 | black | 8 | 100% | 63% |
| 2003-2014 | white | 4 | 100% | 100% |
| 2003-2014 | silver | 8 | 88% | 50% |
| 2003-2014 | gold | 8 | 100% | **0%** |
| 2015+ | yellow | 8 | 100% | 100% |
| 2015+ | white | 8 | 100% | 88% |
| 2015+ | black | 8 | 100% | 75% |
| 2015+ | silver | 8 | 88% | 38% |
| 2015+ | borderless | 6 | **50%** | 67% |
| 2015+ | gold | 8 | **0%** | **0%** |
| *(non-English)* | *all* | *17* | *18%* | *71%* |

`2003-2014/white` was 63% over 8 cards; four of those were Spanish. It is 100%
over the four English ones. That stratum was never broken.

## Where names fail, and why

42 name misses, by root cause:

| share | cause | is it really a failure? |
|---|---|---|
| 30% | one or two glyphs misread | partly — see below |
| 21% | **the card is not in English** | **no** — the read was correct |
| 19% | read rules text instead of a title | yes |
| 16% | other, mostly single cards | mixed |
| 11% | read nothing at all | yes |

### Cards where we read the right thing and are scored wrong

Two distinct populations, and together they are the largest category in this
document. In both the scanner reported exactly what is printed on the card, and
the corpus wanted a different string.

#### Foreign-language printings

The single most misleading number here. A fifth of all name misses are the
scanner reading the card **correctly** and being marked wrong because the
corpus's ground truth is the English name:

```
Strip Mine          -> Miniera a Cielo Aperto   (Italian)
Urza's Power Plant  -> Centrale Energetica di Urza
Rock Hydra          -> hydre de pierre          (French)
Sisters of the Flame-> Sœurs de la flamme
Primal Forcemage    -> Magofuerza primordial    (Spanish)
Mountain            -> Montaña
```

These are concentrated in `pre1998/black`, and were most of the gap between
that stratum and its white sibling before the `lang` split lifted them out.
The corpus's black-bordered pre-1998 sample is heavily foreign, because that is
what those print runs were.

Verified by opening the images: `Strip Mine`'s corpus entry is set `rin`,
Rinascimento, and the picture is an Italian card reading "Miniera a Cielo
Aperto". The manifest stores the English name because that is the identity the
catalog is keyed on.

The counts, from `scan/corpus/lang.py`: 214 English, 7 Spanish, 3 Italian, 3
French, 2 Japanese, 2 Chinese. The downstream limitation these expose is a
product gap, not an OCR one — see
[foreign-language printings](#foreign-language-printings-across-every-group).

#### Flavour names on Universes Beyond cards

`Bolas's Citadel` in the Final Fantasy set prints **"Kefka's Tower"** as its
title, with the Magic name in small italics beneath it. The scanner read
"Kefka's Tower", which is what the card says, and was scored against the Magic
identity.

Same shape of problem as the foreign printings: correct read, different naming
authority. Resolving these needs the catalog's flavour-name field, not a better
read.

### One or two glyphs

Old serif type at small sizes, misread by a character or two:

```
Amrou Kithkin   -> Amrou Kichkin      Pearled Unicorn -> Dearled Unicorn
Ironroot Treefolk -> Ironroot Irecfolk  Pyroblast      -> Lyroblast
Sisters of the Flame -> Sisters of che Flame
```

Scored as misses here, but the corpus scorer is stricter than the app: it wants
an exact or prefix match, while the live pipeline hands these to fuzzy matching
with a 0.88 similarity floor. Most of these land. Treat this bucket as an upper
bound on real-world failure, not a count of it.

### Read rules text instead of a title

Almost all of this bucket is one stratum with one cause, covered next: the head
crop takes the top 30% of a card, and `2015+/gold` is full of cards whose title
is not there.

### `2015+/gold` (0% name, 0% number) — rotated content

Not gold-bordered cards in any useful sense. The stratum is `punk` and `pssc`
from [group 2](#group-2--gold-border-numbers-that-are-not-numbers-11-sets):
**Planechase planes and oversized promos**, whose content is printed landscape
and served in a portrait image. "The Windy City" reads bottom-to-top down the
left edge of its card.

The pipeline assumes text runs horizontally across a portrait card, so the head
crop takes a horizontal band that slices *across* every rotated line — which is
exactly the fragments that come back: `'eginning of'`, `'yer searches'`,
`'library at any'`. The truncated titles (`'ination'` for Deceptive Divination,
`'e Day!'` for Happy Yargle Day!) are the same cut through a title.

**Out of scope, and worth saying so plainly.** Planes, schemes and oversized
cards are a different product with a different layout. Nothing short of
detecting the rotation and re-reading would help, and they are not what this
scanner is for.

### `2015+/borderless` — a stratum that is not about borders

The most misleading label in the corpus. `border=borderless` comes from
Scryfall's `border_color`, but what these cards actually share is being
**special-treatment products** — Secret Lair, Mystical Archive, Universes
Beyond, Alchemy — with unrelated layouts. Grouping them by border colour puts a
normal-layout Secret Lair beside a full-art TMNT card and reports one number for
both.

The ones that pass are Secret Lairs with an ordinary title bar. The four that
fail do so for four different reasons:

| card | what it is | why |
|---|---|---|
| Bolas's Citadel | Universes Beyond, Final Fantasy | prints "Kefka's Tower"; **we read it correctly** |
| Snakeskin Veil | Mystical Archive | title is **vertical and Japanese**, down the left edge |
| Mondo Gecko | Universes Beyond, TMNT | **no title bar at all** — art covers the whole face |
| Rhystic Study | full-art treatment | **no title bar at all** |

So one is a flavour-name miss, one is rotated non-Latin text, and two have **no
title printed on the face**. That last pair is a permanent limitation, not a
bug: there is no text to read.

This matches field experience, where ordinary borderless cards scan well. The
number is measuring special products, not borderless frames.

**The border reader used to over-reach here**, claiming `black` on Kess and
Heliod — borderless cards whose art runs dark to the edge, so the ring sample
read as a black border. The text-anchored reader judges the ring against the
card's own footer tones instead, so a ring that is neither the card's ink nor
its paper abstains. Not measured as a goal; re-check it on borderless cards
before relying on it.

## Where numbers fail, and why

53 number misses, and the split matters:

- **45% found no number at all** — nothing to misread, the digits never reached
  the parser
- **55% read the wrong digits** — small italic serif type, misread

Gold borders score 0% across 1998-2002, 2003-2014 and 2015+ — the one stratum
that is comprehensively broken, and not a regression, since the previous
pipeline scored 0% here too. It is [group 2](#group-2--gold-border-numbers-that-are-not-numbers-11-sets)
and the refusal is deliberate.

The rest is the copyright-row era. `1998-2002/black` at 63% and `2003-2014/*`
at 50-63%: the number is four small italic digits at the tail of the credit
line, and `30` reads as `80` about as often as you would expect. These commit
via the `copyright` number source, which the Go side treats as **upgrade-only
evidence** — a misread here ranks a card wrongly rather than committing it
wrongly.

---

# Part 3 — What the numbers do not cover

## What the corpus cannot show

The structural analysis in part 1 covers every set. Confirming it with images is
possible via `scan/corpus/` (`fetch.sh` then `sweep.sh`), which samples the
catalog and scores the helper against known answers.

**Read that harness's limits before trusting it.** The corpus is built from
clean digital scans, where the card fills the frame — so there is no
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
which is where the rest of group 1's evidence in
[scanner-tuning.md](scanner-tuning.md) comes from too.

## What no harness reaches

Three real limitations that no number in this document covers, because the
corpus has no way to test them:

1. **Foils before 2003.** The foil marker is a star in the set row, and frames
   before 8th Edition print no set row at all. A pre-2003 foil records as
   nonfoil, silently, and foil is worth a multiple. Of a 437-printing sample of
   old-pile cards, 8 were "comes in foil, from before any marker was printed".
2. **Crops that stop short of the footer.** Measured live at ~5% of captures:
   the card is located, but the quad misses its bottom, so the parser sees rules
   text where the collector row should be. A footer recovery pass now reads the
   strip below the located box and rescued 6 foils in one session. The aspect
   check cannot catch this — a quad missing the bottom sixth of a card is still
   card-shaped.
3. **Anything about lighting, focus or motion.** Corpus images are clean scans.
   Real failures on the rig are dominated by the trigger firing on an empty desk
   or a hand mid-swap, which no corpus image can reproduce.

## Reading the numbers honestly

- **The corpus is 745x1040.** The phone captures 4032x3024. Anything resolution
  dependent — small type, the expansion symbol, border sampling — behaves
  differently here than in the field.
- **Ground-truth suffixes are scored as misses.** A correct `130` counts wrong
  against a ground truth of `130p` or `228★`. This affects both pipelines
  equally and is a scorer limitation, not a parser one.
- **`n=8` per stratum** outside pre-1998. One card is 12.5%. Do not read
  movement of a single card as a trend.
