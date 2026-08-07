# hoard as a terminal-medium app

**Status: design exploration, 2026-08-06. Nothing built. No decision made.**

Nothing here is committed to. Stage 0 exists to answer the questions that would
change the plan, and its answers belong back in this file.

---

## Context

`hoard` is a Go terminal program. To use it you clone a repo, build a binary,
and know what a terminal is. That is a hard ceiling on who can ever use it —
TCG players are not, mostly, people with a Go toolchain.

The ambition: **Bloomberg Terminal for TCG.** Keep everything that makes the
TUI good — density, keyboard-first, instant, no chrome, numbers over
decoration — and deliver it as an app you install and open by clicking an icon.
cmux is the reference for how a native macOS app can carry terminal feel
without being a terminal.

Decisions already made by the owner:

| | |
|---|---|
| **Target** | **macOS.** Not iPad. If an iPad version happens it is its own independent app at a future date — we do not pay for a port we have not validated |
| **Scope** | Rewrite the engine *and* the UI. Explore a **Swift + Rust + SQLite** stack |
| **CLI fate** | The app becomes the product. `hoard` the binary drops to scripting-only (`export`, `watch`, `import`) once the app lands |
| **cmux** | Inspiration only. It is GPL-3.0, `hoard` is MIT — read it, do not copy it |
| **libghostty instability** | Accepted. The owner trusts the project's direction and wants a plan to adopt the Swift renderer when it lands |

---

## What we are actually building

"Terminal feel without a shell" is not one thing. It is a set of properties,
and each has a cost. Worth naming them, because the ones that matter are cheap
and the one people assume they need is the expensive one.

| Property | Stance |
|---|---|
| Monospace everywhere, fixed cell grid | **Required.** A font and layout discipline |
| Dense: every pixel is data, no padding, no cards-with-shadows | **Required.** It is a design constraint, it is free |
| Keyboard-first, modal, `:` command line, `/` filter | **Required.** A command router and a key map |
| Column-aligned output that survives resize | **Required.** Already solved in `internal/ui/table.go` |
| **Select text and it copies, no ⌘C** — the way Claude Code does | **Required.** Named explicitly because it constrains the grid design: the cell viewport has to carry a real text model with selection, not just draw glyphs. Cheap if designed in, expensive if bolted on |
| Instant | **Required.** Everything user-facing is a local SQLite read |
| Spinners and animation | **Sparingly, and argued for.** The current TUI uses them where a real wait exists (`internal/progress`, catalog build, price refresh). Not banned. But every new one has to justify itself in the PR — the default is that a thing appears finished, not that it animates into place |
| ANSI escape sequences, PTY, VT100 emulation | **Only if free.** We take it if a library hands it to us as a side effect. We do not build it, and no feature depends on it |

---

## The libghostty question

**We do not need a terminal emulator to start.** A terminal emulator parses
ANSI bytes from a subprocess into a character grid. With no shell and no
subprocess, nothing emits ANSI, so there is nothing to parse. `libghostty-vt`
— the parser and terminal state, and the half targeted for a stable tagged
release first — is the half we have no use for.

What we want is the other half: the **GPU cell renderer** — font atlas,
ligatures, sub-pixel positioning, fast scroll of a dense text grid. Mitchell
has announced a **pure-Swift Metal renderer with libghostty-vt bindings**,
packaged to drop into an Xcode project. That is exactly our component. It is
not shipping yet.

**Plan: build a `GridView` protocol now with our own Core Text/Metal backing,
and adopt GhosttyKit as a second backing when it ships.**

The seam is one protocol:

```
protocol CellSurface {
    func resize(cols: Int, rows: Int)
    func write(cells: CellBuffer)          // our own path
    func selection(from: Cell, to: Cell) -> String   // copy-on-select lives here
}
```

Two implementations behind it: `CoreTextSurface` (ours, ~2–4k lines, ships
Stage 2) and `GhosttySurface` (theirs, adopted when available). The adoption
work is then bounded and schedulable rather than a rewrite — see Stage 5.

Adopting it also gives us the ANSI path for free: with `libghostty-vt` under
the surface, any future need to render real terminal output — a log, a
`hoard export` preview, an embedded shell for power users — becomes free rather
than a project. That is the only reason to want VT emulation, and it is the
right reason: we get it as a side effect, not as work.

### Cell grid vs. native SwiftUI — the hybrid

A pure cell grid gives up native text selection, accessibility, and real
images. Pure SwiftUI gives up density and feel.

**Bloomberg's own answer is the hybrid.** A native shell — window chrome,
panes, command bar, sheets, card art — hosting **cell-grid viewports** for the
data-dense regions: holdings, movers, market, sparklines, the ledger. The grid
is one reusable component. Card images become real images instead of the
Kitty-protocol trick in `internal/ui/image.go`, which is a straight upgrade.

---

## The stack

```
┌─────────────────────────────────────────────────────┐
│  Hoard.app  (Swift, macOS)                          │
│   • SwiftUI shell: panes, command bar, sheets       │
│   • CellSurface: CoreText now, Ghostty later        │
│   • Command router: ":" verbs, "/" filter, keymap   │
│   • ScanKit  ← already exists, 12.6k lines of Swift │
└──────────────────────┬──────────────────────────────┘
                       │  UniFFI-generated Swift bindings
┌──────────────────────┴──────────────────────────────┐
│  hoard-core  (Rust staticlib)                       │
│   • store        — schema, migrations, queries      │
│   • scryfall     — client, rate limits, cache       │
│   • mtgjson      — bulk ingest, 7z/PPMd             │
│   • tcgcsv       — daily price archives             │
│   • pricing / market / movers / report              │
│   • resolve / cardname / import / export            │
└──────────────────────┬──────────────────────────────┘
                       │  rusqlite (bundled SQLite)
┌──────────────────────┴──────────────────────────────┐
│  hoard.db  — schema v23, no extensions, published   │
└─────────────────────────────────────────────────────┘
```

### Why Rust — stated honestly

Dropping the iPad commitment **removed the hard argument.**
`modernc.org/sqlite` does not support iOS, which would have forced a rewrite of
`internal/store`; on macOS it is fully supported and the existing Go engine
works fine. Keeping Go and binding it as a `c-archive` is a real, much cheaper
option — it saves roughly 18k lines of porting.

What is left for Rust is weaker but not nothing:

- **Binding ergonomics.** UniFFI generates a Swift package with records, enums,
  typed errors and async. A Go `c-archive` gives you a C header: manual memory
  discipline across the boundary, hand-marshalled structs, a Go runtime and GC
  living inside the app process.
- **Option value on iPad.** Not a commitment to port. But if an independent
  iPad app is ever wanted, a Rust core makes it a UI project rather than an
  engine project. Go makes it an engine project, today, guaranteed.
- **One fewer runtime in the app bundle.**

Against it: ~18k lines of working, tested Go rewritten for no user-visible
gain, and a third toolchain in CI.

**This should be a Stage 0 decision, not an assumption.** Spike both bindings
before committing. The rest of this plan is written for Rust because that is
the direction asked for, and every stage after Stage 1 is unchanged either way.

Crates: `rusqlite` (bundled), `reqwest`, `serde`, `sevenz-rust2` (native PPMd,
no C compiler), `uniffi`.

---

## What moves where

Non-test Go lines, current tree:

| Package | LOC | Destination |
|---|---|---|
| `internal/browse` | 9,529 | **Swift** — the browser UI |
| `internal/store` | 5,243 | **Rust** — schema, migrations, queries |
| `internal/tui` | 5,010 | **Swift** — the add cascade |
| `internal/ui` | 2,068 | **Swift** — theme, table, sparkline, overlay; image path retired |
| `main.go` + root | 2,557 | Split: verbs become core calls, arg parsing stays in a slim Go CLI |
| `internal/action` | 1,767 | **Rust** |
| `internal/catalog` | 1,190 | **Rust** |
| `internal/mtgjson` | 1,133 | **Rust** |
| `internal/scan` | 774 | **Swift** — already talking to Swift on the other end |
| `internal/pricing` | 712 | **Rust** |
| `internal/hoardjson` | 688 | **Rust** |
| `internal/scryfall` | 678 | **Rust** |
| `internal/market` | 666 | **Rust** |
| `internal/report` | 627 | **Rust** |
| `internal/tcgcsv` | 393 | **Rust** (see PPMd risk) |
| `decksource` / `collsource` / `watchsource` / `export` / `cardname` / `resolve` / `progress` | 1,425 | **Rust** |

Roughly **17k lines to Swift, 18k to Rust**, against ~24k lines of Go tests
that are the real asset and do not port automatically.

Two pieces of leverage not obvious from the table:

1. **`scan/` is already 12,648 lines of shipping Swift** — `ScanKit`,
   `CardKit`, `PeerLink`, `Pairing`, `SessionLog`. Today the Mac side of that
   link is Go (`internal/scan`) speaking a wire protocol to Swift on the phone.
   In the new stack **both ends are Swift**, and pairing/transport becomes one
   shared package instead of two implementations. That also puts the unresolved
   TLS-PSK problem (`docs/app-store-release.md`, blocker 1) back on
   Network.framework at both ends, where it has a better chance.
2. **`scan.KindRemote` and `remoteKind` are matched by string equality across
   the wire.** They move together or pairing breaks.

---

## The migration contract: `hoard.db`

This is what makes the rewrite incremental rather than a big bang.

`schema/sqlite/README.md` already commits to plain SQLite, no extensions, no
custom functions, version in `PRAGMA user_version`, `application_id` =
`0x484F5244`, immutable released schema files, and an explicit promise that "a
Go or Rust driver" can read it.

That promise is now load-bearing:

- The new core and the Go binary **open the same file** for the whole
  transition. Nothing has to be ported before anything else.
- Every port lands with a **differential test**: run the Go path and the new
  path against the same fixture database, diff the output. Same method already
  proven twice in this repo (oracle-diff on the scanner refactor, and
  `docs/scanner-tuning.md`). It turns "did I port this right" from a judgement
  call into a build failure.
- The Go test corpus stays useful as an **oracle** even though the test code
  does not port.
- Migrations get written **once**. Freeze the Go migration list at v23 the
  moment the new core takes over writes — two append-only lists will diverge.

---

## Stages

Each stage is independently useful; nothing is thrown away.

### Stage 0 — Prove the seams (spikes, ~1–2 weeks)

Five things that would each change the plan. Do them before anything else.

1. **Go-as-library vs. Rust core.** Bind one non-trivial call — `report`, which
   returns a nested structure — twice: once as a Go `c-archive` with a C
   header, once as a Rust `hoard-core` through UniFFI. Compare the Swift you'd
   have to write against each. **This is the 18k-line decision.**
2. **PPMd.** `internal/tcgcsv/ppmd.go` exists because tcgcsv archives carry
   only five property bytes and upstream `bodgit/sevenzip` rejects them —
   `hoard` registers a lenient decoder. Verify `sevenz-rust2` either accepts
   them or exposes the same registration hook.
3. **`CellSurface` feasibility.** Render 5,000 rows of monospace cells at
   120fps, with resize, and **selection that copies on release**. If this is
   unpleasant to build, revisit the SwiftUI-only option before committing.
4. **Ghostty adoption readiness.** Read the announced Swift package's shape.
   Confirm the `CellSurface` protocol as drafted could actually sit on top of
   it, and record what would have to change if not.
5. **App Store vs. DMG.** Sandboxing determines where `hoard.db` can live,
   which Stage 2 depends on. cmux chose DMG + Sparkle.

**Done when:** five answers, written into this doc.

### Stage 1 — `hoard-core` reads

Read paths only: `store` queries, `report`, `market`, `movers`, `pricing`
reads, `resolve`, `cardname`. No writes, no network.

Differential-test every one against the Go binary on a shared fixture database.

**Done when:** the new core reproduces `hoard report`, `hoard movers`,
`hoard market` and `hoard unpriced` byte-for-byte against the Go binary.

### Stage 2 — The Mac app, read-only

SwiftUI shell + `CoreTextSurface` + command router. The five browse views —
holdings, movers, market, watches, unpriced — plus card detail. Read-only: no
`+`/`-`/`d`/`u`, no add cascade.

This is where the medium is actually decided. Budget for throwing the first
shell away.

**Done when:** you would rather open the app than run `hoard` to *look* at your
collection. Copy-on-select works everywhere text appears.

### Stage 3 — Writes and the add cascade

Core: `action`, `store` writes, migrations, undo. Swift: the add cascade
(`internal/tui`), inline edits, binder and deck management.

Migrations move here and the Go list freezes at v23.

**Done when:** `docs/parity.md` is re-derived for the app, and every key in
`docs/browsing.md` has an app equivalent or an explicit "dropped, because".

### Stage 4 — Network and ingest

Core: `scryfall`, `mtgjson`, `tcgcsv`, `catalog`, `import`/`export`,
`decksource`, `collsource`, `watchsource`.

This stage carries the **licensing P0s** — `docs/data-licensing.md`. Two
Scryfall per-endpoint rate limits and a missing Fan Content notice are open,
and they gate anything shipped publicly. Rewriting the client is the moment to
fix the rate limits properly rather than port the bug.

This is also where spinners are legitimately earned. `internal/progress` is the
existing vocabulary; carry its restraint over, do not invent a second one.

**Done when:** the app builds a catalog and refreshes prices from cold, rate
limits enforced in one place, Fan Content notice visible in-app.

### Stage 5 — Adopt the Ghostty Swift renderer

Trigger: the pure-Swift Metal renderer package ships. Not schedule-driven —
this stage waits, and everything before it is complete without it.

Work: implement `GhosttySurface` against `CellSurface`, run both backings side
by side behind a flag, compare frame timing and text fidelity on the same
views, then make it the default and keep `CoreTextSurface` as the fallback for
one release.

The payoff beyond rendering: `libghostty-vt` comes with it, so ANSI/VT
rendering becomes available for free if a future feature ever wants it.

**Done when:** the app runs on `GhosttySurface` by default with no visual
regression, and the fallback flag still works.

### Stage 6 — Scanner reunification

Collapse `internal/scan` and `ScanKit`'s Mac-facing protocol into one Swift
package. Revisit TLS on Network.framework at both ends.

**Done when:** pairing and capture work Swift-to-Swift, and
`docs/app-store-release.md` blocker 1 is either fixed or consciously accepted.

### Stage 7 — Ship

`docs/app-store-release.md` applies, plus a record for the Mac app (or a
notarization + Sparkle path, per the Stage 0 decision). Slim the Go CLI to
scripting verbs and say so in the README.

---

## Risks and open questions

**Risks, ranked.**

1. **This is a rewrite of working, tested software with no user-visible gain
   until Stage 2 lands.** The UI has to be rewritten regardless of language —
   that part is unavoidable. The engine rewrite is a choice, and Stage 0 spike 1
   exists to make it a deliberate one.
2. **The 24k lines of Go tests do not port.** The differential-testing
   discipline in Stage 1 is not optional; it is the only thing standing between
   this plan and silently losing years of encoded correctness.
3. **PPMd** — Stage 0, spike 2.
4. **Data licensing gates release, not the build.** The three P0s in
   `docs/data-licensing.md` block any public distribution. Scryfall image usage
   deserves a fresh read of their policy — "terminal tool for me" and
   "distributed app" are not the same posture.
5. **Two languages plus a frozen Go CLI is three toolchains in CI.**
   `docs/release-engineering.md`'s goreleaser plan covers none of it.

**Open questions to answer before Stage 1.**

- Does the Go CLI keep the *write* verbs, or become genuinely read-only? If it
  keeps writes, migrations must exist twice, which the plan says not to do.
- Where does `hoard.db` live once there is an app? Today it is a user-chosen
  path. Sandboxing (Stage 0, spike 5) decides this, and existing users need an
  import path either way.
- Does the app carry the name `hoard`, or a third name beside `hoard` and
  `Hoardling`?
- **"Bloomberg Terminal for TCG"** — TCG, not Magic. Today the whole data layer
  is Magic-specific (Scryfall, MTGJSON, TCGplayer's Magic catalog). If the
  framing is deliberate, the Rust core's type model should not hard-code
  Magic's vocabulary at the boundaries. That is a cheap decision now and an
  expensive one later. Worth answering before Stage 1.

---

## Verification

| Stage | Check |
|---|---|
| 0 | Five spike answers written into this doc |
| 1 | `diff <(hoard report) <(hoard-core report)` empty across a fixture DB and a real one |
| 2 | Owner prefers the app for reads. Subjective, and the only honest test. Copy-on-select verified in every view |
| 3 | Every key in `docs/browsing.md` accounted for; `docs/parity.md` re-derived |
| 4 | Cold catalog build + price refresh; rate limits verified against Scryfall's documented per-endpoint limits |
| 5 | Both surfaces render the same views identically; Ghostty default, fallback flag works |
| 6 | Pair and capture a real box of cards |
| 7 | A stranger installs it and adds a card |

---

## What is already true and helps

- **The database format is published, versioned and immutable per release.**
  The single thing that makes an incremental port possible.
- **`scan/` is already 12.6k lines of shipping Swift**, with pairing, HMAC
  handshake, Keychain storage and an on-device Vision read pipeline.
- **No cgo anywhere in the Go tree** — the port target is clean, not an FFI
  tangle.
- **`internal/ui/table.go`, `spark.go`, `bar.go`, `theme.go` are the visual
  grammar**, already worked out. They are a specification for `CellSurface`,
  not code to be thrown away.
- **The WUBRG identity system** (`internal/ui/palette.go`) carries over
  unchanged.
- **`internal/progress` is the existing restraint around spinners.** Carry the
  vocabulary, not just the behaviour.
- **The oracle-diff / differential-test method** is already proven twice here.
