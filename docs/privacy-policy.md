# Privacy Policy

**Hoardling** (iPhone) and **hoard** (macOS)

Last updated: 13 August 2026

## The short version

Hoardling collects nothing. There are no accounts, no analytics, and no
third-party SDKs. What a card reads as goes to a Mac you have explicitly
paired with, on your own local network, and nowhere else.

## The camera

The camera is used for one thing: reading the card in front of it.
Recognition runs entirely on your iPhone, using Apple's Vision framework.

**No photograph of a card is stored on your phone or sent anywhere.** Each
frame is read and discarded.

## What leaves your iPhone

One thing, and only when you have paired a Mac: the text of a read: the
card's name, set code and collector number.

That connection is encrypted and goes only to a Mac you approved with a
six-digit code; after the first pairing, the two ends recognise each other by
certificate and refuse anyone else. Hoardling makes no other network
connections of any kind. It does not contact us, or an analytics service, or
an advertising service, because no such thing exists in it.

## What is stored on your iPhone

- **Your settings** (the price tiers and which sound each one plays), in the
  app's own preferences.
- **The pairing code and this device's identity key**, in the iOS Keychain.

Both stay on the device. Deleting the app removes them.

## Data collection

None. We run no servers, no accounts and no analytics. There is no data for us
to collect, sell or share, and none has ever reached us.

The app's privacy manifest states this formally: no collected data types, no
tracking, and no tracking domains.

## The Mac companion

hoard runs on your own Mac and keeps your collection in a database file on
that Mac.

To identify cards and show prices it fetches public card data from third-party
services: Scryfall, MTGJSON and TCGCSV, and Archidekt if you import a deck by
URL. Those requests come from your Mac, and those services see your Mac's IP
address the way any website you visit does. hoard sends them no account
information, because it has none to send.

## Changes

Any change to this policy is published here, in version control, where its
full history stays visible.

## Contact

Open an issue at [github.com/spiffcs/hoard](https://github.com/spiffcs/hoard).
