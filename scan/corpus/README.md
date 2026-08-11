# The parser corpus

A stratified sample of clean card scans, replayed through the reader and scored
against known answers, so a frame era that parses badly is visible instead of
averaged away.

```sh
./scan/corpus/fetch.sh [per-stratum] [era]  # default 25, all eras
make cardkit-score                          # score per era × border
make cardkit-score ARGS=--misses            # and list every card that failed
```

`manifest.tsv` currently holds 231 rows: 120 `pre1998`, 47 `2015+`, and 32 each
for `1998-2002` and `2003-2014`.

Sampling is stratified by **frame era × border colour**, not per set: most sets
in an era share one frame, so a few hundred images cover every distinct layout
where two or three from each of ~900 sets would be mostly redundant. Eras cut
at frame changes, not set boundaries — `pre1998` (no collector number printed
at all), `1998-2002` (numbers arrive, old frame), `2003-2014` (the 8th Edition
frame), `2015+` (the M15 frame). Each stratum draws from a seed derived from
the stratum itself, so `fetch.sh` reproduces the same corpus and downloads only
what is missing.

## What it does and does not test

These are clean digital scans. They exercise the parser — layout assumptions,
the collector band, the copyright line, title selection — and nothing else.
They do not test the trigger, rectangle detection, the perspective crop, glare,
focus, or the camera's resolution ceiling. A card that parses perfectly here
can still fail on a desk, and one that fails here is broken in a way no
lighting will fix.

One consequence of the format matters: the card fills the frame, so the
rectangle detector locks onto the boundary between border and inner frame
rather than the card's outer edge, and the crop therefore excludes the printed
border. **Nothing that reads the crop can be tuned here.** Fit here, then
confirm against [scan/fixtures](../fixtures/README.md), which are photographs.

## Layout

    images/       the pixels. Gitignored — third-party, fetched not authored.
    manifest.tsv  one row per image with the known answer. Tracked.

Do not commit the images; they belong to their rightsholders, and the manifest
rebuilds the corpus exactly.

Measured scores, per era and border, are in
[scanner-limits.md](../../docs/specs/scanner-limits.md).
