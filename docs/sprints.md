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
| 1 | Binders + interop | ✅ complete | [sprint-binders-interop.md](sprint-binders-interop.md) |
| 2 | Portfolio + scriptability | ✅ complete (A–E; F superseded by parity) | [sprint-portfolio-scriptability.md](sprint-portfolio-scriptability.md) |
| 3 | Parity — action layer, progress, palette | 🔨 in progress (started 2026-07-31) | [sprint-parity.md](sprint-parity.md) |
| 4 | UI beautification — WUBRG identity | 📋 planned, not started | [sprint-ui-beautification.md](sprint-ui-beautification.md) |
| 5 | Distribution | 💭 backlog (unplanned) | — |

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
- **Parity** (planned): every capability becomes an internal action
  function with a uniform progress contract; CLI and TUI turn into thin
  frontends; the TUI gains a `:` command palette, in-TUI
  update-prices/repair/catalog with live progress, a watches view +
  fired-watch banner, binder management, and movers window cycling. Key
  design decisions are already made and recorded in the doc.
- **UI beautification** (planned): MTG color identity (WUBRG) as the
  design core; runs after parity so it polishes a complete surface.
- **Distribution** (backlog): goreleaser + Homebrew tap + version in
  buildinfo — the "other people can use this" release. Unplanned; plan it
  when parity + beautification land.

## Backlog beyond the queued sprints

Tracked at the end of
[sprint-portfolio-scriptability.md](sprint-portfolio-scriptability.md)
("The way forward"): backup/doctor, want lists with arbitrage-powered
best-vendor pricing, condition/language columns, duplicates report,
location/set-completion tracking, Dragon Shield import, scanner
sleeve/glare fixtures, resolve/catalog unification — plus the
**reverse-skew items** (`hoard search`, CLI quantity set/remove) so user
A's ledger stays visible.
