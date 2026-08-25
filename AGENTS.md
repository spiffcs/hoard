# Working on hoard

Notes for coding agents.
Everything here is a rule that is easy to break without noticing —
nothing fails loudly, and most of it will pass review by looking right.

Contribution policy, including the one for AI-assisted changes, is in
[CONTRIBUTING.md](CONTRIBUTING.md).

## The gates

```sh
make test              # go test ./...    — no network needed
make static-analysis   # golangci-lint + gofmt + a go mod tidy check
```
Both are exactly what CI runs. `make help` lists every target.

Lint runs `--tests=false` and over **both** `GOOS=darwin` and `GOOS=linux`, so
plain `golangci-lint run` reports issues CI does not and misses ones it does.
Use the make target.

## Traps

**Help text is capped at 60 columns.** `TestUsageFitsANarrowTerminal` holds every
line of every command's help to 60. The renderer wraps *flag* descriptions but
copies `Short`, `Long` and `Example` verbatim, so prose is the only part that can
break the cap. Hard-wrap it yourself.

**A test that cannot fail is not a test.** Before claiming a change is covered,
break the change and watch the test fail. Several tests in this repo exist
because an earlier version of them passed against the bug.

**Tracked images are generated and go stale silently.** The logo, app icon,
social card, browser screenshot and demo GIF are all derived from tracked
sources. Nothing checks them — a stale one is simply committed. If you're making
changes to images, copy, or assets like the readme or logos run `make asset-review`
This rebuilds them and reports which are older than their
source. Images are judged by human eye or not at all.

**`scan.yml` is switched off on purpose** behind `if: false`; macOS runners bill
at 10x. Do not re-enable it. The Swift gate is local: `make scan-test scan-check`.

**Card *condition* is never called a grade.** Condition (NM, LP, …) and grading
(a numeric score from a third-party service) are different things, and hoard
does not do the second one.

**Prices are estimates, never quotes.** Aggregated daily from third parties. An
absent price means unpriced, never free — do not let a nil price become `$0.00`
anywhere.

## Layout

| Path | What it is |
|---|---|
| `cmd/hoard` | Entry point, and nothing else — one call into `command.Run` |
| `internal/command` | The cobra tree: one `NewCmdXxx` constructor per file |
| `internal/cli` | The layer under it — `Env`, annotations, the help renderer |
| `internal/action` | Operations shared by the CLI and the browser |
| `internal/store` | SQLite: every query lives here |
| `internal/browse` | The TUI |
| `scan/` | The Swift card scanner — macOS and iPhone. Needs Xcode |
| `scan/fixtures/` | Goldens for the reader. The frames they replay are fetched, not tracked — `make scan-fixtures` |
| `schema/` | The published JSON Schema and the SQLite schema docs |

## Licensing

Code is MIT. **Card imagery under `scan/` is not** — it is Fan Content belonging
to Wizards of the Coast, carved out in [NOTICE](NOTICE). Adding card images means
updating that file. Card data comes from Scryfall, MTGJSON and TCGCSV, each with
its own terms.

## Commits

Conventional-ish prefixes — `feat:`, `fix:`, `docs:`, `chore:` — which
[chronicle](https://github.com/anchore/chronicle) keys off for changelogs.

Write commit messages that say *why*, since the diff already says what. The
existing history is the reference; match it.
