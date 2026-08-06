# Sprint: iPhone capture head — a third scan source

Status document for the sprint started 2026-08-04. Written so a fresh session —
human or AI — can resume with zero prior context. Update the status markers as
stages land.

## Why this sprint

hoard is a three-mode product and only the third mode is missing:

| mode | what the user needs | status |
|---|---|---|
| **TUI, no phone** | nothing — manual entry, deck/collection import | ships |
| **TUI + local capture** | the `hoard-scan` helper beside the binary; Continuity Camera | ships |
| **TUI + iPhone app** | the companion app; App Store eventually | this sprint |

Continuity Camera was the binding constraint on nearly every open scanner
problem. Its real ceiling is 1920x1440 — see the capability ledger in
[scanner-tuning.md](scanner-tuning.md) — and it exposes no zoom, no exposure
control, no white balance lock, no torch, and on the measured device no focus
modes at all, which makes `App/FocusPolicy.swift` inert there.

**Continuity is not being replaced.** It stays a first-class backend so the
scanner keeps working with no companion app. The iPhone head is a third source
alongside it, and macOS `ScanKit` is frozen in behaviour rather than deprecated.

## What was measured

Full numbers live in [scanner-tuning.md](scanner-tuning.md) under "The iPhone
head: what the pixels bought". The short version:

- **48.8 MP** captures (6048x8064) against Continuity's 2.8 MP.
- **13 cards at one operating point gave up every piece of printing evidence
  they physically carry**, including the collector number printed *inside* the
  copyright row on 1993-2003 frames — the case
  [scanner-symbol-plan.md](scanner-symbol-plan.md) records as unresolvable from
  a desk photo.
- **Expansion symbols crop at ~450x370 px** against the 35x23 that document
  measured. Five sets separable by eye: Stronghold, Mirage, Fallen Empires,
  Urza's Saga, Urza's Legacy.
- The rig's ceiling is **working distance, not sensor size** — `minimumFocusDistance`
  is 150 mm on the wide lens, and a card closer than that reads *worse* despite
  being larger.

## Stages

| stage | what | status |
|---|---|---|
| A | capability probe (`hoard-scan --probe`) | ✅ done |
| B | package + Xcode plumbing; ScanKit builds and tests for iOS | ✅ done |
| C | capture — prove the pixels | ✅ done |
| D | `CardKit`, the rewritten read pipeline | 🚧 in progress (border reader and symbol matching outstanding) |
| E | transport, Mac remote backend, source picker | ✅ done |
| F | the trigger, on the phone | ✅ done |
| G | parity (sounds, HUD, cue) then measure | ✅ done — remote row in `docs/scanner-tuning.md`; both timing constants retuned per source |

Three free wins shipped alongside stage A and apply to the **Continuity** path:
raising `activeFormat` after `startRunning` (1920x1080 → 1920x1440), pinning
`VNRecognizeTextRequest.revision`, and a measured rejection of collector-band
upscaling.

## The read pipeline, scored

`CardKit` is a rewrite rather than a port — parser included. The decision was
taken deliberately against the recommendation to reuse the ~870 lines of
camera-independent printing knowledge, for freedom from choices made under
degraded input. The agreed mitigation is that it is **scored against ground
truth**, not merely diffed:

    make cardkit && HELPER=./bin/cardkit-probe ./scan/corpus/sweep.sh

`scan/corpus`'s 231 images carry their real name, set and number in
`manifest.tsv`, and `sweep.sh` already parameterises `HELPER`, so both pipelines
report in the same table.

| | name | number |
|---|---|---|
| old (`ScanKit`) | 75% | 76% |
| new (`CardKit`) | **85%** | 74% |

## Known gaps

**Consult this when picking up backlog work.** Percentages are the corpus
strata; `n` is 8 per stratum except pre-1998 which is 40.

The full accuracy picture — every stratum, every failure classified by root
cause, and which numbers are the scorer's fault rather than the scanner's —
lives in [scanner-accuracy.md](scanner-accuracy.md). Read it before treating any
figure below as a defect: a fifth of all name misses are the scanner reading a
foreign-language card *correctly* and being scored against its English name.

| stratum | measure | new | old | note |
|---|---|---|---|---|
| gold border, all post-1998 | number | 0% | 0% | **parity, not a regression.** World Championship cards; neither pipeline reads them. Their footer carries a sideboard marker (`SB`/`GB`) where a number would be |
| `2015+` yellow | number | 50% | 100% | the band crop does not reach the collector row on some frames — a geometry problem, not a parser one. Band lines come back as rules text and power/toughness with nothing below |
| `2015+` silver | number | 25% | 38% | Un-set layouts. `012/216` with the rarity on the *following* line is now handled; what remains is stranger |
| `2003-2014` white, silver | number | 50% | 50% | parity |
| `1998-2002` white | number | 75% | 88% | regression, uninvestigated |
| `2015+` white | number | 88% | 100% | regression, uninvestigated |
| `2015+` borderless | name | 43% | 14% | improved threefold and still the worst stratum. Borderless art has no edge for a detector to find |
| `pre1998` black | name | 57% | 50% | old serif titles, the long-standing weak spot |
| three cards lost to the head crop | name | — | — | **accepted, deliberately.** The title pass reads the top 30% of the card, which halved the read (185ms → 91ms). It costs `B.F.M. (Big Furry Monster)`, a borderless `Snakeskin Veil` and a World Championship `Swamp` their name — 85% → 82% overall. All three return *empty* rather than wrong, and all three sit in strata already at 43% or 0%. Widening the crop does not help (0.30 and 0.60 score identically) and it is not a resolution artifact (upscaled 4× they still read empty), so the cause is something other than the window. Revisit with the symbol matcher |
| suffixed ground truth (`130p`, `228★`) | number | — | — | **a harness limitation, not a parser one.** `score.py` compares exact strings, so a correct `130` is scored a miss against `130p`. Affects both pipelines equally; fix the scorer, not the parser |

### The link is authenticated but not encrypted

TLS-PSK was built first and did not work. With
`sec_protocol_options_add_pre_shared_key`, a TLS 1.3 ciphersuite
(`AES_128_GCM_SHA256` — 1.3 folded PSK into the normal handshake, so the legacy
`TLS_PSK_WITH_*` names are gone) and a permissive verify block, **both ends sat
in `.connecting` forever** — no error, no timeout, nothing in the state handler.
Plain TCP over the identical code paths pairs in under a second, which is what
isolated it to TLS rather than to Bonjour, the framing or the hello.

What ships instead: plain TCP with an **authenticated hello** — HMAC-SHA256 of a
fresh per-attempt session id under a key derived from the pairing code, verified
in constant time. A peer that cannot prove it knows the code is dropped before
it reaches a session.

That is deliberate about what it does and does not buy. The risk being managed
is a stranger on the network *writing to the collection*: the scanner
auto-commits, so an open listener is an injection path. Card scans are not
secret, so confidentiality buys little; **authentication is the property that
matters and this provides it.** Encryption remains the right end state, and
restoring it is a known gap rather than a decision that it is unnecessary.

### Pumping a run loop does not drain the queue you are standing on

Pairing verification timed out every time while the phone plainly showed
"connected to hoard". The cause is worth keeping: the helper's `start()` runs
inside a `DispatchQueue.main.async` block, and the check recorded the phone's
`ready` from *another* `DispatchQueue.main.async`. The main queue is serial, so
that second block cannot run until the first returns — and the first is sitting
in `spinRunLoop` waiting for it. Pumping the run loop does not help; the run
loop is not what is blocked.

The fix is to set a lock-guarded flag from the link's own queue and poll it.
`ArmedGate` exists for the same reason on the capture path, which is the tell:
any time a background callback has to tell a run-loop pump something, the hop to
the main queue is the bug.

### The iOS shutter sound has no public off switch — so nothing takes a photo

`AVCapturePhotoOutput` plays a system sound at capture and no photo setting
disables it. That matters more than it sounds: the scanner's rule is exactly one
sound per card, played when the scan *resolves*, and the ledger records that a
shutter pop on top "made every card a two-beep event". The audio exists so a
person can work by ear without looking at anything, and a second sound per card
undoes that.

The first answer was `AudioServicesDisposeSystemSoundID(1108)` before each
capture — undocumented, long-standing, and re-applied per capture because the
system recreates the sound on demand. It was shipped behind a Photo/Video
selector so the two paths could be measured against each other.

**Both are now gone.** The video tap is the only capture path and there is no
selector, because the measurement came back one-sided:

| | photo path | video tap |
|---|---|---|
| still | 4032x3024 | 4032x3024 — *the same* |
| shutter | larger | 149 ms |
| captures reading nothing | — | 0 of 61 |
| shutter sound | undocumented workaround | none to suppress |

The "24 MP path" the photo route was being preserved for never materialised as
an advantage: the format chosen for its max photo dimensions delivers those same
dimensions on its video tap, so the still is identical and the workaround bought
nothing. Removing it also removes both of its standing caveats — that the hack
could break in any iOS release with the pop returning as the only symptom, and
that jurisdictions requiring a capture sound would have forced it out before App
Store submission anyway.

`AVCapturePhotoOutput`, its delegate, the sound-disposal call and the mode
picker are all deleted. The format chooser still ranks on
`supportedMaxPhotoDimensions`, which is deliberate rather than left over: it is
a good proxy for "the sensor's full-readout format", and ranking on video
dimensions instead would change the choice on some devices with no way to know
whether that is better except a live session.

### The border reader: white vs black, fitted on real captures

Answers one question — is this border white or black — and abstains otherwise.
That is the question the printing ranker needs: a 1995 copyright line narrows
Prodigal Sorcerer's 23 printings to `4ed/94` and `4bb/94`, which share a set, a
number and a year and differ only by border.

**Scored on real photographs from two different cameras:**

| set | answered | correct | wrong | abstained |
|---|---|---|---|---|
| 16 iPhone stills (the fitting set) | 13 | **13** | **0** | 3 |
| `scan/fixtures`, Continuity photos (held out) | 10 | **10** | **0** | 4 |

**The signal.** `tone` is where the card's outer ring sits in its own
ink-to-paper range, and `standoff` is that ring minus the card's frame just
inside it. Both are local comparisons within one card, so a warm lamp, a
vignette or a shadow moves both terms together and cancels. The measured split
had no overlap — black borders reached 0.21, white ones started at 0.25 — so the
gates sit at 0.22 and 0.30 with the gap between them abstaining. Requiring the
standoff's sign to agree is what makes a 0.04-wide margin safe: where the two
disagree the reader declines rather than guessing.

**Three approaches were measured and rejected first**, all of them for reasons
no amount of reasoning would have surfaced:

- **Chroma, to separate gold and silver.** White card stock under a warm lamp is
  genuinely yellow in RGB, so an absolute chroma gate called three
  white-bordered cards gold. Gold and silver are no longer attempted at all.
- **Footer polarity** — taking the majority tone of the bottom sixth, since the
  copyright line is printed on the border. The border is only the outer tenth of
  that strip; the rest is rules box and the card's coloured frame, so the
  majority measures the frame. It called cream-bordered cards black.
- **Widening the crop** so a trimmed quad still contained the border. Measured
  strictly worse: a card that already fills its frame has nothing outside it,
  and the sample lands on the padding a perspective correction leaves behind.

**A coordinate flip invalidated a whole session before any of this.** The
sampler flipped rows on the reasoning that `CGContext` draws bottom-up, so every
reading described the opposite edge: black borders read light and white read
dark. It produced tidy plausible numbers throughout. `BorderGridTests` now pins
the orientation against a synthetic half-white card, and the code comment that
asserted the flip was necessary is gone.

**The corpus scores 49% and should be ignored for this.** Those are scans in
which the card fills the frame, so segmentation has no background to lock onto
and trims the border away on about half of them — where the crop kept the border
it is 30/33. `scan/corpus/border.sh` says this in its own header: fit on the
corpus, confirm on the photographs. The photographs are what these gates were
fitted on.

### Year + border commits the old-reprint stratum

The border read is now an input to the *ranking*, not just to the ordering. When
the copyright year narrows a card to several printings and the printed border
picks exactly one of those, the card commits unattended at a new rank,
`year+border`.

Measured against the cards that queued in the live session, using the real
catalog:

| card | printings | before | after |
|---|---|---|---|
| Prodigal Sorcerer | 23 | queued | `4ed/94` |
| Control Magic | 20 | queued | `4ed/64` |
| Phantasmal Terrain | 15 | queued | `4ed/89` |
| Energy Tap | 4 | queued | `4ed/69` |
| Gaea's Herald | 6 | queued | `8ed/252` |
| Mana Leak | 21 | queued | queued — reader abstained |
| Seasinger | 2 | `year-only` | unchanged |

**Why one bit is enough here and nowhere else.** An earlier note in this
codebase argued the rank had to stay border-blind, because "one bit that always
matches something cannot be sole evidence for an unattended write". That is
correct about a border used alone, and it still holds: with no year, or with a
year that narrows nothing, the border settles nothing and the code never
consults it. What makes it decisive in this stratum is that the year has already
isolated the candidates, and the survivors are printings like `4ed/94` and
`4bb/94` — same set, same collector number, same release date — for which the
border is not one bit against a catalog but *the only difference that exists*.

**It fails closed in three directions**, each tested:

- a border matching every surviving printing added no information;
- a border contradicting every one of them disagrees with the whole catalog,
  which is cause to distrust the read rather than to pick from nothing;
- a printing in a colour the reader cannot read — gold, silver, borderless — is
  never ruled out, whatever was read. Mana Leak's 1998 line narrows to `sth/36`
  black and `wc98/rb36` gold, so a "black" read leaves both standing and the
  card queues. The reader was never asked about gold.

`borderRulesOut` is the single definition of that exclusion, shared by the
ordering and the ranking, because a printing promoted to the top of a review
list and a printing chosen for an unattended commit must be the same printing.

### First hands-free session on the phone

33 auto captures, video-silent mode, one operating point.

| | iPhone head | macOS ledger |
|---|---|---|
| shutter | 134 ms | — |
| read | 338 ms | — |
| **shutter to sound** | **491 ms** | ~700 ms budget |
| **card-to-card cadence** | **4.45 s** | 5.1 s |
| **captures per commit** | **1.83** | 1.1 |

**Faster than the Mac, and less accurate.** The ledger's own advice applies —
read the captures-per-commit column, not the settle column — and by that
measure this is behind: 33 captures produced 18 commits, 9 review queues, 5
phantom kills and 1 nudge-echo drop.

The queue reason is the same one every time: **"printing unverified"**. Names
read on **33 of 33** captures; collector numbers on only **21 of 33**, set codes
on 20. Nothing is failing to find the card — the footer is failing to read.

And the band lines say why. On the failures they contain flavour text and rules
text rather than the collector row:

    Boomerang        ["Eamund Spenser, The Faerie Queene", "--Alan Rabinowitz", "n0322"]
    Winter Moon      ["make a compenition of our misery.", "Navomin, veteran explorer", "R DA62"]

`n0322` and `R DA62` are the collector row, mangled — and they are the *last*
line in a list sorted bottom-most first, meaning they sit at the **top** of the
crop. The band is landing high: it is catching the flavour box and missing the
footer. Same class of error as "a fixed fraction of the frame is not a band"
above, one level down — the fraction is now of the located card rather than the
frame, and the located card is evidently taller than the card.

**The tap is not preview-sized.** `tap=4032x3024`, not the 1080p
`TriggerRunner` claimed to pin. A video data output delivers the active
format's video dimensions and nothing constrained them. Left alone for now: it
costs nothing measurable, and the silent capture path lifts its still straight
off this tap, so shrinking it would take the still down too.

### Four collector-number layouts, all needed

Found by measurement; missing any one of them costs a stratum.

    R 0338                                    rarity first          (M15 era)
    130/287 M                                 pair, rarity last     (newer)
    012/216   ...with the rarity on the next line
    TM & © 1993-2003 Wizards ... Inc. 15/145  inside the copyright row

Power/toughness is the same `\d+/\d+` shape and sits in the same strip. What
separates them is **a rarity letter or a denominator ≥100** — never digit count,
which was the first attempt and rejected exactly the 8ED numbers that mattered
most.

### The guard that measured the wrong denominator

The first session's review queue was mostly "printing unverified", and the band
lines showed why: the collector row *had* been read, and the parser threw it
away. `n0322` and `R DA62` both arrived intact and were refused because a single
character outside the lookalike map aborted the whole token.

Loosening that to drop stray characters worked — and immediately invented a
printing on a card that prints none. `a7KA2` shed three letters, and its two
survivors read as a clean `72`.

The guard was comparing real digits against the **output** length, so *dropping
more junk made a token look more numeric*. It now measures against the input,
and caps dropped characters at one. Both properties are needed: the cap alone
would still admit `x1y2`, and the ratio alone still admitted `a7KA2`.

Repair may fix a number. It may not assemble one out of debris.

### The trap that invented printings

Digit-lookalike folding turned the World Championship sideboard markers `SB` and
`GB` into collector numbers `58` and `68`, giving a printing to 18% of a
stratum of cards that print none. **Folding may repair a mostly-numeric token;
it must never manufacture one.** `Text.swift` carried a comment warning about
this before the code did it anyway. There is a test.

### Match against reference art, never recall

Reading the symbol table off the captures, the portcullis was first written down
as Urza's Saga — confidently, with Urza's actual gears in the next capture along.
A wrong set does not rank a card badly, it invents a printing. A distance against
reference crops can return "uncertain"; recognition from memory returns a wrong
answer with no signal that it is wrong. The copyright year cannot stand in
either: Stronghold, Urza's Saga and Exodus are all 1998.

## Getting data off the phone

Two things that do not work, both measured:

- **`devicectl process launch --console` forwards nothing.** Documented to
  stream stdout; delivers zero bytes with the app running and the console
  attached.
- **iPhone Mirroring blocks camera access entirely.** The app reports the camera
  unavailable. Unrelated to streaming our own preview frames over our own link,
  which is what stage E does.

What works is the app's Documents container:

    xcrun devicectl device copy from --device <udid> \
      --domain-type appDataContainer \
      --domain-identifier dev.spiffcs.hoard.scan.ios \
      --source Documents --destination .

The app writes `capture-log.txt` plus per-capture frame, band and symbol JPEGs
there, and a Share button in the toolbar is the fallback.

## Signing, once

Four account-side gates, none of them in the repo, every one reporting as the
same misleading `No profiles for 'dev.spiffcs.hoard.scan.ios' were found`:
sign Xcode into the account; accept Apple's revised Program License Agreement;
enable Developer Mode on the phone (the menu item does not appear in Settings
until an install has been attempted); and register the device UDID with the team
by hand, because `-allowProvisioningUpdates` did not. `build-scan-ios.sh`
translates all four.

## Carry forward as a checklist, not as code

The tuning ledger's nine sessions mix camera artifacts with card facts. The
artifacts die with the rewrite; these do not, and the new pipeline must be
tested against them:

- borderless cards defeat rectangle detection
- foil glare under overhead light whites out the title
- old serif titles read as their own rules text
- bare personal names ghost
- a card lifted away must not fire the trigger
