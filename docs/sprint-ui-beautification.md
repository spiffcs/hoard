# Sprint: UI Beautification — WUBRG identity as design core

Status document for the sprint planned 2026-07-30, successor to
[sprint-portfolio-scriptability.md](sprint-portfolio-scriptability.md) (run
that sprint to completion first). Written so a fresh session — human or AI —
can resume with zero prior context. Update the status markers as phases land.

## Why this sprint

Make hoard beautiful in a craftsman-like, purposeful way, with **MTG color
identity as the design's core**: any use of color ties to the cards' WUBRG
identity and mana symbols, bubbling up from the data. If a card is white it
reads white; multicolor reads frame-gold; colorless reads wastes-grey.
UI-semantic color (gain/loss/error) is the supporting cast, kept visually
distinct from the data palette so UI state never reads as card identity.

The planning survey found hoard already has a real design system — the
flex-column `ui.Table` engine with priority-drop (`internal/ui/table.go`),
the `format.go` glyph vocabulary (em dash unknown, `×` qty, `*` estimated),
sparklines/bars, and the width-reproducible `ui.Env` — but two diverged
tiers:

- **Consistent**: everything routed `internal/report` → `ui.Table` shares
  layout, money format, and the bold/dim palette, all testable at any width
  via `HOARD_WIDTH`.
- **Inconsistent**: every mutating command hand-rolls `fmt.Printf` prose
  with literal `"  "`/`"    - "` indentation; the two-style palette is
  defined identically in three places (`internal/ui/env.go:88`,
  `internal/tui/model.go:39`, `internal/browse/view.go:19`); `browse`
  hardcodes `ui.Env{Color: true}` (`view.go:150`) so `NO_COLOR` is ignored
  inside the TUI; TTY detection exists twice (`os.Stdout.Stat()` in main.go
  vs `term.IsTerminal` in ui/env.go); movers backfill sends progress to
  stdout while pricing sends it to stderr.

So the sprint is "unify, then polish": extend the existing design system to
the surfaces that ignore it, then make the identity palette the visible
signature.

### North-star texts (what "beautiful" means here)

- **clig.dev** — human-first output on a TTY, machine-first when piped;
  outcomes → stdout, narration → stderr; honor `NO_COLOR`.
- **Charm idiom** (gum/glow restraint) — we're on bubbletea/lipgloss/bubbles;
  their exemplar apps are the visual reference, but components must earn
  their place.
- **Tufte** (data-ink ratio) — color carries meaning only; alignment and
  whitespace do the structural work; no decoration.
- **gh CLI** — the `✓ Created …` mutation-confirmation idiom.
- **Nielsen** — visibility of system status: long operations show progress.

Exemplars worth a side-by-side look: `gh`, `lazygit`, `fzf`, `delta`.

### Decisions made at planning (user-confirmed, binding)

1. WUBRG identity colors are the design core; semantic color elsewhere,
   sparing; everything honors `NO_COLOR` and degrades to plain when piped.
2. Unify then polish — consolidate theme + prose helpers first.
3. Light-touch Charm adoption — no bangwiz; every component justified.
4. Prominence: **pips column + identity-tinted card names** (gold
   multicolor, grey colorless) — not full-row theming.
5. The add-flow picker gets pips too — the catalog schema bump and its
   one-time full rebuild are accepted.

### Data reality (verified during planning)

`color_identity` and `mana_cost` are **already persisted** as SQLite
generated columns over `cards.raw_json` (migration v5,
`internal/store/migrate.go:193`). `store.CardDetail` already reads them
(`internal/store/detail.go:17`); the browse filter language already matches
on identity (`internal/store/filter.go:91`). Exposing identity to all rows
is a **projection change, not a migration**. `update-prices` already
backfills missing `raw_json` via `IDsNeedingDocuments()`
(`internal/store/printings.go:89`), so existing collections light up with no
new code. The one gap: the add-flow picker shows *unowned* cards from
`internal/catalog`, which deliberately stores no color data — closed in
phase B4. Note `scryfall.Card.BorderColor` is frame/border ("black",
"borderless"), **not** WUBRG — don't confuse them.

## The palette

**Identity (data) palette** — canonical Mana-font pip colors (the community
standard: Andrew Gioia's [mana](https://github.com/andrewgioia/mana)
project, `css/mana.css` `.ms-cost` backgrounds) as the dark-background
variants. Light-background counterparts are darkened versions to be tuned
for contrast at implementation time (starting points below):

| Identity   | Dark bg (canonical pip hex) | Light bg (tune at impl) |
| ---------- | --------------------------- | ----------------------- |
| W          | `#f0f2c0` parchment         | ~`#9a8f4d`              |
| U          | `#b5cde3` island            | ~`#2a6395`              |
| B          | `#aca29a` swamp             | ~`#6b5f66`              |
| R          | `#db8664` clay              | ~`#b0492a`              |
| G          | `#93b483` sage              | ~`#3a7d4f`              |
| Colorless  | `#beb9b2` wastes            | ~`#7d786f`              |
| Multicolor | `#d4af37` frame gold        | ~`#a8860b`              |

(The mana project also defines a brighter variant set — `#fdfbce`,
`#bcdaf7`, `#a7999e`, `#f19b79`, `#9fcba6` — if the `.ms-cost` set proves
too muted in practice.)

Delivered via `lipgloss.AdaptiveColor`; lipgloss/termenv auto-degrade
truecolor → 256 → 16. Piped or `NO_COLOR`: pips render as plain `WU`
letters — the information survives grep by construction.

**Semantic (UI) palette** — deliberately a *different family* (vivid
ANSI-16, not pastels): error bold `9`, ok `10`, warn `11`, accent bold `12`,
gain `10` / loss `9` (deltas keep `+`/`−` signs as the piped-safe channel).
Selection = `Reverse(true)`, no color, so it never fights the user's
terminal theme. **Not colored, ever**: table headers, money-in-place (only
deltas), help lines, hints.

## Order: A → B → C, then D and E in any order

A→B→C are ordered (B needs the theme; C's `Progress` line announces B4's
catalog rebuild). D and E are independent once A+B land. Each phase is one
commit, committed by the maintainer. Sizes: A=M, B=M, C=M, D=L, E=S.

## ✅ A. Theme layer (implemented 2026-07-31)

*As-landed notes:* one `Env{Color:true}` site had become three
(`browse/view.go paneLines`, `browse/market.go marketLines`, and main's
`WithReport` closure) — all now derive color from a detected Env; browse
carries `env ui.Env`/`theme ui.Theme` fields (`WithEnv` option pins them in
tests). `Env.Delta(v)` joined the planned methods so the CLI movers table
and the browse movers pane share one sign-to-color rule. The section-header
assembly moved into `report.Market` (renamed from Arbitrage before this
sprint), and the `arbitrage` flagset name became `market`. The old
`os.Stdout.Stat()` idiom is gone — `isTTY` delegates to `ui.IsTerminal`.

**A1. `internal/ui/theme.go` (new).** One definition of every style, two
consumers:

- CLI layer — extends the existing `Style func(string) string` idiom
  (preserves `table.go`'s styles-after-measurement invariant):
  `Env.OK() / Err() / Warn() / Gain() / Loss() / Accent()`, each gating on
  `e.Color` and returning `plain` otherwise, exactly like `Env.Bold()/Dim()`
  (`env.go:96-109`). Identity: `Env.Identity(colors []string) Style`
  (single → that color, 2+ → gold, `C` → wastes, empty → plain) and
  `Env.Pip(letter byte) Style`. Move `lgBold`/`lgFaint` here; rewrite the
  now-stale "bold and faint only, no color" comment at `env.go:88`.
- TUI layer — bubbletea programs need `lipgloss.Style` values:
  `type Theme struct { Title, Help, Err, OK, Warn, Accent, Prompt, Cursor,
  Inactive lipgloss.Style; Pips map[byte]lipgloss.Style; Multi, ColorlessID
  lipgloss.Style }` + `DefaultTheme()`. The Env methods wrap the same
  underlying definitions — exactly one source of truth for "error red".
- **Invariant (documented + tested):** all theme styles are SGR-only — no
  Width/Padding/Margin — so
  `ansi.StringWidth(style.Render(s)) == ansi.StringWidth(s)` for every
  style; a test loops all Theme fields asserting it.

**A2. Kill triplication + unify detection.** Delete the style var blocks in
`internal/tui/model.go:39-46` and `internal/browse/view.go:19-25`; both
models take a `ui.Theme` field. Thread a real `ui.Detect(os.Stdout)` Env
into browse (fixes the hardcoded `Color: true` at `view.go:150`; browse
honors `NO_COLOR` for the first time). Add `ui.IsTerminal(*os.File)` and
replace the `os.Stdout.Stat()` idiom (`stdoutIsTTY`/`stdinIsTTY` in
main.go). Move the arbitrage section-header assembly from `arbitrage.go:44`
into `report.Arbitrage`, restoring the "column specs live in
internal/report" contract.

**A3. First visible payoff.** Movers delta columns styled with
`Gain()`/`Loss()` — piped output unchanged because `Color=false → plain`.

**Testing.** `TestMain` pins `lipgloss.SetColorProfile(termenv.TrueColor)` +
`SetHasDarkBackground(true)` for deterministic adaptive resolution; a second
suite at `termenv.ANSI` asserts sane degradation; the width-invariant loop
from A1. Existing `report_test.go` cases (all `Color:false`) must pass
unmodified — that is the piped-output regression suite for free.

## ✅ B. Identity bubbles up from the data (implemented 2026-08-01)

*As-landed notes:* `parseColorIdentity` moved to store.go and now keeps
colorless (`[]`, empty non-nil) distinct from unknown (NULL, nil) — the
detail pane's old behavior collapsed the two. `cardScanDest` grew a
`cardAux` NullString shim; `PriceChange` and `UnpricedRow` carry identity
so B3 was pure rendering. The catalog stores the color columns with
`jsonArrayKeepEmpty` (NULL = unknown, `[]` = colorless) for the same
distinction. The hoardjson bump is **1.1.1**, not the planned 1.2.0 — a
new optional field is an ADDITION under SchemaVer's own rules (the plan
misstepped); holdings, unpriced and movers cards now carry
`colorIdentity`, and export.Row threads it from the listing queries.
The `ID` pip column drops first on narrow terminals everywhere it appears.

**B1. Store projection.** Add `ColorIdentity []string` (+ `ManaCost
*string`) to `store.Card` (`internal/store/store.go:63`); extend `cardCols`
/ `cardScanDest` (`store.go:195-203`) — the paired-edit design lights up
binder/deck/holdings queries at once. Promote `parseColorIdentity`
(`detail.go:70`) into the scan helper — the one non-mechanical bit: a
`[]string` needs a `sql.NullString` shim in the scan destinations.

**B2. Format vocabulary.** In `internal/ui/format.go` beside
`Finish`/`Qty`: `IdentityKey(colors) string` (canonical WUBRG ordering,
W<U<B<R<G) and `Pips(colors) string` (`"WU"`; `"C"` for colorless; `—` for
none/unknown, matching the em-dash convention). Styling is applied at
render via `Env.Pip`/`Env.Identity`, never inside format functions.

**B3. Tables get identity.** An `ID` column (Flex:false, low Priority so it
drops first on narrow terminals) with per-letter pip styling, plus NAME
cells tinted via the existing `ui.Cell.Style` hook (`table.go:36`):

- browse card pane (`internal/browse/view.go:192` column set)
- movers (`internal/report/movers.go:60`)
- unpriced (`internal/report/report.go:88`)
- binders table is container-level — no identity column there.

**B4. Catalog + Scryfall client (add-flow reach).** Add `colors`,
`color_identity` to the catalog schema (`internal/catalog/catalog.go:41`),
the bulk decode struct (`build.go:179`), and the insert (`build.go:296`);
bump `schemaVersion` (`catalog.go:28`) — a bump triggers a **one-time full
rebuild** (~100k printings) by design, announced via the phase-C `Progress`
line. Add `ColorIdentity` (+ `Colors`) named fields to
`scryfall.apiCard`/`Card` (`internal/scryfall/scryfall.go:27,89`) — the
add-flow picker holds a `scryfall.Card` and never touches the store.

**B5. JSON export parity (small).** Add `color_identity` to the
`internal/hoardjson` card objects + `schema/json` (SchemaVer ADDITION bump
per the rules in schema/json/README.md) — scriptability consumers get the
same data the UI renders.

## ⬜ C. Prose vocabulary for mutating commands

`internal/ui/report.go` (new). One dialect for command narration, grounded
in the patterns already in the code (headline, 2-space detail, 4-space
bullet, dim hint):

```go
// Results go to Out (stdout); progress and warnings to Err (stderr),
// each styled by its own Env — stdout piped + stderr TTY is common.
type Report struct {
    Out, Err       io.Writer
    OutEnv, ErrEnv Env
}
func NewReport() *Report // Detect()s both streams

func (r *Report) Success(format string, a ...any)  // "✓ " + OK style → Out
func (r *Report) Result(format string, a ...any)   // plain headline  → Out
func (r *Report) Detail(format string, a ...any)   // "  " indent     → Out
func (r *Report) Item(s string)                    // "    - " bullet → Out
func (r *Report) Hint(format string, a ...any)     // dim advisory    → Out
func (r *Report) Warn(format string, a ...any)     // "  ! " Warn     → Err
func (r *Report) Progress(format string, a ...any) // dim narration   → Err

func Confirm(in io.Reader, out io.Writer, q string) (bool, error) // [y/N]
```

Stream policy per clig.dev: the command's *outcome* → stdout; *narration
about work in progress* and *partial-outcome warnings* → stderr. The `✓`
glyph is emitted always (joining the `×`/`—`/`*` vocabulary); color only on
a TTY.

Migrations: `add.go` (Success), `import.go:172-198`
(Result/Warn/Item/Hint), `deck.go:97-146`, `export.go:79`, `catalog.go`
(narration → Progress; the raw `fmt.Scanln` confirm at `catalog.go:209`
becomes `ui.Confirm`, replacing the `confirmFn` indirection with an
injected Reader), `pricing.go:19-53` (already stderr — gains styling),
`movers.go:111` (**fixes the stream bug**: backfill progress currently goes
to stdout), `main.go:138`.

Testing: injected buffers; exact output at `Color:false`, SGR presence at
`Color:true`; `Confirm` via `strings.Reader`.

## ⬜ D. Visible polish (independent slices)

**D1. Browse.** Focused-pane title in Accent, unfocused in Dim (today focus
is only discoverable via the cursor). `ui.Restyle(line, style)` (new
`internal/ui/overlay.go`, ~40 lines): tokenizes with `x/ansi` and
re-asserts the selection SGR after every embedded reset, so the reverse bar
at `view.go:249` stops `ansi.Strip`-ing row styling — *required* for
identity colors to survive selection. Timeboxed; the documented fallback is
the status quo (strip + reverse) behind `theme.Cursor`. **Keep** the
hand-rolled pane math (`paneWidths`/`window`/`fit` — tested,
windowing-aware; `lipgloss.JoinHorizontal` would be decoration, not craft).

**D2. Detail pane.** Render `mana_cost` (`{2}{W}{U}`) with colored pips;
card-name header tinted by identity — the detail view becomes the fullest
expression of the identity theme (`internal/browse/detail.go:66`).

**D3. Add-flow.** Custom single-line list delegate
(`internal/tui/delegate.go`, ~60 lines): Accent `▌` marker, **identity pips
after the card name** (data from B4), Dim annotations — replacing
`list.NewDefaultDelegate()`'s two-line purple default (`model.go:243`, the
one place hoard looks like someone else's app). Batch progress: replace the
`"card %d of %d"` text badge (`model.go:~1119`) with `bubbles/progress`
(solid Accent fill, `WithoutPercentage`, width ~24, shown only when
`batchTotal > 1` — a true fraction exists, so a bar is honest). Spinner
stays `spinner.Dot`; textinput prompt/cursor styled from Theme. **First
step: verify `bubbles/progress` builds against pinned bubbles v1.0.0 /
lipgloss v1.1.0** — fallback is rendering the existing `ui.Bar` in the
View, zero new deps.

**D4. Long-op status for plain-CLI commands.** `Report.Step(done, total
int, label string)`: a `\r`-updating `ui.Bar` line on TTY stderr, falling
back to occasional `Progress` lines when piped. Only where a true fraction
exists (catalog build card count — which now includes the B4 rebuild —
backfill printings processed). **No fake progress bars** for
unknown-duration stages (MTGJSON fetch); those keep narration lines.

Coordinate with phase F of the previous sprint (progress UI) if it slipped:
D4 is its concrete descendant — fold that design debt in here.

## ⬜ E. Usage/help text

Extract the ~70-line hand-aligned `usage` string (`main.go:26-79`) into
structured `{invocation, description}` sections in a new `usage.go` (main
package), rendered via `ui.Table` at `ui.Detect(os.Stderr)` width — the
first output surface that responds to terminal width. Section headers Bold,
prose paragraphs Dim. The `"error:"` prefix at `main.go:89` gets Err style
on a TTY. Per-subcommand `-h` stays stock `flag` output; no cobra.

## ⬜ F. Spike: card images in the detail overlay + MTG-card layout

Added 2026-07-31. Two commits: F1 (layout — real work, ships regardless)
then F2 (images — timeboxed, go/no-go deliverable). Runs last; needs D2.

**Ground truth (verified):** `cards.raw_json` is the full Scryfall object,
so `image_uris` (+ per-face for DFCs) is already on disk for every enriched
card. `x/ansi v0.11.6` (already a direct dep) ships the whole graphics
toolkit: `kitty.EncodeGraphics` with `VirtualPlacement`/Unicode-placeholder
support, `ansi/iterm2` OSC 1337 files, `ansi.SixelGraphics`. Browse runs
altscreen with a cell-diffing renderer, so cursor-anchored graphics get
clobbered by repaints — kitty's Unicode-placeholder mode is the exception
(the placement is ordinary text cells, so it flows through View() and
survives repaints; Ghostty/Kitty/WezTerm support it). Cached image bytes
stay out of hoard.db (the VACUUM INTO backup rule): files under
`os.UserCacheDir()/hoard/images/`, mtgjson's temp-write→rename pattern.

**F1 — MTG-card-style detail layout.** Migration v11: VIRTUAL generated
columns `power`, `toughness`, `loyalty`, `flavor_text` (COALESCE-to-face-0
idiom) + `image_uri`; extend `store.CardDetail`. Reorder `detailLines` into
card-frame order (name+mana cost → type·rarity → oracle box → flavor →
P/T bottom-right → artist·set footer → HELD/PRICE below). Fix `wrap()` to
measure `ansi.StringWidth`, not bytes. Card block renders first so the
no-scroll overflow eats hoard data, not the card. Unenriched keeps its
remedy line; enriched-but-fieldless renders `—`.

**F2 — image tiers (prototype behind `HOARD_CARD_IMAGES=1`).** Async fetch
on detail open (cache-first, never when piped/`NO_COLOR`/unsupported).
Tier 1: kitty graphics + Unicode placeholders (Ghostty/Kitty/WezTerm;
needs cell-pixel geometry via ioctl ws_xpixel / CSI 16t). Tier 2: iTerm2
OSC 1337 — spike-test against altscreen repaints, cut to tier 3 if it
tears. Tier 3: halfblock `▀` truecolor cells of the art_crop — universal
fallback, pure text. Deliverable: findings note here + go/no-go on
default-on. Cut line: ship halfblocks only.

## Non-goals (the craftsman list)

- No full-row rainbow theming — identity lives in pips + name tint + detail
  pane only.
- No cobra/CLI-framework migration; no restructuring of flag dispatch.
- No lipgloss borders, rounded boxes, gradients; no glamour/markdown.
- No `bubbles/help`+`key` — the hand-written context-sensitive help lines
  (`view.go:319`) are already good; revisit only if keybindings become
  configurable.
- No `bubbles/viewport` for the detail overlay (future sprint candidate).
- No color on table headers, totals, or money-in-place; no emoji beyond the
  existing 📷 and the new ✓.
- The semantic palette never borrows identity pastels (and vice versa) —
  two visually distinct families, always.

## Verification discipline

```sh
go build ./... && go vet ./... && gofmt -l . && go test ./...
```

- Existing `ui_test.go` (386 lines) and `report_test.go` (337 lines) must
  pass unmodified through phase A — the piped-output regression suite.
- New golden tests at pinned `HOARD_WIDTH` + pinned color profile
  (truecolor and ANSI suites; `SetHasDarkBackground` pinned in TestMain).
- Manual pass: `NO_COLOR=1 hoard` (browse must be monochrome — new
  behavior); `hoard movers | cat` (zero SGR, pips as plain letters);
  `hoard import x.csv > log` (stderr narration still styled); light **and**
  dark terminal pass over the identity palette; add a multicolor, a
  mono-color, and a colorless card and eyeball pips/tints in browse,
  detail, movers, and the add-flow picker; catalog rebuild announcement on
  first run after upgrade.

## Risks and cut lines

- **Cut order (first → last)**: E usage → D1 `Restyle` (fallback is status
  quo) → D3 progress bar (fallback `ui.Bar`) → B4 catalog/add-flow pips
  (identity everywhere the *store* reaches still ships). **Never cut**: A
  theme unification and B1-B3 — they are this sprint's positioning.
- Light-background contrast of the pastel pips: adaptive dark-on-light
  variants must be tuned by eye + a contrast check; the table above is a
  starting point, not gospel.
- `ui.Restyle` ANSI re-assertion is subtle (nested SGR, OSC): pure
  function, exhaustive table tests, timeboxed, documented fallback.
- Catalog `schemaVersion` bump forces the ~100k-printing rebuild on first
  run — must announce clearly via `Report.Progress`, never hang silently.
- `raw_json` is NULL for never-refreshed cards → `—` pips until
  `update-prices` runs; `EnrichedCount` (`filter.go:129`) distinguishes
  "no traits stored" from "no match" — consider a one-line `Hint`.
- Adaptive-color background detection is process-cached by lipgloss; tests
  pin it via `SetHasDarkBackground`.

## The way forward (after this sprint)

Unchanged from the previous sprint's list — distribution (goreleaser +
Homebrew), backup/doctor, want lists, condition/language columns — plus new
candidates surfaced by this planning: detail-overlay scrolling
(`bubbles/viewport`), configurable keybindings (which would justify
`bubbles/help`+`key`), and identity-based sort/filter keys in browse
(`sort.go:75` has no color key; the `color:` filter already exists).
