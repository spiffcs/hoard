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
