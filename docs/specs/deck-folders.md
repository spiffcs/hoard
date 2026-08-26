# Deck folders: what shipped, and what is left

Deck folders group decks one level deep (`commander/`, `duel_decks/`) so a
sidebar spanning the history of Magic stays readable. This document records
what landed, the decisions behind it, and the work still outstanding.

**Written: 2026-08-23**, against schema v32. Every "shipped" row below is
covered by a test named in this document; every "outstanding" row is not
implemented and has no test.

---

## Bottom line

Folders are usable end to end: create, fill, browse, empty, delete, from both
the CLI and the TUI. The gap that matters is **interop**: `hoard export` and
`hoard import` do not carry folders, so a round trip through hoard's own JSON
silently flattens the grouping.

---

## The model

One column, added in migration 32 (`internal/store/migrate.go`):

```sql
ALTER TABLE containers ADD COLUMN parent_id INTEGER
    REFERENCES containers(id) ON DELETE SET NULL;
```

Three decisions are load-bearing:

**`ON DELETE SET NULL`, never `CASCADE`.** `card_entries.container_id` cascades
from `containers`. A cascading parent would delete a folder's decks, which would
cascade again and destroy every card in them. Deleting a folder must lose the
grouping and nothing else.

**One level, enforced in the store.** `MoveDeckToFolder` requires the moved
container to be a deck and the target to be a folder, so a folder can never be
given a parent. Cycles are not rejected; they are unrepresentable. If nesting
is ever wanted, relax that one guard; no second migration is needed.

**Folders hold no cards, enforced by SQLite.** Two triggers on `card_entries`
(`BEFORE INSERT` and `BEFORE UPDATE OF container_id`) raise
`a folder holds decks, not cards`. This closes every write path at once,
including ones written later and including writes from outside hoard:

```console
$ sqlite3 hoard.db "INSERT INTO card_entries (container_id, ...) VALUES (5, ...)"
Error: stepping, a folder holds decks, not cards (19)
```

These are the schema's first triggers.

---

## Shipped

| Capability | Surface | Covered by |
| --- | --- | --- |
| Create a folder | `hoard folder new NAME`; palette `NewFolder` | `TestFolderNamesAreCheckedLikeBinderNames`, `TestPaletteCreatesAFolder` |
| Rename a folder | `hoard folder rename` | `TestFolderNamesAreCheckedLikeBinderNames` |
| Delete a folder | `hoard folder rm` | `TestRemovingAFolderKeepsItsDecksAndTheirCards` |
| List folders | `hoard folder list`; the TUI sidebar | `TestFolderNewThenDeckMoveFilesTheDeck` |
| Move a deck in | `hoard deck move DECK FOLDER`; TUI `m` | `TestMoveDeckIntoAnExistingFolder` |
| Move a deck out | `hoard deck move DECK`; TUI `m` then blank | `TestMoveDeckOutOfItsFolder` |
| Create-while-moving | TUI `m`, unknown name, y/n confirm | `TestMovingIntoAnUnknownFolderOffersToCreateIt` |
| Nested sidebar, indented | TUI | `TestSidebarNestsDecksUnderTheirFolder` |
| Folder rolls up copies and value | TUI, `folder list` | `TestFolderRowRollsUpItsDecks`, `TestListFoldersRollsUpItsDecks` |
| Selecting a folder shows its decks' cards | TUI | `TestSelectingAFolderShowsEveryDecksCards` |
| Fold/open a folder, `space` | TUI | `TestSpaceFoldsAFolder`, `TestSpaceUnfoldsAgain`, `TestSpaceOnADeckFoldsItsParent` |
| Fold state persists across launches | TUI, `settings` | `TestFoldStateSurvivesAReload` |
| Rename a folder | `hoard folder rename`; TUI `R` | `TestRenameAFolderFromTheSidebar` |
| Scoping movers/dips/watches to a folder | TUI | Rolled up in `rebuildEntryIndex`; no dedicated test, **see gaps** |

Behavioural choices worth knowing:

- **`hoard folder rm` on a non-empty folder succeeds**, returning its decks to
  the top level. Unlike `binder rm`, which refuses when non-empty. A folder
  holds no cards, so deleting one is not destructive.
- **The sidebar sorts folders and unfiled decks together by value**, children
  nested by value beneath. A $500 folder outranks a $200 loose deck.
- **`0` means the top level** in `MoveDeckToFolder`, and `hoard deck move DECK`
  with no folder argument unfiles it.
- **`ListFolders` returns `DeckSummary`**, reusing the shape binders and decks
  already use, so `report.Binders` renders it unchanged.

---

## Outstanding

Ordered by how much it costs a user today.

### 1. Interop drops folders in `hoardjson` (the real gap)

`internal/hoardjson` has no concept of a folder. `Container.Kind` is
`enum=binder,enum=deck` and there is no parent field, so:

- `hoard export` writes decks with no record of the folder they were in.
- `hoard import` and `hoard merge` cannot restore grouping.
- An export/import round trip through hoard's **own** format silently flattens
  a hoard's folders.

The change is an **ADDITION** under the model's own versioning rules
(`hoardjson.go:41-45`): widening the `Kind` enum is an addition on the emit
side, and a `parent` field would be optional. That is a MINOR bump, not a
REVISION: no existing field changes meaning. It needs:

- `Kind` widened to include `folder`, and an optional `parent` on `Container`.
- Emit: folders as containers, decks carrying their parent.
- Read: create folders and re-file decks, tolerating a `parent` naming a folder
  the document does not contain.
- Regenerated JSON Schema, and a round-trip test that a hoard with folders
  survives export → import unchanged.

### 2. `hoard folder list --json`

`folder list` is text-only; it is the only `list` in the CLI that is not
`cli.JSONCapable`. It needs the document kind from item 1, so the two should
land together.

### 3. Delete a folder from the TUI

`R` renames a folder now. `d` still refuses and points at `hoard folder rm`,
deliberately: a folder needs different confirm wording than a binder ("its 3
decks move to the top level", not "and its 200 cards"), and shipping misleading
confirm text would be worse than shipping none.

### 4. Fragment matching when moving a deck

Typing `duel` for `duel_decks` in the TUI move prompt does not match; you get
the create-confirm instead. Safe, but it means a typo'd fragment offers to
create a near-duplicate folder. The CLI's `FolderByRef` already does fragment
matching with an ambiguity error; the TUI deliberately does not, because it
matches against what is on screen. Worth aligning once folders have been used
in anger.

### 5. No direct test for folder-scoped movers, dips and watches

Selecting a folder scopes those views to the union of its decks' cards. It
works by rolling each deck's entries up into its parent in
`rebuildEntryIndex` (`internal/browse/containerfilter.go`), and it is exercised
indirectly, but nothing asserts it. A folder whose decks share a card must
count that card once per deck copy, and no test would catch a regression.

### 6. No fold-all / open-all

Folding is one folder at a time, on `space`. Someone with twenty folders folds
twenty times, or once, since the state persists, but still. A pair of palette
commands would fix it and would be the right shape for the palette, unlike
`space` itself. See `docs/specs/palette-coverage.md`.

### 7. Sets mode ignores folders

`B` (browse by set) replaces the sidebar with sets, where folders have no
meaning; `m` does nothing there. Correct as it stands, noted so it is not
mistaken for an oversight.

---

## What this document does not claim

- No performance measurement. The `containers_parent` index exists; nothing has
  been profiled against a large hoard.
- No upgrade testing beyond the migration suite. v32 applies cleanly from v8
  and v31 in `internal/store/migrate_test.go` and
  `internal/action/merge_test.go`; no real user database has been migrated.
