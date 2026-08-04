# The parser corpus

A stratified sample of card images, replayed through the helper and scored
against known-good answers, so a frame era that parses badly is visible instead
of averaged away.

    ./scan/corpus/fetch.sh [per-stratum]   # default 25; writes images/ + manifest.tsv
    ./scan/corpus/sweep.sh                 # score per era × border
    ./scan/corpus/sweep.sh --misses        # and list every card that failed

## What it does and does not test

**These are clean digital scans.** They exercise the parser — layout
assumptions, the collector band, the copyright line, title selection — and
nothing else. They do not test the trigger, rectangle detection, the
perspective crop, glare, focus, or the camera's 1080p ceiling. A card that
parses perfectly here can still fail on a desk, and one that fails here is
broken in a way no lighting will fix.

That is the point: it isolates frame-era parsing from capture quality, which no
other check in this repo does. `make scan-check` pins specific decisions on
19 real photographs; `TestSessionReplay` measures a whole session end to end.
This one answers "which *kinds* of card can we read at all".

One consequence of the clean-scan format worth knowing: the card fills the
frame, so Vision's rectangle detector locks onto the boundary between the
border and the inner frame rather than the card's outer edge. The perspective
crop therefore excludes the printed border. Anything that wants to look at the
border must not be tuned here.

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
