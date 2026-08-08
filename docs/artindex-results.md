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

Also worth carrying into v2: hashing Scryfall's `art_crop` URIs instead of
cropping the small card image (sharper art pixels for the same transfer), and
a chroma-plane hash as a second channel — foil glare distorts luma more than
hue structure.
