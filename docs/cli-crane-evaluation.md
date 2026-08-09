# hoard's CLI, evaluated against crane

Scope of this evaluation, as ruled: **idiom and layout only**. No command name,
no flag, and no group changes. Everything `hoard` can do today it still does,
spelled exactly the same way, after every recommendation here is applied.

## Method

crane was read as source, not as documentation: `go-containerregistry@v0.20.7`
from the local module cache (`$(go env GOMODCACHE)`), `cmd/crane/` — 24 files,
3038 lines. hoard was read at the `cli-cobra` worktree, `02ecda4` plus its
uncommitted tree. Both trees were built and tested, not described.

Baseline at the time of writing: hoard builds clean, `go vet` clean, full test
suite green. 4 of 18 commands ported to cobra-native flags; 14 still behind the
`legacyCmd` seam.

## Summary

| # | Delta | Recommendation | Cost |
|---|---|---|---|
| D1 | Whole tree + ported constructors live in one `commands.go` | Split to one file per command | High churn, zero behavior |
| D2 | `newXxxCmd` naming | `NewCmdXxx` | Mechanical |
| D3 | Root is a tree with bodies inlined | Root becomes a list of constructor calls | Falls out of D1 |
| D4 | `execCmd` re-implements `buildRoot`'s wiring | Parameterize one builder, as crane's `New` is | Small, removes drift |
| D5 | `cli.Env` streams and cobra's streams are independent | Bind them in the builder | ~4 lines |
| D6 | `Example` holds usage forms, not examples | Keep behavior, rename the concept in docs | Comment only |
| D7 | `legacyCmd` seam | Delete once the 14 are ported | The port itself |
| D8 | 32 `.go` files in `package main` at repo root | **Move** to `cmd/hoard` + `internal/command` | Release path re-proven |

**Status: all eight applied.** All 18 commands are cobra-native, one file each;
`legacyCmd` is deleted and nothing sets `DisableFlagParsing`. `go build`,
`go vet` and the full test suite are green, and the release path is re-proven by
a `goreleaser build --snapshot` run against `./cmd/hoard`.

Three things changed behavior, none of them a rename:

- Stray positional arguments are rejected where they used to be silently
  ignored — `hoard vacuum bogus` now errors. `DisableFlagParsing` had been
  swallowing them.
- `catalog` and `artindex` dispatched `status`/`build`/`update` by hand-parsing
  a positional; they are real subcommands now, so a typo gets cobra's suggestion
  instead of `unknown catalog subcommand "x"`. The spellings are unchanged.
- `hoard deck` with no subcommand still errors, and its message now names
  `repin`, which it has always accepted.

One flag spelling was *added*, unavoidably: `-o` has to be the shorthand of a
long name, because pflag reads a single dash as a run of shorthands. It is now
`--output`/`-o` on both `report` and `export`. `-o` is untouched.

## What already matches crane — leave it alone

**Constructors closing over late-bound state.** crane's commands take
`options *[]crane.Option` and the root's `PersistentPreRun` appends to that
slice after the tree is built. hoard's take `a *app` and the root's
`PersistentPreRunE` fills its fields. Same problem — the tree must exist before
`--db` (or `--platform`) can be parsed, and the store can't be opened until
after — and hoard's answer is the better of the two.

`★ Insight ─────────────────────────────────────`
crane threads a *pointer to a slice* and mutates it with `options = append(...)`
inside `PersistentPreRun`, relying on the closure over `New`'s local. That works,
but aliasing a slice through a pointer while appending to it is subtle — if a
command dereferenced early it would see a stale backing array. hoard's `*app`
assigns struct fields (`a.store = st`), which has no such hazard. This is one
place to *not* follow crane.
`─────────────────────────────────────────────────`

Also already correct and idiomatic: `SilenceUsage` on root; flags declared after
the `&cobra.Command{...}` literal and before `return cmd`; a root `RunE` that
does something sensible with no arguments. hoard's deliberate cobra overrides
(`EnableCommandSorting = false`, no `Args` validator on root, `SilenceErrors`)
are documented decisions and stay.

hoard's `ui.Table` help via `SetHelpFunc` is a divergence from crane, which uses
cobra's stock templates. It is the right divergence — crane's help can't
respond to terminal width — and it stays.

## The deltas

### D1 — One file per command

crane: `cmd/crane/cmd/` holds one file per command. `digest.go` is
`NewCmdDigest` plus the two helpers only it uses (`getDigest`,
`getTarballDigest`). `pull.go` is `NewCmdPull` and nothing else. A command's
flags, its `RunE`, its help text, and its private helpers sit together, and
nothing else sits with them.

hoard: `commands.go` (358 lines) holds the entire tree *and* the four ported
constructors, while the bodies live scattered across the root package —
`cmdAdd` and `cmdGuessed` share `add.go`, `cmdBackfillPrices` lives in
`movers.go`, `cmdUnpriced` in `pricing.go`, `cmdVacuum` in `repair.go`. Reading
what `hoard guessed` does means three files and a grep.

This is the substantive finding. Recommendation: as each legacy command is
ported, move it into its own file named for the command, carrying its
constructor, its flag declarations, and its private helpers.

### D2 — `NewCmdXxx`, not `newXxxCmd`

crane exports `NewCmdDigest`, `NewCmdPull`, `NewCmdAuth`. hoard has
`newMoversCmd`, `newBinderCmd`, `newWatchCmd`, `newVersionCmd`. Purely
cosmetic, and precisely the sort of thing "the idiom an outside contributor
expects" was the argument for. Rename the four; write the fourteen the new way.

Whether they are exported is a non-question while everything is `package main`
— use the `NewCmdXxx` spelling regardless, so a later package move is a `git mv`
and not a rename pass.

### D3 — Root as a list

crane's `root.go` `AddCommand` block is 23 single-line constructor calls. You
can read the whole surface of the tool in one screen.

hoard's `rootCommand` inlines fourteen multi-line `legacyCmd(...)` calls, each
with an example string and a closure — roughly 100 lines of adapter between the
reader and the answer to "what can hoard do". The file's own header comment
promises "one place that says what hoard can do, in the order the help should
read it", and the legacy adapters are what's currently breaking that promise.

This is not separate work: D1 produces it. Once each command is a constructor
in its own file, the tree collapses to crane's shape, and the comment becomes
true.

### D4 — One builder, parameterized

crane exposes `New(use, short string, options []crane.Option) *cobra.Command`
and builds `Root` from it. The parameters exist so `gcrane` can reuse the tree
under a different name — one builder, no second copy.

hoard has `buildRoot(a *app) (*cobra.Command, *globals)`, which hard-wires the
real `globals`. The test helper `execCmd` therefore can't use it, and instead
re-declares the persistent flags and re-installs help by hand:

```go
root.PersistentFlags().String("db", "", "")
root.PersistentFlags().Bool("json", false, "")
```

with the comment "The real root declares these; a test tree needs them so
`--json` parses." That's a copy that can drift from the original — a flag added
to `buildRoot` is silently absent under test.

Recommendation: give `buildRoot` the seam crane's `New` has, so `execCmd` calls
the same builder production does and overrides only the store and streams.

### D5 — Bind `cli.Env` to cobra's streams

Help renders through `cmd.OutOrStdout()` / `cmd.ErrOrStderr()`. Command output
goes through `a.env.Out` / `a.env.Err`. In production nothing calls
`root.SetOut`, so cobra falls back to `os.Stdout` and the two agree by
coincidence. `execCmd` sets both, correctly, by hand.

Two independent paths that must agree is a seam waiting to fail. crane has one
path (`cmd.OutOrStdout()`) because it has no `Env`; hoard needs `Env` because it
carries width, color, and `--json`, which cobra's writer cannot. Keep `Env`, and
have the builder call `root.SetOut(a.env.Out)` / `root.SetErr(a.env.Err)` so a
single assignment drives both.

### D6 — `Example` is being used for usage forms

crane puts genuine shell examples in `Example`, prompts and comments included:

```
# If you wanted to mount a blob from debian to ubuntu.
$ crane auth token -H --push --mount debian ubuntu
```

hoard puts *alternate usage forms* there —
`"hoard add\nhoard add <scryfall-url> [--foil] [--qty N]"` — and
`writeCommandHelp` renders them under a **Usage:** heading. The behavior is
good and the help reads well. The cost is that a standard cobra field means
something non-standard here, which matters exactly as much as an outside
contributor or a future doc generator matters.

No behavior change recommended. Say so in the `cli` package doc, next to
`InstallHelp`, so the choice is discoverable where it's implemented.

### D7 — Delete the seam

`legacyCmd` sets `DisableFlagParsing` and hands argv to the old hand-rolled
parser, including a manual `-h`/`--help` scan that only exists because parsing
is off. It's a migration scaffold and crane has no equivalent. It goes when the
fourteenth command is ported, and its removal is the definition of done.

### D8 — The directory move: doing it now

crane is `cmd/crane/main.go` (30 lines) plus a `cmd/crane/cmd` package. hoard is
32 `.go` files in `package main` at the module root, with a 570-line `main.go`.
By crane's standard this is the largest structural difference, and crane's
layout is better for a public repo.

The cost was weighed and accepted: it is being done before v0.1.0, with the
release path and README updated to match rather than worked around.

- `.goreleaser.yaml` pins `main: .` in three build stanzas → `./cmd/hoard`.
- `README.md` documents `go install github.com/spiffcs/hoard@latest` → that
  becomes `go install github.com/spiffcs/hoard/cmd/hoard@latest`.
- `Taskfile.yaml`'s `go build -o hoard .` → `./cmd/hoard`.
- The release pipeline is re-proven by a `goreleaser --snapshot` run.

**Destination, and why it is not crane's exact path.** crane puts its commands
in `cmd/crane/cmd` because `go-containerregistry`'s module root is a *library* —
the binary is the guest, so it lives entirely under `cmd/`. hoard's module root
*is* the binary, and hoard already keeps every non-trivial package under
`internal/`. So the commands go to `internal/command`, and `cmd/hoard/main.go`
holds only the entry point:

```go
func main() { os.Exit(command.Run(os.Args[1:])) }
```

`internal/cli` and `internal/command` are deliberately two packages, not one:
`cli` is the support layer for what cobra has no opinion about (`Env`, `Usagef`,
the annotations, `InstallHelp`), and `command` is the commands themselves.

Exit codes stay hoard's, as decided — `command.Run` returns the code and does
the printing, so `errWatchFired` (3), `errPartial` (2), and `context.Canceled`
(130) stay unexported next to the code that raises them, and cobra still decides
nothing.

## Target layout

As built:

```
cmd/hoard/main.go     os.Exit(command.Run(os.Args[1:])) — and nothing else
internal/command/
  command.go          Run, execute, globals, buildRoot, detectEnv, the db paths
  commands.go         the tree: 18 NewCmdXxx calls, the groups, the root flags
  browse.go           bare `hoard` (split out of the old 570-line main.go)
  util.go             what more than one command shares: caches, printer, confirm
  add.go artindex.go backfill.go binder.go catalog.go deck.go export.go
  guessed.go import.go market.go movers.go report.go repair.go unpriced.go
  updateprices.go vacuum.go version.go watch.go
internal/cli/         Env (+Report), Usagef, the annotations, InstallHelp
```

`guessed` came out of `add.go`, `backfill` and `movers` were separated,
`unpriced` came out of `pricing.go` (now `updateprices.go`), and `vacuum` came
out of `repair.go` — four commands that had been sharing a file with an
unrelated sibling. `commands.go` went from 358 lines to 135.

Two functions were deleted rather than moved, both dead once the seam went:
`parsePositionals` (it existed only to give `flag.FlagSet` the interleaving
pflag does natively) and `ensureCatalog` (an "interim shim" with no callers).

## Port order

Ordered by risk, cheapest and most-isolated first, so the pattern is proven on
commands whose flags are trivial before it meets the ones that aren't.

1. **No flags at all** — `guessed`, `repair-finishes`, `vacuum`, `unpriced`.
   `cobra.NoArgs` and a `RunE`; nothing to translate.
2. **Simple numeric/string flags** — `update-prices` (`--limit`), `market`
   (`--min`, `--limit`), `backfill-prices`, `artindex`, `catalog`.
3. **Subcommand groups** — `deck` (`add`/`remove`/`repin`), following the
   `newWatchCmd` pattern already proven.
4. **Flag-heavy interop** — `report` (`--top --csv -o`), `export`
   (`--binder --deck --all --format -o`), `import`
   (`--binder --preserve-binders --format --dry-run`).
5. **`add` last** — the most-used command and the one with the most forms
   (bare, URL, `--file`, `--again`, `--binder`, `--foil`, `--qty`).

Each command is done when: it has its own file, a `NewCmdXxx` constructor, its
flags declared on the cobra command, its entry in the tree reduced to one line,
its tests driving it through `execCmd`, and `go test ./...` green. `legacyCmd`
is deleted after the last one.

## Explicitly out of scope

Recorded so a later reader knows these were considered and set aside, not
missed:

- Renaming commands, flags, or groups. `repair-finishes`, `vacuum`, and
  `backfill-prices` are inconsistent as sibling verbs; that is a surface
  question and the ruling was idiom and layout only.
- Reconsidering which commands exist, or collapsing/splitting any of them.
- Anything in `internal/cli` beyond the doc comment in D6 — `Env`, `Usagef`,
  the annotations, and `InstallHelp` are all doing work crane has no answer for.
