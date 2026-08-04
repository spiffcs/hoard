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
| `HOARD_SCAN_FOCUS` | `lock` | focus policy: `lock` = continuous AF, frozen after the first good read (all cards sit at one distance; two consecutive empty reads thaw it); `continuous` = AF plus the hunt-aware fire gate but no freeze; `off` = no focus code at all, the pre-focus behavior |
| `HOARD_SCAN_FOCUS_WAIT` | 1.5 | seconds a completed stability streak waits out a focus hunt before firing anyway |

Focus hunts are first-class trigger input: a hunt blurs every edge in frame,
so the trigger freezes (no streak growth, no grace burn, no reset, no HOLD
disruption) rather than mistaking blur for motion, and defers a ready fire
until the lens settles — mid-hunt captures were the out-of-focus scans, and
hunt-driven rectangle flicker was most of the settle-time tail (71 flicker
resets in a 15-card session). `focus hunt began/ended` lines appear in the
`HOARD_SCAN_AUTO=1` trace; the capability line at session start reports what
the device granted (`focus=af+lock`, `af`, or `fixed`).

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

**Old frames hide their collector number in the copyright line.** Pre-M15
frames print it at the tail of the bottom-centre copyright line ("™ & ©
1993-2003 Wizards of the Coast, Inc. 95/350") in italic serif the band crop
returns as fragments — the full-resolution frame pass reads it far better,
so both feed the extraction. The same line's range *end year equals the
printing's release year*, which is what breaks a number tie: "95" is Remove
Soul in both 7th and 8th Edition, and only one was printed in 2003.
(`parseCopyrightCollector` in main.swift; the year filter in
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
choose a reading the helper dropped. (`mergeInto` in main.swift.)

**Prose fabricates set codes.** The set/language regex tolerates a bare space
between code and language, so once `asciify` uppercases everything, ordinary
rules text matches it: "…and put it into your hand" yields set `PUT` with
language Italian, "…and it ain't you!" yields `AND`. Four captures in one
session shipped a fabricated set code, and a checked-in fixture had been
pinning one for months. Gate *extraction* on the line reading like border
print — the set line is set in caps and carries almost no lowercase — but
leave `boilerplate`'s use of the same regex generous, because there a loose
match only kills a line. (`setLangFurniture` in main.swift.)

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
sits directly behind the primary. (`parseSelfReference` in main.swift.)

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

**1080p is the camera's ceiling.** Continuity Camera reports `still=1920x1080`
even after the format opt-in, so a card fills roughly 680 pixels and the
collector band is a few pixels tall. Everything above is tuned for that; do not
plan around more pixels without checking the capability line first.

## Open questions for the next session

**Does the background baseline poison long sessions?** The trigger's
furniture baseline is taken from the first sample after arming and is never
re-learned — not by `forceRearm`, not by `captureFinished`. Anything in frame
at that instant is furniture for the rest of the session. One session showed
a 37s stall and ~50s of bare `searching → stabilizing → searching` flapping
with no `stable`, `flicker tolerated`, or `scene moved` lines between the
transitions, which is the signature of `novel` being empty on every sample —
either the baseline swallowed the scanning pile, or the detector genuinely
returned nothing. Only `HOARD_SCAN_AUTO_TRACE=1` separates the two: `rects=N
novel=0` is absorption, `rects=0` is dropout. That session did not have the
flag set, so this stays a question rather than a diagnosis — resist "fixing"
it until a trace says which it is, since the last self-tuning baseline idea
is the one that killed auto capture at the exact spot every card lands.
