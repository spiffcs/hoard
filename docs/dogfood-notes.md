# Dogfood notes

Feedback from live sessions on fresh hoards, with dispositions. Add a dated
section per session; keep dispositions honest (done / deferred / declined,
with why).

## 2026-07-31 — fresh hoard, scan telemetry at /tmp/scan-telemetry.log

1. **Auto-add sometimes silent** — ✅ fixed, then widened per follow-up.
   Telemetry showed 13 of 22 captures were nudge-armed, which the shutter
   pop deliberately skips ("a slow moment between cards shouldn't sound
   like the scanner acting up" — docs/scanning.md); auto-adds from those
   captures had no audio at all. The helper gained a `chime` command
   (NSSound "Glass") and the Go side fires it on **every processed card —
   auto-added or queued for review** — because either outcome means the
   same thing at the table: this card is handled, place the next one.
   Ghost protections untouched: nudge echoes are swallowed before
   resolution and stay silent; the duplicate window still queues rather
   than auto-adds (and chimes, since a queued dup also wants action).
   Second follow-up: the helper's capture-time shutter pop was removed —
   with the resolution chime in place it made every card a two-beep
   event; one card, one sound.
2. **Palette suggestions should be view-specific** — ✅ done. Commands
   carry a `rank(*Model)` and the empty-query palette sorts by it: an
   empty movers view leads with "Update prices" and "Backfill 90 days of
   price history"; unpriced leads with repair; watches leads with the
   watch commands; a running op puts "Cancel" on top. A typed query
   overrides ranking entirely — typing means you know what you want.
3. **No palette path to backfill** — ✅ done. `WithBackfill` wires
   `action.BackfillPrices` into the op layer; the palette entry names its
   ~150 MB cost.
4. **No clear way to add/configure watches from the watches view** —
   ✅ done. "Add a watch by name…" (palette, ranked first on the watches
   view) chains two prompts — card name, then an explicit-direction
   threshold — and runs the resolve-and-add as an operation. The empty
   watches view now says both paths.
5. **Per-view populate key, and enter is the wrong key for it** — ✅ done.
   `F` fetches whatever the current view needs: arbitrage quotes, the
   movers pipeline (update prices **then** backfill, composed into one
   operation), finish repair on unpriced, a price refresh elsewhere.
   Per follow-up: arbitrage's enter-to-fetch was removed outright — few
   users, no continuity worth keeping — so F is the one verb everywhere.
   Empty analytical views advertise it.
6. **14 cards scanned → 12 auto-added + 7 queued (5 excess)** — ✅ fixed,
   three telemetry-diagnosed causes:
   1. *Multi-card nudge echoes escaped the swallow.* The echo check
      remembered only the **last** auto-added name, so a nudge re-reading a
      two-card scene swallowed one echo and dup-queued the other. The
      single-name memory is now a recently-processed-names window
      (`recentNames`, same 10 s horizon as the dup window) and a nudge
      re-read of **any** recent name is swallowed.
   2. *Lingering neighbours queued as duplicates.* A card left in frame
      beside each newly placed card re-queued itself capture after capture
      (one card produced five re-sightings live). Commits now remember which
      capture produced them: a duplicate from the **same** capture is a
      fanned playset and still queues; from a **later** capture with other
      cards beside it (or via nudge) it's an un-swapped pile and drops
      silently. A later **solo** re-scan still queues — sequential playset
      scanning keeps its path.
   3. *OCR mangles of lingering cards queued as "uncertain".* A lingering
      card re-read as e.g. "Doc Gal's Hanchmen" failed resolution and
      queued. Queue-bound items from nudge/multi-card captures now run the
      title-shape check (`cardname.Plausible`) against the recent-names
      window and drop when they're a mangled re-read of a just-processed
      card. Solo non-nudge captures skip the probe: a deliberately
      re-scanned worn card must never vanish.
   Drops and swallows stay silent by design — the chime remains the receipt
   for a *handled* card (auto-added or queued), not for the scanner
   recognizing something it already did.
7. **Phantom "Doctor Doom" queued from Aerial Doombot's flavor text** —
   ✅ fixed in the helper. The flavor attribution ("—Doctor Doom") OCR'd
   with its dash dropped, passed the title-shape check, and — being a real
   card in the same set — was *vouched for* by the Scryfall backstop that
   kills other junk. Same failure family as the Kev-Walker artist ghost,
   and structural to licensed sets where quote characters are cards. Two
   rejections in the helper: an explicit leading-attribution-dash check in
   `titleLike`, and `flavorAttribution` — a title-shaped line centered
   inside or just below a line ending in a closing quote mark is an
   attribution, never a card. Geometry lesson baked into the comment: a
   tilted card's axis-aligned boxes bleed, so the fixture's quote box
   vertically *contained* its attribution and a clean-gap test never fired.
   Verified by replaying all 18 captures of the session's fixture directory
   (`HOARD_SCAN_DEBUG_DIR`) through `--image`: capture 9 loses exactly the
   phantom, the other 17 are byte-identical.
8. **Palette add exits the TUI / should add be seamless?** — ✅ done
   (2026-07-31, TUI-completion sprint). The cascade now runs *inside*
   browse as an embedded child model (`tui.Child` facade; browse routes
   messages, sizes it, owns camera-session teardown and the exit
   receipt). No flicker, no state loss, and ops keep running behind an
   add. The paired parity-ledger items landed in the same sprint:
   import/export prompts, deck-add-by-URL, the valuation report overlay,
   and the `Deps.Confirm` bridge (catalog download questions now appear
   as a real confirm instead of silently declining). See
   docs/sprint-tui-completion.md.

## 2026-07-31 — round 2, live session on the TUI-completion build

All landed same-day, in feedback order:

1. **Palette ellipses** — removed; nearly every command asks for more, so
   the marker distinguished nothing.
2. **F on movers took 31s with nothing to do** — two fixes. Same-day
   re-runs skip via a ledger receipt keyed to (date + holdings). And the
   real cost was profiled: of 31s, 30.2s was encoding/json's tokenizer
   walking the 1.2 GB decoded archive (disk 0.02s, gunzip 2s). Replaced
   with a byte-level key scanner (`scanKeyedObjects`) that searches for the
   wanted UUID keys directly and stops at the last one: 2–3s total.
   Scaling: per-needle search is linear in owned printings, so past ~24
   keys the scanner switches to a colon-anchored single pass with a set
   lookup (UUIDs are fixed-width) — ~2.6s flat however large the hoard.
3. **Watch add was awkward** — replaced with a picker: "Add a watch" jumps
   to holdings with the filter open; enter from the filter bar picks the
   card into the ordinary threshold prompt; the flow returns to the
   watches view once the watch lands. By-name kept, demoted, for unowned
   cards. Threshold prompts prefill the current value on edit and their
   help spells out under/over syntax.
4. **Arbitrage "liquid" misread** — three rounds of feedback, three fixes:
   liquid rows label the buy side `retail` and say "pays N%" instead of a
   GAIN column; the status line states both prices plainly with no
   editorial; the section gained a 70%-of-retail floor (a shop paying 27%
   of retail is not liquidity); and per the follow-up, the flat table was
   the smell itself — the view now renders the CLI's three sections
   stacked in one scrolling pane, each with its own title row and honest
   column headers (PROFIT / RETAIL·BUYLIST·PAYS / LOW·HIGH·APART).
5. **Help lines** — wrap between entries on narrow terminals instead of
   truncating (extra rows come out of the panes; frame height invariant);
   view-specific verbs lead each analytical view; unpriced dropped the
   redundant `f repair finishes`; holdings advertises `n new binder`,
   `R rename`, and `: import/export`; the empty palette ranks collection
   verbs first on holdings.
6. **Key policy** — one quit chord (ctrl+c) everywhere; q is inert; esc
   backs out one frame and asks y/n at the top; the embedded cascade's
   help says "esc back to browser"; enter no longer aliases the shutter in
   the capture step.
7. **Detail view** — the palette opens over the overlay instead of
   replacing it (ops run behind it, prompts render in its slot); hints
   point at `:` commands instead of CLI invocations.
8. **Add view** — the session tally carries the running dollar value
   beside the count.
