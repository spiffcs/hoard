# The foil-sparkle corpus

Labelled card captures for the old-frame foil reader — the detector that answers
"foil, or not foil" on frames that print no set/language row and therefore carry
no `finishFromSeparator` evidence.

Unlike `scan/corpus`, **these pixels cannot be refetched.** They are photographs
of a specific pile of cards on a specific desk under a specific lamp, and they
are the entire empirical basis for `SparkleGate.accept`. Session 1's original
stills were already lost to `/tmp` once before this directory existed.

## Layout

    cards/       284x246 PNG, the sparkle's neighbourhood in canonical card
                 space (u 0.00-0.45, v 0.72-1.00 at 630x880). TRACKED. This
                 is what the fitter and the scorer consume, so a clean clone
                 can re-derive the template and reproduce the scoreboard.
    full/        the same cards flattened whole, 630x880. Not tracked — 44 MB,
                 and everything the detector needs is in cards/.
    stills/      session 2's original camera stills, 3024x4032. Not tracked
                 (165 MB). Kept so the flatten can be re-run if locateCard or
                 the card geometry changes.
    labels.tsv   one row per card. TRACKED.
    session2-telemetry.log   the live wire log for session 2. TRACKED.

`cards/` is deliberately a generous crop rather than a tight one: the search
window is ±0.025 in u and ±0.022 in v, and this region gives roughly eight
times that in every direction, so `sparkleU`/`sparkleV` can be re-measured
without re-cropping.

## labels.tsv

| column | meaning |
| --- | --- |
| `id` | `s1-NN` / `s2-NN`, the filename stem under `cards/` |
| `session` | `s1` or `s2`. Fit and score across sessions, never within one. |
| `frame` | `retro` (prints a sparkle when foil) or `modern` (never prints one) |
| `era` | copyright year as read, `-` when the footer did not read |
| `finish` | `foil` or `nonfoil`, established by eye from `cards/` |
| `physical` | which physical card this is. Several were captured twice. |
| `name` | card name |

Counts: **27 retro foil, 18 retro nonfoil, 5 modern foil.**

The modern five are neither positives nor negatives. Their frame prints no
sparkle at all, so they exist to prove the detector leaves them alone — the
set-row separator already reads them correctly and must keep doing so.

`physical` matters when quoting a rate. Hunter Sliver, No-Dachi and Slith
Predator each appear twice, so 18 nonfoil rows are 15 distinct cards; counting
rows as independent samples overstates the evidence.

## How the labels were established

By eye, from `cards/`, one at a time. Not from the scanner's own `finishHint`
— that is the signal being replaced, and on these frames it is empty or wrong
(Charitable Levy recorded `nonfoil` off a set row whose code parsed as `CARD`).

One label was corrected during the build: **Root Elemental (`s2-15`) was first
recorded as foil from a contact sheet and is a nonfoil** — its text-box corner
is clean. It had been the detector's worst "miss" at 0.214; it is in fact a
correct rejection. Read the crop, not the sheet.

## Reproducing the scoreboard

    make cardkit
    ./bin/cardkit-probe --sparkle-score scan/foil-corpus            # the table
    ./bin/cardkit-probe --sparkle-score scan/foil-corpus --cards    # per card

At `SparkleGate.accept`, over `cards/`:

| class | n | min | median | max | accepted |
| --- | --- | --- | --- | --- | --- |
| retro foil | 27 | 0.409 | 0.686 | 0.798 | **26** |
| retro nonfoil | 18 | −0.535 | 0.320 | 0.458 | **0** |
| modern | 5 | 0.354 | 0.406 | 0.438 | **0** |

The single miss is Charitable Levy — the FDN retro frame's text box is taller,
so its marker sits below where `CardLayout.sparkleV` looks.

**That table is in-sample**, because the shipping template is fitted on all 27
foils. The honest number is held out, and `--only-session` is how to get it:

    ./bin/cardkit-probe --sparkle-fit scan/foil-corpus /tmp/t.swift --only-session s1
    # install /tmp/t.swift, rebuild, then:
    ./bin/cardkit-probe --sparkle-score scan/foil-corpus --only-session s2

Fit on s1, score s2: 12 of 13 foils, 0 of 18 nonfoils. Fit on s2, score s1:
13 of 14. Same single card missed in both directions.

## Refitting

    ./bin/cardkit-probe --sparkle-fit scan/foil-corpus \
        scan/hoard-scan/Sources/BorderKit/SparkleTemplateData.swift

Four passes: an unaligned mean, then three re-align-and-rebuild rounds. It
prints how far the template moved each round; the last should be ≳0.99.

## What this corpus does not cover

- **Nothing before 2001.** Every card here is 2001 or later. The pre-1998 frame
  lays its text box out differently and the reader scores noise on it — which is
  why `SparkleGate.firstFoilYear` exists rather than being decoration.
- **No white borders.** Every retro card in both sessions is black-bordered.
  The patch normalisation should make border colour irrelevant; that is an
  argument, not a measurement.
- **No foil between 2003 and 2024.** The 2001, 2004 and 2006 cards here are all
  nonfoils, so the two fitted eras are the only ones with positive examples.
- **One rig, one lamp, two sessions.** Both were shot the same way.
