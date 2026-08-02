# Dogfood notes

Feedback from live sessions on fresh hoards, with dispositions. Add a dated
section per session; keep dispositions honest (done / deferred / declined,
with why).

## 2026-08-01 — round 2, post-beautification build

1. **A mega list of every card, with binders/decks as subsets** — ✅ done.
   The left pane leads with a synthetic **All cards** row (id −1, kind
   "all"): `store.AllByFinish()` merges every container one-row-per-
   printing-and-finish (same card in binder + deck sums), the row carries
   the whole hoard's totals, and the card pane shows it read-only —
   edits, rename and removal refuse with pointers to the real container;
   its export runs the export-everything flow. Named "All cards" rather
   than reusing "Binder".
2. **Add-session exits were scary** — ✅ done. `ctrl+d` is the deliberate
   "done adding" from anywhere (blocked while review items are pending —
   those get the queue's own exits); `esc` now opens a leave gate that
   states what is saved (confirmed cards) and what leaving drops
   (anything mid-pick or queued), and requires `y` then `enter`.
3. **Deck-URL prompt lacked provider help** — ✅ done. The prompt's help
   names archidekt.com as the supported link and points Moxfield users at
   `deck add --file`.
4. **"Watch this card" offered from the container pane** — ✅ fixed.
   `subjectCard` returns nothing on holdings unless the cursor is in the
   card pane.
5. **Manual F after every add session** — ✅ done. Closing a cascade that
   added cards auto-runs the view's fetch (prices, identity). The smarter
   first run landed as its own piece, revised after a live check: opening
   the browser with **no catalog built auto-starts the catalog download**
   as an ordinary operation — progress in the usual slot, cancellable —
   before the first add session, where the fast lookups actually matter.
   (First attempt was a y/n gate; the owner wanted the op to just run.)
   Live-repro footnote: `--db /tmp/fresh.db` is a fresh *database*, not a
   fresh install — the catalog lives in the cache directory
   (`~/Library/Caches/hoard/catalog`), so testing the first-run path
   needs that cleared too (or `XDG_CACHE_HOME` pointed elsewhere).
6. **Backfill windows** — ✅ done. `action.BackfillPrices` takes days
   (ledger key includes it; observations filtered before insert); the
   collection palette offers "Backfill 30 days", the movers view offers
   30 and 90, and movers' F pipeline stays at 90.
7. **"Add a card by Scryfall URL" palette entry** — ✅ removed, along
   with its now-dead browse plumbing (`WithAddByURL` and friends); the
   CLI `hoard add <url>` path is untouched.
8. **"Add a watch for any card" everywhere** — ✅ now watches-only, same
   rule as the collection picker.

## 2026-08-01 — beautification-sprint build

1. **Movers offered "finish" as a sort option** — ✅ done. Dropped from
   the movers sort cycle (and its compare case); the other views keep it.
2. **PAYS and BELOW percentages should grade on a gradient** — ✅ done.
   `Env.Grade` ramps amber → green over a normalized 0..1; the
   normalizers (`market.LiquidityGrade`/`BelowMarketGrade`) live beside
   the section floors they read (70% liquidity, 25% discount, saturating
   at 100%/60%), so the TUI and CLI can never disagree about the scale.
3. **The collection pane does nothing on the market view** — ✅ resolved
   as "say so": every hoard-wide view (movers, unpriced, watches, market)
   now dims the container pane wholesale, title included, and tab/left
   explain — "this view spans the whole hoard" — instead of moving a
   cursor that changes nothing. Making the pane *filter* those views is a
   real feature for the backlog, not a quick fix.
4. **Palette noise** — ✅ done. The key reflexes left the palette but
   kept their keys (`hidden` on the registry): sort, sort reverse, mask,
   view cycle, movers lookback. The keyless view jumps and the fixed
   movers-window commands (7/30/90) were deleted outright — `v` and `W`
   are faster than typing either.
5. **New binder didn't show its empty state** — ✅ fixed. The create
   commit reloaded the card pane before moving the selection; now it
   reloads after, so the new binder's empty pane appears selected.
6. **"(~150 MB)" hardcoded in the backfill label** — ✅ done. The size
   moved into the description as prose ("a large file") — a number goes
   stale the day MTGJSON grows.
7. **Palette commands need explanations** — ✅ done. Every visible
   command carries a one-line `desc`, rendered dim under the palette's
   help line for whichever command is highlighted; the description costs
   a chrome row so the frame keeps its height.
8. **M mask missing from help lines** — ✅ done. Added to every view it
   applies on: both holdings lines, movers, watches, market. Unpriced is
   exempt by design and stays silent about it.

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
   of retail is not liquidity); per the follow-up, the flat table was the
   smell itself — the view now renders three sections stacked in one
   scrolling pane, each with its own title row and honest headers; and per
   the final follow-up the whole analysis re-anchored on **tcgplayer's
   sales-derived market price**: liquidity is buylist÷sales-price, the
   spread section became BELOW MARKET (real asks ≥25% under what the card
   actually sells for), and lone high marketplace asks — scalper noise —
   produce no rows at all. Enter on any arbitrage row opens the card
   detail. JSON schema bumped to 1.1.0 (marketUsd/belowMarket replace
   dearUsd/dearFrom/spread; ignoredListings dropped).
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
