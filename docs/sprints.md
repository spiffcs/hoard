# Sprints

The index of hoard's sprint documents: what shipped, what is planned, and
the order the pending work should land in. Each sprint has its own status
document (linked) written so a fresh session — human or AI — can resume it
with zero prior context. **Update this file whenever a sprint starts,
lands, or gets reordered**; the per-sprint docs track phase-level status.

## Order of pending work

Parity before beautification is deliberate: polishing half a surface would
lock the CLI/TUI skew in. Distribution follows once the product surface is
stable and looks the part.

| # | Sprint | Status | Doc |
|---|--------|--------|-----|
| 1 | Binders + interop | ✅ complete | plan pruned; see git history |
| 2 | Portfolio + scriptability | ✅ complete (A–E; F superseded by parity) | [sprint-portfolio-scriptability.md](sprint-portfolio-scriptability.md) |
| 3 | Parity — action layer, progress, palette | ✅ complete (2026-07-31) | plan pruned; the contract lives in [parity.md](parity.md) |
| 4 | TUI completion — seamless add + parity ledger | ✅ implemented 2026-07-31 | plan pruned; the add flow lives in [browsing.md](browsing.md) |
| 5 | UI beautification — WUBRG identity | ✅ implemented 2026-08-01 (A–F; live visual smoke pending) | [sprint-ui-beautification.md](sprint-ui-beautification.md) |
| 6 | Distribution | 💭 backlog (unplanned) | — |
| — | App Store release (placeholder, nothing attempted) | ⬜ not started | [app-store-release.md](app-store-release.md) |
| 7 | iPhone capture head — a third scan source | 🚧 in progress (A–C done, D building) | [sprint-iphone-capture-head.md](sprint-iphone-capture-head.md) |

## One-line summaries

- **Binders + interop** (complete): named binders, CSV export/import with
  ledger dedupe, TUI destination picker, multi-card + hands-free scanning,
  plus a hardening interlude (stderr/exit-code discipline, atomic writes,
  network resilience).
- **Portfolio + scriptability** (complete): `--json` documents with
  generated versioned schemas, `hoard report`, value snapshots + the
  header sparkline, `hoard watch` with exit-3 alerts, bulk paste entry.
  Phase F (progress UI) was superseded — it became a pillar of the parity
  sprint.
- **Parity** (complete): every capability is an action function with a
  uniform progress contract; CLI and TUI are thin frontends; the TUI has a
  `:` command palette, in-TUI update-prices/repair/catalog with live
  progress, a watches view + fired-watch banner, binder management, and
  movers window cycling.
- **TUI completion** (implemented): the add cascade embeds into browse as
  a child model (no more quit-and-return flicker), and the parity ledger
  closes — deck add by URL, a real confirm modal bridging `Deps.Confirm`,
  import/export prompts, and the valuation report in a text overlay.
- **UI beautification** (implemented): MTG color identity (WUBRG) as the
  design core; runs after parity so it polishes a complete surface.
- **Distribution** (backlog): goreleaser + Homebrew tap + version in
  buildinfo — the "other people can use this" release. Unplanned; plan it
  when parity + beautification land.
- **iPhone capture head** (in progress): a companion iOS app as a third scan
  source beside Continuity Camera, which stays first-class so scanning still
  works with no app installed. Captures at 48.8 MP against Continuity's
  1920x1440 ceiling and reads printing evidence the old rig could not resolve.
  Its **known-gaps table is the place to look when picking up scanner backlog
  work** — it separates real regressions from parity and from harness
  limitations.

## Scanner: expansion symbols (planned, not started)

[scanner-symbol-plan.md](scanner-symbol-plan.md) — reading the set symbol is
what pins a printing when no collector number is printed (pre-1998) or none is
legible (8th Edition on a desk photo). Symbol identity equals set identity,
which pins 83% of ambiguous pre-1998 printings, hands-free. Blocked on
anchoring the patch to the type line rather than the footer; the plan opens
with the cheapest test that would kill it.

## Backlog beyond the queued sprints

Tracked at the end of
[sprint-portfolio-scriptability.md](sprint-portfolio-scriptability.md)
("The way forward"): backup/doctor, want lists with arbitrage-powered
best-vendor pricing, condition/language columns, duplicates report,
location/set-completion tracking, Dragon Shield import, scanner
sleeve/glare fixtures, resolve/catalog unification — plus the
**reverse-skew items** (`hoard search`, CLI quantity set/remove) so user
A's ledger stays visible.
