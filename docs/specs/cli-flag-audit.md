# hoard's CLI flags, audited

Written 2026-08-10 against `03422f8`, on the `cli-flag-audit` worktree. Nothing
in this document is a change; it is an inventory and a judgement.

## Why this exists

A proposed `hoard export --min-price` was declined with "flag bloat is a real
thing". The reasoning behind that decline is this audit's starting point, not
its conclusion: `--min-price` was individually reasonable, and would have
invited `--rarity`, `--set`, `--color` behind it, while hoard already has a
query grammar (`internal/browse/filter.go`) that covers price, value, qty,
rarity, type, color, cmc and set with comparisons and quoting — and that grammar
is unexported inside `package browse`, so `export` cannot reach it.

So the question this answers is not "which flags are ugly". It is: **where has
the CLI grown a flag that encodes a concept the codebase already expresses
better somewhere else, and where would a user reasonably expect one surface to
work the way another already does.**

## Method, so the count is reproducible

The tree was walked in code, not read out of `--help`. `buildRoot` was called
through a test injected with `go test -overlay`, so no file in the repository
was added or modified to produce the inventory:

```go
// scratch file, mapped to internal/command/zz_flagdump_test.go via -overlay
func TestZZFlagDump(t *testing.T) {
	a := &app{env: &cli.Env{Out: io.Discard, Err: io.Discard}}
	root, _ := buildRoot(a, func(io.Writer) ui.Env { return ui.Env{Width: 80} })
	// recurse: c.LocalNonPersistentFlags() and c.PersistentFlags() at each node,
	// skipping cobra's own `completion` and `help`.
}
```

`LocalNonPersistentFlags()` plus `PersistentFlags()` at each node counts every
declaration exactly once — inherited persistent flags are excluded from the
former, so `--db` and `--json` are counted at root and nowhere else. Behavioural
claims below were then checked against a binary built from this tree
(`go build ./cmd/hoard`) running against a scratch database, not inferred from
reading. The full suite is green at this commit (`go test ./internal/...`, 28
packages ok).

## 1. The inventory

**33 commands** (root + 32, excluding cobra's `completion` and `help`).
**40 flag declarations**: 2 persistent (global) and 38 local, over **27 distinct
flag names**. **No hidden commands and no hidden flags anywhere in the tree.**
Shorthands are almost absent: `-v` on root's `--version` and `-o` on `--output`
in three places, two distinct letters in total.

`AnnotationJSON` is carried by 8 commands: root, `movers`, `unpriced`,
`market`, `report`, `watch`, `export`, `schema`. `AnnotationNoStore` by 2:
`schema` and `version`.

| Command | Local flags | JSON | NoStore |
|---|---|---|---|
| `hoard` (root) | `--version`/`-v`; **persistent:** `--db`, `--json` | ✓ | — |
| `add` | `--foil`, `--qty`, `--file`, `--binder`, `--again` | — | — |
| `update-prices` | `--limit` | — | — |
| `movers` | `--since`, `--limit` | ✓ | — |
| `backfill-prices` | — | — | — |
| `unpriced` | — | ✓ | — |
| `guessed` | — | — | — |
| `repair-finishes` | — | — | — |
| `vacuum` | — | — | — |
| `market` | `--min`, `--limit` | ✓ | — |
| `report` | `--top`, `--csv`, `--output`/`-o` | ✓ | — |
| `watch` | — | ✓ | — |
| `watch add` | `--under`, `--over`, `--foil` | — | — |
| `watch import` \| `list` \| `rm` | — | — | — |
| `catalog` + `status`, `update` | — | — | — |
| `binder` + `list`, `new`, `rename`, `rm` | — | — | — |
| `deck` | — | — | — |
| `deck add` | `--file`, `--name`, `--source`, `--refresh`, `--dry-run` | — | — |
| `deck remove` \| `repin` | — | — | — |
| `export` | `--format`, `--binder`, `--deck`, `--all`, `--output`/`-o` | ✓ | — |
| `import` | `--format`, `--binder`, `--dry-run`, `--preserve-binders`, `--again` | — | — |
| `merge` | `--dry-run`, `--again`, `--replace-decks`, `--replace-watches`, `--output`/`-o` | — | — |
| `schema` | `--kind` | ✓ | ✓ |
| `version` | — | — | ✓ |

Names reused across commands — the surface where consistency is even testable:
`--binder` (3), `--again` (3), `--dry-run` (3), `--output`/`-o` (3),
`--limit` (3), `--file` (2), `--foil` (2), `--format` (2).

### The organizing distinction

`market.go:51-52` already states the axis this whole audit turns on, in a
comment written for an unrelated reason:

> `--min` still shapes the data (it is a selection, not a truncation); `--limit`
> does not, for the same reason movers emits everything.

Four different jobs are being done by things that all look like flags, and they
have different answers to "should the grammar own this":

- **Selection** — which rows exist at all. `market --min`, the declined
  `export --min-price`, `export --binder/--deck/--all`. **This is the filter
  grammar's job**, and the only category where the `--min-price` reasoning
  applies.
- **Projection** — which columns. `export --format`, the proposed `--fields`.
  The grammar has nothing to say about projection; a filter language cannot
  express it. Flags are correct here.
- **Truncation** — how many rows are *displayed* after the data is fixed.
  `--limit`, `--top`. Explicitly documented as not shaping data (`movers.go:78`,
  `market.go:51`). Presentation, correctly a flag.
- **Computation parameters** — inputs to what is computed, not filters on the
  result. `movers --since`, `watch add --under/--over`, `schema --kind`. Not
  grammar-shaped at all.

Only the first bucket is contested. It has three members.

## 2. INCONSISTENT

These are the defects the audit exists to find: the same concept spelled
differently, or present on one surface and missing where a user would expect it.

### I1. `-` for stdin is honoured on one of four file-reading surfaces

`hoard add --file` accepts `-` (`add.go:120-135`, and its help says so:
`"add a pasted/exported card list (a path, or - for stdin)"`). Three sibling
surfaces do not, all for the same reason — they call `os.ReadFile`/`os.Open` on
the argument directly:

| Surface | Code | `-` behaviour |
|---|---|---|
| `add --file` | `add.go:125-130` | reads stdin ✓ |
| `import FILE` | `import.go:81` | `error: open -: no such file or directory` |
| `deck add --file` | `deck.go:163` | `error: open -: no such file or directory` |
| `watch import FILE` | `watch.go:58` | `error: open -: no such file or directory` |

Verified by running the binary, not by reading. `watch import` is being fixed by
another lane in flight; **`import` and `deck add --file` are the two remaining
instances and are not covered by that lane.**

This one has a concrete cost beyond symmetry. `import.go:117-119` tells the user
to restore a deck like this:

> Skipped %d deck rows: import fills binders. Restore a deck with
> `hoard export --deck NAME --format text`, then `hoard deck add --file`.

The obvious composition of that advice —
`hoard export --deck X --format text | hoard deck add --file -` — does not work.
The round trip hoard itself recommends requires a temporary file. `--format text`
was built specifically so `deck add --file` could read what hoard writes
(`export.go:79-85`); the pipe is the missing half of that feature.

### I2. `hoard add` silently ignores `--qty` and `--foil`, having decided not to

Within a single command, two flags in the same position are handled two
opposite ways. `--binder` on a path that cannot honour it is a usage error, and
`add.go:87-88` gives the reason:

> Silently ignoring the flag would file cards somewhere the user did not say;
> the interactive picker chooses destinations itself.

`--qty` and `--foil` are marked "(URL form only)" in their usage strings and are
simply dropped on the other two paths. Measured:
`hoard add --file list.txt --qty 4 --foil` reports `Added 1 cards into Binder`
— not 4, not foil, no warning. The argument quoted above applies verbatim: a
silently ignored `--qty 4` records a quantity the user did not say. This is the
same defect the comment was written to prevent, left standing two lines above
it. **Bug, not taste.**

### I3. `--format csv --json` silently overrides an explicit format

`export.go:51-58` reconciles the two spellings of "give me JSON":

```go
if env.JSON {
    if format != "csv" && format != "json" {
        return cli.Usagef("--json conflicts with --format %s", format)
    }
    format = "json"
}
```

`docs/specs/json-agent-surface.md:213-215` records the coexistence as
deliberate, and says "the one ambiguous combination is a clean usage error".
There are in fact **two** ambiguous combinations and only one errors:

- `export --format text --json` → `error: --json conflicts with --format text` ✓
- `export --format csv --json` → emits JSON, silently ✓ measured

The cause is that the code cannot distinguish an explicit `--format csv` from
the default, because it tests the value rather than `cmd.Flags().Changed("format")`.
A script that says `--format csv` and inherits `--json` from a wrapper gets JSON
without being told. Small, but it is a real gap between a recorded intention and
the behaviour, and the fix is one `Changed` call.

### I4. Output format: `--csv` on `report`, `--format` on `export`

`report --csv` is a boolean; `export --format csv` is a value in an enum. Same
concept — "what shape do I want this in" — two grammars. Both then have to
reconcile with global `--json` separately, and they do it differently:
`report.go:48` returns `choose --csv or --json, not both`, while `export`
promotes silently as above. Two commands, two spellings, two conflict policies.
`report` has exactly two formats today, so the boolean is defensible; the cost
is that a third would force the migration anyway.

### I5. Row-cap flag: `--limit` three times, `--top` once, four descriptions

The same presentational concept is declared four times:

| Command | Flag | Usage string | Default |
|---|---|---|---|
| `movers` | `--limit` | "rows per section" | `report.DefaultMoverRows` |
| `market` | `--limit` | "rows per section" | 10 (literal) |
| `update-prices` | `--limit` | "risers/sinkers to list" | `report.DefaultMoverRows` |
| `report` | `--top` | "holdings to itemize in the text report" | 10 (literal) |

`update-prices --limit` shapes an embedded movers table, so its wording differs
from `movers --limit` while doing the same thing to the same report. `report`'s
is genuinely a different noun (holdings, not movers), which is a real argument
for a different name — but "top" and "limit" are not distinguishable to a reader
on that basis. `market`'s default is a literal 10 where the two movers surfaces
share a named constant; that is a drift hazard, not a defect today.

**Taste, except the constant.** Not worth breaking invocations over.

### I6. `--json` on the `watch` group but none of its subcommands

`hoard watch --json` works; `hoard watch list --json` is refused. This is
**recorded as deliberate** at `watch.go:185-186`:

> The `--json` split that cmdWatch enforced by hand is now structural: the group
> is JSONCapable and none of its subcommands are.

I am quoting rather than overriding it. What that comment records is the
mechanism and that the split is intended; it does not argue that `watch list`
specifically should have no JSON. `watch list` is the natural scripting surface
of the group, and `hoardjson` already has a watch document kind
(`schema --kind watch`). Worth a *question* to the owner, not a change: is the
absence of `watch list --json` a decision, or a consequence of the mechanism?

### I7. `guessed` is the only audit queue with no JSON

`unpriced` and `guessed` are the same shape of command — a list of holdings
needing human attention — and `unpriced` is JSONCapable while `guessed` is not.
There is no `guessed` kind in the schema list (`schema.go:62`), so the two are
consistent *with each other*; the gap is in the JSON surface, not in the flag.
**No reason recorded** for the asymmetry. Flagging it as an observation, not a
finding.

## 3. REDUNDANT

### R1. `export --all` does nothing

`export --all` is declared as "export every binder and deck (the default)"
(`export.go:44`) and is read in exactly one place — the mutual-exclusion check
at `export.go:70-72`. Measured: `hoard export` and `hoard export --all` produce
byte-identical output.

It is not *pointless* — it lets a script state intent, and it makes
`--all --binder X` a clean error rather than a silent precedence rule. But it is
the only flag in the tree whose entire behaviour is to be rejected in company.
**No reason recorded** in a comment. Keep it if the explicitness is wanted; this
is taste, and my recommendation is to keep it, because removing it would break
working invocations to buy nothing.

### R2. The three selection flags, against the grammar

`export --binder`, `export --deck` and `market --min` are the CLI's entire
selection surface, and all three are expressible in the existing grammar
(`set:`/container terms and `price>N`). That is the `--min-price` argument
applied to what already shipped. See §4 for whether it is worth acting on — the
short answer is no, and the reason matters.

Note that `export --binder` and `--deck` are not purely selection: they also
gate `--format text`, which refuses to write more than one container
(`export.go:83-85`). A grammar term would have to carry that same constraint.

## 4. SINGLE-USE ESCAPE HATCHES

Flags that exist for one workflow and would not be invented today.

- **`add --again`, `import --again`, `merge --again`** (3 declarations) — each
  defeats a duplicate-detection guard for its own surface. Consistently named
  and consistently worded, which is the good outcome; but three flags is the
  symptom of three separate guards rather than one. Not worth unifying: they
  guard different things (a list, a file, a database).
- **`merge --replace-decks`, `merge --replace-watches`** — two booleans for one
  concept ("take the other hoard's copy"), split by object type. The receipts at
  `merge.go:114` and `merge.go:122` name them individually, so they are
  discoverable at the moment they matter. Defensible; a single `--replace=decks,watches`
  would be worse to type and no clearer.
- **`deck add --source`** — "provider label for text imports (e.g. moxfield)".
  A free-text metadata field set at import time, used by one path. This is the
  closest thing in the tree to a flag that would not be invented today; it
  exists because text decklists carry no provenance. **No reason recorded**
  beyond the usage string.
- **`deck add --name`** — defaults to the file's basename; exists because a text
  file has no name inside it. Narrow but necessary.
- **`import --preserve-binders`** — one workflow (restoring a hoard export)
  against the normal one (funnelling into a destination). Mutually exclusive
  with `--binder` and errors cleanly (`import.go:77-79`). Justified by the
  restore path being real.
- **`update-prices --limit`** — a display knob on a report the command prints as
  a side effect of doing something else. The one case where I would say the flag
  is on the wrong command: the movers table belongs to `movers`, which has its
  own `--limit`.

## 5. LOAD-BEARING — flags that look excessive and are not

This section is the reason the audit is trustworthy. Several of the flags that
would be first against the wall in a naive pass are structural.

- **`--db`** — looks like clutter on every help page; it is the sandbox
  mechanism the entire test story rests on. `command.go:92-93`, and
  `cli.go:203-205` records what its *absence from help* cost:

  > the only global hoard has — `--db`, the one safe way to point the binary at
  > a scratch database — was reachable from nothing but the source.

  Every behavioural check in this audit was run through it. Untouchable.

- **`--json` as a persistent flag rather than 8 local ones** — the annotation
  mechanism (`cli.go:56-59`) makes one declaration behave as if it were absent
  on the 25 commands that cannot honour it, and `CheckJSON` refuses it rather
  than ignoring it. The comment states the safety argument:

  > Absent means `--json` is refused, which is the safe direction: output that
  > silently ignored the flag is indistinguishable, to a parser, from output
  > that honored it.

  This is one global flag doing the work of a per-command policy, and the help
  renderer hides it exactly where it is refused (`cli.go:289-298`). It is the
  best-engineered flag in the tree.

- **`-o` as a shorthand of `--output`, in three places** — looks like three
  redundant declarations of the same flag. `report.go:38-41`:

  > `-o` is declared as the shorthand of `--output` rather than as a flag named
  > "o": pflag reads a single dash as a run of shorthands, so a shorthand has to
  > hang off a long name.

  `--output` exists because `-o` had to; the crane evaluation records this as
  the one flag spelling that was *added* during that migration, unavoidably. The
  three declarations are three commands that each legitimately write a file, and
  `merge --output` writes something different from the other two (the
  interchange document, not the command's own output). Correct as is.

- **`schema --kind`** — narrows a JSON Schema to one document kind. Looks like a
  filter; it is a reachability walk over `$defs` (`schema.go:19-22`) and has no
  grammar equivalent. `schema` also carries both annotations for reasons written
  down at `schema.go:26-34`, including why a command whose entire output is JSON
  still has to *declare* `AnnotationJSON`:

  > refusing would answer "hoard schema has no JSON output" to the one command
  > whose entire output is JSON.

- **`deck add --refresh`** — pre-answers a `[y/N]` confirm for scripts
  (`deck.go:108-113`). The confirm exists because the re-import replaces a
  deck's entries wholesale and discards manual edits. Removing the flag would
  make the command unscriptable; removing the confirm would make it dangerous.

- **`--dry-run` on `import`, `merge`, `deck add`** — three declarations that a
  tidying pass would want to unify. They are already unified where it counts:
  `import.go:157-181` centralizes the verb and the closing line, and explains
  why:

  > three literals of a sentence this load-bearing is three chances for a user
  > to be told "nothing was written" in a wording that does not match the last
  > command that told them so.

  The flag is per-command because the commands are; the *wording* is shared,
  which is the part that could actually drift.

- **`root --version` alongside the `version` subcommand** — textbook redundancy,
  and `commands.go:63-66` gets there first:

  > `-v`/`--version` is a flag as well as a subcommand because both spellings
  > predate the tree and scripts use them.

  It is also special-cased in `PersistentPreRunE` (`command.go:141-143`) so that
  asking the binary what it is does not create a database. Two spellings, one of
  which is load-bearing for existing scripts.

- **`watch add --under` / `--over`** — two flags where one signed threshold
  would do. `watch.go:33-35` requires exactly one and errors otherwise. The
  alternative spelling (`--threshold -2`) cannot express direction without
  overloading the sign of a price, which is worse.

## 6. The filter-grammar question, answered

**What it would take.** Less than the framing assumed, because the split has
already been done — and the portable half is already in `store`.

`internal/browse/filter.go` is 307 lines. It divides cleanly:

| Part | Lines | Depends on | Portable? |
|---|---|---|---|
| `filter` struct, `numericKeys`, `knownKeys`, `keyHelp` | 19-53 | `store.NumCond`, `store.TraitFilter` | ✓ as-is |
| `parseFilter` | 62-130 | same | ✓ as-is |
| `tokenize`, `splitTerm` | 138-170 | nothing | ✓ as-is |
| `compare`, `containsFold` | 290-307 | nothing | ✓ as-is |
| `matches` | 177-223 | `browse.card` (8 fields) | needs a row abstraction |
| `moverAsCard` | 237-250 | `store.PriceChange` → `browse.card` | moves with `card` |
| `unsupportedOnMovers` | 256-261 | nothing | ✓ |
| `filterMatchCount`, `filterUnsupported` | 268-288 | `browse.Model` | ✗ browse-only, stays |

**The trait half is already portable and already exported.** `store.TraitFilter`
and `store.MatchingCardIDs` live in `internal/store/filter.go:8-58` and are
public API. `browse` reaches them through an interface (`browse.go:71`).
`export` could call `st.MatchingCardIDs(...)` today without moving a single
line. The trait half — rarity, type, artist, layout, setname, color, cmc, the
half that compiles to SQL — is **not** the blocker and never was.

What is stuck in `browse` is (a) the *parser*, which is pure and depends on
nothing browse-specific, and (b) `matches`, the holding half, which is bound to
the unexported `browse.card`.

**The cost, concretely.** Move lines 19-170 and 290-307 into a new
`internal/query` package (~200 lines, mechanical, zero behaviour change), and
give `matches` a row interface of eight accessors — or, simpler, a small
exported struct that both `browse.card` and `export.Row` convert to. Then:

- ~200 lines move, ~40 lines stay in `browse`.
- `internal/browse/filter_test.go` (217 lines, 7 tests) and
  `moversfilter_test.go` (240 lines, 9 tests) split along the same seam; the
  parser tests move with the parser, the `card`-shaped match tests need the same
  conversion the production code does.
- **`export.Row` can already answer every term.** It carries `Name`, `Set`,
  `Finish`, `Board`, `Condition`, `Count`, `PriceUSD` and `ScryfallID`
  (`export/csv.go:27-63`), which covers `name`, `set`, `finish`, `board`, `qty`,
  `price`, `value` (= `Count × PriceUSD`) and the trait-half id intersection. No
  new query, no new field. This is the part that makes the refactor small.
- Then `export --query "price>5 rarity:mythic"` replaces `--min-price` and the
  four flags behind it, and `--binder`/`--deck` could follow later or never.

Call it a day's work with tests, and genuinely low risk: `browse` keeps its
behaviour because the parser is pure, and the SQL half does not move at all.

**Is it worth it? Not yet — and the reason is not cost.**

Three arguments against, in order of weight:

1. **The demand does not exist.** The selection surface it would replace is
   three flags (`export --binder`, `export --deck`, `market --min`), all of
   which work, none of which anyone has complained about. `--min-price` was
   declined, not requested twice. A grammar built for one declined flag is a
   plan nobody executes.
2. **It would be hoard's second query language, not its first.** `export`
   already has `--format`, and the JSON surface doc proposes `--fields`
   (`json-agent-surface.md:170-173`) — which is *projection*, and which a filter
   grammar cannot express. So `export` would end up with a grammar for rows and
   flags for columns. That is a coherent design, but it is a bigger commitment
   than "reuse what we have", and it should be decided as a design, not as a
   refactor.
3. **The grammar is typed one-handed into a filter bar.** `filter.go:57-59`
   records that there is deliberately no OR because "adding a precedence to get
   wrong buys nothing for a bar you type into one-handed". A grammar that
   becomes a scripting interface will immediately be asked for OR, and the
   argument that kept it simple stops applying. Extracting it exports a design
   constraint along with the code.

**What I would do instead**: keep the grammar in `browse`, and when the next
`--min-price` arrives, extract *then* — the extraction is cheap and stays cheap,
because `store.TraitFilter` already carries the hard half. Recording that it is
a day's work, not a rewrite, is the useful output here. The refactor has no
deadline; the two stdin bugs do.

## 7. Ranked shortlist

Hardest evidence first. Every item was reproduced against a built binary.

| # | Change | Kind | Blast radius | Breaks? |
|---|---|---|---|---|
| 1 | `import` and `deck add --file` accept `-` for stdin | **bug** | 2 call sites (`import.go:81`, `deck.go:163`); mirror `add.go:120-135` | No — `-` is currently an error |
| 2 | `add` rejects `--qty`/`--foil` on the list and picker paths | **bug** | 1 guard in `runAdd`, beside the `--binder` one | Yes, deliberately: silently-ignoring invocations start erroring |
| 3 | `export` errors on explicit `--format csv --json` | **bug** | 1 line: test `Changed("format")` in `export.go:52` | Yes: `--format csv --json` currently succeeds |
| 4 | `market --limit` uses `report.DefaultMoverRows` | drift guard | 1 literal (`market.go:38`) | No |
| 5 | Ask the owner whether `watch list --json` is intended | question | none | — |
| 6 | Extract the filter grammar to `internal/query` | refactor | ~200 lines + test split | No |
| 7 | `--csv` → `--format csv` on `report`; `--top` → `--limit` | taste | 2 flags | Yes, and buys little |

**1 is the strongest and should go first.** It is the defect class this audit
was commissioned to find, it is the same defect the in-flight `watch import`
lane is fixing, it has hard evidence (`error: open -: no such file or
directory`), and it makes hoard's own printed advice
(`export --deck X --format text | deck add --file -`) actually compose. Two
files, one helper, no invocation breaks.

**2 has the strongest *internal* justification** — the comment two lines away
argues for it — but it breaks working invocations. Pre-release means that is
allowed; it should still be a deliberate call, because someone's script may pass
`--qty` harmlessly today.

**3 is small and closes a gap between a written decision and the code.**

**6 is a legitimate "not now"**, per §6. It is listed so the cost is on record.

**7 I do not recommend.** It breaks invocations to make four usage strings
rhyme.

## Notes and caveats

- **Two lanes are in flight** in `internal/command/watch.go` and
  `internal/action/watch_import.go`. Line numbers cited in this document for
  those two files (`watch.go:54-61`, `watch.go:185-186`, `watch.go:210-212`)
  are against `03422f8` and **will drift**. Finding I1's `watch import` row is
  expected to be fixed by that lane; the `import` and `deck add` rows are not.
- **Help width is already a constraint on new flags.**
  `TestUsageFitsANarrowTerminal` (`usage_test.go:88-104`) walks every page in the
  tree at 60 columns. Flag usage strings go through `FlagUsagesWrapped`
  (`cli.go:306-308`) and re-wrap, so a long *flag* usage is safe; `Long` and
  `Example` are copied verbatim (`cli.go:229-249`) and are only as narrow as
  they were typed. Two commands carry comments recording that they were found by
  that sweep rather than by eye (`movers.go:31-34`, `schema.go:42-45`). Any new
  flag prose should assume the asymmetry.
- **The crane evaluation explicitly did not cover flags.**
  `docs/cli-crane-evaluation.md` was deleted in `736c809` and reads, in its
  first paragraph: "Scope of this evaluation, as ruled: **idiom and layout
  only**. No command name, no flag, and no group changes." So nothing in the
  current flag surface is blessed by that review, with one exception it records:
  `--output` was added because `-o` needed a long name to hang off. This audit
  is the first look at flags as such.
- **What surprised me**: the filter grammar is *already half-extracted*.
  `store.TraitFilter` and `store.MatchingCardIDs` have been exported in `store`
  the whole time, so the SQL-compiling half — the half that sounds expensive —
  was never the obstacle. The reachability problem is the ~200-line pure parser
  and a struct field set. That materially changes the answer to "what would it
  take", even though it does not change my recommendation to defer.
