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
| update prices | `action.UpdatePrices` | `update-prices` | planned: palette op | checking catalog (i) · downloading catalog (d) · refreshing cards (d, cards) · saving (i) · filling price gaps (i) · recording history (i) | via ensure | `UpdatePricesResult` | 0/1 | migrated |
| gap fill | `action.FillGaps` | (inside update-prices, import, adds) | — | filling price gaps (i, notes) | — | `pricing.GapReport` | — | migrated |
| backfill prices | `action.BackfillPrices` | `backfill-prices` | — (next sprint) | downloading history (d, bytes) · recording (d, rows) | — | `BackfillResult` | 0/1 | migrated |
| import | `action.ImportCollection` | `import` | — (next sprint) | resolving cards (d, cards) | — | `ImportResult` | 0/1/2 | migrated |
| deck add | `action.DeckAdd` | `deck add` | — | resolving cards (d, cards) | — | `DeckAddResult` | 0/1/2 | migrated |
| bulk add / URL add | `action.AddList` / `action.AddByURL` | `add --file/-`, `add <url>` | `a` (cascade) | resolving cards (d, cards) | — | `AddListResult`/`AddByURLResult` | 0/1/2 | migrated |
| repair finishes | `action.RepairFinishes` | `repair-finishes` | planned: `f` in unpriced view | refreshing cards (d, cards) | — | `RepairResult` | 0/1 | migrated |
| arbitrage | `action.Arbitrage` | `arbitrage` | `v` view (bespoke async) | reading vendor prices (i) | — | `arbitrage.Result` | 0/1 | migrated |
| watch check | `Deps.WatchCheck` | `watch` | planned: banner + view | — | — | fired/checked | 0/1/3 | migrated |
| watch add/list/rm | `action.WatchAdd`, `Deps.WatchList/WatchRemove` | `watch add/list/rm` | planned: `w`, watches view | — | — | — | 0/1 | migrated |
| report | `Deps.Valuation` | `report` | — (artifact; palette later) | — | — | `report.ValuationData` | 0/1 | migrated |
| export | `Deps.ExportRows` | `export` | — (next sprint) | — | — | rows | 0/1 | migrated |
| summary | `Deps.Summary` | `hoard` (piped) / `--json` | left pane + header | — | — | totals | 0/1 | migrated |
| movers | `Deps.Movers` | `movers` | `v` view | — | — | changes | 0/1 | migrated |
| unpriced | `Deps.Unpriced` | `unpriced` | `v` view | — | — | rows | 0/1 | migrated |
| binder new/rename/rm | store-direct (by design) | `binder …` | planned: `n`/`R`/`d` keys | — | — | — | 0/1 | by design |
| browse/filter/edit | — (TUI-native) | reverse-skew backlog: `hoard search`, quantity set/rm | `/`, `+/-`, `d`, `u` | — | — | — | — | documented gap |
