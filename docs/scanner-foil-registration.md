# Why the foil sparkle reader misses, and what to do about it

The old-frame foil reader (`BorderKit/Sparkle.swift`, shipped in `ba1412c`)
answers "foil or not foil" on retro frames by correlating a fitted template
against the printed starburst at the text box's lower-left corner. On its first
live session it missed 4 of 15 foils.

This note is the measurement of *why*, and what the options are. It exists
because the first two explanations were both wrong, and both were plausible
enough to be worth writing down as refuted.

Everything here is reproducible from `scan/foil-corpus/stills/s3-*.jpg`:

    make cardkit
    ./bin/cardkit-probe --sparkle-where scan/foil-corpus/stills/s3-02.jpg

## What raised the question

Session 3 (2026-08-06) was one pile, every card a retro-frame foil, 15 captures.
Four read as nonfoil: Glowrider, Trap Digger, Hard Evidence, and Brainsurge's
first of two captures.

All four are **in the training corpus, labelled foil**, on crops that score
0.55–0.80 against a bar of 0.52. The same physical cards. The offline probe
reproduces the live verdict 15 out of 15, so nothing is lost between the corpus
harness and the phone — the difference is in the photographs.

| card | s1 crop | s2 crop | s3 still |
| --- | --- | --- | --- |
| Glowrider | 0.798 | 0.781 | **0.020** |
| Trap Digger | 0.693 | 0.719 | **0.339** |
| Hard Evidence | 0.568 / 0.778 | 0.554 | **0.425** |
| Brainsurge | 0.650 / 0.667 | 0.578 / 0.681 | **0.496** / 0.708 |

## Refuted: the live sampler loses something the harness keeps

The corpus harness feeds pre-cut crops through a `CardSampler` backed by a flat
image; live, `sparkleInCard` samples the perspective-corrected flatten. Two
different backings for the same search, so a discrepancy there was the first
guess.

It is not that. `cardkit-probe --image ... --border` on the s3 stills reproduces
the live finish verdict **15 of 15**, including all four misses. The two paths
agree.

## Refuted: the marker is outside the search window

This one had real evidence behind it. Glowrider's best match sits at
`du = -0.0238`, which is *exactly* `-SparkleGate.searchU` — the search hit the
wall of its own window. Its sample count drops to 34112 from the usual 54444,
which is the same fact from the other side: `sparkleScan` clips the refine
neighbourhood to `abs(i) <= cellsU`, so both coarse candidates sitting against
the corner is what costs those reads. Trap Digger is one cell short of the same
wall in both axes.

That is the signature `docs/scanner-tuning.md` already describes for a
mis-centred search, and it points at registration: the corpus crops are cut from
a *known* card-space rect and get their alignment for free, while a live still
gets it from `locateCard`, whose per-capture variance is comparable to the whole
±0.0238 window.

**`--sparkle-where` was built to test exactly this, and it refutes it.** It runs
the same search twice, once at the fitted window and once at four times its
half-width in each axis, and reports both peaks:

| card | fitted | wide (4×) | peak outside the fitted window? |
| --- | --- | --- | --- |
| Glowrider | 0.020 | **0.000** | yes, and worthless |
| Trap Digger | 0.339 | 0.473 | yes — still under the bar |
| Hard Evidence | 0.425 | 0.446 | yes — +0.02 |
| Brainsurge #1 | 0.496 | 0.513 | yes — +0.02 |

A search with sixteen times the area to hunt in **rescues none of them**.
Glowrider's wide peak of 0.000 is not a low correlation — `sparkleScan` reports
exactly 0 when the patch it settled on fails `minContrast`, so that is an
abstention. Widening the window is not the fix, which is fortunate, because
widening it is separately measured to admit two false positives.

## What actually separates the misses: patch contrast

`SparkleReading.contrast` is the median absolute deviation of the patch the
verdict was taken from — how much structure is there to correlate against at
all. Across all fifteen stills:

| still | card | score | contrast | verdict |
| --- | --- | --- | --- | --- |
| s3-09 | Trap Digger | 0.339 | **0.0089** | miss |
| s3-02 | Glowrider | 0.020 | **0.0241** | miss |
| s3-13 | Hard Evidence | 0.425 | **0.0343** | miss |
| s3-14 | Brainsurge #2 | 0.708 | 0.0361 | pass |
| s3-03 | Brainsurge #1 | 0.496 | **0.0380** | miss |
| s3-04 | Abiding Grace | 0.636 | 0.0549 | pass |
| s3-08 | Dress Down | 0.544 | 0.0713 | pass |
| s3-12 | Lion Umbra | 0.719 | 0.0768 | pass |
| s3-06 | Charitable Levy | 0.525 | 0.0792 | pass |
| s3-10 | Victimize | 0.738 | 0.0842 | pass |
| s3-11 | Consuming Corruption | 0.637 | 0.1213 | pass |
| s3-01 | Meltdown | 0.713 | 0.1245 | pass |
| s3-05 | Primal Prayers | 0.647 | 0.1252 | pass |
| s3-15 | Unstable Amulet | 0.754 | 0.1260 | pass |
| s3-07 | Unholy Heat | 0.699 | 0.1342 | pass |

**Every miss is in the bottom five by contrast.** Nothing above 0.0549 missed.
The one card that breaks a clean threshold is Brainsurge's second capture, which
passes at 0.0361 — and Brainsurge is also the card that was captured twice in
one session and answered differently each time (0.496 then 0.708), which is the
same phenomenon seen twice.

So the marker is in the right place and the search finds it. There is just very
little of it left in those pixels. The neighbourhood is washed out — the most
likely cause being the foil's own specular response to the desk lamp at that
angle, which is exactly the thing the reader was designed to be immune to by
reading printed ink rather than diffraction sheen. It reads ink; it just needs
the ink to still have contrast.

**`SparkleGate.minContrast = 0.005` is 7–11× below where the misses live.** It
was set to catch a fully blown or fully crushed patch, and it does. It does not
catch a patch with a tenth of the structure of a good one, so those get scored
as though they were evidence and their low correlation is read as "not foil".

## The open anomaly: a stronger peak at du ≈ +0.07

Separate from the misses, and unexplained. On several cards the wide search
finds a peak *well outside* the fitted window that scores **higher** than the
one inside it:

| card | fitted | wide | wide offset |
| --- | --- | --- | --- |
| Meltdown | 0.713 @ +0.013 | **0.850** | +0.073 |
| Consuming Corruption | 0.637 @ +0.005 | 0.659 | +0.073 |
| Lion Umbra | 0.719 @ −0.011 | 0.729 | +0.067 |
| Brainsurge #1 | 0.496 @ +0.003 | 0.513 | +0.076 |

Four cards clustering at du ≈ +0.07 is not noise. Either `CardLayout.sparkleU`
(0.205) is mis-centred by about 0.07 of a card width for some frames and the
fitted window is matching a weaker secondary feature, or the template correlates
with some other piece of card furniture about 0.07 to the right. **This should
be understood before the template is refitted**, because a refit that silently
re-centres onto the wrong feature would look like an improvement on this corpus
and fail on the next.

Note it does not rescue any miss: Glowrider and Trap Digger and Hard Evidence
are not in that cluster.

## Why the false negative rate is now the number that matters

As of 2026-08-06 an unread finish auto-commits as **nonfoil** rather than
queueing (see `verdict` in `internal/tui/autoscan.go`). Queuing was built and
reversed the same day: the miss is this reader's to fix, and making the operator
answer for it on every retro card is a worse trade than a row to correct.

The consequence is that every miss here is now a silently underpriced row.
There is no false-positive budget to spend in exchange — the corpus has never
produced one, and this session did not either — so the work is all on recall.

## Options, and what each would buy

**1. Raise `minContrast` so a washed-out patch abstains instead of answering.**
Cheap, and it makes the log honest — `finishSource` and `sparkleScore` are on
the wire now, so an abstention would be visible. But under the current policy
abstain and "not foil" write the same row, so on its own **this changes no
outcome.** Worth doing as instrumentation, not as a fix.

**2. Fix it at the capture.** If the cause is the lamp's specular response, the
lever is exposure or angle, not correlation. The strongest evidence for this is
Brainsurge answering twice, differently, in one session, and every corpus crop
of these same cards scoring fine. Cheapest real experiment: reshoot the four
failing cards with the lamp moved and see whether contrast recovers. If it does,
this is a rig note and a capture-side gate, not an algorithm change.

**3. Retry on low contrast.** The trigger already re-fires on placement, and
Brainsurge's second look answered correctly. A card whose patch contrast is
below a floor could be worth one more capture rather than a verdict — the
hands-free flow already has the machinery to ask for another look, and this is
the one case where a second look is known to have worked.

**4. Normalise for local contrast before correlating.** `sparkleNormalise`
already makes the patch zero-mean and unit-norm, which is why the reader
survives a desk lamp at all. That does not help when the *structure* is gone
rather than the level — a unit-norm vector of noise correlates like noise. This
is probably not the lever, but it is cheap to test against the corpus.

**5. Re-centre `sparkleU`/`sparkleV`.** Blocked on the du ≈ +0.07 anomaly above,
and refuted as a fix for the misses by `--sparkle-where`. Do not start here.

## What not to do

- **Do not widen `searchU`/`searchV`.** Measured twice now: it admits false
  positives (0.470 → 0.676 on the corpus's highest nonfoil), and
  `--sparkle-where` shows it rescues nothing.
- **Do not lower `SparkleGate.accept`.** The lowest miss is 0.020 and the
  highest known nonfoil is 0.509. There is no threshold that separates them.
- **Do not refit the template on session 3.** Half its foils are the washed-out
  captures this note is about; fitting on them would pull the template toward
  the failure.

## The reshoot happened, and the answer is half

Session 4 (`scan/foil-corpus/session4-telemetry.log`, 2026-08-06 22:25) is the
experiment this section used to ask for: the same pile, a day later, same rig.
It does not settle option 2 the way it was expected to.

| card | s3 contrast | s4 contrast | s4 verdict |
| --- | --- | --- | --- |
| Hard Evidence | 0.0343 | 0.0468 | **recovered** |
| Brainsurge #1 | 0.0380 | 0.0393 | **recovered** |
| Trap Digger | 0.0089 | 0.0086 | miss, unchanged |
| Glowrider | 0.0241 | **0.0039** | miss, and worse |

So the lamp explains the marginal cards and does not explain the two that
matter. Four misses became two, and the two that remain are the same two, at the
same contrast, on a different day. A capture-side gate would not have caught
them; a retry (option 3) has now effectively been run twice and did not converge
for them either.

What does separate them is not in the options list above: **Glowrider and Trap
Digger are the only genuine 2003 Legions/Scourge printings in the pile.** Every
card that reads cleanly is a 2024 retro reprint (footers `…Coast 407`, `…Coast
418`); these two print the old `N/TOTAL` form, `15/145` and `24/143`. Older
foiling is glossier. That is a hypothesis, not a measurement, but it is the one
consistent with two sessions and it is testable — the corpus has no foil at all
between 2003 and 2024 (see its README), so the era is confounded with everything
else and no amount of tuning on this corpus can tell them apart.

Glowrider's 0.0039 is also below `SparkleGate.minContrast`, so on that card the
reader **already abstained** and `verdict` wrote `nonfoil` from the abstention
anyway. Option 1 above says raising `minContrast` "changes no outcome" under the
current policy, and session 4 is that prediction coming true in the field. The
thing worth reconsidering is therefore the policy, not the threshold.

The next measurement is a pile of genuine pre-2024 foils, wide enough to tell
era from lamp. Until the corpus has them, every constant here is fitted on
2024 reprints and quietly assumed to generalise.

## The colour channel: built, measured, and not switched on

Built 2026-08-06 after session 5 (`/tmp/scan-telemetry.log`, 132 reads of a pile
that was entirely foil, of which only 37% read foil). It correlates the same
template, over the same search window, on a **warm-cool axis** (blue minus red)
instead of luma. `PixelReader.warmCool`, `SparkleVerdict`, and its own fitted
template in `SparkleChromaTemplateData.swift`.

It reports and does not vote. Both results below are why.

**On `scan/foil-corpus` it is the best thing that has happened to this reader.**
With its own template and a bar at 0.68:

| class | n | luma accepts | either channel |
| --- | --- | --- | --- |
| retro foil | 27 | 24 | **27** |
| retro nonfoil | 18 | 0 | **0** |
| modern | 5 | 0 | **0** |

Held out — template fitted on session 1, scored on session 2 — either-channel
takes 13 of 13 against luma's 11, at 0 of 18 nonfoils. It rescues **Charitable
Levy**, the only foil this corpus has ever missed, and it does it live too: on
session 5's two captures the colour channel scores 0.812 and 0.713 where luma
scores 0.414 and 0.366.

**On `scan/fixtures`, which is a different rig, the ordering inverts.**

| fixture | truth | luma | chroma |
| --- | --- | --- | --- |
| Eternal Dragon (`old-frame-set-code-from-rules`) | **nonfoil** | 0.363 | **0.796** |
| Cephalid Looter (`old-frame-same-set-variants`) | **nonfoil** | 0.428 | **0.782** |
| Meltdown (`modern-copyright-tail-number`) | foil | 0.645 | 0.633 |
| `ocr-mangle` | foil | 0.473 | 0.000 |

Both nonfoils were confirmed by eye from the flatten — matte, no sheen, no
starburst at the text box corner. They are the two highest-scoring cards in the
set and they outscore every foil in it. **No threshold fixes that.** It is not a
bar in the wrong place; it is the channel ranking the classes backwards on
photographs it was not fitted on.

The likely cause, and the thing to test before trying again: the warm-cool axis
carries the text box's own boundary, which every retro card has, foil or not.
`scan/foil-corpus` is one desk under one lamp, so a template fitted on it can
encode that rig's colour cast, score beautifully in-corpus, and have learned the
furniture instead of the marker. The luma channel survives the move between
rigs; this one does not, yet.

**What would switch it on:** a second foil corpus, shot on another rig, *with
nonfoils in it*. `--sparkle-score` prints both channels and the either-channel
row; `sparkleChromaScore` and `sparkleChromaContrast` are on the wire and in the
session log as `chroma=…`. If a rig-two corpus reproduces the 0-false-positive
result, turning it on is one `||` in `SparkleVerdict.isFoil`.

### What it does not fix

Glowrider and Trap Digger. Measured on session 5's stills, the colour channel
scores **0.005 / 0.000** on Glowrider and **0.201** on Trap Digger — no better
than luma and mostly worse.

That refutes the prediction this channel was built on. The argument was that
those two cards' markers carry 5-13x more *structure* (median absolute
deviation) in warm-cool than in luma, which is true and measurable. It does not
follow that the structure is the marker: more spread in a channel is not the
same as a better match to a template in it, and here it is not. Their failure
remains what session 4 said it was — the patch is washed out — and the colour
axis does not recover it.
