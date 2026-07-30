# Scanning cards with an iPhone

> macOS only, and it needs the `hoard-scan.app` helper built by `make all` (or
> `make scan` on its own).

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

A window opens with the live feed **and stays open**. Frame a card, press
<kbd>space</kbd>, and the add prompts run in the terminal; once the card is saved
you're back at framing for the next one. Working through a box of cards is:
frame, press space, confirm, repeat.

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

It is deliberately a suggestion rather than a decision. A misread digit is
visible before you commit, and enter is all it takes to accept. If the number
matches none of the printings, the list is left in its normal order and hoard
says so rather than quietly pretending nothing was read. Cards too old to carry
a number, or a border too blurred to read, simply fall back to the ordinary
printing picker.

## Rotation

The preview starts rotated a quarter-turn clockwise, which is what a
portrait-held iPhone needs: Continuity Camera hands over a landscape frame and
macOS often can't tell how the phone is being held. If the framing is still wrong
use **←/→** to rotate the preview, and the corrected angle is saved to
`scan.json` beside the database, so you only need to fix it once. The window
title always shows the current angle and how much of it came from macOS's
automatic correction.

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
- Backing out is always available: <kbd>Esc</kbd> in the capture window, or
  <kbd>esc</kbd> in the terminal, cancels the scan and returns to the prompt
  without ending the session.
- If OCR misreads the name, you land back at the prompt with the recognized text
  pre-filled, so you can fix it and search manually.
