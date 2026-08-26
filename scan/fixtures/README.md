# Scan fixtures

28 real capture frames, each with the card list it should produce. `sweep.sh`
replays them through the reader and diffs the result against the
`.golden.json` beside each frame.

```console
$ make scan-check
ok       single-card-crop
ok       title-lost-block-intact
ok       two-card-pile
...
```

Every frame is here because it pinned a decision the reader once got wrong: a
flavor credit becoming a card, a copyright line's tail digits turning out to be
the collector number, a paintbrush artist credit outranking the real title. The
filename says which.

This sweep does not run in CI: [scan.yml](../../.github/workflows/scan.yml) is
switched off because macOS runners bill at 10x. It is a local gate, so run it
before pushing anything that touches the reader.

## Re-baselining

```sh
./scan/fixtures/sweep.sh --update
```

`--update` rewrites the goldens. Run it only when a behavior change is
deliberate, and quote the diff in the commit message.

## Adding a fixture

Capture a session with `HOARD_SCAN_DEBUG_DIR`, copy the problem frame's
`capture-N-ocr.png` here under a name that says what it pins, then `--update`
and commit the frame and its golden together.

These are photographs of a real desk, so crop or choose deliberately before
committing. Whole tuning sessions stay out of the repo (`scan-fixtures/` is
gitignored); only distilled regressions live here.

## When a golden moves

Vision's OCR drifts across macOS releases, so a changed golden is not
automatically a regression. The header of `sweep.sh` covers telling drift from
regression.

These frames pin decisions; they do not measure accuracy. For that see
[scanner-limits.md](../../docs/specs/scanner-limits.md), and for the clean-scan
counterpart see [the parser corpus](../corpus/README.md).
