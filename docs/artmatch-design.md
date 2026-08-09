# Art-match: designing the identification channel correctly

**Status: design proposal, 2026-08-08. Nothing in the "proposed design" section
is built.** Everything in "What we measured" is built and green.

This document exists because the v2 sprint's premise was tested and refuted,
and the refutation pointed somewhere more interesting than the fix it ruled
out. It records what was measured, what production scanners actually do, and
what that combination says hoard should build.

The operating pipeline is `docs/artindex-pipeline.md`; the measurement history
is `docs/artindex-results.md`; the sprint this supersedes is
`docs/sprint-artmatch-v2.md`.

---

## What we measured, and what it refuted

Stage A widened the perceptual hash from 64 bits (8×8 DCT keep block) to 256
(16×16). The premise, written down in `docs/artindex-results.md` in August
2026, was that foil glare was compressing the winner-to-runner-up margin to
2–6 bits and that more bits would widen it.

Stage C measured both widths over **identical fetched bytes and identical
crops**, each given the best gate it can possibly achieve by exhaustive sweep,
scored against 35 labelled hand-held stills:

| | 64-bit (8×8 keep) | 256-bit (16×16 keep) |
|---|---|---|
| correct nearest-neighbour | 30/35 (86%) | **32/35 (91%)** |
| margins observed | 0–14 | 0–72 |
| best zero-wrong gate | distance ≤16, margin ≥0 | distance ≤92, margin ≥0 |
| decisive at that gate | **28/35 (80%)** | 26/35 (74%) |

The margins widened about 4×, exactly as the arithmetic guarantees. **Gate
coverage did not improve.** The wider hash identifies slightly better and gates
slightly worse; both differences are two reads at n=35, which is noise.

Three things that measurement exposed:

1. **The margin criterion does no work.** Both footprints' optimal gate is
   `margin ≥ 0`. Only absolute distance separates anything. "Margins too thin
   to gate" named the wrong variable, and Stage A was aimed at it.
2. **The separation is two units wide at both widths** — 64-bit thresholds at
   16 with the nearest wrong read at 18; 256-bit at 92 with the nearest wrong
   read at 94. That is 3.1% of a 64-bit range and 0.8% of a 256-bit one, so
   the wider hash is proportionally *tighter*.
3. **The failures move when the width changes.** 256-bit misses {s9-07, s9-11,
   s9-14}; 64-bit misses {s9-04, s9-14, s9-21, s9-26, s9-27}, overlapping only
   at s9-14. Failures that relocate under a footprint change are marginal reads
   near a decision boundary, not something more bits address.

The 256-bit hash was kept: it identifies better, costs nothing extra, and its
distances are more legible. But it did not earn the sprint's Stage D, and the
full 107k eval was not run — it costs a three-hour index rebuild to test a gate
the cheap measurement already says is not there.

---

## How production scanners actually do this

The original premise for this channel was "this is how Delver Lens and other
scanners identify cards." That is worth checking rather than assuming, so here
is what the public record says.

### The classical pipeline is universal, and hoard already has it

Every open implementation converges on the same four stages: normalise
lighting, find the card's contour, perspective-correct it to a rectangle,
then match. Timo Mikonen's detector resizes to 1000px on the long edge and
applies CLAHE in LAB space before thresholding and contour extraction, then
takes the smallest-area bounding quadrilateral and applies a four-point
perspective transform. The Thoughtseize C++/OpenCV implementation does the
same with `cv::getPerspectiveTransform` and `warpPerspective`. Nessy's
walkthrough thresholds, takes the second-largest contour, and warps by corner
points.

hoard has this already, and better than most: CardKit locates the card and
`cardkit-probe --emit-card` emits the flatten. **This part is not the problem
and does not need work.**

### Perceptual hashing is the mainstream matcher, and our parameters are right

The most production-grade open implementation found — Moss Machine, an
automated sorter covering 317 card games — uses **pHash at 256 bits with a
16×16 hash size**. That is, independently, exactly the footprint Stage A just
landed. It also uses **separate hashes for the R, G and B channels** rather
than a single luma hash, and a default Hamming threshold of 10 with "adaptive
threshold scanning — progressive tightening from strict to relaxed", early
termination on an exact match, and a minimum confidence of 85%. Throughput is
about 2 seconds per card.

Two useful confirmations there: the 16×16/256-bit choice is the industry
default rather than an overreach, and **multi-channel hashing is standard
practice, not an exotic fallback**. hoard's own foil work independently
measured the warm–cool chroma channel as 5–13× more angle-stable than luma on
these cards (`docs/sprint-foil-recognition.md`), so this converges from two
directions.

Weaker implementations hash the whole card at small hash sizes and hit exactly
the ceiling hoard already measured and documented: hj3yoo's detector settled on
a 16-bit hash across ~10,000 cards and reports it "suprisingly effective" on
10-card classification while "impractical for large card databases". That is
the same finding as hoard's whole-card refutation, arrived at from the other
end.

The known fragility of the approach is stated plainly in the survey literature:
the perceptual hash "is quite sensitive to the segmentation of the card, and if
too little or too much extra border is included in the segmented candidate, the
hash difference test fails." pHash is also not rotation invariant.

### Feature matching and CNN embeddings are where scale goes

Where implementations outgrow hashing they move to keypoint descriptors
(SIFT/SURF/ORB with FLANN matching and RANSAC verification) and then to learned
embeddings. SIFT is consistently more accurate and ORB substantially faster —
SIFT beats ORB on inlier ratio even at 100 keypoints versus ORB's 2000.

Collectors (PSA) — grading 40–45k cards a day — publicly describes rejecting
SIFT/SURF/ORB and shipping **CNN-derived deep embeddings of a few thousand
floating-point values, searched by approximate nearest neighbour**. They do not
publish accuracy, dimensions, or index type, and notably do not describe how
they separate reprints; human researchers make the final call.

### What the market leader gets wrong is exactly our problem

Delver Lens — the reference implementation in the original premise — began in
2016, recognises Alpha through current sets, works offline, and scans the full
card. Reviews are consistent about where it succeeds and where it fails:

> "Delver Lens is fast and usually strong at recognizing the card name. However
> … the weak spot is exact printing accuracy, with community reports repeatedly
> mentioning cards being identified correctly by name but matched to the wrong
> set, which can turn a useful scan into a misleading price for cards with many
> reprints."

The recommended workflow is "scan fast, verify sets carefully, then export."

**That is the whole finding.** Name recognition is solved. Printing
disambiguation is not solved by the market leader, is not addressed by PSA's
public writeup, and is not attempted by any open implementation surveyed — most
hash the whole card, which is precisely the signal that cannot separate two
printings of one name.

---

## The deeper diagnosis: a domain gap, not a hash problem

Following the "check the crop first" step turned up something that reframes
everything below it. s9-14 was chosen because it fails under *both* hash
widths, which normally means the input is bad. Three hypotheses were tested and
all three were wrong:

- **Bad flatten?** No. The crop is clean, fully framed and perspective-correct
  (compare s9-14 and s9-15 side by side — both are good).
- **Glare blowing out the art?** No. Over the exact rectangle `FromCard`
  hashes, s9-14 measures mean luma 0.294 and contrast 0.529 against s9-15's
  0.309 / 0.524 — s9-14's contrast is *higher* than s9-13's, and neither has a
  single blown highlight above 97%.
- **Aspect-ratio drift shifting the fixed-fraction crop?** No. s9-14's flatten
  is 0.667 against its sibling's 0.689, but rescaling both to a real card's
  63:88 before hashing leaves the distance between them unchanged at 24 bits.

Then the actual measurement, over all 35 s9 stills — the same hash, the same
crops, split by whether the two things being compared come from the same
*domain*:

| | min | median | mean | max | n |
|---|---|---|---|---|---|
| capture ↔ capture, **same physical card** | 4 | 36 | 38 | **68** | 25 |
| capture ↔ capture, different cards | **104** | 126 | 126 | 148 | 570 |
| capture ↔ reference, **its own card** | 30 | 82 | 79 | **112** | 35 |
| capture ↔ reference, a different card | **100** | 120 | 120 | 146 | 525 |

**In-domain, the hash is essentially perfect.** Two photographs of the same
card never exceed 68 bits; two photographs of different cards never come closer
than 104. That is a 36-bit gap with **zero overlap** across 595 pairs — a gate
could sit anywhere in it and be exactly right.

**Cross-domain, the distributions overlap.** A photograph reaches as far as 112
bits from its own card's Scryfall scan while coming as close as 100 bits to a
different card's. That 12-bit overlap is the entire problem, and it is
structural: the index is built from clean, evenly-lit, flatbed-quality scans
and every query is a hand-held photograph of a foil under a desk lamp.

Every failed diagnosis so far — thin margins, too few bits, glare, crops —
was a symptom of this one cause. **The hash is not the weak component. The
reference domain is.**

### What this says about s9-14 specifically

s9-14 sits 24 bits from its sibling capture, comfortably inside the same-card
band. It failed only because its cross-domain distance to its own reference
(108) rose above its distance to a stranger's. Nothing about the capture was
wrong.

### And a measurement error the harness has been hiding

Both Lion Umbra captures are closer to **mh3/160** (102 and 92) than to
**mh3/426** (108 and 94) — but the card on the desk is the retro-frame 426.
The two printings share Julia Metzger's artwork exactly and differ only in
frame, so an art-region hash *cannot* separate them, at any bit width.

The eval never noticed, because it scores by **name**. Every number this
project has published about art-match — including 32/35 above — measures name
identification, not printing identification. The channel's actual job has never
been measured.

---

## The ceiling: most printings are not separable by art at all

Before asking whether a change improves printing identification, it is worth
asking how much printing identification is *available*. Measured 2026-08-08:
for every labelled card, the closest distance between two of its own printings'
reference hashes, read against the hash's own same-card noise floor of 68 bits.

| card | printings | closest same-name pair | |
|---|---|---|---|
| dress-down | 5 | **0 bits** — mh2/39 vs plst/MH2-39 | unseparable |
| victimize | 19 | **0 bits** — cma/72 vs plst/CMA-72 | unseparable |
| hard-evidence | 3 | **0 bits** — mh2/46 vs plst/MH2-46 | unseparable |
| kalastria-highborn | 5 | **0 bits** — plst/WWK-59 vs wwk/59 | unseparable |
| ornithopter | 28 | 2 bits — plst/M15-223 vs m15/223 | unseparable |
| unstable-amulet | 3 | 6 bits — mh3/142 vs mh3/514 | unseparable |
| unholy-heat | 6 | 8 bits — otc/182 vs mb2/63 | unseparable |
| lion-umbra | 2 | 24 bits — mh3/426 vs mh3/160 | unseparable |
| abiding-grace | 2 | 74 bits — h2r/1 vs mh2/1 | separable |
| root-elemental | 2 | 70 bits — scg/127 vs mkc/182 | separable |

**57 of 74 labelled stills are of a card whose printings sit inside the hash's
own noise floor. The ceiling on printing-level accuracy for this corpus is
23%.**

This refutes the channel's founding assumption, written at the top of
`internal/tui/artmatch.go`: *"same-name printings differ in art or frame, which
is exactly what the text ranks cannot separate."* They very often differ **only**
in frame, set symbol or collector number. The List (`plst`) reprints are
literally the same image file — distance 0.

### It also refutes this document's own Tier 1 proposal

The section below argues art-match should arbitrate among the printings of a
known name. For 77% of these cards that job is impossible: knowing the name,
art contributes nothing further. The proposal was written before the ceiling
was measured and is kept here, marked, rather than quietly deleted.

**The value runs the other way.** Art-match is a *name-recovery* channel — it
supplies the name when OCR cannot, which is glare on the title, damage, or a
non-English printing. It does that well: 33/35 → 35/35 with the 4% inset. The
*printing* is then the footer and frame readers' job, and those are the only
components that can do it.

### `illustration_id` makes this free to know in advance

Scryfall publishes an `illustration_id` per card, which groups printings sharing
the same artwork exactly — Dress Down returns 5 printings across 2 illustrations.
hoard's catalog does not store it (`PRAGMA table_info(cards)` has no such
column). Adding it costs a schema column and a rebuild, and buys two things:

- **Measurement:** the eval can partition stills into art-decidable and
  art-blind and score them separately. Without it, every number blends a
  solvable problem with an unsolvable one.
- **Production:** art-match can *decline* on an art-blind family rather than
  guess between identical pictures. That is a correctness fix independent of
  every other question in this document.

---

## The diagnosis: we are solving the wrong search problem

hoard's art-match asks the hash a global question: *which of 107,169 printings
is this?* The system does not need that question answered, and asking it is
what makes the gate impossible.

### The catalog's shape

Measured against the live catalog, 2026-08-08:

| | |
|---|---|
| printings with images | 107,169 |
| distinct names | 37,099 |
| names with exactly one printing | 17,358 (47%) |
| printings per name — median | **2** |
| printings per name — p90 | 5 |
| printings per name — p99 | 18 |
| printings per name — max | 864 (*Forest*) |

Scoping the search to a known name shrinks the candidate set from 107,169 to a
**median of 2**, and p99 of 18. That is not an optimisation; it is four orders
of magnitude, and it lands squarely on the failure mode the measurements found.
For 47% of names it removes the question entirely — the name determines the
printing.

### Every observed failure was a cross-name confusion

Across both footprints, the Stage C harness produced eight wrong reads. All
eight named a **different card**, not a different printing of the right card:

| still | on the desk | 256-bit said | 64-bit said |
|---|---|---|---|
| s9-04 | dress-down | *(correct)* | Unstable Amulet |
| s9-07 | victimize | Sevinne's Reclamation | *(correct)* |
| s9-11 | consuming-corruption | Alaborn Grenadier | *(correct)* |
| s9-14 | lion-umbra | Cruel Revival | Trumpet … |
| s9-21 | charitable-levy | *(correct)* | Worship |
| s9-26 | abiding-grace | *(correct)* | Windfall |
| s9-27 | abiding-grace | *(correct)* | Smite th… |

Eight for eight. **Not one failure was a wrong printing of the right card** —
the case art-match exists to arbitrate. Every failure was the hash losing a
global popularity contest it should never have been entered in.

The s9 names carry 1–28 printings each (Hollow Specter 1, Trap Digger 1,
Brainsurge 2, Lion Umbra 2, Consuming Corruption 2, Victimize 19, Ornithopter
28). Under a name-scoped search, every one of those eight failures is not
merely unlikely — it is **structurally impossible**, because the wrong answer
is not in the candidate set.

### Why this was missed

The channel was designed as an *independent* evidence source: "OCR never sees
the art, so glare on the copyright line cannot touch this evidence." That
reasoning is sound for the *footer* — the collector number and set code, which
is what glare actually eats. It was over-applied to the *title*, which sits in
a different place on the card, is read from a different band, and usually
survives. Treating the whole text channel as untrusted threw away the one piece
of evidence that makes the art comparison tractable.

---

## The proposed design

### Principle: art-match arbitrates, it does not search

> **SUPERSEDED 2026-08-08 by the ceiling measurement above.** For 77% of the
> labelled corpus the printings of a name are art-identical, so arbitrating
> among them is impossible and knowing the name already exhausts what art can
> contribute. The tiers below are still the right *search* structure — scoping
> by name is what makes the distances mean anything — but the output should be
> an illustration group, not a printing, and the channel's value is supplying a
> name OCR could not read. Kept as written, marked, because the reasoning about
> candidate-set size stands.

The art hash's job is to choose among the printings of a card whose name is
already known. It searches globally only when there is no name at all, and
then under a much higher bar.

### Tier 1 — name-scoped arbitration (the common case)

When the queued card has a resolved name, rank only that name's printings.

The plumbing is already in place: `queueItem` (`internal/tui/autoscan.go:263`)
carries `canonical` — the resolved name, `""` when it never resolved — and
`prints []scryfall.Card`, that name's printings already fetched and ranked. At
the moment `artMatchCmd` fires, the candidate set is **already in hand**. No
new lookup, no new index, no schema change.

What this changes statistically:

- The runner-up is a genuine sibling printing rather than the closest of 107k
  strangers, so the margin finally measures something meaningful — which is
  what would give the margin criterion the job it currently does not do.
- With a median of two candidates, the question is usually a straight binary.
- Gates can be *tighter*, not looser, because the prior is enormously stronger.

Ranking one name's printings needs the index only for those ids, so this can be
a `BestAmong(hash, ids)` alongside `Best`.

### Tier 2 — global search (no name resolved)

When OCR produced nothing usable, the global search is all there is, and it
must stay fail-closed. Stage C says the honest global gate is roughly
`distance ≤92` with ~74% coverage and no margin requirement — on 536 confusers.
Against 107,169 it will be worse and must be refit before it is trusted. This
tier stays inert until the full eval runs.

### MEASURED 2026-08-08: a 4% reference inset closes most of the gap

The augmentation idea below was tried. Harness: `HOARD_AUGMENT=1
HOARD_CROPS=<dir> [HOARD_SESSION=s5] go test ./internal/artindex -run
TestAugmentationSweep` (`augment_test.go`). Four augmentation families were
measured separately, because a family that does nothing is a family not worth
paying for on 107k printings.

**Only one of them does anything.** Blur, non-linear glare and warm/cool cast
all landed within noise of the baseline. Framing jitter — re-framing the
reference to show slightly less of the card — closed the overlap on its own,
and "all four combined" scored identically to jitter alone.

Better still, the mismatch turned out to be **systematic, not random**, so it
does not need a variant set at all. A single fixed inset applied to every
reference before hashing:

| inset | own max | other min | overlap | own median | correct |
|---|---|---|---|---|---|
| 0.00 (today) | 112 | 100 | **+12** | 82 | 33/35 |
| 0.02 | 104 | 96 | +8 | 68 | 35/35 |
| 0.03 | 94 | 102 | −8 | 62 | 35/35 |
| **0.04** | 88 | 100 | **−12** | **60** | **35/35** |
| 0.05 | 96 | 98 | −2 | 64 | 35/35 |
| 0.06 | 104 | 102 | +2 | 68 | 35/35 |

A clean unimodal curve peaking at 4% — the signature of a real systematic
offset rather than noise. It also **beats the five-variant jitter set** (−12
against −8) at one fifth the cost, and the reason is worth keeping: extra
variants give the *wrong* candidates extra chances to match too (other-min 94
with variants, 100 with a single inset). A calibrated constant only ever helps
the right answer.

**Two things that did not work, and why:**

- **Query-side jitter is actively harmful** (+18, worse than doing nothing).
  `jitter` only insets — it zooms *in* — and the probe's flatten is already
  zoomed in relative to the reference, so jittering the query moves it further
  away. Own-median stayed at exactly 82, the signature of the minimum always
  falling back to the identity variant. The correction has to go on the
  reference side because **the reference is the only side that still has the
  whole card**; what the flatten cut off cannot be recovered.
- **Linear contrast and exposure cannot move this hash at all**, so no
  augmentation modelling them can help. Every kept DCT coefficient scales by
  the same factor, leaving each one's position relative to their median — which
  is what the bits record — unchanged, and the DC term is dropped outright.
  Pinned in `TestHashIgnoresLinearExposureAndContrast`. This retires the
  standing "glare washes out the art and eats the margins" theory unless the
  washing is non-linear.

**Validation, and its limits.** The constant was fitted on s9 and checked
against s5, the only other labelled session (s3 has stills but no labels):

| session | baseline overlap | at 4% | baseline own-median | at 4% | correct |
|---|---|---|---|---|---|
| s9 (foil-heavy retro frames) | +12 | **−12** | 82 | 60 | 33/35 → 35/35 |
| s5 | +4 | +2 | 62 | 52 | 38/38 → 38/38 |

4% is the best or tied-best inset in both, and own-median improves in both —
the direction and the optimum are consistent. But **the magnitude is
s9-specific**: s5's distributions were nearly separated already, and there the
inset is a small improvement rather than a fix. Do not read "closes the gap"
as a general claim on this evidence.

Three further caveats, all of which matter before this ships:

1. **73 labelled stills across two sessions and one rig.** Small.
2. **`other min` is measured against ~15 other cards, not 107,168.** At full
   scale the wrong-card floor drops and a −12 overlap will not survive as-is.
   The durable result is the own-distance reduction (82 → 60, 62 → 52), which
   is what buys headroom for that floor to fall into.
3. **The constant is a property of how CardKit frames its flatten.** Change the
   flatten and it must be refit — which argues for fixing the framing at the
   source instead, so the two sides agree without a calibration constant. That
   is a Swift change and needs a live session; the inset is the cheap version
   that needs neither.

### Close the domain gap (the highest-value lever)

The measurement above says the hash separates cleanly in-domain and overlaps
across domains, so the lever is making the two sides look alike. Options, in
rough order of cost:

1. **Augment the reference side.** Hash several degraded variants of each
   Scryfall image — mild blur, exposure shift, a warm cast, slight
   desaturation — and keep the minimum distance across them. This is a standard
   trick and it costs only CPU, because the image cache and `hoard artindex
   rehash` already make re-deriving every hash a local pass. It also fits the
   existing schema: several rows, or several hashes, per printing.
2. **Normalise the capture side.** Per-crop contrast normalisation (CLAHE is
   what every open implementation applies for exactly this reason) before
   hashing, on both the index and the scanner side. Cheap, symmetric, and it
   attacks the lighting difference directly.
3. **Only then, learned embeddings.** This is where PSA ended up. It is a much
   larger project and should not start until 1 and 2 are measured.

The harness for judging all three already exists: the in-domain / cross-domain
table above is the metric. The target is to move the cross-domain overlap to
zero, the way the in-domain numbers already are.

### Same-art reprints need the frame, not the art

Lion Umbra mh3/160 and mh3/426 share their artwork exactly. No art-region hash
separates them, so for this family the discriminator has to be the frame —
which hoard already reads, in BorderKit. Art-match should *decline* on
same-art printings rather than pick one, and say so, letting the frame and
footer readers arbitrate. Detecting the case is cheap: two printings whose
reference hashes are within a few bits of each other are the same art.

### Multi-channel hashes

Moss Machine's per-channel R/G/B hashing and hoard's own 5–13× chroma stability
measurement point the same way. The proposal is a second hash on a chroma plane
beside the luma one, with agreement between planes as evidence and disagreement
as a reason to stay queued.

This is now cheap to iterate on: `task artindex-cache` populates a local corpus
and `hoard artindex rehash` re-derives every hash from disk with no network, so
a new plane costs a CPU pass rather than a three-hour download. The `algorithm`
footprint marker means a plane change cannot silently corrupt an existing
index.

### Check the crop before building anything

s9-14 fails under *both* footprints. A failure that survives a change of hash
width is a signal about the input, not the hash. Look at what
`cardkit-probe --emit-card` actually produced for it first — the survey
literature is explicit that pHash is "quite sensitive to the segmentation of
the card", and a bad flatten cannot be fixed downstream.

---

## What changes in the code

| where | change |
|---|---|
| `internal/artindex/index.go` | add `BestAmong(h Hash, ids []string) (best, second Match)` |
| `internal/tui/artmatch.go` | pass `it.canonical` / `it.prints`; call `BestAmong` when non-empty, `Best` otherwise |
| `internal/tui/artmatch.go` | two gate sets — scoped and global — rather than one |
| `internal/artindex/stagec_test.go` | add a name-scoped column so the two are measured side by side |

The measurement comes first. `stagec_test.go` already fetches once, hashes
several footprints over identical bytes, scores against
`scan/foil-corpus/stills-labels.tsv`, and finds each footprint's best zero-wrong
gate by exhaustive sweep. Adding a name-scoped column is a small change and
turns this document's central claim into a number.

---

## Risks and open questions

- **35 stills is a small corpus**, drawn from one session and one rig. "Eight
  for eight are cross-name" is a strong signal but not a large sample. The
  name-scoped column should be measured across s3 and s5 too.
- **Name-scoping inherits OCR's errors.** If the title is misread, the correct
  printing is not in the candidate set and art-match will confidently pick the
  best of the wrong family. This is a *new* failure mode the global search does
  not have, and it is the main thing to measure. Mitigation: require the
  scoped winner to also be plausible in absolute terms, so a whole wrong family
  fails the distance gate rather than yielding a confident wrong answer.
- **Basic lands are pathological** (*Forest*: 864 printings). Scoping helps far
  less there, and art genuinely differs between them — this may be the one case
  where the global-style gate is the right tool inside a name.
- **The 700ms budget** (`artMatchTimeout`) is unchanged and comfortable:
  scanning 2 candidates instead of 107,169 makes the lookup faster, not slower.
- **Delver Lens's failure is inferred from reviews**, not from its source. The
  conclusion that printing disambiguation is unsolved in the market rests on
  user reports and the absence of any published approach, not on inspection.

---

## Refuted — do not re-propose

- **More hash bits as the lever.** Measured 2026-08-08: 64 → 256 bits improved
  identification by two reads and made gating worse by two. Keep the 256-bit
  hash; do not widen further expecting gates.
- **`art_crop` as the image source.** 4.9× the transfer of `small` (66KB vs
  13KB; 7.0GB vs 1.4GB catalog-wide), not "the same transfer" as previously
  written — and structurally incompatible with `FromCard`, whose fixed-fraction
  crop is the thing a scanner flatten can reproduce. Full reasoning in
  `docs/artindex-results.md`.
- **Whole-card hashing.** Refuted 2026-08-07 and independently by every small
  open implementation that tried it.
- **Photometric augmentation of the reference** (blur, glare curves, warm/cool
  cast). Measured 2026-08-08: all three land within noise of the baseline, and
  the four families combined score identically to framing jitter alone. The
  domain gap is geometric, not photometric.
- **Augmenting the query side.** Measured at +18 overlap against a +12
  baseline — worse than doing nothing. Extra query variants give every *wrong*
  candidate more chances to match while the right one gains none, because the
  flatten is already cropped inside the card and no augmentation can recover
  what it cut off. The correction belongs on the reference, which is the only
  side still holding the whole card.
- **Modelling glare as a contrast change.** This hash is provably invariant to
  linear exposure and contrast — every kept DCT coefficient scales by the same
  factor, so none moves relative to their median, and DC is dropped outright.
  Pinned in `TestHashIgnoresLinearExposureAndContrast`. Only clipping and
  quantisation break the invariance.
- **Art as a printing discriminator for same-illustration reprints.** Not a
  tuning problem: `plst` reprints are the identical image file, distance 0.
  Use `illustration_id` to detect the case and the frame reader to resolve it.

---

## Getting a definitive answer

The question "will this improve card matching" cannot be answered as posed,
because the corpus caps printing-level accuracy at 23% and every number
published so far is scored by **name**. The plan below splits the question into
two that *are* answerable and orders the work by what it rules out.

### 1. Add `illustration_id` to the catalog — ~1h, no network

Schema column plus a rebuild. Until it exists, every measurement mixes a
solvable problem with an unsolvable one. Ships a correctness fix on its own:
art-match declines on art-blind families instead of guessing.

### 2. Score three metrics separately, not one — an afternoon, no network

- **name correct** — what every published number actually measures
- **illustration-group correct** — art-match's true ceiling and its real job
- **printing correct** — needs the frame and footer readers; art cannot do it

This is the step that decides whether to continue at all.

### 3. Fit and evaluate on disjoint data — free

The 4% inset was swept on s9 and "checked" on s5, where 4% also happened to
win. That is not held out. Freeze the constant from one session, evaluate it
frozen on the other, and report *that* number.

### 4. Scale test against the full 107,169 index — one 3h download, then free

`other min` measured against ~15 confusers is meaningless; every gate decision
depends on the real wrong-card floor. Because the Stage B cache exists, inset-0
versus inset-4% is a `hoard artindex rehash` — CPU only, not a second download.

### 5. Size the corpus from the error rate the channel must meet — needs captures

Zero wrong in 73 stills bounds the true error rate at only ~4% (rule of three,
95% confidence). For a channel that commits rows to a collection that is far
too loose. Below 1% needs ~300 clean reads; below 0.5%, ~600. This is the same
reasoning behind the border reader's existing "200 clean reads" gate.

### 6. Pre-commit ship/kill criteria before running step 4

So the result cannot be rationalised afterwards. Proposed: **ship** if, on the
full index and a held-out session, illustration-group accuracy clears an agreed
bar at **zero wrong groups**, with latency inside the 700ms budget; **kill** on
any wrong group.

### 7. A live pile session — the tuning ledger's standing rule

Ideally one deliberately reprint-heavy pile and one mixed non-foil pile. The
foil corpus is unrepresentative in exactly the way that matters here: it is
dense in MH2/MH3/The List, which is where art-identical reprints concentrate.

**Steps 1–3 cost an afternoon and no network, and are decisive about whether to
continue.** Do not spend the three-hour download until step 2 has said which
question is being answered.

---

## Sources

- [Magic Card Detector — Timo Mikonen](https://tmikonen.github.io/quantitatively/2020-01-01-magic-card-detector/)
- [Recognizing MTG cards with C++/OpenCV — Thoughtseize](https://thoughtseize.io/2020/07/10/recognizing-magic-the-gathering-cards-with-cpp-and-opencv/)
- [Magic: The Gathering Card Recognition — Nessy](https://nessy.info/post/2018-01-12-magic-the-gathering-card-recognition/)
- [Moss Machine — open-source TCG sorting & recognition](https://kairicollections.github.io/Moss-Machines-Magic-the-Gathering-sorting/)
- [hj3yoo/mtg_card_detector](https://github.com/hj3yoo/mtg_card_detector)
- [klanderfri/CardReaderLibrary](https://github.com/klanderfri/CardReaderLibrary)
- [Automating Card Identification Using Computer Vision — Collectors (PSA)](https://blog.collectors.com/image-search/)
- [Delver Lens review, 1,000 cards — Lotus Scan](https://www.scanyourmtg.com/review/delver-lens/)
- [MTG Scanner — Delver Lens (official)](https://www.delverlab.com/)
- [Comparing SIFT and ORB for feature matching](https://medium.com/@beauc_37732/comparing-sift-and-orb-for-feature-matching-a-visual-and-practical-exploration-6c194c72e4d6)
