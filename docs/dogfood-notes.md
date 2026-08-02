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
7. **Palette add exits the TUI / should add be seamless?** — 🔶 deferred,
   design decision. Two bubbletea programs cannot share a terminal, so
   the add cascade hands off via quit-and-return today. Making it seamless
   means either embedding the cascade as a child model inside browse (big:
   the cascade owns scanning, pickers, its own state machine) or a nested
   program handoff that hides the flicker. Worth its own design pass —
   candidate opener for the next TUI sprint alongside the remaining parity
   ledger (import/export/deck-URL prompts, TUI confirm modal).
