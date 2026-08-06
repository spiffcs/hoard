# Scanning cards with an iPhone

> macOS only. It needs two things built: the `hoard-scan.app` helper (`make all`,
> or `make scan` on its own) and **Hoardling**, the companion app, on your
> iPhone — see
> [ios-development.md](ios-development.md) for that side. Working on the scanner
> itself? Start with [scanner-tuning.md](scanner-tuning.md) — the tuning loop and
> the field lessons the current behavior was built from.

Inside an add session (`hoard add`, or <kbd>a</kbd> in the browser), press
<kbd>ctrl+o</kbd> to identify a card with your iPhone instead of typing its name.

## How it works

**The phone is the scanner.** It owns the camera, fires its own shutter, and
reads the card on-device; the Mac resolves the reading against the catalog and
keeps the queue. Nothing on the Mac captures anything — there is no webcam
fallback, because a fixed user-facing lens cannot be aimed at a card on the desk
and a capture from one is unreadable in a way that looks like bad OCR rather
than the wrong camera.

hoard used to also drive an iPhone over **Continuity Camera**, with no app
required. That path was removed on 2026-08-05: the app beat it on every axis
that mattered (48 MP stills against Continuity's 1920x1440 ceiling, a lens
locked at a measured distance, exposure and white balance frozen, a real torch),
and carrying two capture backends meant two read pipelines and two sets of
tuning constants. One consequence is called out under
[Scanning several cards at once](#scanning-several-cards-at-once).

**Pair once, from the name prompt: <kbd>ctrl+p</kbd>.** It opens with the three
things that have to be true — the app open, on screen, and showing its Pair tab
— because a phone that is not yet running looks exactly like a phone that is not
there. Press enter when it is, and hoard searches. <kbd>ctrl+p</kbd> lists the
phones it can see, and typing that code pairs them. The code is kept on the
phone and the pairing beside the database, so this is a once-per-phone step
rather than a once-per-session one. To revoke a Mac, use **Generate a new code**
on the Pair tab: every existing pairing stops working, and <kbd>ctrl+p</kbd>
re-pairs.

<kbd>ctrl+o</kbd> then opens a session, asking which phone only when it can see
more than one. While a session is already open, <kbd>ctrl+o</kbd> takes you back
to it instead; close it with <kbd>c</kbd> first to switch phones.

The phone shows the live feed and watches it: set a card down, hold it still for
about a second, and the shutter fires on its own. An outline traces the card the
trigger is watching — yellow while it settles, green once it's shot — and the
trigger won't re-fire until the scene changes, that is, until you swap the
card. Whatever rectangles are already in frame when auto arms (a notepad, a
mousepad, a coaster — a desk is full of rectangles) are treated as furniture:
they get no outline and can never fire. Two consequences worth knowing:
**arm the camera with the staging area clear** — a card already sitting there
when auto turns on reads as furniture until you remove and re-place it (and
the scanning pile must grow from the first scanned card, not pre-exist) — and
if you rearrange the desk mid-session, toggle auto off and on to re-learn
what's furniture. <kbd>space</kbd> in the terminal still captures manually at
any time, and cards on a surface too close to their own color may need it,
since the trigger never sees an outline there. Working through a box is: set a
card down, wait for the flash, swap in the next — **or stack the next card straight
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

That one sound is priced: the phone flashes the
amount just scanned and plays the price's tier — a muted grey flash and low
knock for bulk (under $1), a gold flash and bright bell for a win, and a
coin shower with a rising glissando for a jackpot ($20 and up). The
thresholds are tunable per run with `HOARD_SCAN_WIN` and
`HOARD_SCAN_JACKPOT` (dollars). They can also be moved mid-session: press
**:** on the capture step to open the scanner command line and type
`win 5` or `jackpot 30` — a live tweak lasts for the session, the env vars
are what persist.

The sounds themselves are synthesized on the phone (no third-party audio), and
are not currently replaceable. The Mac helper used to hold a sound bank that
`HOARD_SCAN_SOUND_BULK` and friends could point at your own files, plus a
`HOARD_SCAN_HUD_VOLUME`; that bank played through the Continuity window and went
with it. Nothing reads those variables now — the phone is where the sound comes
out, and it takes its cue from the Mac rather than its configuration. A running
session total sits at the phone's top right and counts *committed*
cards only — a card that queues for review flashes "Needs Review" with a
soft rising two-note sound, the inflection of a question, instead of a
price (its printing is still unverified). Confirming it in review answers
on the phone: the amount that landed flashes with its tier's
sound, and the total moves. An unpriced card flashes "$—" with the
familiar plain chime.

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

On pre-M15 frames the number hides at the tail of the copyright line ("™ & ©
1993-2003 Wizards of the Coast, Inc. 95/350"), and hoard reads it from there
too — along with the copyright range's end year, which equals the printing's
release year and settles a number shared across editions ("95" is Remove Soul
in both 7th and 8th Edition; the year says which). Because that italic print
misreads digits more often than the modern collector block, a copyright-line
read only ever *strengthens* a match — a garbled digit never demotes a card
that would have added itself without it.

The card is located in the frame first, and the border is then read relative to
the card's own bottom edge, so the card does not need to fill the shot or sit at
any particular height — anywhere in frame, roughly upright, is enough.

## Confident scans add themselves; the rest queue

A scanned card writes itself to the collection — quantity 1, no keys
pressed — only when the evidence adds up. The printing must be pinned: the
collector read matched a real printing of the resolved card (or it is the
only printing that exists — where a same-set variation row, like a
theme-deck alternate art, counts as the same printing). A full
set-and-number verification carries the
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
tally as the audit trail.

A recently added card seen again is judged by *how* it came back, not by how
long ago. Two copies visible in one capture — a fanned playset — are two cards
and both commit. So does a repeat the phone reports as a **placement it
watched happen**: it compares the card now on the mat against the one it shot,
and where that comparison is decisive it is believed, which is what lets you
stack a playset copy-on-copy as fast as your hands move. A repeat with nothing
behind it still waits out a three-second floor, because nobody swaps a card
that fast and a claim made without a measurement is not evidence.

What comes back the other way is dropped: the same card re-read by a nudge,
still sitting beside the card you just placed, or one the phone itself says
only *moved* — an un-swapped pile is not a playset. Those go silently, with a
"still seeing…" note in the status line and no chime, even when the re-read
arrives with an OCR mangle that would otherwise queue as uncertain.

Because every one of those judgements is about a physical act, the drop is
never final. The status line names the card and offers <kbd>+</kbd>
(or <kbd>=</kbd>, unshifted, for the hand that isn't holding cards) — press it
and the suppressed copy is written, and shows on the session receipt as a
confirmed duplicate. It costs a keystroke exactly when the scanner guessed
wrong, and nothing when it guessed right.

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

**You can't, currently.** One card per capture.

This is the capability the Continuity Camera removal cost, and it is worth
stating plainly rather than leaving to be discovered. The old macOS pipeline
read a frame on two channels — card outlines perspective-corrected and read
individually, plus title bands of overlapped cards picked out of the whole-frame
text — so a fanned spread yielded one entry per readable title. `CardKit`, the
phone's reader, emits at most one card per frame.

Nothing downstream was removed with it. The wire still carries a `cards: []`
list, and the Go side still resolves, de-duplicates and queues every entry in
it, so this can come back as a change to the phone's reader alone. The
`two-card-pile`, `white-border-control` and `white-border-on-light-desk`
fixtures still hold the frames it would have to pass; their goldens record the
single-card readings for now (`scan/fixtures/README.md`).

What still works, and is what a pile actually needs: something that reads
title-like but isn't a card (a keycap, a leaflet) fails the Scryfall match and
is skipped automatically with a note. Flavor-text attributions get their own
filter — "—Doctor Doom" under a quote names a character who often *is* a card in
the same set, so Scryfall would vouch for the phantom rather than kill it, and a
title-shaped line hanging directly beneath a line ending in a closing quote is
recognized as an attribution and never becomes a card.

## The wire

The phone's `scan` event carries a `cards: []` list (name, candidates, per-card
`setCode`/`collectorNumber` when read, plus `confidence` and a `source` channel
tag) alongside flat `confidence`/`bandAnchored`/`auto` fields. The `ready` event
advertises what the source can do — `["torch", "hud", "border"]`, plus `auto`
and `rearm` once its video tap is attached — and hoard only sends a verb the
source claimed. Auto capture is opt-in over stdin (`auto-on`/`auto-off`), and an
`auto` event reports the trigger's state transitions.

Three verbs retired with Continuity Camera: `rotate-left`/`rotate-right`,
`frame-on`/`frame-off` and `effects`. They were a preview the Mac had to turn
upright, Center Stage, and the system Video Effects panel — none of which mean
anything to a phone that reads its own already-upright frame. They are not
reserved; an old binary sending one gets the ordinary "unknown verb" error and
the session stays up.

Each scan carries a `fireReason` saying why the shutter went: `removed` (the
card left), `replaced` (a card was laid over it), `moved` (a box held the spot
and still looks like the card already read), or `nudge` (hoard asked for another
look, with no physical evidence at all). The first two are placements the phone
watched happen; the last two are the phone saying it has no such evidence, and
hoard drops a repeat carrying either rather than counting a second copy. `moved`
exists because `replaced` used to claim it — sliding a card and swapping one look
identical through a window pinned at the last shutter, and one card once
committed five times in six seconds on that confusion.

The trigger's thresholds are tunable without recompiling, mainly for
experimenting: `HOARD_SCAN_AUTO_STABLE` and `HOARD_SCAN_AUTO_INTERVAL` are
forwarded to the phone when the session opens. Their current defaults and the
sessions they were fitted against live in
[scanner-tuning.md](scanner-tuning.md), which is the one source that tracks
them.

To watch the trigger decide, set `HOARD_SCAN_LOG=/tmp/scan.log`. The phone's own
trace lines cross the link and are re-emitted on the helper's stderr, so one
file holds both sides of the wire — which is the only way to see the feed while
the TUI owns the pipes.

## Lighting

The phone has a real torch and hoard can drive it: press **t** on the capture
step to toggle it, and it stays on for the session only. Mind the glare on
foils — light straight overhead can white out the title line, an angle reads
better. A desk lamp is still the dependable fix.

(Under Continuity Camera there was no torch at all — macOS never bridged it —
and the workarounds that existed instead, Studio Light and the system Video
Effects panel, went with that path.)

## The split between phone and terminal

**The phone shows the price and plays the sound; the terminal has everything
else.** That split is deliberate: the phone is in a stand facing the desk, so it
gets the feedback you can take in without looking, and the queue, the tally and
the review cascade stay where your attention already is.

**The app must be open and on screen.** iOS suspends background apps, and a
suspended app stops advertising, so a phone that has been switched away from
looks exactly like a phone that is not there. If <kbd>ctrl+o</kbd> finds
nothing, that is usually why.

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

## Where the code lives

Two halves. The phone captures and reads; the Mac helper is the other end of the
link and owns no camera.

    scan/hoard-scan-ios/       the iPhone app — see docs/ios-development.md
      Sources/Capture/         AVFoundation, the trigger runner
      Sources/Link/            pairing, session view, price overlay, log

    scan/hoard-scan/           the SwiftPM package both ends share
      Sources/CardKit/         the read pipeline: title, collector line, trigger
      Sources/BorderKit/       the border reader, anchored on footer text
      Sources/ScanLink/        Bonjour, and the pairing handshake
      Sources/ScanWire/        Event, ScanCommand — the NDJSON contract with Go
      Sources/ScanKit/         the Mac end: the translator and --mirror window
      Sources/cardkit-probe/   CardKit over an image file, for the harnesses

`ScanWire` is the contract with `internal/scan`, and it must not fork — that is
why it is a target rather than a struct on each side. `ScanKit` is Mac-only and
the phone does not link it: it browses for the phone, forwards stdin verbs onto
the link, and writes what comes back to stdout verbatim.

There used to be a second read pipeline under `ScanKit/Core/` — the macOS
scanner, for the Continuity Camera path. It was deleted with that path, and
`CardKit` is now the only reader in the repo.

Three commands, all fast:

```sh
make scan-test    # unit tests: the trigger machine, the parsers, the maths
make scan-check   # replay the 29 capture fixtures against their goldens
make cardkit-score  # score the labelled corpus per frame era
```

`docs/scanner-tuning.md` names the function each field lesson is enforced at;
this tree is how to find them.

## Notes and troubleshooting

- **No phone found?** The app has to be open and on screen, and both devices on
  the same Wi-Fi (or the phone plugged in). Browsing waits 1.5s and opening a
  session waits 2.5s; `HOARD_SCAN_WAIT=5` raises the latter on a slow network.
- To confirm what the helper can see, independent of the TUI:
  `./bin/hoard-scan.app/Contents/MacOS/hoard-scan --list-devices`
- To check what a photo of a card actually reads as, without a phone:
  `make cardkit && ./bin/cardkit-probe --image card.heic --rotate 0`.
  It takes the same code path a live capture does — it *is* the phone's reader —
  and reports `collectorNumber`, `setCode` and the raw `bottomLines` it matched
  against, which is the quickest way to see why a border did not read. Add
  `--border` for every number the border reader weighed.
- The first scan prompts for camera permission **on the phone**, and for local
  network access on the Mac. OCR happens on the phone and the reading crosses
  your own network, so no images leave your devices.
- The link is authenticated but not encrypted: nobody joins a session without
  the pairing code, but anyone on the network can read one.
- Backing out is always available: <kbd>esc</kbd> in the terminal cancels the
  scan and returns to the prompt without ending the session — with a decision
  point first if scanned cards are still waiting in the review queue.
- If OCR misreads the name, the card waits in the review queue with the
  recognized text pre-filled when you open it, so you can fix it and search
  manually.
