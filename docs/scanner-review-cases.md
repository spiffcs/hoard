# What still goes to review

Room to improve, captured from a live session while the evidence was in hand.
Nothing here is a regression — it is the residue left after the border reader
was rebuilt, and it is all one shape.

## The session

A 17-card pile, 5 August 2026, telemetry in `/tmp/scan-telemetry.log` around
15:18-15:19. Fourteen committed unattended, three queued. The three 4th Edition
cards that motivated the border rebuild — Control Magic, Phantasmal Terrain,
Prodigal Sorcerer — all committed as 4ED, which is what that work was for.

The three that queued:

| card | printings | year | border | rank |
|---|---|---|---|---|
| Okk | 5 | 2003 | abstained, `t=1.65 standoff=0.04` | `none` |
| Hill Giant | 25 | 2003 | abstained, `t=1.34 standoff=-0.01` | `none` |
| Balduvian Barbarians | 8 | **none** | no anchor at all | `none` |

Every one had an **exact** name match. Not one had a set code or a collector
number. All three are 8th Edition.

## They are the same failure

The contrast that explains it is three cards from the same pile, the same set,
minutes apart:

| | Canyon Wildcat, Cinder Wall, Stone Rain | Okk, Hill Giant, Balduvian |
|---|---|---|
| border | `white(footer+ring)` | abstained |
| tone `t` | 2.65 | 1.65, 1.34, — |
| standoff | 0.20 | 0.04, -0.01, — |
| rank | `year+marks` → **committed 8ED** | `none` → **queued** |

Same set, same frame, same desk, same lamp. The difference is entirely the
border, and the border is what pins an 8th Edition card: the year alone leaves
25 printings of Hill Giant standing, while year plus white picks one.

### The upstream cause is the copyright row

All three queued cards read their footer badly, and the border reader anchors
its geometry on that row:

```
Okk         'with greater power aiso Diocks.'  '4/4'  '-Peter Bollingerdy'
            '4993-2003 Wizards of the'  'aSey'  'Incr 2'
Hill Giant  '→- Dany Orizio'  '3-2003 Wizards of the'  '3/3'
Balduvian   '—Balduvian tavern song'  '*Jim Neison'  '1993-2003, Wizands'
            'of the Co'  'Inc.170'  '3/2'
```

`4993` for 1993. `3-2003` with the opening lost. And Balduvian's copyright row
split across **three** observations — `'1993-2003, Wizands'`, `'of the Co'`,
`'Inc.170'` — which is why it produced no year and no anchor.

The reader takes the card's scale from that row's own box. A row read in
fragments gives a fragment's box, the reconstruction drifts, the ring lands off
the border, and the standoff collapses. So the border numbers are a *symptom*;
the defect is one row of small italic serif type on a glossy 2003 card.

## Where the work is

Ranked by what it would have recovered here.

### 1. Reassemble a copyright row split across observations

Balduvian's row is three observations on one baseline. They are adjacent, they
share a line, and joined they read `1993-2003, Wizands of the Co Inc.170` —
which yields a year, an anchor, and a collector number. Nothing needs
re-recognizing; the pieces are already in hand and are being looked at one at a
time.

Would have fixed: Balduvian Barbarians outright, and given the other two a
better anchor box.

### 2. A collector number fused to the copyright tail

`'Inc.170'` is Balduvian Barbarians' collector number in 8th Edition, glued to
the word before it. `trailingNumber` already reads a bare number at the tail of
a copyright row; it wants the case where the space did not survive OCR. Guard
it: only split where a run of digits ends the line and what precedes it is
recognisable furniture, or `Inc.170` becomes a licence to invent numbers out of
any trailing digits — **an invented printing is the worst thing this pipeline
can produce**.

### 3. Do not just lower the standoff floor

The obvious move is to drop `minInnerDelta` (0.05) so `Okk`'s 0.04 passes. Do
not, on this evidence. The constant was fitted over 52 cards where white
measured +0.168 to +0.698 and black −0.068 to −0.616; 0.04 is not a weak white,
it is *nowhere*, and Hill Giant's −0.01 has the sign of a black border on a
white card. Both are the geometry saying it does not know. Fix the anchor and
these numbers move on their own — and if they do not, the reader is right to
abstain.

The one measurement worth taking first: re-run these three with the copyright
row reassembled and see where the standoff lands. If it jumps to the 0.17+ the
committed cards show, the floor was never the problem.

## The other direction

`Confiscate` committed **twice**, 0.75 seconds apart — one physical card, two
rows. The second capture fired with `fireReason: replaced` and `boxes=2`: a
card was laid on top, the trigger correctly saw a placement, and the OCR read
the card still visible underneath rather than the new one.

Recorded here rather than fixed because it is the deliberate side of a trade
already made — false positives preferred over false negatives, one row to
correct against a card silently lost. It is worth knowing that stacking without
clearing the frame is how you buy a duplicate.

## Not the problem

Worth saying, so a future session does not re-derive it:

- **Not the name.** All three matched exactly. The name gate never fired.
- **Not the border reader's judgement.** It abstained on all three and was
  right to; every gate it refused on had a number that genuinely said "I cannot
  tell". Abstaining queued a card. Guessing would have committed a wrong
  printing.
- **Not resolution.** These read at the same 4032x3024 as their shelf-mates
  that committed.

# The 6 August 2026 retro-foil pile

A second session, a year of work later, and the residue has moved. Telemetry in
`scan/foil-corpus/session4-telemetry.log`; the same pile a day earlier is
`session3-telemetry.log`, and comparing the two is what this section is for.

Fourteen distinct cards, every one a retro-frame foil. Ten committed unattended,
four queued, and **card identification was 10/10 correct on everything that
committed**. So the name gate and the border work above are done arguing; what
is left is the collector number.

The four that queued, and all four read `finish=foil(sparkle)` correctly — none
of this is a foil problem:

| card | copyright row, as read | number | printings |
|---|---|---|---|
| Dress Down | `bards of the Coast 14` | `14` | 5 |
| Unstable Amulet | `IN&O2024 Wizaids of the Coast 431-` | `431` | 3 |
| Unholy Heat | `2024 Wizards of the Coast` | none | 6 |
| Charitable Levy | `TM & 0 200` | none | 2 |

## The read is a coin flip per photograph

This is the finding, and it is the one that says what to build. Against session
3, on the same physical pile:

| card | session 3 | session 4 |
|---|---|---|
| Charitable Levy | **390, committed** | none, queued |
| Unholy Heat | **13, committed** | none, queued |
| Victimize | none, queued | **413, committed** |
| Consuming Corruption | none, queued | **407, committed** |
| Lion Umbra | 420, matched nothing | **426, committed** |
| Meltdown | 18, matched nothing | **418, committed** |

Every card that queued in one session read its number correctly in the other.
Nothing about these cards is unreadable; the four small glyphs at the bottom
edge either survive a given photograph or they do not. That is why the fix is
another photograph — `wantsSecondLook` in `internal/tui/autoscan.go`, one retry
when a card queues for an unverified printing, bounded to one so a card that
will never read cannot hold the session open.

**The retry goes out at queue time, into `held`.** This paragraph used to
claim the opposite — that a `Rearm` sent into `held` "silently never happens"
and the retry must wait for the next `armed` — and the code was built to
match. The phone's actual contract is the inverse: `Trigger.forceRearm()`
opens with `guard phase == .hold`, so a `Rearm` acts *exactly* while the
trigger is parked on the card it just shot (reported as `held`) and is a
guaranteed no-op sent into `armed` (the machine already left `.hold` on its
own; its next fire is coming, or the scene gate is holding it). Under the
inverted version every retry was a no-op, and every rescue the session logs
showed was the phone's own accidental re-fire.

The bimodal `held → armed` gap that motivated the waiting design — four
captures at ~130ms, fourteen at 760-855ms over one session — is the phone's
own disruption accumulation and decay. The queue-time send into `held` skips
it entirely. The 5500ms `nudgeDelay` timer stays armed underneath as the
backstop for a scene that never changes and a helper that never reports its
state.

None of this can make a session feel slower. A pending retry is not a gate on
anything: if the operator places the next card, the trigger fires on that
placement and the real capture voids the armed timer (`m.nudgeGen++` in
`onSessionEvent`). The retry only ever occupies time the operator was not using.

## The phone is not told about a review it might not be getting

`reviewFlash` is the one thing a queue sends over the wire — a `tierReview`
HUD result, or a chime on a helper without a HUD. It is a **stop**: the operator
looks up. Sending it on a card the retry is about to rescue showed a stop that
un-happened a second later, which was worse than the queue it was announcing.

So the flash is held while a second look is out (`deferredFlashFor`), and
resolved exactly three ways. The queue entry itself is *not* held — it goes into
`m.review` immediately, so a retry that never answers cannot lose the card:

| what happens | the phone |
| --- | --- |
| the retry reads the card and it commits | never hears anything — `clearDeferredFlash` |
| the retry reads no better and it queues again | flashes then |
| no retry capture arrives at all | flashes when the quiet period lapses, in `onNudge` |

The third row is what makes the first two safe. Reaching `onNudge` means the
quiet period elapsed with no capture in it — any real one bumps `nudgeGen` and
voids that timer — so a retry that was going to answer has not, and the card is
owed the flash it did not get. Every other exit from a held flash is an event;
that one is a timeout, and without it a queued card could sit in the list having
never made a sound.

The hold is scoped to an unverified printing. A card queued for a shaky *name*
flashes at once: a retry cannot improve on it, so delaying would be latency
bought for nothing.

## What the misreads actually are

Worth writing down precisely, because they look alike in the log and are not
alike at all. Checked against the catalog:

| card | read | truth | what happened |
|---|---|---|---|
| Meltdown | `18` | mh3/**418** | a digit **lost** |
| Dress Down | `14` | h2r/**4** | a digit **gained** |
| Unstable Amulet | `431` | mh3/**421** | 3 for 2 |
| Lion Umbra | `420` | mh3/**426** | 0 for 6 |

Only the first is repairable from the digits alone, and `numberTailMatches`
repairs it: 418 is the only one of Meltdown's four printings ending in 18.

**The generalisation is a trap and this is the measurement that says so.** Three
of the four are substitutions or insertions, all one edit from the truth, and a
match-within-one-edit rule would commit every one of them — Lion Umbra has two
printings and `420` sits one edit from `426` and nothing else, so it would look
like a clean win and be one only by luck. A tail is the sole repair where every
digit that was read is still true. See
`TestNumberTailMatchLeavesSubstitutionsAlone`.

## Still open: the finish on genuine 2003 foils

Out of scope for the above and not fixed by it. Glowrider and Trap Digger both
committed the right card with the **wrong finish** — the sparkle patch had no
structure left to correlate against (contrast 0.0039 and 0.0086, against
0.027–0.093 on every card that did read foil). Glowrider's fell below
`SparkleGate.minContrast`, so the reader returned its abstention and `verdict`
wrote `nonfoil` from it anyway.

Both are the only genuine 2003 Legions/Scourge printings in the pile; every card
that read cleanly is a 2024 retro reprint. Session 4 is also the reshoot
`docs/scanner-foil-registration.md` asks for at its end, and the answer is
**half**: two of session 3's four misses recovered, these two did not, and
Glowrider got worse (0.0241 → 0.0039). Whatever is happening to those two is not
the lamp angle.
