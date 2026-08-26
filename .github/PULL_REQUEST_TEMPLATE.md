<!--
Delete any heading that does not apply. A short PR with two honest sections
beats a long one with five padded ones.
-->

## What changes

<!-- What the code does now that it did not do before. Not a diff summary;
     the reader can see the diff. -->

## Why

<!-- The problem this solves. If it fixes an issue, `Fixes #123` here so the
     issue closes on merge. -->

## How it was verified

<!-- What you ran, and what it proved. `make test` passing is the floor, not
     the answer: say which new test fails without this change, or paste the
     before/after output of the command you fixed. A change to the TUI or the
     card scanner needs a real run, because no unit test sees what the screen
     or the camera did. -->
---

- [ ] `make static-analysis` and `make test` pass locally
- [ ] Behavior changes are covered by a test that fails without this change
- [ ] User-facing output, flags, or the on-disk schema changed → docs updated to match
