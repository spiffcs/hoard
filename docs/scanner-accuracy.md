# What the scanner gets wrong

Measured against `scan/corpus` — 231 labelled images, stratified by frame era and
border colour. Regenerate with `make cardkit-score` (about 23 seconds), or
`make cardkit-score ARGS=--misses` to see every failing card.

**Read this before trusting a stratum, and before chasing a bug.** Several of
the numbers below are not the scanner being wrong; they are the scorer asking a
question the scanner was never trying to answer. Those are called out.

For what queues on a *live pile* rather than in the corpus, and the work that
would recover it, see [scanner-review-cases.md](scanner-review-cases.md).

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
question it was never trying to answer. **Reading foreign cards is a planned
sprint, not a defect.**

Their 71% number score is the interesting part: the collector row is digits and
reads fine regardless of language. It is only the *name* we cannot use.

The corpus is Scryfall scans in which the card fills the frame. That is the
right place to fit parser rules and the **wrong** place to judge anything that
depends on finding the card, because segmentation has no background to lock
onto. `scan/corpus/border.sh` says so in its own header.

The border reader no longer reads the crop at all, which is what moved that
number. It anchors its geometry on the copyright and credit rows — lines whose
identity is proven by what they *say* — and judges the ring against the card's
own printed ink and paper. Coverage fell from 37% of cards to 24% and accuracy
rose from 60% to 90%: **wrong answers went from 32 to 5**, which is the trade
worth having, because a wrong border commits a wrong printing while an abstain
only queues the card.

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

These are concentrated in `pre1998/black`, which is most of why that stratum
reads 58% against 93% for its white sibling — the corpus's black-bordered
pre-1998 sample is heavily foreign, because that is what those print runs were.

Verified by opening the images: `Strip Mine`'s corpus entry is set `rin`,
Rinascimento, and the picture is an Italian card reading "Miniera a Cielo
Aperto". The manifest stores the English name because that is the identity the
catalog is keyed on.

**The real limitation is downstream, not in the read.** Fuzzy matching resolves
against English names, so a correctly-read Italian card will not find its
printing. If you scan non-English cards, expect them to queue. That is a genuine
gap and a real product decision — it is just not an OCR one, and **reading
foreign cards is planned as its own sprint** rather than treated as a defect
here.

**This is now handled.** `scan/corpus/lang.py` adds a `lang` column from
Scryfall and the scorer reports non-English printings on their own row. The
counts: 214 English, 7 Spanish, 3 Italian, 3 French, 2 Japanese, 2 Chinese.

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

Not gold-bordered cards in any useful sense. The stratum is `punk` and `pssc`:
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

### `2015+/borderless` (43%) — a stratum that is not about borders

The most misleading label in the corpus. `border=borderless` comes from
Scryfall's `border_color`, but what these seven cards actually share is being
**special-treatment products** — Secret Lair, Mystical Archive, Universes
Beyond, Alchemy — with unrelated layouts. Grouping them by border colour puts a
normal-layout Secret Lair beside a full-art TMNT card and reports one number for
both.

The three that pass are Secret Lairs with an ordinary title bar. The four that
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
43% is measuring special products, not borderless frames.

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

### Gold borders: 0% across 1998-2002, 2003-2014 and 2015+

The one stratum that is comprehensively broken, and it is not a regression —
the previous pipeline scored 0% here too. These are World Championship cards.
Their footer prints a **sideboard marker** (`SB`, `GB`) where a collector number
would be, and they carry no number at all.

The parser refuses them deliberately. Folding digit lookalikes would turn `SB`
into `58` and `GB` into `68`, inventing a collector number on cards that print
none — measured at 18% of the pre-1998 gold stratum before the guard existed.
**An invented printing is the worst thing this pipeline can produce**, and
refusing a real number occasionally is the right trade.

Gold-bordered cards will not auto-commit on a number. They can still commit on
year plus border.

### The rest

`1998-2002/black` at 63% and `2003-2014/*` at 50-63% are the copyright-row era:
the number is four small italic digits at the tail of the credit line, and
`30` reads as `80` about as often as you would expect. These commit via the
`copyright` number source, which the Go side treats as **upgrade-only
evidence** — a misread here ranks a card wrongly rather than committing it
wrongly.

## Things the corpus cannot tell you

Three real limitations that this document's numbers do **not** cover, because
the corpus has no way to test them:

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
