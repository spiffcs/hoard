# Sprint: art-match v2 — the identification channel earns its gates

PLANNED (future sprint). Prereqs live in `docs/artindex-results.md` (the v1
measurements) and `docs/artindex-pipeline.md` (the refresh pipeline). Status
at planning time: channel built end-to-end, inert by design — whole-card
64-bit hashes refuted, art-region footprint validated directionally (31/35
correct nearest-neighbour on hand-held stills), margins too thin to gate.

## What this sprint is — and is not — for

This is the **identification** lever: naming the exact printing when OCR
cannot, glare-proof, inside the 1000ms rescue window. Scope honesty, measured
2026-08-07: the test pile's 16 printings are all dual-finish, so the
`SoleFinish` shortcut contributes **zero direct foil evidence there**. The
foil value is indirect — single-finish printings in the wild (foil-only
Secret Lairs, etched rows, promos) close their finish by identity alone;
everywhere else v2 buys right-row attachment and frees the rescue cycle for
finish evidence. Foil *detection* is `docs/sprint-foil-recognition.md`.

## Stages

A. **256-bit hash.** 16×16 DCT keep block; `artindex.Hash` → `[4]uint64`,
   index column → BLOB. The direct answer to foil glare compressing 64-bit
   margins to 2-6 bits.
B. **Image cache.** `Build` keeps downloads under `artindex/images/`
   (~3.5GB): hash iteration must never again cost a 3-hour re-download.
   Consider switching the source to Scryfall's `art_crop` URIs while the
   cache is rebuilt — sharper art pixels for the same transfer.
C. **Re-measure, in order.** `HOARD_MINI_EVAL=1` (footprint × bits go/no-go:
   the s9 margins must widen from 2-6 to gate-usable), then
   `HOARD_ARTMATCH_EVAL=1` against all 107k (gate refit from measured
   distributions; **zero wrong-printing matches** is the bar, ≥90% decisive
   on clean reads the target). A chroma-plane second hash is the fallback
   lever if luma alone still smears under glare.
D. **Flip the live path.** Only after C: `artmatch.go` moves to `FromCard`,
   the shipped index rebuilds on the same footprint, gates take the fitted
   values. Both sides change in one release — a footprint mismatch is worse
   than inert.
E. **Distribution.** The ~12MB artifact joins the release pipeline per
   `docs/artindex-pipeline.md`; `hoard artindex fetch` lands beside it.

## Verification

Stage C's eval tables are the artifact; D ships only on C's numbers; a live
pile run scores decisive art-match commits and their latency against the
1000ms ceiling. Ledger entries in `docs/scanner-tuning.md` for every fitted
gate.
