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
| `rules-quote-title` | a crop whose "title" is the rules line quoting the name ("If Quicksilver, Brash Blur is in your…") keeps the frame's clean title and contributes only its printing — wholesale replacement fed the junk name downstream, broke the nudge echo-swallow, and let a keyword fallback line become a phantom ("Haste" → Haste Magic, observed live) |
| `old-frame-copyright-number` | an old frame's collector number lives at the tail of the copyright line ("… Wizards of the Coast, Inc. 95/350") and is extracted along with the range's end year, which breaks the 7ED/8ED number tie downstream |
| `old-frame-copyright-misread` | the copyright line's italic serif misreads digits ("30/145" → "80/145") — the helper ships the number *as read* but tagged `copyright`, and the Go side's upgrade-only rule owns keeping the card auto-committable |
| `old-frame-same-set-variants` | a clean old-frame read with no number anywhere (Cephalid Looter) — the same-set variation collapse on the Go side owns the single-print verdict |
| `old-frame-fuzzy-title` | the old serif title face misreads ("Scho Tracer") and ships as read; downstream fuzzy matching owns it |
| `old-frame-no-number` | an old frame whose copyright line is too mangled to yield a number (Frozen Solid) — genuinely ambiguous across two sets, and must stay an empty collector read rather than a guess |

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
