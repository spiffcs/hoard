# Follow-ups — 2026-08-09 interop and link round

**Status: open, none blocking.** These came out of fixing the five items in a
bug report against the JSON export surface, all of which landed the same day as
`943cc88..718da8a` (CSV field count, `deck add --dry-run`, `export --format
text` plus the import exit status, help's global-flags sections, `hoard schema`)
along with two iOS fixes (`14a78cd` reconnect, `718da8a` silent tiers). Nothing
here was part of that report — each is something found *while* fixing it and
deliberately left alone, either because it was outside the lane being worked or
because it needs a decision rather than a patch.

Line numbers are as of `718da8a` and will drift — verify before editing.

Format per item: **where** → what's wrong → how it fails → fix direction.

Every claim below was reproduced or read at `718da8a`, not carried over from the
report. Where an item was checked by running it, it says so.

## CLI and interop

**A1. `internal/command/browse.go:165`** → the TUI carries its own copy of the
import deck-skip message, and it still reads *"decks come back via 'hoard deck
add', not as loose cards"*. That sentence was true and useless: until `c9d5b87`
no exporter emitted anything `deck add --file` could read. `import.go`'s copy was
rewritten to name the route that now works; this one was not, because it was
nobody's lane and the TUI has no exit status to change. → Copy the new wording:
restore a deck with `hoard export --deck NAME --format text`, then `hoard deck
add --file`. One line.

**A2. `internal/action/add.go:285` and `:308` (`DeckAdd`, from `:210`)** →
`deck add` disagrees with itself about what counts as partial. Both guards test
`len(res.Unresolved)` only; `res.Skipped` — the *unreadable lines* — is reported
and then ignored. `AddList` (`:68`) tests `len(res.Skipped) +
len(res.Unresolved)` at `:123`, so two sibling code paths treat the same
condition differently. → Verified by running it: a decklist of one good line
plus two unparseable ones prints both skipped lines and **exits 0**.

```
Imported deck #2 "Mixed" (text): 1 cards resolved.
  2 lines could not be read and were skipped:
    - line 2: ~~~ garbage ~~~
    - line 3: also not a card line
exit 0
```

A scripted deck restore therefore cannot distinguish "read the whole list" from
"read one line of it" — which is the same defect class as the import exit status
fixed in `c9d5b87`, one command over. → Fold `Skipped` into both guards so it
raises `ErrPartial` (exit 2), matching `AddList`. Worth deciding deliberately:
it is a contract change on a second scripted surface, and the argument that
carried for import applies here unchanged.

**A3. `"Dry run: nothing was written."` is written out three times** —
`internal/command/deck.go:159`, `merge.go:136`, `import.go:128` — along with the
`Would…` verb switch each one wraps. Two of the three were added by separate
agents working in parallel who could not collapse it without entering each
other's files. → Extract once. Low value alone; do it the next time one of the
three needs an edit, so the string cannot drift into three variants.

**A4. `internal/command/usage_test.go:36` (`TestUsageFitsANarrowTerminal`)** →
renders **root help only**, so no subcommand's `Usage` or `Example` text is
width-checked. That gap let a 69-column `deck add` example land during this round
and survive a green suite; it was caught by an ad-hoc sweep, not by CI. → Extend
the test across the command tree. It will fail immediately on three pre-existing
lines, which is the reason it was not extended during the round:

| where | columns |
|---|---|
| `import` description | 67 |
| `movers` long description | 65 |
| `movers` long description | 70 |

Reword those three, then keep the sweep. Measure through
`renderHelp(&b, ui.Env{Width: 60, Clamp: true})` — **not** the binary. `hoard`
does not honour `COLUMNS`, and piping its help removes the TTY, so it falls back
to roughly 80 columns and any measurement taken that way is wrong.

## iOS and the link

**B1. `scan/hoard-scan-ios/Sources/Link/LinkController.swift:193`** → an
`NWListener` that enters `.failed` is reported through `onError` and never
restarted. Nothing on the scanning screen rebuilds a listener, so the phone goes
off Bonjour and stays off. → This is the same user-visible symptom as the bug
fixed in `14a78cd` — "the phone is invisible and only unpair/repair brings it
back" — reached by a different route, so the fix there does not cover it.
Restart on `.failed` the way `Sounds.restartEngine` handles an audio-session
failure: idempotent, and log honestly when it cannot.

**B2. `internal/scan/client.go:99`** → `NeedsPairing` is computed as
`!paired[s.Name]`, matching the Bonjour *instance name* against the pin store's
names. Trust is by certificate fingerprint everywhere else, so a phone the user
has renamed reads as unpaired despite being pinned. Harmless today because the
single-phone path ignores the flag — it would send a two-phone user to the code
screen for a phone that needs no code. → Decide by fingerprint, not by name.

## Owed validation — hardware, not code

**C1.** `14a78cd` has never run on a phone. The test is concrete: pair, scan,
leave the add flow, re-enter, `ctrl+o` — expect a reconnect with **no code
prompt**. Its unit test covers the layer beneath (a pinned pair re-establishing
over a surviving listener); the `.quit` change itself is covered by a build and
by inspection only, because the iOS app target has no test target.

**C2.** `718da8a` likewise. Beyond that, the tier-sound feature *as a whole* has
never had a live run — see the settings work from earlier the same day. The
silent-tier change was verified by compiling the real `TierSettings.swift` and
`Sounds.swift` for the simulator and running each phase as its own process
against a scratch `UserDefaults` suite, which proves the persistence round trip
but exercises neither SwiftUI nor `AVAudioEngine`.

Both need the same session, so do them together.

## Dependency

**D1.** `origin/dependabot/go_modules/.../jsonschema/v6-6.0.3` proposes
`v6.0.2 → v6.0.3`. Only two files import it, and one of them —
`internal/command/schema_test.go` — landed in `609a743` on the same day, so the
bump now touches newer code than it was raised against. Its tests compile the
sliced schema to prove `--kind` output is still a valid, self-contained schema,
which is exactly the behaviour a jsonschema patch release could move. → Re-run
`go test ./internal/command/ ./internal/hoardjson/schemagen/` on the branch
before merging rather than trusting green CI from before those tests existed.

## Verified working — do not re-investigate

Checked against a real 2,235-copy collection during the round that produced this
list, and recorded so the next pass does not spend time on them:

- All eight document kinds emit correctly: `summary` (bare `hoard --json`),
  `holdings`, `report`, `market`, `movers`, `unpriced`, `watch`, `hoard`.
- The `--json` versus `--format json` conflict is handled deliberately; the one
  ambiguous combination is a clean usage error.
- `deck add --file` round-trips a hand- or LLM-authored text decklist including
  foil (`*F*` → `"finish":"foil"`).
- `priceUsd` is **per copy** as documented; totals need `priceUsd * count`.
- `--db` works correctly as a sandbox mechanism, and is now documented — it was
  invisible in help until `609a743`.
