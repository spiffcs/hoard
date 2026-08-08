# The art-index pipeline: keeping the picture channel current

The scanner's art-identification channel (`internal/artindex`) matches a
scanned card's picture against a perceptual hash of every printing's Scryfall
image. New sets arrive every month or two, so the index is a dataset with a
freshness problem — this document is the pipeline that keeps it current and
the reasoning behind its shape.

## What the index is, in numbers

- One row per printing: scryfall id, a 64-bit DCT hash, and the printing's
  sole finish when it has exactly one. ~108 bytes/row measured — the full
  ~107k-printing index is **~12MB on disk**.
- Building it from scratch transfers **~3–4GB** of small card images (never
  stored — decoded, hashed, discarded) and takes **~3 hours** at the polite
  ≤10 images/s pace `Build` enforces. That cost is why users should receive
  the index, not the recipe.
- The hash is derived data at its most transformative: 64 bits per image,
  unreconstructable. Distribution still gets a line in
  `docs/data-licensing.md`'s review before any public release — it rides the
  same Scryfall bulk data the catalog already uses, under the same Fan
  Content posture.

## The refresh cycle

The index is derived from the catalog (`image_uri` column, schema 5+), which
is derived from Scryfall's daily `default_cards` bundle. Refresh is therefore
two commands, in order, plus a check:

    hoard catalog update        # pull the current bulk bundle
    hoard artindex build        # hash whatever the catalog has that the index lacks
    hoard artindex status       # counts must match

`build` is incremental and resumable by construction: rows already present
are skipped, so a monthly run after a new set costs only that set's images —
a few hundred downloads, minutes not hours. An interrupted run (network,
sleep, SIGINT) resumes exactly where it stopped.

## What incremental refresh does NOT catch, and the answer

The skip check is by scryfall id alone. Two kinds of drift slip past it:

1. **Re-imaged printings.** Scryfall occasionally re-scans a card; the id
   keeps its row, the image behind the URI changes, and the stored hash goes
   stale. Rare, and a stale hash fails closed (the match just misses), but it
   accumulates.
2. **Removed printings.** A row deleted from the bundle leaves an orphan hash
   that can still win a match. Rarer still.

The answer is a **periodic full rebuild** rather than change detection:
delete `artindex.db`, run `build` from zero. Quarterly is the right cadence —
drift is slow, the rebuild is ~3 hours of unattended machine time, and
change-detection plumbing (storing and diffing URIs) would be more code
guarding against less loss than one quarterly cron entry. Revisit only if a
live session ever demonstrates a stale-hash mismatch, which the fail-closed
gates make unlikely to matter before the next rebuild anyway.

## The CI shape (release engineering, Stage 0-gated)

Per `docs/release-engineering.md`, releases are goreleaser-built and gated on
the licensing P0s. The index slots in as a release artifact:

1. A scheduled CI job (monthly, plus on-demand when a set drops) runs the
   refresh cycle above on a runner, from the previous release's `artindex.db`
   as the incremental base — so the job normally transfers a few hundred
   images, not 4GB. The quarterly run starts from zero instead.
2. The job replays the offline verification before publishing: the corpus
   stills through `cardkit-probe --emit-card` → hash → `Best`, with the
   standing bars — no wrong-printing matches, and the accept/margin gates'
   measured separation intact (see `internal/tui/artmatch.go`). A refresh
   that moves those numbers does not ship without a ledger entry in
   `docs/scanner-tuning.md`.
3. The artifact is the ~12MB `artindex.db`, attached to the release and
   fetched by a future `hoard artindex fetch` (small: download to the cache
   dir, then `status` to confirm counts). Local `build` remains the
   from-source fallback.

Until that CI exists, the pipeline is this document run by hand on one
machine, and the artifact travels with whatever release process ships the
catalog-bearing binary.

## Failure modes worth naming

- **Catalog/schema drift**: `build` reads the catalog's `image_uri` column;
  a schema bump that touches it must bump this pipeline too. The
  `ImageSources` query failing loudly (zero sources → `build` refuses) is
  the guard.
- **Scryfall CDN failures mid-build**: single-image failures are skipped and
  retried on the next run by the same skip-by-id logic — a build that ends
  with `status` short of the catalog count is incomplete, not broken.
  (`status` says so and names the fix.)
- **Concurrent readers**: the database opens with a 10s busy timeout because
  the first live build was killed by its own `status` check — see the
  comment in `internal/artindex/index.go`. Cron jobs and curious humans can
  coexist with the writer.
