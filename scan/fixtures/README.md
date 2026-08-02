# Scan fixtures

Real capture frames, replayed through the helper's exact pipeline by
`sweep.sh` and diffed against `.golden.json` card lists. `make scan-check`
runs the sweep; `./scan/fixtures/sweep.sh --update` regenerates goldens
after a *deliberate* behavior change (quote the diff in the commit).

Each frame is here because it pinned a decision:

| fixture | pins |
| --- | --- |
| `single-card-crop` | clean outline → perspective crop, collector read off the border (a non-Marvel frame, for set diversity) |
| `frame-fallback-collector` | no usable outline — whole-frame channel still pairs the title with a collector block |
| `flavor-attribution` | the "—Doctor Doom" phantom: a flavor credit under a quote must not become a card, even tilted (the quote's box *contains* the attribution's) and with the dash lost by OCR |
| `two-card-pile` | two title bands from one frame via the text channel |
| `ocr-mangle` | a misread title ("Manfled Marauder") ships as read — downstream fuzzy matching owns the fix, the helper must not invent |
| `empty-desk` | a frame with no cards yields an empty list, not junk entries |

Adding one: capture a live session with `HOARD_SCAN_DEBUG_DIR`, copy the
problem frame's `capture-N-ocr.png` here under a name that says what it
pins, then `--update` and commit frame + golden together. These are photos
of a real desk — crop or choose deliberately before committing. Whole
tuning sessions stay out of the repo (`scan-fixtures/` is gitignored);
only distilled regressions live here.

Vision OCR can drift across macOS releases; see the header of `sweep.sh`
for how to tell drift from regression. CI runs the sweep on a pinned
`macos-15` runner (`.github/workflows/scan.yml`, path-filtered to
scan-related changes since private-repo macOS minutes bill at 10x) —
bumping that pin and refreshing the goldens is one deliberate commit.
