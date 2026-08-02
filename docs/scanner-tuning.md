# Tuning the scanner: the loop and the field lessons

How the hands-free scanner was tuned, and what nine live sessions taught us —
kept so future scanner work starts from these lessons instead of rediscovering
them. Recognition went from 7/14 to 14/14 across these sessions, and every
failure along the way is now a named regression test, a fixture, or a comment
at the code it shaped.

## The tuning loop

The loop that made every failure reproducible offline:

1. **Capture telemetry while scanning for real.** `HOARD_SCAN_LOG` makes the
   Go session tee the helper's entire stream — events prefixed `<`, stderr
   traces prefixed `!` — to a file with millisecond timestamps, even while the
   TUI owns the pipes:

   ```sh
   rm -f /tmp/scan-telemetry.log
   HOARD_SCAN_LOG=/tmp/scan-telemetry.log HOARD_SCAN_AUTO=1 HOARD_SCAN_MULTI=1 \
   ./hoard --db /tmp/scan-test.db add
   ```

   Always test against a scratch `--db`: auto-commit writes without asking.
   The timestamps turn the stream into speed data — trigger settle time is
   the `armed → capturing` delta, OCR latency is `capturing → scan` — and the
   per-capture payloads (candidates, collector blocks, finish markers) are
   the accuracy data. The session summary printed at exit is the outcome
   tally.

2. **Turn problem captures into fixtures.** `HOARD_SCAN_DEBUG_DIR` saves
   every capture's raw and OCR-processed frames. `--image` replays a frame
   through the *identical* pipeline:

   ```sh
   HOARD_SCAN_DEBUG_DIR=$PWD/scan-fixtures ./bin/hoard-scan.app/Contents/MacOS/hoard-scan --auto
   ./bin/hoard-scan.app/Contents/MacOS/hoard-scan --image scan-fixtures/capture-3-ocr.png --rotate 0
   ```

   Once a problem card is on disk, iterate offline: change the helper, re-run
   the sweep over every fixture, and diff. Session directories stay out of
   the repo (gitignored — the frames are photos of your desk), but a frame
   that pinned a decision gets distilled into `scan/fixtures/` with a golden
   card list: `make scan-check` sweeps the checked-in set, and
   `scan/fixtures/README.md` says what each frame pins and how to add one.
   Re-run the sweep after any parser or trigger change; it is the scanner's
   regression suite.

3. **Diagnose the trigger with the per-sample firehose.** For a card the
   trigger won't see, `HOARD_SCAN_AUTO_TRACE=1` logs every sample's raw
   rectangle count, surviving candidates, and biggest box. The three failure
   shapes read directly off it: *no rectangles at all* → detector thresholds
   or edge contrast; *flickering rectangles* → grace/stability tuning;
   *rectangles but zero candidates* → the background baseline swallowed them.

4. **Ask for ground truth.** A photo of the actual card (or its Scryfall
   page) settles which read was right. More than one "misread" turned out to
   be the parser correctly reading a *different* card's border in frame.

### Trigger knobs (env, no rebuild)

| Variable | Default | Meaning |
|---|---|---|
| `HOARD_SCAN_AUTO_INTERVAL` | 0.2 | sample period, seconds |
| `HOARD_SCAN_AUTO_STABLE` | 3 | still samples before firing |
| `HOARD_SCAN_AUTO_REARM` | 3 | pooled disruption samples before re-arming |
| `HOARD_SCAN_AUTO_GRACE` | 3 | bad samples tolerated mid-stabilization |
| `HOARD_SCAN_AUTO_IOU` | 0.65 | overlap for "same rectangle, still" |
| `HOARD_SCAN_AUTO_BG_IOU` | 0.5 | overlap for "that's background furniture" |

## Field lessons

Each was observed live, diagnosed from telemetry, and is enforced by a test
or a load-bearing comment at the code it shaped. Symptom → cause → where the
fix lives.

**A creature's power/toughness reads as a collector number.** "2/2" matches
the pair regex perfectly and shares the bottom band. Guard: a pair only
counts if the total is ≥ 20 or the numerator is zero-padded.
(`parseCollectorInfo`, main.swift.)

**Licensed frames break format assumptions.** Marvel frames print the rarity
*before* the number ("R 0657"), and mythic's M arrives as Cyrillic М until
`asciify` folds it. Never assume the M15 layout is the only layout.

**The foil marker is printed text, not computer vision.** Modern frames star
the set/language separator on foils (`MSC ★ EN`) and bullet it on nonfoils.
The star misreads as `*`, `+`, and even letters (`X A K T` all observed) —
but letter separators must require leading whitespace, or `KRAKEN` and
`MOLTEN` inside real card names parse as borders and real titles get eaten.

**Border and artist lines become ghost cards.** Small-caps frame furniture
("KEy WALKER" — the artist credit) survives filters and fuzzy-resolves to
real cards (Kev Walker became *Kiln Walker* in the queue). Titles are Title
Case; a multi-word line with ≤1 lowercase letter is furniture. Similarly,
fallback OCR lines carrying type-line vocabulary ("creature.") must never
reach the searcher — "creature." resolved to *Creature Guy*.
(`titleLike`/`boilerplate` in main.swift; `fallbackLineSuspect` in
internal/tui/autoscan.go.)

**Scanning off a stack is normal, so every border in frame is a candidate.**
The card beneath the top one shows a sliver whose block parses as well as the
target's. Parse *all* of them, ship them as alternates, and keep whichever
number matches a real printing of the resolved card — a neighbour's number
cannot verify, the true one can. The same verification rescues misread
digits: wrong reads fail closed into the queue.

**Set-and-number verification is self-consistent evidence.** A name
fuzzy-resolved to the wrong card cannot have its number match that card's
printings. So a full set+number match carries a glare-truncated name (79%
similarity) or a low-confidence exact read; the strict name gates apply only
when printing evidence is weaker. Weigh evidence, don't stack vetoes.
(`verdict` in internal/tui/autoscan.go.)

**Vision's rectangle detection crumbles on hard cards.** Foils and
borderless/full-art cards blink out (a third of all samples, measured) and
return slivers of their own art instead of the card. A fragment *inside* the
known box is evidence of stillness — count it toward firing; don't reset the
streak. Tolerance for flicker must live in every phase, not just one.

**Hold-phase counters must pool and decay.** A hand placing the next card
alternates between occluding the scene and reading as a moving box; separate
empty/moved counters reset each other and park the trigger. One disruption
counter, decremented (not zeroed) by calm samples, accumulates through real
placements and drains under isolated blinks.

**Don't let the machine learn the wrong lesson.** Absorbing "fired but read
nothing" rectangles into the background baseline sounded like self-tuning —
and then one glared shot absorbed the scanning pile itself, killing auto
capture at the exact spot every card lands. HOLD already prevents re-fires;
resist adding memory that can poison the workflow.

**Beware stale evidence after blocking work.** Video samples queued behind
the ~1s OCR replayed against the post-capture state and faked a full
disruption burst in one millisecond (instant double-fires). Timestamp
samples; drop the ones older than the last capture.

**When geometry is exhausted, use content.** A card stacked squarely on the
pile is geometrically identical to the card just shot. The recheck nudge
re-arms the trigger after a quiet beat and lets the *resolution* decide: an
echo of the just-processed card is discarded silently, a new card commits.
One echo means "parked" — stop rechecking until something happens. And make
the nudge tag a time window, not a consumed flag: a real scan can race the
nudge onto the wire, and the flag was observed being spent on the racing scan
while the true echo slipped through.

**Fail-safe beats fail-silent, in both directions.** Everything uncertain
queues rather than guessing (wrong-set commits are invisible until
valuation), but phantoms in multi-card captures die with a note rather than
queueing — a queue full of ghosts trains the user to ignore it. The one card
of a single-card capture must never vanish silently.
