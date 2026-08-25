# Adding cards

There are two ways into the same add flow. You can type a card's name, or you can
point an iPhone at the card and let it read it. Both land in the same place, so
you can start typing, switch to the camera, and go back again without leaving
the screen.

Everything here happens inside `hoard`. Press <kbd>a</kbd> anywhere in the
browser to open the add flow.

- [Typing a card](#typing-a-card)
- [Scanning with an iPhone](#scanning-with-an-iphone)
  - [1. What you need](#1-what-you-need)
  - [2. Build and install Hoardling](#2-build-and-install-hoardling)
  - [3. Pair the phone](#3-pair-the-phone)
  - [4. Scan](#4-scan)
- [The review queue](#the-review-queue)
- [Where the pairing lives](#where-the-pairing-lives)

## Typing a card

Press <kbd>a</kbd>. The screen says **Add cards to your collection** and gives
you a text field.

```
Add cards to your collection

> lightning bolt

: commands · ctrl+p pair · ctrl+o scan · enter search · ctrl+d done · esc back
```

Type any part of a name and press <kbd>enter</kbd>. hoard searches Scryfall and
walks you through the available choices. Each one is a list you can move through with
<kbd>↑</kbd>/<kbd>↓</kbd>, narrow by pressing <kbd>/</kbd>, and pick with
<kbd>enter</kbd>. <kbd>esc</kbd> steps back.

**1. Which card.** Only asked when the name matched more than one card.

**2. Which printing.** The set and collector number. By default this offers the
printings hoard thinks you mean; <kbd>ctrl+a</kbd> widens it to every printing
of that card.

**3. Which finish.** Nonfoil, foil, or etched — only the finishes that printing
was actually made in.

**4. Which binder.** Skipped when you only have one, so a fresh collection never
asks.

Then it asks for a quantity:

```
Quantity for Lightning Bolt

> 1

enter to continue · esc cancel
```

And shows you what it is about to write:

```
Confirm

1× Lightning Bolt
M11 #146 · Magic 2011
finish: nonfoil   price: $2.41
add to: Binder

enter to add · esc cancel
```

<kbd>enter</kbd> writes it. You come back to the name field with a running
tally, ready for the next card. When you are done, <kbd>ctrl+d</kbd> returns you
to the browser.

Nothing is held back until the end — each card is saved as you confirm it. If
you quit halfway through, everything you already confirmed is in your
collection.

## Scanning with an iPhone

The phone app is called **Hoardling**. It is a camera head: it reads the card
and sends the result to `hoard` on your Mac over your local network. Your
collection is never on the phone.

Hoardling is not on the App Store yet, so you build it yourself and install it
onto your own device.

### 1. What you need

| | |
|---|---|
| A Mac | with Xcode installed, signed into an Apple developer account |
| An iPhone | running **iOS 18.0 or later** |
| A cable | for the first install |
| One network | the Mac and the phone must be on the same Wi-Fi |
| `xcodegen` | `brew install xcodegen` |

The Xcode project is generated from `scan/hoard-scan-ios/project.yml` rather
than checked in, which is why `xcodegen` is needed.

### 2. Build and install Hoardling

Plug the iPhone in, unlock it, and trust the Mac if it asks. Then, from a clone
of hoard:

```sh
task scan-ios-install
```

That generates the Xcode project, builds the app, finds the attached phone and
installs it. To build without installing, use `task scan-ios`.

The first run writes `scan/hoard-scan-ios/Signing.xcconfig` with your team ID,
guessed from your keychain. It is gitignored — a team identifier is account
data, not project configuration. If no signing identity was found, the script
stops and tells you to fill that file in yourself.

If the build fails with **"No profiles were found"** read the error line above it; the script prints the four causes it has actually seen, which are an Xcode account that is not signed in, an
unaccepted Program License Agreement, a phone whose UDID is not registered to
the team, or a missing iOS platform payload.

To check the code still compiles while you sort signing out:

```sh
xcodebuild -project scan/hoard-scan-ios/HoardScan.xcodeproj -scheme HoardScan \
    -destination 'generic/platform=iOS' CODE_SIGNING_ALLOWED=NO build
```

On the phone, the app appears as **Hoardling**. Open it once and allow camera
access and local network access since it needs both

### 3. Pair the phone

Pairing is a one-time exchange of a six-digit code. It happens inside the add
flow.

1. In `hoard`, press <kbd>a</kbd> to open the add flow.
2. Press <kbd>ctrl+p</kbd>. hoard shows the pairing instructions.
3. On the iPhone, open Hoardling and select **Pair**. It shows a six-digit code.
4. Back in `hoard`, press <kbd>enter</kbd>. It looks for the phone on your
   network.
5. When it finds it, type the six digits and press <kbd>enter</kbd>.

```
Pair with iPhone

Enter your six digit code

> 481 902

press enter to pair · esc back
```

That is it. The phone is remembered, so you do not do this again unless the 
code rotates, in which case press <kbd>ctrl+p</kbd> and repeat.

### 4. Scan

With a phone paired, press <kbd>ctrl+o</kbd> in the add flow to open the camera.

```
Scanning with iPhone

✓ Auto-added: 1× Lightning Bolt (m11/146) nonfoil · $2.41
✓ Auto-added: 1× Counterspell (7ed/67) nonfoil · $2.67

2 auto-added ($5.08) · 0 need review

Set a card down and the app will run auto capture. Press spacebar to
manually trigger a scan.

ctrl+p pair · ctrl+o camera · space capture · c close camera · ctrl+d done
```

Put a card down and it captures on its own; the screen says *Captured. Swap in
the next card.* while it works. <kbd>space</kbd> forces a capture if you would
rather drive it by hand.

Cards it is confident about are added immediately and listed as **Auto-added**.
Anything ambiguous goes to the review queue instead of being guessed at.

<kbd>c</kbd> closes the camera, <kbd>ctrl+d</kbd> ends the session.

## The review queue

When a scan is ambiguous, hoard does not guess. The card waits, and the counter
on the camera screen shows how many are waiting.

Press <kbd>tab</kbd> to work through them. Each one drops you into the same
picker sequence as typing. 

The queue is **not saved**. If you end the session with cards still in it, they
are discarded, and hoard tells you the count before it lets you go. Anything
already added is safe; it is only the unreviewed scans that go.

## Where the pairing lives

Two files, next to your database:

| File | What it is |
|---|---|
| `link-identity.pem` | this Mac's identity for the link |
| `link-pins.json` | the phones you have paired with |

On macOS that is `~/Library/Application Support/hoard/`. Both are private to
your user. Deleting them un-pairs every phone; you can pair again with
<kbd>ctrl+p</kbd>.
