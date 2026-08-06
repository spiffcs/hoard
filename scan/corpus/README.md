# The parser corpus

A stratified sample of card images, replayed through the reader and scored
against known-good answers, so a frame era that parses badly is visible instead
of averaged away.

    ./scan/corpus/fetch.sh [per-stratum]   # default 25; writes images/ + manifest.tsv
    make cardkit-score                     # score per era × border
    make cardkit-score ARGS=--misses       # and list every card that failed

The reader is `CardKit` — the pipeline the iPhone app runs — scored in one
process by `cardkit-probe --score`. There used to be a `sweep.sh` beside it that
scored the macOS helper's own reader by launching one process per image; that
reader existed for the Continuity Camera path and went with it, and the script
went too. It was the same numbers several minutes slower.

## What it does and does not test

**These are clean digital scans.** They exercise the parser — layout
assumptions, the collector band, the copyright line, title selection — and
nothing else. They do not test the trigger, rectangle detection, the
perspective crop, glare, focus, or the camera's 1080p ceiling. A card that
parses perfectly here can still fail on a desk, and one that fails here is
broken in a way no lighting will fix.

That is the point: it isolates frame-era parsing from capture quality, which no
other check in this repo does. `make scan-check` pins specific decisions on
29 real photographs; `TestSessionReplay` measures a whole session end to end.
This one answers "which *kinds* of card can we read at all".

One consequence of the clean-scan format worth knowing: the card fills the
frame, so Vision's rectangle detector locks onto the boundary between the
border and the inner frame rather than the card's outer edge. The perspective
crop therefore excludes the printed border. **Nothing that reads the crop can
be tuned here.**

The border *reader* is a deliberate exception, and the distinction is worth
stating because it is load-bearing. It never touches the crop: it reconstructs
the card from the position of text it has identified by content, then samples
the full-resolution frame. Here the card *is* the image, so the true card rect
is known exactly — which makes this the one place the layout constants in
`CardLayout` can be fitted against ground truth rather than guessed. See
`./border.sh`.

What it still cannot tell you is how any of it behaves under a desk lamp, and
that is not a small caveat. A gate on the ring's colour saturation looked
perfect here — gold measured 0.36 against ≤0.20 for white and black — and was
wrong the first time it met a photograph, where a *white* border under warm
light reads 0.40 and is indistinguishable from gold. Fit here; confirm on
`scan/fixtures/`, which are real.

## Sampling

Stratified by **frame era × border colour**, not per set. Most sets in an era
share one frame, so ~300 images cover every distinct layout where two or three
from each of ~900 sets would be mostly redundant and far slower to sweep. Eras
are cut at frame changes rather than set boundaries:

| era | what changed |
| --- | --- |
| `pre1998` | no collector number printed at all |
| `1998-2002` | collector numbers arrive, old frame |
| `2003-2014` | the 8th Edition frame |
| `2015+` | the M15 frame |

The sample is drawn with a fixed seed, so re-running `fetch.sh` reproduces the
same corpus and only downloads what is missing.

## Layout

    images/       the pixels. Gitignored — third-party, fetched not authored.
    manifest.tsv  one row per image with the known answer. Tracked.

Do not commit the images. They belong to their rightsholders; the manifest is
enough to rebuild the corpus exactly.

## Baseline, 2026-08-03 (135 images, 8 per stratum)

Name is scored leniently — a prefix either way counts, since the helper's job
is to hand downstream something fuzzy matching can land. Number is strict,
except that reading none is correct for `pre1998`, where the card has none.

| era | border | n | name | number |
| --- | --- | --- | --- | --- |
| 1998-2002 | black | 8 | 100% | 62% |
| 1998-2002 | gold | 8 | 100% | 0% |
| 1998-2002 | silver | 8 | 62% | 75% |
| 1998-2002 | white | 8 | 100% | 88% |
| 2003-2014 | black | 8 | 88% | 50% |
| 2003-2014 | gold | 8 | 88% | 0% |
| 2003-2014 | silver | 8 | 75% | 50% |
| 2003-2014 | white | 8 | 62% | 50% |
| 2015+ | black | 8 | 100% | 75% |
| 2015+ | borderless | 7 | **14%** | 57% |
| 2015+ | gold | 8 | 62% | 0% |
| 2015+ | silver | 8 | 75% | 38% |
| 2015+ | white | 8 | 88% | 100% |
| 2015+ | yellow | 8 | 88% | 100% |
| pre1998 | black | 8 | 75% | 100% |
| pre1998 | gold | 8 | 100% | 100% |
| pre1998 | white | 8 | 88% | 100% |
| **all** | | **135** | **81%** | **61%** |

### What that baseline says

- **Borderless at 14% by name is an artifact of this corpus, not a real gap.**
  A later live session of nothing but borderless cards read their names fine —
  Quicksilver, Bullseye, Springheart Nantuko, Arna Kennerüd, Laelia, Winter
  Moon, Flare of Duplication all resolved. The difference is the format: in a
  full-bleed scan the card *is* the frame, so there is no card-against-desk
  edge, the detector finds nothing to crop, and a borderless card has no inner
  border to fall back on either. It is the same limitation that makes this
  corpus useless for border colour, showing up as a name failure.

  Treat any borderless row here as unmeasured. The real borderless problem is
  in the trigger, not the parser — see `docs/scanner-tuning.md`.
- **Gold borders never yield a number: 0% in every era.** Those are World
  Championship decks, whose collector numbers are player-prefixed (`jn12`,
  `gn12a`) and match no numeric pattern. Arguably correct to skip, but it
  should be a decision rather than an accident.
- **Silver borders read poorly across the board** (62-75% by name) — Un-set
  layouts break the usual assumptions on purpose.
- **`pre1998` names read fine** (75-100%). That era's difficulty is not
  reading the card, it is that the card carries nothing to identify *which
  printing* it is. See `docs/scanner-tuning.md`.

Re-run the sweep after any parser change and update this table when it moves;
a per-stratum drop is the earliest warning that a frame era has regressed.

## Border baseline, 2026-08-04 (231 images, 40 per pre-1998 stratum)

`./border.sh` scores the border reader. Coverage and precision are reported
separately and never averaged: the property that matters is *when it speaks it
is right*, not *it is usually right*, because a wrong border silently picks the
wrong printing.

| era | border | n | spoke | correct | wrong |
| --- | --- | --- | --- | --- | --- |
| pre1998 | black | 40 | 92% | 100% | 0 |
| pre1998 | white | 40 | 75% | 100% | 0 |
| 1998-2002 | black | 8 | 88% | 100% | 0 |
| 1998-2002 | white | 8 | 100% | 100% | 0 |
| **white + black, all eras** | | **127** | **~60%** | **100%** | **0** |
| gold + silver | | 104 | 8% | 0% | **8** |

Only the pre-1998 rows are a target — everything later prints a collector
number, which beats a border as evidence, so silence there costs nothing.
**The number that must stay zero is the white/black one.**

The eight wrong reads are all gold or silver called white, and they are a real
limit rather than a tuning miss: a silver border is light grey, and the chroma
that separates gold on a clean scan is the same number a warm lamp fabricates
on white. The Go side handles it structurally instead — a printing whose border
the reader cannot recognise is never *ruled out* — so those reads cost ordering
on World Championship and Un-set cards, groups 2 and 3, and nothing else.

What it declines on pre-1998 is mostly basic lands and cards whose title never
read (two rows are what make the reconstructed scale checkable), plus a handful
where the ring and the frame reference land on the same surface — which is the
case it is *supposed* to refuse, since that is what a drifted ring looks like.
