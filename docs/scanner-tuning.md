# Tuning the scanner: the loop and the field lessons

How the hands-free scanner was tuned, and what nine live sessions taught us —
kept so future scanner work starts from these lessons instead of rediscovering
them. Recognition went from 7/14 to 14/14 across these sessions, and every
failure along the way is now a named regression test, a fixture, or a comment
at the code it shaped.

> **2026-08-05: the Continuity Camera path was removed.** Everything below that
> was measured on the macOS Continuity rig — its 1920x1440 ceiling, its
> unlockable lens, its own read pipeline under `ScanKit/Core/` — is a historical
> record and is kept as one. The scanner is now the iPhone app alone, reading
> with `CardKit`. Where a measurement below names a Continuity number, the live
> figure is the phone's; where it names a file under `ScanKit/Core/` or a
> `hoard-scan` mode like `--image`, `--probe` or `--auto`, that code is gone and
> `bin/cardkit-probe` is the equivalent.

## The tuning loop

The loop that made every failure reproducible offline:

1. **Capture telemetry while scanning for real.** `HOARD_SCAN_LOG` makes the
   Go session tee the helper's entire stream — events prefixed `<`, stderr
   traces prefixed `!` — to a file with millisecond timestamps, even while the
   TUI owns the pipes:

   ```sh
   rm -f /tmp/scan-telemetry.log
   HOARD_SCAN_LOG=/tmp/scan-telemetry.log \
   ./hoard --db /tmp/scan-test.db add
   ```

   Always test against a scratch `--db`: auto-commit writes without asking.
   The timestamps turn the stream into speed data — trigger settle time is
   the `armed → capturing` delta, OCR latency is `capturing → scan` — and the
   per-capture payloads (candidates, collector blocks, finish markers) are
   the accuracy data. The session summary printed at exit is the outcome
   tally.

   The helper also stamps its own per-stage costs, always on, as
   `! timing:` stderr lines: `settle=…ms` (stabilize start → fire, the
   machine's share of the settle), `scanFrame frameOCR=… rects=… crops=N
   cropOCR=… total=…` (the Vision passes), and `capture N shutter+decode=…
   rotate=… total=…` (everything around them). The Go side adds `~ resolve
   "name" line=N name=…ms prints=…ms rank=… match=… set=… num=… prints=N` per
   card — the catalog/Scryfall lookups, the one place a network round trip
   can hide, followed by the evidence the verdict is about to weigh. Each
   card then gets a `~ outcome "name" committed|queued|dropped|killed: …`
   line carrying the reason verbatim. Together a capture's whole latency
   budget and its whole decision read straight off the log.

   That second half is worth the noise. A log that records only what the
   helper saw can show a card resolving perfectly at 0ms and never explain
   why it failed to reach the collection — which is exactly where one
   session's Sandstone Needle and Glowrider went, unanswerably, because
   re-deriving the verdict offline means refetching every printing.

2. **Turn problem captures into fixtures.** `HOARD_SCAN_DEBUG_DIR` asks the
   phone to send each capture's full-resolution still back, and
   `cardkit-probe --image` replays one through the *identical* pipeline:

   ```sh
   HOARD_SCAN_DEBUG_DIR=$PWD/scan-fixtures ./hoard add
   make cardkit
   ./bin/cardkit-probe --image scan-fixtures/capture-3-ocr.png --rotate 0
   ```

   Once a problem card is on disk, iterate offline: change the reader, re-run
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

Two knobs exist. This table used to list twelve more; they were the macOS
Continuity-era trigger's, and none of them is read by any current source (the
stillness-fires-without-a-rectangle path they configured does not exist in the
phone's trigger at all). Verified 2026-08-07 by grepping `scan/**/Sources` for
`environment[` — the only reads are the two below plus `HOARD_SCAN_DEBUG_DIR`
(save incoming stills) and `HOARD_SCAN_WAIT`.

| Variable | Default | Meaning |
|---|---|---|
| `HOARD_SCAN_AUTO_INTERVAL` | 0.1 | sample period, seconds (forwarded to the phone as the `tune` verb) |
| `HOARD_SCAN_AUTO_STABLE` | 6 | still samples before firing — 0.6s at the 0.1s period, **0.198s at the 0.033s period a live rig sets**, which is short enough to photograph a card still in the operator's hand |

Everything else in the phone's `TriggerTuning` (grace, IoU, background reset,
rearm samples, scene gates, `movedFaceMax`) is compile-time only.

Focus hunts are first-class trigger input: a hunt blurs every edge in frame,
so the trigger freezes (no streak growth, no grace burn, no reset, no HOLD
disruption) rather than mistaking blur for motion, and defers a ready fire
until the lens settles — mid-hunt captures were the out-of-focus scans, and
hunt-driven rectangle flicker was most of the settle-time tail (71 flicker
resets in a 15-card session). `focus hunt began/ended` lines appear in the
trace, which now reaches the same log by way of the phone's trace frames; the
capability line at session start reports what the device granted
(`focus=af+lock`, `af`, or `fixed`).

## The tuning ledger

Every trigger configuration actually measured on live sessions, in order. Kept
because most of these look like obvious wins in isolation and are not — the
sequence is the argument.

| interval | stable | grace | settle med | wasted captures | captures/commit | note |
|---|---|---|---|---|---|---|
| 0.2s | 3 | 3 | 1,732ms | 10% | 1.9 | the original |
| 0.1s | 3 | 3 | 667ms | 28% | 2.5 | halving the period halved the evidence too |
| 0.1s | 6 | 3 | 1,198ms | 19% | 2.2 | evidence restored |
| 0.1s | 6 | 3 | 1,666ms | 7% | 1.2 | + blink rule, guarded |
| 0.1s | 4 | 3 | 1,536ms | 12% | — | shortening the streak bought almost nothing |
| **0.1s** | **6** | **6** | 1,700ms | **7%** | **1.1** | + wider grace; **cadence 9.6s → 5.1s** |

### The remote source (iPhone app), first measured session

Same trigger, different camera and a network in the middle, so it gets its own
row rather than inheriting the one above. 61 captures, 48 commits, 2026-08-04.

| source | cadence med | captures/commit | captures reading nothing | shutter→result |
|---|---|---|---|---|
| Continuity (local) | 5.1s | 1.1 | 7% | ~700ms budget |
| **Hoard Scan (iPhone)** | **4.90s** | **1.27** | **0%** | **456ms** |

The phone is faster than the pipe it replaced and reads more reliably — not one
capture in 61 came back with nothing — but it spends that lead on extra
captures. Both facts have the same cause, and it is not the trigger.

### 2026-08-05 — a card that moved counted as a card that arrived

27 outcomes, **8 of them duplicate writes**; Skirk Volcanist committed five
times in six seconds while sitting on the desk. Every duplicate carried
`fireReason: replaced`.

**The cause is structural, not a threshold.** `Trigger.hold()` calls a card
replaced when the picture inside the window *pinned at the last shutter*
changes past `cardChanged`, while a box still overlaps `watched`. Sliding a
card satisfies both clauses — the pinned window now sees a different part of
the card, and the card's new box still overlaps its old one. The measurement
that set `cardChanged = 12` compared **static (2.7)** against **swapped in
place (29-50)**; it never measured **moved**.

Same-printing gaps in that session, against the swap floor already fitted here
(60 gaps, never under 3856ms):

| gap | verdict |
|---|---|
| 0.91s, 0.93s, 0.93s, 0.96s, 1.60s, 2.60s | impossible as swaps |
| 5.44s, 7.12s | plausible as swaps, still the same card sitting there |

**Two fixes, one per half.**

- The trigger gained a `moved` rearm cause. When the pinned window says the
  picture changed, it now asks a second question — does any live box still
  *look like* the card that was shot, sampled through the box itself rather
  than the pinned window. A card that slid keeps its face; a new card does not.
  `movedFaceMax = 20` sits in the gap between jitter (13.6 for the same card
  through a shifted window) and a different card (29-50). **Interpolated, not
  measured at that value** — see below.
- The Mac stopped treating `replaced` as proof. A same-printing repeat is
  dropped when the source says `moved`, or when it lands inside
  `sameCardFloor = 3s`, whichever fires first. The floor re-anchors on every
  sighting, so a card announcing itself once a second stays suppressed.

**Two instrumentation bugs found in the process, both fixed.** `settleMS` had
been printing `0` on every fire since it was added — `count()` changes phase
before returning `.fire`, and `observe`'s `defer` zeroed the counter before the
caller read the snapshot, so the largest single term in the delay a person feels
had never once been visible. And the delta that *decides* `replaced` was never
traced at all. The trigger line now carries `hold=` and `sinceCapture=`, which
is what `movedFaceMax` should be fitted against on the next session rather than
interpolated.

**Where the loop goes.** shutter 149ms, read 302ms, phone total 447ms, `net`
(wire + the Mac's resolve) 8ms median and 45ms at p90. The network is not the
problem and never was; the read is 68% of the loop and the wire is noise. That
is why the ledger's remote row is about the *parser*, and why `net=` is now on
the timing line — so a future session can tell the two halves apart without
guessing.

**The nudge was firing on every card.** `nudgeBaseDelay` is 2500ms, fitted to
the local pipe. Across 60 result-to-next-capture gaps on the phone, **none was
under 2500ms** — the fastest swap was 3856ms, the median 4896ms, p75 7047ms. So
the nudge never once caught a parked card; it fired mid-swap, every time, and 43
of 61 resolves came back tagged `nudged`. Each one re-arms during the swap and
buys a capture nobody asked for, which is most of the distance between 1.27
captures per commit and the local 1.1.

Retuned to **5500ms for the remote source only** — above the observed median
swap, below the p75 — so it fires when a card is genuinely parked and stays
quiet when the operator is just working at their own pace. Pinned by
`TestSourceTuningIsPerSource`, with the measurement in the test's comment so the
number cannot be "cleaned up" back to the local one.

`remoteNameTimeout` went the other way, **500ms → 250ms for the remote source**.
The 700ms budget is shutter-to-result, the phone already spends 447ms of it at
the median and 472ms at p90, and a 500ms Scryfall escalation on top blows it
outright. 250ms still catches a catalog miss on a fast network and gives up
before the card stops feeling immediate.

**Read the numbers as one session.** Cadence and captures-per-commit are the
honest columns here as above, and one sitting of 48 commits is enough to justify
a retune but not to close the question. The next session should show
captures/commit falling toward 1.1 with cadence unchanged; if cadence rises
instead, 5500ms is too long and the operator is waiting on the nudge.

Read the last column, not the settle column. Settle medians mislead once grace
is wide: a pass that used to fail fast and retry now lingers and succeeds, so
the median rises while the user waits less. **Card-to-card cadence and captures
per commit are the honest numbers**; both improved roughly twofold across this
sequence while waste fell.

### 2026-08-06 — the swap floor never described a stack

19 fires. One card lost: a second No-Dachi, stacked 1671ms after the first,
dropped and not written for **73.5 seconds**.

```
23:54:51.601  fire  hold=38.5 face=39.8  cause=removed   → committed CHK/264
23:54:53.342  fire  hold=34.3 face=32.5  cause=replaced  → DROPPED
              outcome "No-Dachi" dropped: same card, re-read 1671ms after the last sighting
```

**`sameCardFloor` was fitted on the wrong motion.** Its 3s rests on 60 measured
gaps in which "a human swapping a card was never faster than 3856ms" — and
every one of those was a *swap*: remove, then place. **Stacking skips the
removal.** It is quicker, and it is the natural motion for a hands-free
session, so the floor never described the case it now governs most often.

**And the fallback had outlived its premise.** The floor was introduced above
to catch what the source got wrong, on the reasoning that a phone too old to
send `moved` reports these as `replaced`. That was true when the
`movedFaceMax` branch never fired. It fires now — the same session dropped
Root Elemental correctly at `hold=13.1 face=15.8 cause=moved`. The
discriminator the clock stood in for works, and the clock was still outranking
it.

Separation across the session, which is the measurement the entry above asked
the next session to take:

| cause | `face=` readings |
|---|---|
| `moved` (same card) | 15.8 |
| real placements | 20.1, 26.4, 29.4, 32.5, 36.6, 37.1, 39.8, 42.7, 44.1 |

Real separation, **one negative sample**. So `placementFaceFloor = 25.0` —
`movedFaceMax` plus a 25% margin — and a marginal `replaced` still defers to
the clock. Only `replaced` qualifies: `removed` means the captured card left
the watched rect, which is equally what picking a card up and setting it back
down does.

**`faceDelta` was stale on `removed`, and the trace was lying.**
`stillLooksLikeTheCapturedCard` only runs on the occupied branch, and
`lastFaceDelta` is cleared nowhere inside a HOLD run — so a reading taken while
a box *was* there survived into every later `.removed` sample. Observed:
`boxes=0 hold=49.1 face=29.4 cause=removed`, a frame with nothing in it
reporting a face comparison. Fixed at the source. **Any `face=` reading on a
`removed` line in a log older than this entry is a leftover — do not fit
against it.**

Both deltas now cross the wire as `holdDelta`/`faceDelta` rather than living
only in the stderr trace, so the next refit is a log query instead of a
transcription. They are pointers on the Go side: absent and zero are different
answers, and zero is the reading for an identical picture.

**Two nudge bugs turned a 10s suppression into 73.5s.** The nudge-echo branch
returned no command at all, which *ended* the recheck loop rather than bounding
it, so a suppressed card waited on the phone's own re-arm. And `nudgeDrops`,
the counter documenting "the next nudge backs off", was incremented and never
read — the back-off did not exist. The loop now always reschedules, and
`nudgeBackoff` doubles per consecutive echo to a cap of 3 doublings (5.5s →
44s). The cap is safe because a card arriving while the timer is parked never
waits for it: the phone fires on disruption and voids the pending generation.

**What to measure next.** `placementFaceFloor` is fitted against a single
negative sample, so the session that confirms or moves it is the one that
jostles a card *without* swapping it — under glare, at an angle, at the edge of
the mat — and records every `face=` it produces. Until then the margin is doing
the work a measurement should. The `+` key is what makes being wrong cost a
keystroke rather than a card; if a live session needs it more than rarely, the
threshold is wrong and not the affordance.

### 2026-08-06 — the old-frame foil marker, read at last

Retro-frame foils committed as nonfoil, silently, because the only finish
signal is the star the modern set row prints and old frames have no set row.
Fixed by reading the printed **sparkle** — the eight-point starburst with a
comet trail at the text box's lower-left corner — by normalised
cross-correlation against a fitted template. `SparkleGate` and
`BorderKit/Sparkle.swift`; corpus and labels in `scan/foil-corpus`.

**Where it landed.** End to end over session 2's 71 stills:

| class | asked | accepted | range |
| --- | --- | --- | --- |
| retro foil | 13 | **11** | 0.374 … 0.778 |
| retro nonfoil | 17 | **0** | −0.427 … 0.443 |
| modern foil | 0 | 0 | never asked — the separator already answered |

`sparkleMS` median 1.04, max 1.18, on a 129 ms read — **on the Mac bench**.
That qualifier was missing and it matters: live on the iPhone the same code
costs 16-23 ms inside a 214-348 ms read. Both are cheap; only one of them is
the number a session will show you. `make cardkit-score` holds at 87% / 78%.

**Constants, and what each one cost to learn.**

- `accept = 0.52`. The live gap is 0.443 → 0.533, and the middle of it would be
  0.49 — but `scan/fixtures`' Sacred Ground is a *confirmed nonfoil* scoring
  **0.505** and Builder's Bane scores 0.509, both higher than any nonfoil either
  live session produced. The bar sits above them. Margin is 0.015 on one side
  and 0.013 on the other, pinned by single cards both ways: widen the corpus
  before trusting it further.
- `searchU/V = 0.0238 / 0.0216`. **Fitted, not slack.** At ±0.037/±0.042 the
  highest nonfoil goes 0.470 → 0.676 and two false positives appear. Wide
  horizontal with tight vertical is no better — foils median 0.320 against
  nonfoils' 0.364, i.e. no separation at all. Fix recall by re-centring
  `sparkleU`/`sparkleV`, never by searching wider.
- `firstFoilYear = 1999`. Premium foils began with Urza's Legacy. Not
  decoration: the template is fitted on 2001-2024 frames and the pre-1998 frame
  lays its text box out differently, so the search runs to its window edge and
  scores noise. Three pre-1998 fixtures cleared 0.50 before this gate. An
  *unread* year deliberately does **not** skip — see below.
- `spanU/spanV` are tied to `cols`/`rows` at 630×880. Widening the span without
  adding cells samples a bigger region more coarsely, which is a different
  template wearing the same name: at 0.150×0.075 the held-out foils fell from a
  0.611 median into the nonfoils.

**Three approaches that do not work, measured so nobody repeats them.**

- **Whole-card holographic chroma does not separate at all.** Hue spread over
  the text box, the art and the border overlaps completely between foil and
  nonfoil — the desk lamp's cast and the card's own ink dominate any regional
  colour measure. This is the intuitive approach and it is a dead end.
- **Taking the verdict from the cheap decimated pass** is 31× faster and
  manufactures a false positive (Frenetic Raptor, a nonfoil, 0.470 → 0.503).
  Decimated scores run high. Locate cheaply; judge once at full resolution.
- **Peak relief** (best score minus the median of the search surface) adds
  nothing: a nonfoil scored 0.757 relief on a 0.312 match.

**The reader does not use `CardGeometry`, and that is not an inconsistency.**
Going through it was built first and produced no separation live — every score
collapsed into 0.2-0.38. The cause is a real bug in `CardLayout.leftU`: it
returns the 8th Edition frame's landmark for any card whose year is ≥ 2003, but
**Legions and Scourge are 2003 printings of the old frame**. Their copyright row
starts near u=0.23 against a table value of 0.080, so every derived position
lands ~0.115 of a card-width right — measured, with the search pinned at its
boundary. The sparkle sits at u≈0.2, v≈0.89, well inside the card, so it reads
the perspective-corrected flatten directly and needs no text anchor. The `leftU`
era bug is still there and still worth fixing for the expansion-symbol reader.

**A fixture was deleted, not fixed.** `old-frame-border-glare` (Seasinger, a
nonfoil) has a finger across its footer, so its year never reads and it scored
0.751 on noise. Making the gate refuse it required treating an unread year as
pre-1998, which costs Victimize and Consuming Corruption — two real foils whose
copyright rows also fail. Degrading the reader to accommodate a broken capture
is backwards; the fixture went instead. `modern-copyright-tail-number`
(Meltdown) was a genuine catch and its golden was updated to `foil`.

### 2026-08-06 — leftU had one bucket holding two frames

`CardLayout.leftU` is the card's horizontal landmark: find the copyright row,
look up how far in from the left it starts, measure everything else from there.
It was keyed on the copyright year, in three buckets, and the last one —
"2003 and later" — held **two frame designs that put that row half a card
apart**.

Measured over `scan/corpus` with `cardkit-probe --anchor-fit`, which reads the
raw corpus images (they *are* cards, so the anchor's box is card space with no
card-location or flatten in between):

| frame | copyright row starts at | n | IQR | table said |
| --- | --- | --- | --- | --- |
| pre-1998 | 0.086 | 80 | 0.016 | 0.086 ✓ |
| 1998–2002 (trademark) | 0.233 | 6 | 0.006 | 0.231 ✓ |
| 8th Edition (2003–2013) | 0.079 | 16 | 0.004 | 0.080 ✓ |
| **M15 (2014+)** | **0.593** | **35** | **0.004** | **0.080** ✗ |

Both are tight. They are simply different places, and every M15 card was told
the 8th Edition value.

**What it cost.** Nothing shipping, because the only consumer is `symbolInk`
and nothing consumes that yet — which is exactly why it survived. The damage is
visible the moment you look: on session 2's four M15-frame cards,
`symbolCoverage` was **0.000** before the fix and **0.188–0.312** after. Zero
because `point()` was throwing every sample clean off the image, which is the
failure the function's own doc comment predicted for a wrong offset.

**The year cannot decide it alone.** Magic 2015 shipped in **July 2014**, so
2014 holds both frames and every later year holds only the new one. `2014` is
therefore settled on evidence — the M15 frame is the first to print a
set/language row and the first to put the collector number on its own line, and
either is enough. Requiring *both* misses three real M15 cards (a Snakeskin Veil
whose set code did not read, two Unsanctioned cards whose number did not);
accepting either *without* the year mislabels four older cards whose joke-set
text misparses as a set code (`S.N.O.T.` reads as set `CYRIL`). See
`FrameEvidence`.

**A wrong turn worth recording.** The first instinct was to stop guessing and
measure the landmark off the flattened card. It does not work: against ground
truth on clean scans the flatten reads pre-1998 anchors at 0.045 where they are
really 0.086, because the located quad does not reliably bound the printed card
— which is the reason the text-anchored table exists at all. Measuring through
the flatten is what makes a right answer look wrong. Fit this table on raw
corpus images or not at all.

Also corrected while in there: the 1998–2002 `year` prefix from 0.271 to 0.260
(n=9, IQR 0.005).

Still open: the `1998-2002` / `copyrightGlyph` combination spreads 0.214–0.274
and stays `nil`, and the 8th Edition credit row is still unmeasured.

### 2026-08-06 — session 3, and what the sparkle reader is actually limited by

The first live session after the marker shipped, on a pile where **every card
was a retro-frame foil**: 15 scans, 6 auto-committed, and 3 of those 6 recorded
the wrong finish. Stills and log are `scan/foil-corpus/stills/s3-*.jpg` and
`session3-telemetry.log`.

**Nine of the fifteen queued, and not one of them was a foil problem.** Four
causes, all in the footer read, all now fixed and regression-tested:

- The collector number was read *inside* the copyright year's branch, so a row
  whose four small italic digits failed OCR dropped a number sitting in plain
  text at the end of the same line — `wards of the Coast 399`,
  `zards of the Coast 14`, `Wizards of the Coast 413`,
  `4 Wizards of the Coast 407`. Nothing about `trailingNumber`'s safety ever
  came from the year; it only happened to be standing in the doorway. Splitting
  the two gates needed a fourth guard in its place — the digits must sit against
  the company name — because `looksLikeCompanyRow` matches substrings and
  `beasts of the coastal plain 12` otherwise reads as printing 12.
- A `$18` price tag in frame and a `T 89` fragment off a mangled `Illus.` credit
  both became collector numbers, and `.ownRow` outranked the real number on the
  copyright row below. A number that matches nothing is not neutral: it
  outranked the year *and* then failed the ranking.
- `rankByScanStrength` returned `scanMatchNone` the moment a number matched no
  printing, throwing away the copyright year and the markings read off the same
  card. Lion Umbra queued holding a clean 2024 and a foil sparkle.
- An unevidenced finish committed silently. `finishFromEvidence` had always
  reported whether the card told us and `verdict` was the one caller discarding
  the answer, so Glowrider, Trap Digger and Hard Evidence each wrote `nonfoil`
  off silence with nothing on the row saying it was a guess.

**The four foils that read nonfoil have washed-out pixels, not a mislocated
search.** Full working in `docs/scanner-foil-registration.md`; the short version
and the two refuted explanations, because both were plausible:

- *Not* the live sampler. `cardkit-probe --image --border` reproduces the live
  verdict 15 of 15 on those stills, misses included.
- *Not* the search window, though the evidence for it was good — Glowrider's
  best match sits at `du = -0.0238`, exactly `-searchU`, and its sample count
  drops to 34112 from 54444 because `sparkleScan` clips the refine neighbourhood
  at the window edge. `--sparkle-where` re-runs the search at four times the
  half-width in each axis and **rescues none of the four**: 0.020 → 0.000,
  0.339 → 0.473, 0.425 → 0.446, 0.496 → 0.513.

What separates them is `SparkleReading.contrast`. Every miss is in the bottom
five of fifteen (0.0089-0.0380); nothing above 0.0549 missed. The marker is
where the reader looks, there is just very little of it left in those pixels.
`SparkleGate.minContrast = 0.005` sits 7-11× below where the misses live, so a
patch with a tenth of a good one's structure is scored as though it were
evidence.

Corollary for the tuning rule above: "re-centre, never widen" is still right
about *widening*, but re-centring is not the fix either, and there is an
unexplained peak at `du ≈ +0.07` on four cards that must be understood before
anything refits the template. See the design note.

Read the score before touching the threshold. It is on the wire now
(`sparkleScore`, `sparkleOffsetU/V`) and on the `resolve` line, which is the
half of the finish-provenance argument that did not land the first time: a
verdict without its measurement made this session unreadable.

### 2026-08-07 — the CoreML marker classifier: eval-only, and why

A CreateML transfer-learning classifier over the sparkle-patch crops
(`scan/foil-corpus/extract-crops.py` → `dataset/<rig>/<finish>/`, trained and
scored by `train-foil.swift <held-out-rig>`), measured with each rig held out
in turn:

    s9 held out      34/35   0 foils missed   1 FALSE-FOIL  (s9-16, Ornithopter)
    s5 held out      36/38   0 foils missed   2 FALSE-FOILS (s5-01, s5-03)
    corpus held out  37/49  11 foils missed   1 FALSE-FOIL  (s2-51)

Recall on the rigs the template collapses on is transformative — s9's foils
went 12/35 committed under the template gates to 35/35 recognised — but the
ship-gate is zero false-foils on every held-out rig, and 4/33 nonfoils across
the folds read foil. The standing preference makes a false-foil the expensive
error (a silently overpriced row), so the model does NOT drive the verdict.
It stays as tooling: rerun the folds whenever a rig is added, and the next
step worth measuring before any promotion is a probability threshold — the
folds above use the argmax label; a high p(foil) bar may buy the recall
without the false-foils, or show the same cross-rig fragility the chroma
score had. `train-foil.swift all <out.mlmodel>` builds a shipping artifact
only if that study ever passes the gate.

### 2026-08-07 — the decision ceiling, and where it should end up

`decisionCeiling` (internal/tui/autoscan.go) caps how long a queued card holds
its review flash while a second look is out: 1300ms, against the nudge clock's
5.5s-doubling-to-44s. The measurement it stands on: across four live sessions,
every second look that ever rescued a card landed 0.70-0.90s after its queue,
and no retry that missed that window ever answered — the late reads arrive
mangled off a nudge and drop.

**1300 started tight by choice, to be raised on evidence.** The tell in a
session log is a rescue — a "re-read … beats the queued …" or a second-look
commit — landing *after* its card's "no better read within" line: that is the
ceiling cutting into real rescues. The 0.9s cap was measured on one operator
and one rig; the tight start spent that unknown deliberately.

**2026-08-07, later: pinned at 1000 by requirement.** It rode at 1800 for one
session while the queue-time Rearm landed. Then the operator set a hard
product bound — no card decision past one second, the hand-held pile flow
stalls otherwise — and the first pile session measured the active rescue at
584-614ms after queue, 5 of 5. 1000 clears that with ~40% margin. The
escalation ladder (1300 → 1800 → 2500) is retired: if a rescue ever lands
after its "no better read within" line again, make the retake faster rather
than the ceiling later.

### Knobs that do not do what they look like they do

- **`AUTO_STABLE` is not the latency knob.** Cutting it 6 → 4 moved settle 8%
  and cost 5 points of accuracy. Settle is not bound by how long the streak is;
  it is bound by how often the streak is abandoned or reset.
- **`AUTO_GRACE` governs abandonment, not evidence.** Grace freezes the streak
  rather than feeding it, so widening it never lowers the bar a shutter must
  clear. It was still only half the story — see the sliver lesson below, since
  passes were dying on mismatches that grace does not govern at all.
- **Per-sample evidence and wasted shutters trade close to linearly.** 0.3s of
  stillness bought 28% waste; 0.6s bought 7%. Any knob that only slides along
  that curve is not an improvement, it is a preference. The changes that
  actually moved both at once were the ones that stopped *discarding* evidence:
  wider grace, and the symmetric fragment rule.
- **Do not assume a change is free because one number improved.** The
  unguarded blink rule cut settle to 667ms and fired at bare desk 90% of the
  time. Always read waste and commits beside any speed number.

## Field lessons

Each was observed live, diagnosed from telemetry, and is enforced by a test
or a load-bearing comment at the code it shaped. Symptom → cause → where the
fix lives.

**A creature's power/toughness reads as a collector number.** "2/2" matches
the pair regex perfectly and shares the bottom band. Guard: a pair only
counts if the total is ≥ 20 or the numerator is zero-padded.
(`parseCollectorInfo`; see docs/scanning.md for where each name lives.)

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
(`titleLike`/`boilerplate` in ScanKit; `fallbackLineSuspect` in
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

**Old frames hide their collector number in the copyright line.** Pre-M15
frames print it at the tail of the bottom-centre copyright line ("™ & ©
1993-2003 Wizards of the Coast, Inc. 95/350") in italic serif the band crop
returns as fragments — the full-resolution frame pass reads it far better,
so both feed the extraction. The same line's range *end year equals the
printing's release year*, which is what breaks a number tie: "95" is Remove
Soul in both 7th and 8th Edition, and only one was printed in 2003.
(`parseCopyrightCollector` in ScanKit; the year filter in
`rankByScanStrength`, internal/tui/autoscan.go.)

**A copyright-line number may upgrade, never veto.** That glyph size misreads
digits — Aven Envoy's "30/145" arrived as "80/145", live — and a trusted
number that matches no printing rightly vetoes an auto-commit. So copyright
reads carry `numberSource: "copyright"` on the wire, and the resolver adds an
empty sentinel candidate whenever no *band* number was read: the sentinel
re-derives the no-number outcome as the floor, and the copyright number can
only rank higher. The flat event fields stay band-only for the same reason —
an old parent binary would trust anything it finds there.
(`resolveCardCmd`, internal/tui/autoscan.go.)

**A defaulted finish must yield to a later look that has evidence.** The foil
marker does not read on every capture of the same card. A first look with no
legible marker commits the nonfoil default; the recheck nudge then fires,
reads the star cleanly — and the echo swallow throws that capture away as "a
card already handled", leaving a foil recorded as nonfoil (Inspired Fire,
MSC 690, live). So commits remember whether the finish was *chosen by the
card* or defaulted, and an echo carrying a contradicting marker queues instead
of dying.

The correction re-keys the row in place (`MoveCardFinish`) rather than adding
beside it, and does not touch the card count — only the value, since the two
finishes price differently. An evidenced finish is never reopened: without that
guard a genuine foil commit would undo itself the moment a later capture failed
to read its marker, and the pair would flip back and forth.

The accepted risk is two copies of a card, one foil and one not, scanned back
to back — that produces evidence identical to one card read twice, so the
nonfoil row is corrected to foil and the second copy goes unrecorded. It was
already the losing case (the echo swallow dropped that capture outright), and
the guessed finish it overwrites was never evidence to begin with. A failed
correction queues loudly instead of passing silently: that is the one path
where staying quiet would leave a wrong price in the collection.
(`finishConflict` in autoscan.go.)

**The 8th Edition frame draws its illustrator credit instead of writing it.**
A pile of nothing but 8th Edition produced 8 frames, 0 footer anchors, 0
borders and 0 collector numbers — every card went to review. The cause is not
tuning. Where every earlier frame prints "Illus. Una Fricker", the 8th Edition
frame prints a **paintbrush glyph** and then the name, so `artistCredit` and
`illusToken` cannot match it by content and the credit row can never anchor
anything. Worse, the bare name then wins the title slot: live captures resolved
as *Pete Venters* and *Steve White* rather than Tremor and Reflexes.

The copyright row is the other half. It sits lower on this frame (v≈0.948
against 0.9375 before it) and is set thinner, and on a 1080p desk photo it did
not OCR at all across those 8 frames — the band returned power/toughness,
flavour text and name fragments and nothing else. It reads perfectly on a clean
scan, collector number and all ("Inc. 262/350"), which is exactly the trap: the
corpus says this frame is fine.

That one line failing costs *both* signals at once, and the measurement says so
across a mixed session: of 14 frames whose footer read, 9 also yielded a
collector number, and the 5 that did not were 4th Edition, which prints none. Of
the 31 whose footer did not read, **one** yielded a number. Bottom-band
legibility is therefore worth more than any further border or symbol work — it
is upstream of both, and of the commit itself. (Note also that
`CardLayout.innerV` at 0.950 lands on this frame's copyright text.)

**The collector block identifies a card the title cannot.** A name is not the
only key a card carries. When every line of text fails — an old serif title,
glare across the name, a title that reads as its own rules text — the bottom
band still says exactly which printing this is, and "MSH 412" is one card and
no other. Without that fallback the card became an unidentifiable queue entry
and went unrecorded for a whole session (Quicksilver, Brash Blur, live). The
catalog already indexed `(set_code, collector_number)`, so it is one local
query. Both halves are required, because a number without a set belongs to
every set ever printed; and a copyright-sourced number is refused outright,
since a misread there would not rank a card wrongly but invent one.
(`PrintBySetNumber`, `resolveByBlock`.)

**Modern frames print one year and a bare number.** The copyright parser was
built on the old frame's shape — a year *range* and an `n/total` pair — and
rejected the modern line outright, losing both the release year and the
collector number sitting on it ("™ & © 2024 Wizards of the Coast 418"). Accept
a lone year as corroboration, and a lone trailing number. Tie that number to
the brand word rather than merely anchoring it to the line end: a free-floating
tail match harvested "350", the *total* of a half-read "143/350", and a
truncated "14" — both measured before the rule was tightened.

**Vetoes must weigh evidence at every gate, not just the last one.** Two gates
sat outside that rule and queued cards nothing was actually wrong with. The
fallback-line veto fired before the rank was even consulted, so a Forest
holding an exact name *and* a full MSH/286 match was refused for the crime of
having been found on the wrong line. The OCR-confidence veto refused Eternal
Dragon and Hobgoblin on a 0.5 reading while their numbers each named exactly
one printing. A number that matches a printing of the card the name resolved to
is self-consistent evidence — the name could not be wrong and the number agree
— so it clears both. Below a verified number both keep full force.
(`numberVerified` in autoscan.go.)

**A nudge re-look that resolves to nothing is noise, not a lost card.** The
phantom kill only fired on multi-card captures, so junk from a recheck — a
mana ability, a type line, a fragment — queued one entry each. A nudge fires on
a scene already handled, so there is no new card to lose. The guard that makes
it safe: never kill an entry still carrying a collector block, because that
block is evidence a real card is in frame and the rule above can name it.

**Speed is a rate-versus-evidence trade, and it was measured three ways.**
Sampling twice as fast cuts every sample-denominated cost, but firing still
wanted three stable samples, so the evidence bar halved as a side effect:

| interval / stable | settle (median) | captures reading nothing |
| --- | --- | --- |
| 0.2s / 3 | 1,732ms | 10% |
| 0.1s / 3 | 667ms | 28% |
| **0.1s / 6** | **1,198ms** | **19%** |

Six samples at 0.1s is the same 0.6s of proven stillness the original demanded,
but recovers from detector flicker twice as fast — which is where the win
actually comes from, since settle was running at 3× its floor on resets rather
than on the floor itself.

**The detector alternates between a card and slivers of it, and the fragment
rule only forgave that one way round.** `fragmentsOf` asked whether the new
boxes sit inside the remembered one, so a streak that had latched onto a sliver
treated the card reappearing *whole* as motion and reset — at the exact moment
the detector finally got it right. Live: a motionless Flare of Cultivation
alternated between 0.37x0.88 and slivers as small as 0.08x0.13, and took
3,867ms to settle while reading perfectly (`rank=set+number`, exact name).

A box that contains what we were watching is the detector finding *more* of the
same still card — better evidence, not worse. Count it, and grow the remembered
box so the streak continues from the fuller read. Gated on the frame being
unchanged, because a hand sweeping in also produces a box that contains the old
one and that is motion; geometry says "same card, seen more of it", pixels
confirm "and nothing moved".

This is also why widening grace did not cut the abandon rate as predicted:
those passes were dying on sliver-versus-card mismatches, which grace does not
govern.

**Stillness proves the picture is not moving, not that a card is there.** The
first cut of the rule below counted any still-but-empty sample toward the
streak, and it was a disaster: **90% of captures read nothing** in one session,
153 shutters for 12 cards, the same unchanged desk photographed over and over.
A spurious box puts the trigger in stabilizing, every later sample is empty,
the desk is perfectly still — and it counts its way to a shutter on nothing.

Guarded, the same rule is sound: the detector must really have seen the card
(at least two genuine detections), the middle of the frame must have something
in it, the scene must differ from the one last photographed, and **at most half
the streak may be blinks**. That last cap is the load-bearing one — stillness
may accelerate a pass that is already going well, and can never carry one on
its own. Waste fell to **7%**, the lowest measured.

Note what that cost: with the guards in place the rule fires rarely, and settle
went back up. The knobs are a dial between the two, and the honest reading of
the sequence is that per-sample evidence and wasted shutters trade off almost
linearly — 0.6s of stillness bought 7% waste at 1,666ms settle, 0.3s bought 28%
at 667ms. Pick a point deliberately and measure it; do not assume a change is
free because one number improved.

**An empty sample means the detector blinked, not that the card moved.** The
per-sample firehose finally settled where settle time goes: **220 of 522
stabilizing samples returned no rectangle at all** while a card sat motionless
in frame. `novel` equalled `rects` in every bucket, so nothing was being
filtered — Vision simply drops the card on two samples in five. Each of those
burned grace and eventually restarted the streak, which is why settle ran at
more than twice its 0.6s floor.

The pixels settle it. If the picture is unchanged since the last sample then
nothing moved, so the miss was the detector and the card is exactly where it
was — count it toward the streak. It is the same argument the fragment rule
already makes, on better evidence: frame-to-frame stillness is a stronger proof
that a card is holding still than a box happening to land twice in the same
place. A card being *removed* still exits the pass, because removing it changes
the picture, which is not stillness, and grace runs out as it always did.

This is also what the pixel signature was really for. Added as a rival path
that fired on its own and wasted two thirds of its shutters, it works far
better vouching for the detector than racing it.

**A parallel fallback is not a fallback.** Splitting those captures by which
path fired them showed the rectangle path wasting 7% of its shutters and the
new pixel-stillness path wasting **64%** of its own. It was firing whenever the
scene was still, including when the detector was working perfectly. Gated
behind a run of abandoned passes it only engages where it was needed — on cards
the detector cannot hold — and the cards it genuinely rescued (4 of 11 fires)
are exactly the ones that had been abandoning passes anyway.

**"Network lag in the add pipeline" was neither.** The commit path never
touches the network, and printings came back in 0ms on all 52 resolves — the
catalog answered everything. Every stall was one thing: the helper's title
guess missing in the catalog and escalating to Scryfall. Eight resolves took
≥300ms for 7.9s total, and **six returned nothing at all** after ~600ms each,
because an unreadable frame yields a junk line 0 and we then ask the network
about a string that was never a name. The catalog try stays unfiltered — that
is what keeps a card genuinely named with a keyword scannable, and it is free —
but the escalation now requires the line to look like a title, and is bounded
by a timeout so a slow link queues the card instead of freezing the session.

**A borderless card defeats the detector, and that is what "slow" means.** The
complaint was speed; the cause was the trigger abandoning **75% of its
attempts** — 93 stabilization passes started, 21 fired, in one borderless
session. A borderless card's art runs to the edge, so the only edge is
card-against-desk, and Vision loses it sample after sample. What the user sees
is a scanner that will not fire, and what the user does is nudge the card,
which restarts the cycle. The measured 9.6s cadence was not someone handling
cards at their own pace; roughly 6s of it was compensating.

The machine's own 3.6s split settle 1.7s (p90 3.3s), shutter+OCR 0.7s, re-arm
0.6s, searching 0.6s. Settle's floor is three samples, so it was running near
3× its minimum purely on resets: 21 successful passes contained 24 streak
resets, 74 tolerated flickers and 46 counted fragments. OCR was never the
problem — the whole of `scanFrame` is 487ms.

Two changes. The sample period halved to 0.1s, which cuts every sample-denominated
cost mechanically. And the fire decision stopped depending on rectangles: a
coarse luma grid per sample answers "has the picture stopped moving" without
knowing what is in it, and a run of identical frames fires the shutter on its
own. Three conditions together, each load-bearing — *still* (nothing moving),
*changed* since the last capture (a parked scene cannot photograph itself
forever), and *detail* in the middle of the frame (lifting a card away leaves
a scene that is changed and still and yet bare desk). Drop the third and the
shutter fires every time a card is removed.

It is a floor under the rectangle path, never a replacement: when rectangles
work they fire first and the stillness count never matures. `HOARD_SCAN_AUTO_STILL=0`
turns it off without a rebuild, which is the thing to try first if a session
starts firing at nothing.

This trades evidence for latency, so some captures read nothing. That is the
accepted deal — a wasted capture costs ~0.7s and is invisible, against a 1.7s
settle and constant nudging — but the rate is the number to watch, and the
outcome telemetry already counts it.

**Before collector numbers, the card says almost nothing about its printing.**
A 4th Edition card's whole bottom line is two centred rows:

    Illus. Dameon Willich
    © 1995 Wizards of the Coast, Inc. All rights reserved.

No collector number, no set code, no language code, and a **single year** where
later frames print a `1993-2003` range. `parseCopyrightCollector` extracted the
year only through the range regex, so that entire era shipped with no year at
all — confirmed against a pristine scan that read the line perfectly and still
reported nothing. Accepting a lone year is the fix, and it is the only printing
evidence those cards carry.

It goes only so far. Measured over the 6,411 pre-1998 printings of cards that
have more than one printing — the ones that queue — the year alone pins 24% of
them outright, and cuts the rest from a median of 12 candidate printings (worst
case 861) to a median of 3. The residual is real: Alpha and Beta are both 1993
and both black-bordered, Revised and Summer Magic both 1994 and both white. The
card face cannot separate those, which is why collector numbers exist.

**Border colour doubles that, and the signal was never in the pixels.** Border
is the era's other discriminator and the catalog already stores it; year plus
border pins 47% rather than 24%, and 4th Edition goes from **0% to 95%**,
because 4ED (white, 1995) and 4BB (black, 1995) share their year, art and
artist and differ in nothing else a camera can see.

The first attempt was a pixel classifier over the perspective crop, and it was
removed rather than shipped. The crop does not reliably contain the border:
Vision locks onto whichever edge has contrast, so sometimes the crop starts at
the card's outer edge and sometimes at the frame just inside it. An 8th Edition
Gaea's Herald — white-bordered — was classified black off its gold-brown inner
frame. Saturation did not separate the cases (0.40 for a genuine white border
under tungsten, 0.43 for the gold frame); luminance separated cleanly (0.95 vs
0.18) but was measuring the wrong ring. **The missing signal was not in the
pixels, it was knowing whether what you are looking at is the card.**

So the second attempt reads no crop at all. It anchors on text the helper has
already identified *by content* — `copyrightFurniture` and `artistCredit` know
the copyright line and the illustrator credit by what they say — recovers the
card's own height from the distance between that row and the title, and samples
the border on the full-resolution frame. No edge contrast can fake a line that
says "Wizards of the Coast". Measured against the corpus, where the card fills
the image so the true card rect is known exactly, the reconstruction lands
within **1.5%** of the real card height.

**What the border is measured against is the whole trick.** The ring is scored
as a position in the range of tones the card prints its *own footer* with —
0 is that line's ink, 1 is the surface under it:

    tone = (ring − footerInk) / (footerPaper − footerInk)

A white border is brighter than the card's own paper and a black border darker
than its own ink, so both verdicts live **outside [0, 1]** and the ambiguous
middle is everything the card also prints with. Both endpoints move with the
lamp, so their ratio does not. White reads ≥1.05 and black ≤−0.03 across clean
scans, desk photographs and a live session alike; the gates are 1.30 and −0.01.

Absolute ring luminance was the first rule, and it is the one a lamp destroys.
It looked flawless on clean scans — white 0.92–0.93, black 0.04–0.18, gold
parked at 0.57 in the dead zone between them. Then a live session of
white-bordered bulk cards read **0.44–0.64**, straddling exactly where gold
sits, and the reader went silent on all six. The same six score 1.36–2.44 on
the ratio and all six read white. **A gate fitted on studio scans is a gate
that has never met a desk lamp.**

Four lessons came out of building it, and all four were the pixels
contradicting a story that sounded right:

- **The footer is not printed on the border.** The plan was to normalize
  illumination using the copyright line's own two tones — black ink on a white
  border, white ink on a black one — which would have made the whole decision
  relative. On an old frame that line sits on the *coloured frame* inside the
  border. Its bright mode reads 0.72 on a card whose border reads 0.93. The
  reference is now the frame just inside the border instead, used for standoff
  rather than as a white point.
- **A colour gate fitted on scans died on its first photograph.** Gold measured
  0.36 chroma against ≤0.20 for white and black, which looked like a clean way
  to reject it. Then a real white border under a warm desk lamp read **0.40** —
  channel spread measures the lamp as much as the ink. The gate was also
  redundant, since gold already sits in the luminance dead zone, so it went.
  Fit constants on `scan/corpus`; confirm every one of them on `scan/fixtures`.
- **Vision merges the two footer rows about a third of the time**, returning
  one observation twice as tall ("Illus: © Jeff A: Menges"). A scale check that
  assumed one row rejected 30 of 80 cards whose geometry was in fact fine.
- **A card on a desk is not lit evenly.** Requiring the far edge of the card to
  independently clear the same bar as the near edge is requiring uniform
  illumination: measured live, the top ring ran 0.10–0.15 darker than the
  bottom and failed three of six plainly-white cards. The opposite edge now
  vetoes only when it actively says the *other* colour; an indeterminate second
  opinion is not a contradiction, it just does not earn the `footer+ring` label.

Two things the reader genuinely cannot do, both left as limits rather than
papered over. It cannot separate **gold or silver from white** — a silver
border is light grey, and the chroma that separates them on a scan is the same
number a warm lamp fabricates. Eight such cards across the corpus read white.
So the Go side never *rules out* a printing whose border it cannot recognise;
those rows keep their place while the genuinely-excluded ones sink. (Suppressing
the border outright for such cards was the first fix and far too blunt: 22% of
pre-1998 multi-printing cards have a gold or silver sibling, and Control Magic
— one of the cards the feature exists for — lost its border to two Pro Tour
Collector Set rows.) And it needs a **title** as well as a footer, because two
rows are what make the scale checkable; with one anchor a card once
reconstructed 50% too tall while agreeing perfectly with itself.

What ships is ordering only. `applyBorderEvidence` promotes the printings whose
border matches, and never removes one, never raises a rank, never blocks a
commit. That restraint is the point, and it is not timidity: **every other
signal here fails closed and a border cannot.** A misread year matches no
printing and evaporates; a misread number matches nothing and the empty
sentinel re-derives the floor. A border is one bit, and a wrong bit always
matches *something*, because there is always a black-bordered candidate. So it
sorts the review queue and waits, and every read lands on the resolve line as
`border=white(footer+ring)→4ED` — the set it put on top. Those top rows are the
measurement: each one the user overrides is a recorded miss on a real card
under real light, which is the evidence needed before the pairing earns a rank
of its own.

Measured so far: **zero wrong on white or black** across 231 corpus images, 21
desk fixtures and two live sessions. A white-bordered pile read 7 of the 9
frames holding a legible card; the misses were a footer neither OCR pass found
and an artist credit read as the card's title. A black-bordered pile read 3 of
4, including Builder's Bane — Mirage, 1996, the first genuinely pre-1998 black
border photographed rather than scanned — at tone −0.57.

Two numbers from those sessions are worth keeping. The white pile's Energy Tap
came back `border=white→4ED`, which is the whole argument in one line: `leg`,
`4bb` and `ren` are black, `4ed` is white, and nothing else on the card says
so. And the black pile's one abstention was a glared re-shot of a card the
previous capture had read cleanly — tone 0.18 against its sibling's −0.90 —
which is exactly the reading that must not round toward white, because that is
the shape of every wrong-set commit this reader could ever cause.
(`readBorder` in ScanKit; `applyBorderEvidence` in autoscan.go.)

**The background baseline could swallow a card, and never gave it back.** The
furniture baseline is taken from the first sample after auto arms and is never
re-learned, so a card already on the desk at that instant became furniture for
the whole session — invisible at exactly the spot every card lands. Live:
`baseline: 1 background rect(s)`, then 46 seconds in which the detector kept
finding Sacred Ground and the filter kept deleting it, ending only when the
card was lifted and put back far enough off the learned box to read as novel.
The stall's fingerprint is bare `searching → stabilizing → searching` pairs
lasting exactly `grace + 1` samples, with no `stable`, `flicker tolerated` or
`scene moved` line between them.

The recovery counts *abandoned stabilization passes* rather than swallowed
samples, and the distinction is the whole safety of it: a desk whose real
furniture is correctly absorbed never enters stabilizing at all, so it can
never trip this, while a half-absorbed card keeps almost producing a candidate
and trips it quickly. Measured on the session that prompted it — 58 of 59
captures fired after fewer than 8 abandoned passes, worst healthy stretch 6,
the stall 13 — so the bar is 8, with room on both sides.

It only ever *forgets*. Nothing is added to the baseline at runtime; that is
the memory that once killed auto capture outright, and this is the same lesson
read from the other side — a baseline that can only be learned and never
questioned is just as capable of parking the trigger.

**Beware "improvements" that only look measured.** Opting the photo output in
to the format's largest still, to beat the preset's cap, read `activeFormat`
before the session was running — so it got the device's low-res default and
pinned every capture to *that*, dropping stills from 1920x1080 to 640x480 and
quietly shrinking every collector number for a whole session. The opt-in had
never gained anything (Continuity Camera reports 1920x1080 either way); it only
ever cost. Reverted, and the still size is now reported after `startRunning`,
where the number is true. Check that line first when reads go soft.

**Two agreeing signals outrank a mangled title; one does not.** A number that
named exactly one printing used to stop there, and a title OCR'd badly enough
queued the card anyway — "Stemal Dragon" resolved to Eternal Dragon at 76%
similarity while the band read a clean `12/143` and the copyright said
1993-2003, which is Scourge 12 released 2003, agreeing twice over (live). The
year was never consulted, because it was only ever used to break a tie *between*
number matches, and here the number had already picked one.

So the year is now checked against the winner even when it had nothing to
break, and the pairing earns its own rank (`number+year`) that waives the name
gate exactly as `set+number` does. The pairing is what matters, not the number:
collector number 12 alone is shared by five cards released in 2003, so a fuzzy
match onto the wrong card could collide with it by luck. A second field of the
same card agreeing is what removes the luck, and a misread year simply matches
nothing and leaves the rank where it was.

**The copyright year can name a printing on its own.** Old frames print no
number the band can reach, so most of what queues from them queues for want of
*any* printing evidence — while the copyright line has been carrying some all
along. Its range end is the release year, and on a card reprinted years apart
that names exactly one printing (Brain Freeze, ten printings, one of them
2003). Ranked below a number deliberately: the year is four small italic
digits, the same glyphs that turn "30" into "80". Ambiguity fails closed —
zero or several printings in that year leave the card queued.

Both directions were observed in one session. Brain Freeze's 2003 was unique
and committed; Shivan Oasis's 2003 is shared by two of its six printings and
stayed queued; and Keeper of the Nine Gales read **2009**, a year it has no
printing in at all, which is what a misread looks like and why the rule must
match nothing rather than guess. That last one is the argument against ever
promoting the year above a real number. (`soleIndexInYear` in autoscan.go.)

**An exact name with one printing outlives a bad number.** A band number that
matches nothing vetoes an auto-commit, because the mismatch suggests the
*name* landed on the wrong card. That reasoning is about a fuzzy match, and it
inverts when the name is exact: there the wrong thing is the digits, which
this glyph size misreads constantly (Aven Envoy's "30" arrived as "80", live;
Lethal Vapors' "68" as "8"). Left alone, the veto made a garbage number *worse
than no number at all* — an unreadable band commits such a card on name and a
lone printing. So an exact name earns the same empty sentinel the copyright
case gets, and the floor pays out only when the printings collapse to one; a
card with nine printings and a bad number still queues. The veto keeps its
full force behind a fuzzy name. It is the one path that commits in spite of
collector evidence, so every use is marked `number-overridden` on the resolve
line — audit it after a session. (`resolveCardCmd` in autoscan.go.)

**Catalog variation rows defeat the single-print bar.** Scryfall's bulk data
carries same-set variations as separate rows (`ody 72` beside `ody 72†`, the
theme-deck alternate), so a card with one real printing counts as two and
queues "printing unverified" on a perfect read (Cephalid Looter, live). Rows
differing only by a trailing variation marker (†, ★, Φ) within one set are
one logical printing; the unmarked row leads. (`collapseVariants`,
internal/tui/autoscan.go.)

**The merge ladder must decide the name, not assume the frame's.** When a
crop's rectangle contains a frame line's title box, the frame's read used to
win by construction. Every one of those decisions in an old-frame session was
wrong: the crop had read "Caller of the Claw" exactly while the frame offered
the rules fragment "When Caller of", and a frame's "Gremal Dragon" fuzzy-
resolved to the unrelated *Green Dragon* while the crop's "Eiteral Dragon"
would have landed on Eternal Dragon. Adopt the crop's name when the frame's
is furniture and the crop's is title-shaped — and ship whichever name loses
as a candidate either way, since downstream owns fuzzy matching and cannot
choose a reading the helper dropped. (`mergeInto` in ScanKit.)

**Prose fabricates set codes.** The set/language regex tolerates a bare space
between code and language, so once `asciify` uppercases everything, ordinary
rules text matches it: "…and put it into your hand" yields set `PUT` with
language Italian, "…and it ain't you!" yields `AND`. Four captures in one
session shipped a fabricated set code, and a checked-in fixture had been
pinning one for months. Gate *extraction* on the line reading like border
print — the set line is set in caps and carries almost no lowercase — but
leave `boilerplate`'s use of the same regex generous, because there a loose
match only kills a line. (`setLangFurniture` in ScanKit.)

**A pair-form number is its own corroboration.** The crop channel demanded a
set code beside a number, because a bare number is a mana cost as often as a
collector number. But "29/143" carries the set total, which a mana cost has
no way to fake — and once prose stopped inventing set codes, that rule started
throwing away correct numbers along with the fake sets they were paired with
(Brain Freeze, live). Trust the pair form on its own.

**Cards name themselves, so a lost title is often recoverable.** Magic rules
text refers to the card by name, and on an old frame — where the serif title
sits against the art and the band crop returns fragments — that is the only
place the name survives. "Dwarven Ruins comes into play tapped." names the
card twice over. Mine the leading Title Case run back out and ship it as an
extra candidate, never as the primary: it is a heuristic guess, and the
resolver already owns choosing among candidates. Order matters as much as
extraction — the Go side gives up after five lines, so the recovered name
sits directly behind the primary. (`parseSelfReference` in ScanKit.)

**Border print must never become the primary name.** Choosing the name from
the top *plausible* line — three letters or more — let an old frame's
copyright tail become the card's name at full confidence ("008 Wizards of the
Coast, Iac. 15/145", already logged as not title-like). Prefer a title-like
line, fall back to any non-boilerplate line so single-word titles still work,
and otherwise report no name at all: a frame that read nothing but its own
border has no title, and an empty name queues the card honestly. The same
applies to the artist credit, whose exact `illus` prefix missed the mangled
"Tins. Liz Danforth" — match the credit word loosely, but only behind the
trailing period and two-word personal name that no real title has.

**Fallback OCR lines are catalog-only.** The searcher falls through to
Scryfall on any local miss, which is what keeps newly-released cards
scannable. But the auto-commit bar already refuses every fallback-line match,
so those round trips can buy nothing but latency and a chance to ghost a real
card into the queue — one session spent 19s across 15 failed resolutions, the
worst single line taking 4.8s, against 0-15ms for every catalog hit. Only the
helper's own title guess goes off-machine. (`resolveName` in autoscan.go.)

**Token names ghost the queue.** The catalog's name index includes token
cards, so a type-line fragment ("Creature — Bird Soldier" split across OCR
lines) resolves to the *token* "Bird Soldier" and queues, and a partial read
("aldier") lands on the token "Soldier". Left as-is for now — filtering
token names would also make real token cards unscannable — but it is the
known residual source of queue ghosts.

**Bare personal names still ghost.** An artist credit whose "Illus." was lost
entirely reads as "Rob Alexander" — two Title Case words, indistinguishable
from a card name by any string rule, and the same shape that once made Kev
Walker into *Kiln Walker*. Geometry could settle it (the credit sits at the
card's foot, the way `flavorAttribution` uses the quote above it), but the
self-reference candidate usually rescues these frames anyway, so the ghost
costs a queue entry rather than a card. Unfixed, and known.

### Measuring which kinds of card parse at all

`scan/corpus/` samples card images stratified by frame era × border colour and
scores the reader against known answers, per stratum:

    ./scan/corpus/fetch.sh && make cardkit-score

It is the only check that isolates frame-era parsing from capture quality —
the fixtures pin decisions on real photographs, `TestSessionReplay` measures a
session, and this answers "which *kinds* of card can we read". The images are
clean digital scans, so nothing about the trigger, the crop, glare or focus is
exercised; and because the card fills the frame, the detector locks onto the
border/frame boundary rather than the card edge, so the crop excludes the
printed border. Do not tune anything border-related against it.

The first run found two gaps worth naming: **borderless cards read 14% by
name** — the title sits over art with no frame to anchor it — and **gold
borders never yield a collector number**, because World Championship cards
number them `jn12`/`gn12a` and no numeric pattern matches. Baselines per
stratum live in `scan/corpus/README.md`; update them when they move.

### Measuring a session end to end

`make scan-check` proves what the *helper* reads. It cannot prove the number a
session is judged on — how many cards land in review — because that decision is
all on the Go side. `TestSessionReplay` (`internal/tui/scanreplay_test.go`)
closes the gap: it runs every saved frame through the real helper, the real
resolution and the real verdict against the real catalog, and prints
commit/queue/killed per card with the rank that decided it.

    HOARD_REPLAY_FRAMES=/tmp/scan-fixtures go test ./internal/tui -run TestSessionReplay -v

It skips without frames, a built helper, or a populated catalog. Read its
totals as a direction rather than a forecast: it has no camera, so it never
models the nudge echo, the duplicate window, or a capture the trigger would
never have fired, and it therefore counts re-looks the live session would have
collapsed. The per-card lines are the reliable part.

One more caveat, learned by being confused by it: **replaying a saved frame does
not reproduce the live read exactly.** Repeated `--image` runs on one PNG are
deterministic, but the live capture path OCR'd the same card as
"son curtnot jon the storm" where the replay reads "sou curtnot", and one
Keeper of the Nine Gales that committed live on a 2003 copyright read 2009 on
replay and queued. Before blaming a change for a difference between a session
log and its replay, check whether the OCR text itself moved.

**1080p is the camera's ceiling.** Continuity Camera reports `still=1920x1080`
on the `scan: still=` line the helper prints once the session is live, so a
card fills roughly 680 pixels and the collector band is a few pixels tall.
Everything above is tuned for that; do not plan around more pixels without
reading that line first — and see the lesson above for what happened the last
time it was "improved".

*Superseded — the ceiling is 1920x1440, and `.photo` is what costs the 360
lines. See the capability ledger below.*

## Camera capability ledger

What the camera admitted to, rather than what it was believed to do. Measured
2026-08-04, macOS 15.6 (24G84), iPhone 16 (`iPhone17,3`) over Continuity, with
`hoard-scan --probe`.

*Historical.* The probe and the Continuity path it interrogated were both
removed on 2026-08-05, so this ledger can no longer be regenerated — it is kept
because it is the evidence for why the phone replaced that path, and the whole
section below is the argument. The phone's own numbers are in the sections after
it.

**Continuity Camera formats — eight, topping out at 1920x1440.**

      [0] 640x480    [2] 1280x720   [4] 1920x1080  [6] 1920x1440
      [1] 640x480    [3] 1280x720   [5] 1920x1080  [7] 1920x1440   (odd = 60fps)

**`sessionPreset = .photo` is a downgrade, not an upgrade.** The session
experiment reads `activeFormat` at three moments:

      before any configuration          video=1920x1440 photo=1920x1440
      after .photo, before startRunning video=1920x1440 photo=1920x1440
      after startRunning                video=1920x1080 photo=1920x1080   ← the preset lands here

The device wakes up on its best format and the preset *reduces* it once the
session starts. That is the whole explanation for `still=1920x1080`.

**Assigning `activeFormat` after `startRunning` works.** This is the path the
`maxPhotoDimensions` lesson above never tried — that one set the *output's*
dimensions before the session ran, when `activeFormat` was still the low-res
default, and got 640x480. Setting the *device's* format once the session is
live is accepted:

      activeFormat = <1920x1440 format>   accepted
      photoOutput.maxPhotoDimensions      set 1920x1440, reads back 1920x1440

**But 1440 is field of view, not detail.** Both formats are 1920 across; 4:3
is the uncropped sensor and 16:9 is a vertical crop of it. The card's long axis
sits on the 1920 axis either way, so this does not by itself put more pixels on
the card — it removes a crop. Whether it converts into a legible collector band
depends on reframing the rig closer, and that is a live session's question, not
arithmetic. Worth taking regardless: it is free and it is strictly more sensor.

**Every lens and light control is unsupported, including the ones the code
handles.** Measured on the Continuity Camera device:

      focus     continuousAutoFocus no · locked no · pointOfInterest no
      exposure  continuousAutoExposure no · locked no
      white bal locked no
      light     hasTorch no · torchAvailable no · hasFlash yes

The focus row is the surprising one. `App/FocusPolicy.swift` guards every
mutation on `isFocusModeSupported`, so on this hardware the policy is inert:
it never sets continuous AF, never locks after a good read, and its
`isAdjustingFocus` observer never fires. The trigger's `focusSettled` input is
therefore always true, and the `focusWait` valve never opens. The capability
line prints `focus=fixed`. None of this is broken — it is a whole subsystem
that costs nothing and does nothing here, and any field lesson attributing
behaviour to focus hunts on this setup needs re-reading.

`hasFlash yes` alongside `hasTorch no` is unexplained and untested; nothing
sets `AVCapturePhotoSettings.flashMode` today.

**Desk View is also 1920x1440.** Equal pixel count to the main camera's best
format, which weakens the resolution argument in `CameraDiscovery.swift` for
excluding it — though it is a dewarped crop of the ultra-wide, so equal pixels
are not equal detail. Re-measure against a real card before acting on this.

**Vision's revision is now pinned.** `ReadCard.textRecognitionRevision` sets
`VNRecognizeTextRequest.revision` on both passes. Left unset it takes
`defaultRevision`, which is whatever the OS build ships — so the goldens
described the machine that generated them rather than this code, and that is a
candidate explanation for the live-vs-replay disagreements noted above. On
macOS 15.6 the supported set is `[1, 2, 3]` and the default is already 3, so
pinning changed no golden. It is insurance, not a fix, and it pins the
algorithm generation rather than the model weights behind it.

## Upscaling the collector band does not work

Measured 2026-08-04 against all 26 fixtures, and recorded here because it is the
most obvious idea in the scanner and it is wrong.

The premise: the bottom band is the highest-value read in the pipeline — the
field lesson above puts bottom-band legibility "worth more than any further
border or symbol work" — and its glyphs are a few pixels tall. So crop the band,
enlarge it with `CILanczosScaleTransform`, and read it again on its own handler.
Rescue only: the second read was used *only* where the normal band pass had
parsed no collector block at all, so nothing that already worked could be
disturbed.

**Result at 2x and 3x: zero rescues, three corruptions.**

| fixture | golden | with upscale |
|---|---|---|
| `old-frame-black-border` (Builder's Bane) | `""` | `"1000"` — invented from an `01000` misread |
| `old-frame-same-set-variants` (Cephalid Looter) | `""` | `"72"` — invented |
| `old-frame-copyright-misread` | `"80"` | `"30"` — a correct read corrupted |

4x was worse. Narrowing the merge to replace only the parsed block, leaving
`bottomLines` alone, changed nothing — the corruption is in the rescued parse
itself, not in what it was allowed to touch.

**Why, and this is the transferable part.** The band's failure mode is not "text
too small for Vision to attempt". It is "text too degraded to read correctly".
Vision was already attempting these rows and correctly declining to commit.
Enlarging degraded pixels does not add detail; it adds *confidence*, and it
turns an abstention into a confident error. For a pipeline where an invented
collector number does not rank a card wrongly but invents one, that trade is
strictly bad. Any future "help Vision see it better" idea — sharpening,
contrast stretch, thresholding, super-resolution — should be measured against
this table first, because they all fail the same way.

**`minimumTextHeight` is a dead end too.** It is never set, and Vision's default
is 1/32 of the image height, which looks like it should be excluding the band
outright. It is not: setting it to 0.005 and 0.01 changed nothing across all 26
fixtures. The band read is not floor-limited. `ReadCard.bandTextRequest` carries
a comment saying so.

**What this leaves.** The band does not need better software on the same pixels.
It needs more pixels — and Continuity Camera has none left to give beyond the
1920x1440 above.

## The iPhone head: what the pixels bought

First real captures from the native iOS app, 2026-08-04, iPhone 16, wide lens,
focus locked at ~0.24, exposure and white balance locked, no torch, no zoom.

    still   6048x8064   48.8 MP        (Continuity's ceiling: 1920x1440, 2.8 MP)
    card    ~2400x3370  38-42% of frame
    band    ~2400x605
    read    ~540 ms for two full-resolution Vision passes

**Three cards, three bands read.**

| card | what came off the bottom |
|---|---|
| MSC (2026) | `R 0339` · `MSC • EN ALEXANDER SKRIPNIKOY` · `TM & C 2026 Wizards of the Coast` |
| MH3 (2024) | `R 0338` · `MH3 • EN OLENA RICHARDS` · `2024 Wizards of the Coast` |
| FEM (1994) | `Illus. Amy Weber` · `©1994 Wizards of the Coast, Inc. All rights reserved` |

Against the 31% footer-read rate in the field lessons above. The FEM row is the
one that matters: that copyright line is what `docs/scanner-symbol-plan.md`
calls "precisely the line a desk photo of that frame fails to read", and names
as the blocker for anchoring the symbol patch. At 48 MP it is crisp.

Note what the FEM row does *not* contain: a set code. Nothing in that capture
identifies the set, because the frame does not print one — the year and the
illustrator are the whole of the printing evidence. That is the case the symbol
plan exists for, and it is worth being strict about, because the set is very
easy to supply from outside the read and call it a result.

**The card fills 40% of the frame, so all of this used ~16% of the sensor.**
Card height is ~3370 px against the ~880 px this document records at 1080p —
close to 4x linear before any attempt to frame tighter, and framing tighter is
worth roughly 2.5x more. The expansion symbol goes from 35x23 px to about
134x88, or ~350x230 if the card fills the frame.

**A fixed fraction of the frame is not a band.** The first two captures read
*nothing* off the bottom while the rules text read perfectly, because the band
was taken as the bottom 18% of the *frame* and the card only occupies the middle
40% of it — so the crop was a photograph of the desk. Anchoring the band to a
detected card fixed it outright. This is the same lesson `collectorBand` already
encodes on the macOS side; it had to be relearned because the iOS pipeline
deliberately started from nothing.

**Two ways a shape test lies, both worth carrying into the new parser.** The
first pass at counting "did a collector number read" was wrong in both
directions on real data:

- *False negative.* The modern band prints the number and the set code on
  **separate lines** — `R 0338` then `MH3 • EN OLENA RICHARDS`. A pattern
  expecting them adjacent scored a perfect read as unreadable.
- *False positive.* `\d+/\d+` matches the **power/toughness box**, which sits in
  the same bottom strip. A 1994 card scored a collector number of `0/1`.
  Pre-Exodus cards carry none, so it never could have been one.

**Total megapixels is the wrong number. Count pixels on the card.**

Zooming drops the still from 48.8 MP to 24.5 and then 12.2, because the 48 MP
mode is 1x only and past it the wide camera falls back to a binned readout. That
looks alarming and is nearly irrelevant, because the sensor area being discarded
is desk. Measured on one card at a fixed distance:

| zoom | still | card fills | **card height** |
|---|---|---|---|
| 1.00x | 6048x8064 (48.8 MP) | 41% | 3306 px |
| 1.41x | 4288x5716 (24.5 MP) | 60% | 3462 px |
| 2.27x | 3024x4032 (12.2 MP) | 97% | 3911 px |

Card pixels go *up* with zoom, mildly. Digital zoom is pre-cropping, and the
binned mode holds up slightly better than a naive crop would. An earlier version
of this section read the megapixel column and concluded zoom was a costly
mistake; that was measuring the frame instead of the card, and it was wrong.

**Sharpness beats resolution, and it is not close.** The same session, two
captures:

| | closest possible | backed off |
|---|---|---|
| focus | **0.00** (lens at its near limit) | 0.23 |
| card height | **5608 px** | 3462 px |
| copyright read | `т* д C 2028 Wizacd of dox Coкel` | `™ & © 2026 Wizards of the Coast` |
| set row read | `MSC•EN • ALERANDER SKRIPNINON` | `MSC • EN ALEXANDER SKRIPNIKOV` |

A 62% larger card read *worse* — garbled into Cyrillic, and the year wrong by
two. `minimumFocusDistance` on the wide lens is **150 mm**, and a card closer
than that cannot be focused no matter what the lens position is set to. A focus
position pinned at 0.00 is the tell: autofocus wanted to go nearer than the
optics allow.

So the ceiling on useful card resolution is working distance, not sensor size,
and the ultra-wide's macro range was tried and rejected on image quality.

## The iOS rig's operating point

Fixed by the above, and what the read pipeline should be designed against rather
than hoped to exceed:

    lens        wide (not ultra-wide — macro range costs too much quality)
    distance    at or beyond 150 mm; nearer will not focus
    focus       locked ~0.23, and 0.00 means the card is too close
    zoom        whatever fills the frame at that distance (~1.4x)
    card        ~3460 px tall · band crop ~2470x625
    still       24.5 MP

At that operating point the collector row, the set code, the artist, the
copyright line and the expansion symbol all read cleanly on a modern frame, and
the symbol is around 150 px across.

### Thirteen cards at the operating point

A deliberate spread — white-bordered and black-bordered 1994/1995, the
1993-2003 reprint frames, and four modern cards — all at zoom 1.61x, focus
0.24-0.25, card 3320-3660 px tall.

**Every card gave up every piece of printing evidence it physically carries.**

| frame era | n | what the band gave |
|---|---|---|
| 1994-1995 | 5 | illustrator + copyright year. There is no collector number on these cards to miss |
| 1993-2003 | 4 | **the collector number, out of the copyright line** — `93/350`, `112/350`, `15/145`, `24/143` |
| modern | 4 | rarity + number, set code + language + artist, copyright |

The middle row is the one that matters. `docs/scanner-symbol-plan.md` records
that "8ED cards queue because their collector number sits in a copyright line
that a 1080p desk photo cannot resolve". It resolves, denominator included, on
every one of them.

Against a 31% footer-read rate. The comparison is not quite like for like — this
is a fixed, well-lit rig rather than a live session — but the failure mode it
replaces was *illegibility*, and illegibility is gone.

**Residual OCR noise, none of it load-bearing so far:** `™ &` reads as `IM &`,
`TN &`, `rM &`; `Illus.` as `fllus,` and `Ius.`; `Inc.` as `Inr.`. One year
came back `1093-2003` for `1993-2003`, which is the only error that touches a
field the parser uses — and the `-2003` half survived it.

### The expansion symbol is legible

Five pre-1999 cards, patches cropped from card space at the operating point
above. The symbol is **not** in the collector band — it sits at the right end of
the type line, a little over half way down the card, and a band crop that appears
to contain one is showing the holofoil rarity stamp instead.

    patch size   ~450x370 px      (Continuity, per the symbol plan: 35x23)
    card height  3200-3540 px

Five sets, five distinct glyphs, every one of them sharp:

| symbol | set | copyright row |
|---|---|---|
| portcullis | Stronghold | ©1998 |
| palm tree | Mirage | ©1996 |
| crown | Fallen Empires | ©1994 |
| interlocking gears | Urza's Saga | ©1993-1998 |
| hammer | Urza's Legacy | ©1993-1999, 36/143 |

That answers `scanner-symbol-plan.md`'s own cheapest disconfirming test — "see
whether anything can tell a 7 from an 8; if the pixels do not carry it at that
size, the feature is dead" — in the affirmative, with roughly twenty times the
area it assumed.

**Match against reference art. Never against recall.** Reading this table off
the captures, the portcullis was first written down as Urza's Saga — confidently,
with Urza's actual gears sitting in the next capture along. That is the whole
risk of this feature in miniature: a wrong set does not rank a card badly, it
invents a printing, exactly as a misread collector number does. A distance
against reference crops can return "uncertain"; recognition from memory returns
a wrong answer with no signal that it is wrong.

Note also that the copyright row cannot stand in for the symbol here.
Stronghold, Urza's Saga and Exodus are all 1998. The year narrows; the glyph
separates.

The Fallen Empires card is the case worth keeping in mind: its footer carries no
set information whatsoever, only `Illus. Amy Weber` and `1994 Wizards of the
Coast`. The crown is the only set evidence the card has.

**Two things measured while looking.** The symbol's position inside the patch
drifts by frame era — centre-right on the Urza's frame, upper-left on the old
frame — which matches the two positions the symbol plan measured, and is why the
crop window here is deliberately generous rather than fitted. And rarity colour
varies within a set exactly as that document warns: the portcullis came back
gold, the hammer black. Match shape, never colour.

**Still unproven:** the patch is computed from an axis-aligned bounding box, so
it assumes the card is square to the frame. It held over five captures on a
fixed overhead rig. Under real tilt it will drift, and the fix is perspective-
correcting the card before cropping rather than adjusting constants.

**Power/toughness versus collector number.** Both are `\d+/\d+` and both sit in
the bottom strip. Digit count does not separate them: `93/350` and `2/2` are
equally plausible shapes, and a rule requiring three digits on the left rejects
exactly the 8ED numbers above. What separates them is position — the collector
pair is printed *inside* the copyright line, after "Wizards of the Coast, Inc.",
while power/toughness stands alone on its own line. Observed across all thirteen
without exception.

Card detection here is `VNDetectDocumentSegmentationRequest` rather than the
`VNDetectRectanglesRequest` the macOS path uses. Too early to claim it is
better — but it is the right shape of tool for one printed rectangle on a desk,
and the rectangle detector's habit of returning quads that span several cards is
what the macOS merge ladder exists to survive.

## Open questions for the next session

*(The background-baseline question that stood here is answered — see "The
background baseline could swallow a card" in the field lessons.)*
