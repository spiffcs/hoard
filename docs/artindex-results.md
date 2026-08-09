# Art-identification (Phase B): results, and the revisit recipe

2026-08-07. The perceptual-hash identification channel was built end-to-end,
measured against 124 labelled captures, and parked at a precise stopping
point. This document records what works, what the measurements said, and
exactly how to resume — so revisiting costs an afternoon, not an
archaeology dig. The operating pipeline is `docs/artindex-pipeline.md`.

## What was built and works

- Catalog `image_uri` column (schema 5), front-face fallback for double-faced
  cards; `Catalog.ImageSources()` feeds the build.
- `internal/artindex`: pure-stdlib 64-bit DCT pHash; SQLite-persisted index
  (`~/Library/Caches/hoard/artindex/artindex.db`, 10s busy timeout — the
  first build was killed by its own status check); linear-scan `Best`
  returning winner *and* runner-up; `SoleFinish` carried per printing so an
  art match can settle foil without a lookup; resumable paced `Build`
  (≤10 images/s, skip-by-id).
- `hoard artindex status|build` CLI. Full build measured: 107,169 printings,
  ~3h transfer (~3.5GB, images not kept), **11.7MB** on disk.
- `cardkit-probe --emit-card` (the flatten as PNG; exit 4 = no card located).
- The live channel (`internal/tui/artmatch.go`): at queue time, beside the
  rescue Rearm — wait for the capture's still, flatten via the probe, hash,
  match, and re-enter resolution as a synthetic read at `art-match` (the top
  rank) through `upgradeQueued`. Fail-closed gates; every failure path is
  silent and costs nothing.

## What the measurements said

1. **Whole-card hashing is refuted.** Against the full index, the
   124-capture eval matched near-randomly: best distances 4-16 with margins
   0-2, right or wrong alike. Every card shares its global structure —
   border, art box, text box at identical positions — so the low-frequency
   DCT signs agree across the whole catalog and 64 bits saturate.
2. **The art-region footprint works directionally.** `FromCard` (central
   crop, u 8-92% × v 10-58%, same footprint both sides) against a 536-entry
   mini-index: **31/35** hand-held stills rank the correct card first.
3. **The safety property held on real data.** Every wrong nearest-neighbour
   sat at margin ≤2 and the gates (≤10, margin ≥8) rejected it — zero wrong
   matches would have committed.
4. **Why it cannot ship yet.** Correct matches sit at distance 6-18 with
   margins mostly 2-6 — foil glare smears the art luma — and runners-up only
   get closer at 107k scale. The gates would fire on a handful of reads;
   coverage is not there at 64 bits.

The live path was deliberately left consistent (index and `artmatch.go` both
whole-card), so the channel is inert — it cannot misfire — until v2 lands.

## 2026-08-08: the 256-bit hash, measured — and the premise it refutes

Stage A shipped (`Hash` is `[4]uint64`, 16×16 keep block, BLOB column,
footprint marker). Stage C step 1 then measured **both** hash widths over
**identical fetched bytes and identical crops**, so the comparison is a
measurement rather than a comparison against this document's older prose.
Harness: `HOARD_STAGEC=1 go test ./internal/artindex -run
TestStageCFootprintComparison` (`stagec_test.go`). 536-entry mini-index, 35
hand-held s9 stills, `FromCard` on both sides, gates found by exhaustive sweep
for the best zero-wrong-match coverage each footprint can achieve.

| | 64-bit (8×8 keep) | 256-bit (16×16 keep) |
|---|---|---|
| correct nearest-neighbour | 30/35 (86%) | **32/35 (91%)** |
| margins observed | 0–14 | 0–72 |
| best zero-wrong gate | distance ≤16, margin ≥0 | distance ≤92, margin ≥0 |
| decisive at that gate | **28/35 (80%)** | 26/35 (74%) |

**The premise does not survive.** More bits was supposed to widen the usable
margin. The margins did widen — roughly 4×, exactly as the arithmetic
guarantees — and **gate coverage did not improve**. The 256-bit hash identifies
slightly better and gates slightly worse. Both differences are two reads at
n=35, which is noise; the honest statement is that the widening bought nothing
measurable at the gate.

Three things the measurement exposed that the old framing missed:

1. **The margin criterion does no work.** Both footprints' optimal gate is
   `margin ≥0` — the margin is free. Only the absolute distance separates, so
   "margins too thin to gate" named the wrong variable.
2. **The separation is two units wide in both cases, and proportionally worse
   at 256 bits.** The 64-bit threshold sits at 16 with the nearest wrong read
   at 18; the 256-bit threshold at 92 with the nearest wrong read at 94. Two
   units is 3.1% of a 64-bit range and 0.8% of a 256-bit one.
3. **The two footprints fail on largely disjoint stills** — 256-bit misses
   {s9-07, s9-11, s9-14}, 64-bit misses {s9-04, s9-14, s9-21, s9-26, s9-27},
   overlapping only at s9-14. Failures that move when the hash width changes
   are marginal reads near a decision boundary, not a defect extra bits
   address. s9-14 failing in both suggests a bad crop rather than a bad hash.

**Both gates are optimistic.** The mini-index holds 536 printings; the real one
holds 107,169. The nearest *wrong* neighbour is the minimum over that many
draws, so the wrong-read distance floor drops with 200× the confusers — and
both gates depend entirely on a 2-unit distance separation that has no room to
absorb it.

**Verdict: no-go for Stage D on bit width alone**, by this document's own rule
("the mini-eval decides if it is enough"). The full 107k eval was not run: it
costs a ~3h index rebuild to test a gate the cheap measurement already says is
not there. The named fallback — a chroma-plane second hash, since foil glare
distorts luma more than hue structure — is now the lever to try, and it is
cheap to iterate on because the image cache and `hoard artindex rehash` landed
in Stage B.

## The revisit recipe (task #15)

1. **256-bit hash**: 16×16 DCT keep-block; `Hash` becomes `[4]uint64`, the
   index column a BLOB. More bits is the direct answer to glare eating the
   margins; the mini-eval decides if it is enough.
2. **Cache the images this time** (~3.5GB under `artindex/images/`): the
   3-hour cost was the *download*, and hash iteration must not pay it twice.
3. Re-run the harnesses in order: `HOARD_MINI_EVAL=1 go test
   ./internal/artindex -run TestMiniFootprintEval` (go/no-go on footprint ×
   bits), then the full `HOARD_ARTMATCH_EVAL=1 … TestArtMatchEval`
   (gate refit from 107k-scale distributions; zero wrong matches required).
4. Only after the gates refit: switch `artmatch.go` to `FromCard` and rebuild
   the shipped index — both sides must change footprint in the same release.

Also worth carrying into v2: a chroma-plane hash as a second channel — foil
glare distorts luma more than hue structure. After 2026-08-08 this is the
*primary* remaining lever, not an extra.

**`art_crop` is refuted as a source — do not re-propose it.** Two measurements
(2026-08-08), both against the claim made here that it offers "sharper art
pixels for the same transfer":

- **It is not the same transfer.** Sampled across eight printings, `art_crop`
  averages 66KB against `small`'s 13KB — **4.9×**, or 7.0GB against 1.4GB
  across the catalog. (`normal` is 91KB/9.7GB, `large` 143KB/15.3GB.) All four
  take the same ~3 hours regardless, because `Build` is paced at ten images a
  second and is therefore ticker-bound, not bandwidth-bound.
- **It does not compose with `FromCard`, which is the footprint that works.**
  `FromCard` takes a *fixed fraction* of a whole card — frame-independent, and
  reproducible from a scanner flatten, which is the whole reason both sides can
  agree. Scryfall's `art_crop` is their own crop to the art box, its geometry
  varies by frame, and the phone cannot reproduce it from a flatten. Using it
  means the index side and the scanner side stop hashing the same region.
- **And the sharpness argument is weak anyway**: every hash reduces to a 32×32
  grid, against which even `small`'s ~123×98 art region is already ~4×
  oversampled.

A cache of whole cards is a strict superset — any art crop can be re-derived
from it offline — so `normal` is the variant to cache if more source pixels are
ever wanted. `task artindex-cache` defaults to it.
