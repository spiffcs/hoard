# What the command palette does not reach

An audit of hoard's TUI command palette (`:` / `ctrl+p`): which functionality it
can run, which it cannot, and which of those gaps look deliberate. **For
review** — nothing here is a decision, and no code changes on its account.

**Derived: 2026-08-23**, by reading `internal/browse/command.go` (the command
table), `internal/browse/palette.go` (what the palette filters), the key
handlers in `internal/browse/model.go` and `internal/browse/textview.go`, and
`hoard --help`. Counts were taken by parsing the command table rather than by
eye. Every row below was checked against the code, not recalled.

**Includes deck folders and folding**, added the same day: `deck.move` (key
`m`), `folder.new` (palette-only), and `space` to fold a folder. `RenameBinder`
became `RenameSelected` (`rename`), which now renames binders, decks and
folders. Counts below include all of it.

---

## Bottom line

The command table holds **43 commands. 32 reach the palette; 11 are hidden from
it** and exist only as key bindings. Separately, a dozen pieces of real
functionality have **no command entry at all** — they are `case` arms in a key
handler, so they are neither in the palette nor in the command table that the
palette and the help line are both built from.

The single clearest gap is **`x`, include/exclude a binder or deck from your
collection totals**. It changes what every value in the app reports, it has a
CLI equivalent in `hoard binder exclude|include` and `hoard deck
exclude|include`, and it is reachable in the TUI only by knowing the letter.

---

## Method, and what "unreachable" means

A command reaches the palette when it is in `commands()` and is not marked
`hidden: true`, subject to its `where`/`hide` predicates. Anything handled
directly in `handleBrowseKey`, `handleDetailKey`, or the modal handlers is
invisible to the palette by construction — the palette can only run entries in
the command table.

Pure navigation (arrows, `tab`, `pgup`/`pgdown`, `home`/`end`, focus movement)
is excluded from every table below. A palette entry for "move the cursor down"
would be noise, and no judgement is being asked about it.

---

## 1. Hidden commands — a key, but no palette entry

These are in the command table with `hidden: true`. They are discoverable only
through the help line or by being told.

| ID | Key | What it does |
| --- | --- | --- |
| `sort` | `s` | Cycle the sort column of the focused table |
| `sort.reverse` | `S` | Reverse the current sort |
| `floor.cycle` | `M` | Cycle the price floor that hides cheap rows |
| `view.next` | `v` | Next view (holdings → movers → market → dip → watches) |
| `movers.window` | `W` | Cycle the movers lookback: 7 / 30 / 90 days |
| `table.next` | `]` | Next table within a multi-table view |
| `table.prev` | `[` | Previous table |
| `page.next` | `>` | Next page of a long table |
| `page.prev` | `<` | Previous page |
| `market.buylist.band` | `b` | Toggle the market buylist band |
| `market.comps.side` | `b` | Toggle which side comps are shown from |

**Assessment (a judgement, not a measurement):** most of these are *repeat-press
cycles* — you press `s` four times to reach the column you want. A palette
entry that runs one step of a cycle is a poor fit, so hiding them is defensible.
The two that read least like cycles are `movers.window` and `floor.cycle`, both
of which set a value the user may want to reach by name.

---

## 2. Functionality with no command entry at all

These are `case` arms in `handleBrowseKey` (`internal/browse/model.go`). They
are not in the command table, so they appear in neither the palette nor
anything else generated from it.

| Key | What it does | Has a CLI equivalent? |
| --- | --- | --- |
| `x` | Include/exclude the selected binder or deck from collection totals | **Yes** — `hoard binder exclude\|include`, `hoard deck exclude\|include` |
| `space` | Fold or open the selected folder | No |
| `+` / `=` | Increase the selected card's quantity | No |
| `-` / `_` | Decrease the selected card's quantity | No |
| `enter` | Open the card detail view (or confirm a watch pick) | No |
| `/` | Open the filter | No |
| `shift+↑` / `shift+↓` | Extend the multi-row selection | No |
| `esc` | Clear selection, clear the filter, cancel a watch pick, cancel a market fetch | Partly — `op.cancel` covers cancelling an operation |

**Assessment:** `x` is the outlier and the reason this audit was worth doing.
`space` is deliberate — folding acts on the row under the cursor, so the same
argument applies as below; a *fold all / open all* pair would earn palette
entries and does not exist yet.
The others are direct manipulations of the thing under the cursor — a palette
entry for "increase quantity" would still need you to have selected the card
first, so the palette adds nothing. `/` (filter) is a plausible palette
candidate on discoverability grounds.

---

## 3. The detail view

Opening a card's detail view narrows the palette to six IDs, hardcoded in
`detailPaletteIDs` (`internal/browse/palette.go`): `op.update-prices`,
`op.backfill`, `op.backfill.90`, `op.repair-finishes`, `market.fetch`,
`op.cancel`.

Everything else the detail view can do is key-only and has no palette route:

| Key | What it does |
| --- | --- |
| `+` / `-` | Adjust the quantity of the highlighted holding |
| `d` | Remove the highlighted holding |
| `enter` | Edit the highlighted field, or open the highlighted link |
| `tab` / `shift+tab` | Move between the holdings zone and the links zone |
| `←` / `→` | Move between fields, or between links |

**Assessment:** the narrowing is deliberate — the whitelist is exactly the
long-running operations that make sense while looking at a card. The key-only
detail actions all act on the highlighted row, so the same argument as §2
applies.

---

## 4. Modes where the palette cannot be opened

`handleKey` dispatches by mode, and only some modes forward `:` / `ctrl+p`.

| Mode | Palette reachable? |
| --- | --- |
| Browse | Yes |
| Detail | Yes, narrowed to six commands (§3) |
| Text view (e.g. the valuation report) | Yes |
| Prompt | **No** — `:` is typed into the field, which is correct |
| Filter | **No** — same |
| Confirm (y/n) | **No** — any key that is not `y` cancels |
| Add cascade (`a`) | **No** — keys are forwarded to the child model |

**Assessment:** all four "no" rows are correct as they stand. A modal prompt
that swallowed `:` as a command would be a bug, not a feature.

---

## 5. CLI functionality with no TUI route at all

Not a palette gap so much as a TUI gap — these have no key *and* no command.

| CLI | TUI status |
| --- | --- |
| `hoard guessed` | **No TUI surface.** Scanned finishes nothing on the card chose |
| `hoard refused` | **No TUI surface.** Prices replaced by the cheapest ask |
| `hoard vacuum` | **No TUI surface.** Delete orphaned printings |
| `hoard merge` | **No TUI surface.** Fold another hoard database in |
| `hoard deck repin` | **No TUI surface.** Re-pin a deck to a set |
| `hoard folder rename` | Covered — `R` renames binders, decks and folders |
| `hoard folder rm` | **No TUI surface.** `d` on a folder refuses and names the CLI |
| `hoard deck rename` | Covered — `R` on a deck |
| `hoard deck add --rename-from-source` | **No TUI surface.** Takes an imported name back |
| `hoard folder new` | Covered — palette-only `folder.new` |
| `hoard deck move` | Covered — key `m`, palette `deck.move` |
| `hoard folder list` | Covered by the sidebar itself |
| `hoard binder exclude\|include` | Key `x` only — see §2 |
| `hoard deck exclude\|include` | Key `x` only — see §2 |

**Assessment:** `merge` and `vacuum` are destructive-ish maintenance and are
arguably right to stay CLI-only, where a `--dry-run` and a scriptable exit code
mean something. `guessed` and `refused` are *review queues* — lists of things
hoard was unsure about — and a review queue with no home in the browser is the
kind of gap that leaves data quietly unreviewed. Those two look like the most
substantive omissions in this table.

---

## Open questions for review

1. **Should `x` become a palette command?** It is the only gap in §2 that
   changes reported totals rather than one row under the cursor. Suggested
   entry: `binder.counted`, title `IncludeBinderInTotals` /
   `ExcludeBinderFromTotals`, keeping `x` as the shortcut.
2. **Should `guessed` and `refused` get TUI views?** They are review queues
   with no browser surface at all (§5). This is the largest item here and is
   a feature, not a palette entry.
3. **Should `floor.cycle` and `movers.window` stop being hidden?** They set a
   value rather than stepping a cycle, so naming the value in the palette may
   read better than pressing a key repeatedly.
4. **Should `/` (filter) get a palette entry** purely for discoverability?
5. **`folder rename` / `folder rm` in the TUI.** Deferred deliberately while
   building deck folders; `d` on a folder currently refuses and points at the
   CLI. Worth deciding once folders have been used in anger.

## What this document does not claim

- No measurement of *use*. Nothing here says which commands anyone reaches for;
  the assessments are readings of the code and of intent, and are labelled as
  judgements where they are.
- No completeness claim about future commands. The tables were parsed from the
  tree on the date above and will drift as commands are added.
