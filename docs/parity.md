# Capability parity

**The rule: a capability lands as a function in `internal/action` first;
the CLI and the TUI only render.** A gap between the two surfaces is a
decision recorded here, not an accident. Update this table when a
capability is added, migrated, or wired to a new surface.

Progress steps are `internal/progress` Step labels; (d) marks a
determinate step (Done/Total with a unit), (i) indeterminate. Confirm
names the yes/no questions an operation may ask through `Deps.Confirm`.

| Capability | Action | CLI | TUI | Steps | Confirm | Result | Exit | Status |
|---|---|---|---|---|---|---|---|---|
| catalog status | `action.CatalogStatus` | `catalog status` | — | — | — | `catalog.Status` | 0/1 | migrated |
| catalog update | `action.CatalogUpdate` | `catalog update` | planned: palette op | downloading catalog (d, bytes) | — | `CatalogUpdateResult` | 0/1 | migrated |
| ensure catalog | `action.EnsureCatalog` | (inside update-prices) | planned | downloading catalog (d, bytes) | build?/refresh? | usable bool | — | migrated |
| update prices | pending | `update-prices` | planned: palette op | checking catalog (i) · downloading catalog (d) · refreshing cards (d, cards) · saving (i) · filling price gaps (i) · recording history (i) | via ensure | pending | 0/1 | pending |
| gap fill | pending | (inside update-prices, import, adds) | — | filling price gaps (i, notes) | — | `pricing.GapReport` | — | pending |
| backfill prices | pending | `backfill-prices` | — (next sprint) | downloading history (d, bytes) · recording (d, rows) | — | pending | 0/1 | pending |
| import | pending | `import` | — (next sprint) | resolving cards (d, cards) | — | pending | 0/1/2 | pending |
| deck add | pending | `deck add` | — | resolving cards (d, cards) | — | pending | 0/1/2 | pending |
| bulk add / URL add | pending | `add --file/-`, `add <url>` | `a` (cascade) | resolving cards (d, cards) | — | pending | 0/1/2 | pending |
| repair finishes | pending | `repair-finishes` | planned: `f` in unpriced view | refreshing cards (d, cards) | — | pending | 0/1 | pending |
| arbitrage | pending | `arbitrage` | `v` view (bespoke async) | reading vendor prices (i) | — | `arbitrage.Result` | 0/1 | pending |
| watch check | pending | `watch` | planned: banner + view | — | — | fired/checked | 0/1/3 | pending |
| watch add/list/rm | pending | `watch add/list/rm` | planned: `w`, watches view | — | — | — | 0/1 | pending |
| report | pending | `report` | — (artifact; palette later) | — | — | `report.ValuationData` | 0/1 | pending |
| export | pending | `export` | — (next sprint) | — | — | rows | 0/1 | pending |
| summary | pending | `hoard` (piped) / `--json` | left pane + header | — | — | totals | 0/1 | pending |
| movers | pending | `movers` | `v` view | — | — | changes | 0/1 | pending |
| unpriced | pending | `unpriced` | `v` view | — | — | rows | 0/1 | pending |
| binder new/rename/rm | pending | `binder …` | planned: `n`/`R`/`d` keys | — | — | — | 0/1 | pending |
| browse/filter/edit | — (TUI-native) | reverse-skew backlog: `hoard search`, quantity set/rm | `/`, `+/-`, `d`, `u` | — | — | — | — | documented gap |
