# Scanning cards with an iPhone

> macOS only, and it needs the `hoard-scan.app` helper built by `make all` (or
> `make scan` on its own). Working on the scanner itself? Start with
> [scanner-tuning.md](scanner-tuning.md) — the tuning loop and the field
> lessons the current behavior was built from.

Inside an add session (`hoard add`, or <kbd>a</kbd> in the browser), press
<kbd>ctrl+o</kbd> to identify a card with your iPhone instead of typing its name.

## How it works

Scanning uses **Continuity Camera only**, meaning your iPhone and never the Mac's
built-in webcam. A fixed, user-facing camera can't be aimed at a card on the
desk, so rather than fall back to one and produce unreadable captures, hoard
tells you no iPhone is connected. If you have more than one iPhone paired you're
asked which to use; the choice is remembered for the session so bulk scanning
doesn't ask again, and <kbd>ctrl+r</kbd> at the prompt re-runs detection or
switches phones.

A window opens with the live feed **and stays open** — and with a current
helper it watches the feed itself: set a card down, hold it still for about a
second, and the shutter fires on its own. An outline traces the card the
trigger is watching — yellow while it settles, green once it's shot — and the
trigger won't re-fire until the scene changes, that is, until you swap the
card. Whatever rectangles are already in frame when auto arms (a notepad, a
mousepad, a coaster — a desk is full of rectangles) are treated as furniture:
they get no outline and can never fire. Two consequences worth knowing:
**arm the camera with the staging area clear** — a card already sitting there
when auto turns on reads as furniture until you remove and re-place it (and
the scanning pile must grow from the first scanned card, not pre-exist) — and
if you rearrange the desk mid-session, toggle auto off and on (<kbd>a</kbd>
twice in the camera window) to re-learn what's furniture. <kbd>space</kbd> still captures manually at any time,
<kbd>a</kbd> in the camera window toggles the auto trigger, and cards on a
surface too close to their own color may need the spacebar, since the trigger
never sees an outline there. Working through a box of cards is: set a card
down, wait for the flash, swap in the next — **or stack the next card straight
on top of the last one**. Stacking is a supported rhythm, not a mistake: the
hand's moment over the pile is what re-arms the trigger, the new top card
fires even though it sits exactly where the last one did, and the sliver of
the card beneath showing at the bottom edge is handled by parsing every border
block in frame and keeping the one that matches a real printing of the
recognized card.

A placement the trigger's geometry misses entirely is caught by the
**recheck**: a couple of seconds after each processed scan, hoard quietly
re-arms the trigger once. If the recheck finds a new card, it commits like
any scan; if it finds the card just processed, the re-read is discarded
silently ("still seeing … — waiting for the next card") and no further
rechecks fire until something actually happens.

Audio is one sound per card, played when the scan *resolves* — auto-added
or queued for review — because either outcome means the same thing at the
table: this card is handled, place the next one. Captures themselves are
silent (the old shutter pop made every card a two-beep event), and a
discarded recheck stays silent too, so a slow moment between cards never
sounds like the scanner acting up.

On current helpers that one sound is priced: the camera window flashes the
amount just scanned and plays the price's tier — a muted grey flash and low
knock for bulk (under $1), a gold flash and bright bell for a win, and a
coin shower with a rising glissando for a jackpot ($20 and up). The
thresholds are tunable per run with `HOARD_SCAN_WIN` and
`HOARD_SCAN_JACKPOT` (dollars), and the sound volume with
`HOARD_SCAN_HUD_VOLUME` (0–1). They can also be moved mid-session: press
**:** on the capture step to open the scanner command line and type
`win 5` or `jackpot 30` — a live tweak lasts for the session, the env vars
are what persist. The built-in sounds are synthesized (no
third-party audio), but each can be replaced with your own file —
`HOARD_SCAN_SOUND_BULK`, `HOARD_SCAN_SOUND_WIN`, `HOARD_SCAN_SOUND_JACKPOT`,
and `HOARD_SCAN_SOUND_REVIEW` each take a path to anything macOS can play
(wav, aiff, mp3, m4a) — for sessions that get filmed or published with
audio you hold a license to distribute. An unreadable path reports an
error banner and falls back to the built-in. A running
session total sits at the video frame's top right and counts *committed*
cards only — a card that queues for review flashes "Needs Review" with a
soft rising two-note sound, the inflection of a question, instead of a
price (its printing is still unverified). Confirming it in review answers
on the camera window: the amount that landed flashes with its tier's
sound, and the total moves. An unpriced card flashes "$—" with the
familiar plain chime. Older helpers without the HUD keep the plain chime
for everything. The
looks and sounds can be previewed without a camera via
`hoard-scan --hud-demo` (pipe `result {...}` lines on stdin).

The card's title is read on-device with Apple's Vision OCR, then matched to a
real card — against the [local catalog](pricing.md#the-local-catalog) when one is
built, falling back to Scryfall's fuzzy name search.

The bottom border is read too, in a second pass. Magic cards have carried a
collector number since Exodus (1998), printed bottom centre on older frames and
bottom left from the M15 frame (2014) onward, where it sits alongside the set
code. When that read succeeds, the matching printing is moved to the top of the
printing list and marked `← scanned`, with the cursor already on it. This matters
for reprinted cards: Sol Ring has over a hundred printings, and the number in
your hand says which one it is.

The card is located in the frame first, and the border is then read relative to
the card's own bottom edge, so the card does not need to fill the shot or sit at
any particular height — anywhere in frame, roughly upright, is enough.

## Confident scans add themselves; the rest queue

A scanned card writes itself to the collection — quantity 1, no keys
pressed — only when the evidence adds up. The printing must be pinned: the
collector read matched a real printing of the resolved card (or it is the
only printing that exists). A full set-and-number verification carries the
rest by itself — it is self-consistent, since a name misresolved to the wrong
card could not have its number match that card's printings — so a
glare-truncated title or a low-confidence read doesn't queue a card whose
border already identified it. Short of that, the name has to stand on its
own: an exact (or near-exact) match on the helper's own title-line guess,
with the helper's OCR confidence clearing a floor when the match needed
fuzzing. The headline rule: **a name-only match with several printings never
commits itself** — hoard would be guessing the set, and the newest printing
is usually the wrong guess.

Every unattended write is visible twice: a live `✓ Auto-added:` tally in the
terminal while you keep scanning, and a session summary printed to the
scrollback when the add session ends. Foil is read off the card itself where
the frame prints it: modern collector lines star the set/language separator on
foil printings (`MSC ★ EN`) and bullet it on nonfoil ones (`MAR • EN`), and a
starred card whose printing offers both finishes is recorded foil — in the
review cascade the finish picker opens with the marker's answer pre-selected.
Frames without the marker (roughly pre-2020) default to nonfoil, with the
tally as the audit trail. A recently added card seen again is judged by
*how* it came back. Two copies in one capture — a fanned playset — queue the
second as a *possible duplicate*, and so does deliberately re-scanning a
card on its own; confirming from the queue is the "yes, really" a real
playset needs. But the same card re-read by a nudge, or still sitting beside
the next card you placed, is just an un-swapped pile: those re-sightings are
dropped silently (a "still seeing…" note in the status line, no chime), even
when the re-read comes back with an OCR mangle that would otherwise queue as
uncertain.

Everything that doesn't clear the bar lands in a **review queue** with the
reason it queued. <kbd>tab</kbd> at the capture step opens the queue
mid-session — fix a card through the ordinary cascade (printing, finish,
destination, quantity) and <kbd>tab</kbd> back to keep scanning; the camera
and any in-flight lookups keep running the whole time. Closing the camera with
cards still queued asks first: <kbd>enter</kbd> walks the queue, <kbd>d</kbd>
discards it, <kbd>esc</kbd> returns to scanning. With more than one binder or
deck, the destination is asked once when the camera opens and stamps every
auto-add; a queued card can still override it in its cascade.

For the cards that do reach the printing picker, the collector read stays a
suggestion rather than a decision: the matched printing is promoted and marked
`← scanned` with the cursor on it, a misread digit is visible before you
commit, and enter is all it takes to accept. If the number matches none of the
printings, the list is left in its normal order and hoard says so rather than
quietly pretending nothing was read. Cards too old to carry a number, or a
border too blurred to read, simply fall back to the ordinary printing picker.

## Scanning several cards at once

Fan a spread of cards — or lay them out with gaps — and capture once: every
readable title becomes a card, each resolving in the background like any other
scan. Cards in a fan hide their bottom borders, so they rarely carry the
collector info the auto-commit bar demands — a spread mostly lands in the
review queue by design, walked one cascade at a time (<kbd>ctrl+s</kbd> skips
a card). Adding cards to a spread one at a time re-captures everything
visible; the already-added ones are recognized and dropped rather than
double-counted or re-queued. A single card in frame behaves exactly the
same — no mode, nothing to switch.

Detection is two-channel: card outlines found in the frame are
perspective-corrected and read individually (the only way collector info can
be read per card — cards laid out with gaps get the most of this), while
title bands of overlapped cards are picked out of the whole-frame text. Titles
survive fanning; outlines and bottom borders don't, so cards in a fan usually
resolve by name and land in the normal printing picker. Something that reads
title-like but isn't a card (a keycap, a leaflet) fails the Scryfall match and
is skipped automatically with a note. Flavor-text attributions get their own
filter in the helper: "—Doctor Doom" under a quote names a character who often
*is* a card in the same set, so Scryfall would vouch for the phantom rather
than kill it — a title-shaped line hanging directly beneath a line that ends
in a closing quote is recognized as an attribution and never becomes a card.

On the wire, the helper's `scan` event now carries a `cards: []` list (name,
candidates, per-card `setCode`/`collectorNumber` when read, plus `confidence`
and a `source` channel tag), flat `confidence`/`bandAnchored`/`auto` fields,
and the `ready` event advertises `features: ["auto"]`. Auto capture is opt-in
over stdin (`auto-on`/`auto-off`, or the `--auto` flag), and a new `auto`
event reports the trigger's state transitions. Compatibility is by
construction: an old hoard binary never sends `auto-on` and ignores the extra
fields, and a new binary only enables auto on a helper that advertised it —
an old helper against a new binary simply stays on the spacebar. The flat
single-card fields remain populated from the frame-wide read, so the
single-card path works across any pairing.

The trigger's thresholds are tunable without recompiling, mainly for
experimenting — the knobs and their current defaults live in
[scanner-tuning.md](scanner-tuning.md), which is the one source that tracks
them. `HOARD_SCAN_AUTO=1` traces the trigger's decisions to stderr.

## Rotation

The preview starts rotated a quarter-turn clockwise, which is what a
portrait-held iPhone needs: Continuity Camera hands over a landscape frame and
macOS often can't tell how the phone is being held. If the framing is still wrong
use **←/→** to rotate the preview, and the corrected angle is saved to
`scan.json` beside the database, so you only need to fix it once. The window
title always shows the current angle and how much of it came from macOS's
automatic correction.

## Framing (the startup "too close" zoom)

macOS gives apps no camera zoom control at all — Apple's zoom APIs are
iOS-only — but the one thing that *does* auto-zoom a Continuity Camera is
Center Stage, the system's subject-tracking crop, and its state persists
system-wide: a FaceTime call that left it on is why the scanner sometimes
wakes up framed too close. The helper therefore takes app control of it and
forces it off at every session start, so the camera always opens on the full,
uncropped frame; the physical phone position is the zoom. Press **z** (in the
add view or the camera window) to toggle the auto-framing back on for the rare
setup its crop suits — the window title shows `FRAMED` while it's active, and
the toggle lasts for the session only.

## Lighting

Continuity Camera does not bridge the phone's flashlight to the Mac — the
device reports no torch to AVFoundation, so the **t** toggle answers "this
camera has no torch" today. The control stays wired for the day Apple bridges
it (the helper logs the device's real capabilities to stderr at session
start, visible via `HOARD_SCAN_LOG`, so this is checkable rather than a
matter of memory). If a torch is ever advertised, **t** toggles it, the
window title shows `TORCH` while lit, and the light is session-only.

What macOS does offer is **Studio Light** — software subject lighting — which
lives in the system's Video Effects panel along with the system's own Center
Stage and Desk View toggles. Press **v** (in the add view or the camera
window) to open that panel. Real exposure control, like zoom, is iOS-only.
The dependable fix remains a desk lamp; mind the glare on foils either way —
light straight overhead can white out the title line, an angle reads better.

## Desk View

The phone's **Desk View** feed (the ultra-wide dewarped into a top-down view
of the desk) is deliberately *not* offered in the camera picker. The
ergonomics are tempting — cards lying flat, no stand — but the feed sits well
below the sensor's full photo resolution, and the collector number is already
at the edge of what Vision resolves at full resolution; on Desk View it
becomes a coin flip and the review queue fills up. The main camera at photo
resolution is the scanner.

## Finding the helper

hoard looks for the helper next to its own binary, then in a `bin/` directory
beside it, so running `./hoard` from the repo just works. If you move `hoard`
onto your `PATH`, either bring `bin/hoard-scan.app` along with it or point at it
directly:

```sh
export HOARD_SCAN=~/src/hoard/bin/hoard-scan.app
```

Everything else works without the helper; if it isn't found, the in-app scan
action reports that it's unavailable rather than failing.

## Notes and troubleshooting

- Continuity Camera needs an iPhone signed into the same Apple ID, nearby and
  unlocked-then-locked, with Continuity Camera enabled (Settings › General ›
  AirPlay & Continuity). A USB cable can also be used as it's the most reliable
  way to get it connected.
- If you tapped **Disconnect** on the phone during a previous session, toggle
  that same Continuity Camera setting off and on to make it offer itself again.
- Detection waits up to 2.5s for a phone to publish itself; `HOARD_SCAN_WAIT=5`
  raises it.
- To confirm what the helper can see, independent of the TUI:
  `./bin/hoard-scan.app/Contents/MacOS/hoard-scan --list-devices`
- To check what a photo of a card actually reads as, without a camera:
  `./bin/hoard-scan.app/Contents/MacOS/hoard-scan --image card.heic --rotate 0`.
  It takes the same code path as a live capture and reports `collectorNumber`,
  `setCode`, and the raw `bottomLines` it matched against, which is the quickest
  way to see why a border did not read.
- The first scan prompts for camera permission (System Settings › Privacy &
  Security › Camera). On-device OCR only, so no images leave your machine.
- To try the auto trigger without the TUI, run the helper directly:
  `HOARD_SCAN_AUTO=1 ./bin/hoard-scan.app/Contents/MacOS/hoard-scan --auto` —
  events print to stdout, trigger decisions to stderr, and commands
  (`capture`, `auto-off`, `quit`) can be typed straight into stdin.
- Backing out is always available: <kbd>Esc</kbd> in the capture window, or
  <kbd>esc</kbd> in the terminal, cancels the scan and returns to the prompt
  without ending the session — with a decision point first if scanned cards
  are still waiting in the review queue.
- If OCR misreads the name, the card waits in the review queue with the
  recognized text pre-filled when you open it, so you can fix it and search
  manually.
