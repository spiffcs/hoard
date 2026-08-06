# Scan fixtures

Real capture frames, replayed through the reader's exact pipeline by
`sweep.sh` and diffed against `.golden.json` card lists. `make scan-check`
runs the sweep; `./scan/fixtures/sweep.sh --update` regenerates goldens
after a *deliberate* behavior change (quote the diff in the commit).

> **The goldens were re-baselined on 2026-08-05 against a different reader.**
> They used to record the macOS helper's own pipeline, which existed for the
> Continuity Camera path; that path and that pipeline were both removed, and
> the reader is now `CardKit` via `bin/cardkit-probe` — the same code the
> iPhone app runs. 28 of 29 fixtures changed. What the goldens pin is
> unchanged in kind (the decisions, not the readings); what they pin is now
> the behavior that actually ships.
>
> The table below still says what each frame was *captured to pin*, because
> that is why the frame is worth keeping. Where the current reader does not
> deliver it, the row is marked — those are open gaps, recorded rather than
> quietly rewritten. See **Gaps against the current reader** below.

Each frame is here because it pinned a decision:

| fixture | pins |
| --- | --- |
| `single-card-crop` | clean outline → perspective crop, collector read off the border — and the set code comes from the border line (`MSH *EN …`), not from the flavor text's "…and it ain't you!", which used to win as set `AND` |
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
| `old-frame-crop-title-wins` | the crop read "Caller of the Claw" exactly while the frame offered the rules fragment "When Caller of"; the merge must adopt the crop's name instead of pinning the frame's furniture |
| `old-frame-crop-title-disagree` | the same card read two ways ("Etemal Dragon"), with the frame's rival reading kept as a candidate — the live session's "Gremal Dragon" fuzzy-resolved to the unrelated *Green Dragon* |
| `old-frame-self-reference` | the title band is lost and the card names itself in its rules text — "Dwarven Ruins" is recovered as the second candidate, and the mangled artist credit ("Tins. Liz Danforth") must not become the name |
| `old-frame-set-code-from-rules` | "…and put it into your hand" must not parse as set `PUT` + Italian; the collector number survives the set code's removal |
| `old-frame-pair-number-no-set` | a pair-form number ("29/143") is its own corroboration — it stays after the prose-derived set code (`FOR`) is refused, where requiring a set would have dropped a correct number |
| `modern-copyright-tail-number` | a modern frame prints one year and a bare number ("© 2024 Wizards of the Coast 418"), not the old range-and-pair — both must be read, and the number is tied to the brand word so a half-read "143/350" cannot donate its total |
| `old-frame-white-border` | a white border read off a real photograph, where the ring's absolute luminance is 0.46 — nothing like the 0.93 of a clean scan. Scoring it against the card's own footer tones is what still calls it white. Energy Tap's other printings are `leg`, `4bb` and `ren`, all black, so this one bit is the whole difference between 4ED and three wrong answers |
| `8ed-frame-brush-credit` | the 8th Edition frame draws a paintbrush where earlier frames write "Illus.", so its credit arrives as the bare name "Pete Venters" — two Title Case words, which outranked the real single-word title and made this card resolve as its own artist. A title-shaped line at the foot of the card is furniture, whatever it says |
| `8ed-frame-positional-anchor` | the same frame anchored anyway: with no content-proven footer, the bottom-most personal name is the credit by *position*, and the border reads white through it. The collector number is also present here, so the border must agree rather than interfere |
| `old-frame-black-border` | the other half of the border decision on a real photograph, and the first genuinely pre-1998 one: Builder's Bane is Mirage, 1996. Reads tone −0.57, i.e. darker than the ink of its own copyright line |
| `old-frame-border-glare` | the same card as a neighbouring capture read at tone 0.18 — glare lifted the ring off the border — and it must abstain rather than round toward white. This is the shape of every wrong-set commit the reader could ever cause |
| `old-frame-white-border-committed` | the same read where the collector number already settled the printing (SCG 39, 2003): the border must agree rather than interfere, and it is the corroboration a rank pairing year with border would rest on |
| `title-lost-block-intact` | the title reads as a line of rules text while the collector block is perfect (MSH/412) — the helper ships the block, and the Go side resolves the card from it rather than queueing an unidentifiable entry |

## Gaps against the current reader

Recorded at the 2026-08-05 re-baseline. Each is a decision a fixture was
captured to pin that `CardKit` does not currently deliver; the golden records
what it does instead, so the gap is visible in the diff the day it closes.

| fixture | the gap |
| --- | --- |
| `two-card-pile` | **Multi-card capture is gone.** `CardKit` emits at most one card per frame (`cards: cardEntry.map { [$0] } ?? []`), so the second title band is not read. `white-border-control` (3 cards) and `white-border-on-light-desk` (2) collapsed to one the same way. This left with the Continuity path — the two-channel frame reader was the macOS pipeline's — and is the largest single capability the removal cost |
| `old-frame-self-reference` | **Reads nothing at all** (empty name, empty candidate list) where the old pipeline recovered "Dwarven Ruins" from the rules text. The hardest frame in the set, and now a total miss rather than a partial one |
| `old-frame-black-border` | the black border read is lost (was `black`/`footer+ring`). `8ed-frame-positional-anchor` likewise lost its `white`/`footer` read. `BorderKit` is shared, so this is the anchoring in front of it, not the border maths — matching the corpus, where the reader answers only 24% of the time |
| `old-frame-border-glare` | the name now reads as the subtitle ("Seasinger" rather than "Summon Merfolk"). The border still abstains correctly, which is what the frame was really captured for |
| `old-frame-copyright-misread`, `old-frame-fuzzy-title`, `old-frame-no-number`, `old-frame-same-set-variants` | the copyright-line year is no longer read (2003/2001 → 0), and with it the tie-break it existed to feed. `flavor-attribution`, `old-frame-pair-number-no-set` and `old-frame-copyright-misread` likewise lost their collector numbers |

Not everything moved that way. `title-lost-block-intact` now reads the real
title instead of a line of rules text, `old-frame-crop-title-disagree` reads
"Eternal Dragon" where the old pipeline read "Etemal Dragon",
`8ed-frame-brush-credit` gained a white border read and stopped confusing the
paintbrush credit for a title, and `two-card-pile`'s surviving card lost its
trailing OCR junk. The corpus numbers are the fuller picture:
`make cardkit-score`.

These fixtures are real photographs; the corpus is clean scans. The two
disagree most on exactly this era, which is why both harnesses exist.

The goldens also pin the first three candidates. The Go side gives up after
the first few lines, so which readings sit at the front decides whether a
name recovered from rules text is reachable at all; the jittery tail of the
list stays out. `CardKit` returns a shorter list than the old pipeline did —
typically the title alone rather than title, type line and a rules fragment —
which is visible in nearly every golden here.

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
