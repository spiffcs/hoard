# Contributing to hoard

## Getting started

```sh
make tools     # bootstrap the pinned toolchain (binny → .tool/)
make build     # go build -o hoard ./cmd/hoard
make test      # go test ./...   (no network needed)
make help      # list every target
```

## What CI gates on

Every PR runs [validations.yaml](.github/workflows/validations.yaml):

- `make static-analysis` — golangci-lint (config in [.golangci.yaml](.golangci.yaml)),
  gofmt, and a `go mod tidy` check
- `make test` and `make build`

Run both before pushing; they are the same commands CI runs.

## The Swift scanner

The card scanner — a macOS helper and an iPhone app, both under `scan/` — needs
macOS and Xcode. Its Taskfile tasks carry `platforms: [darwin]` and skip
silently on Linux, so a green CI run proves nothing about the Swift side.

Its workflow, [scan.yml](.github/workflows/scan.yml), is **switched off** behind
an `if: false` guard: macOS runners bill at 10x. Leave it off. The gate is
local:

```sh
make scan-test scan-check
```

See [scan/hoard-scan-ios/README.md](scan/hoard-scan-ios/README.md) for the
iPhone app.

## Before you ask for a review

Contributing code has never been easier. Reading it has not gotten any cheaper at all,
and that asymmetry can cause maintainership burden. A patch costs its author
minutes; it costs a reviewer longer and that longer is spent
whether or not the change was ready.

Spend the cheap resource to protect the expensive one. Everything below is
something you can check without involving anyone else.

**Run the gates.** Both, and locally. They are the same commands CI runs, so a
failure here is a failure you were going to get anyway — just an hour later and
in public.

```sh
make static-analysis
make test
```
**Prove the new tests fail without your change.** Revert the fix, keep the test, and
watch it go red. A test that passes against the bug is not coverage, it is a
claim of coverage, and it is worse than nothing because it stops the next person
looking. This is the single highest-value minute you can spend.

**Exercise what no test sees.** A change to the browser or the card scanner needs
a real run — no unit test knows what the screen or the camera did. A change to a
command's flags or help needs `hoard <cmd> --help` read once, because help text
is capped at 60 columns and prose is copied verbatim. A change to any tracked
image needs `make asset-review`.

**Re-read your own diff before anyone else does.** Most of what a reviewer would
catch first is visible on a second pass: the debug print, the commented-out line,
the file you did not mean to touch, the comment that describes the previous
version of the code.

Then make the patch cheap to read:

- **One concern per PR.** Two unrelated fixes in one branch cost more than twice
  as much to review, because neither can be reasoned about alone.
- **Say why, not what.** The diff already says what. The reason it is correct,
  the alternative you rejected, the thing you are unsure about — those are what
  a reviewer cannot reconstruct.
- **Show the evidence.** The output before and after, the test that fails
  without the change, the session you ran. Asserting that something works is an
  invitation to go and check; showing it is not.
- **Flag your own doubts.** "I am not sure this is the right layer" saves more
  time than it costs, every time. Nobody is annoyed by it.
- **Separate mechanical changes.** A rename, a reformat, or a regenerated file
  in with real logic buries the part that needs thought. Put it in its own
  commit, and say which one it is.

None of this is a barrier to entry. A small change from someone who has never
been here before is welcome and will be reviewed. It is about respecting a
finite resource.

## AI-assisted contributions

Welcome. hoard is built this way and it would be dishonest to hold contributors
to a different standard.

Three conditions, none of them about the tool:

1. **You understand every line and can defend it in review.** "The model wrote
   it" is not an answer to "why does this work?". If you cannot explain a change,
   you cannot maintain it, and neither can anyone else.
2. **The tests are real.** Break your own change and watch the test fail before
   you send it. A test that passes against the bug is worse than no test — it is
   a claim of coverage that is not there.
3. **You have the right to submit it.** The same requirement as any other
   contribution. Generated code that reproduces someone else's licensed work is
   their work, whatever produced it.

Disclosure is not required and not tracked. The conditions above are what
matters, and all three are checked by reading the patch rather than by asking
about its provenance.

Repository-specific traps an agent tends to hit — the 60-column help cap, the
generated images that go stale silently, the `--json` opt-in — are collected in
[AGENTS.md](AGENTS.md). Point your tooling at it.

## Commit messages

Conventional-ish prefixes (`feat:`, `fix:`, `docs:`, `chore:`), which the
existing history follows and which
[chronicle](https://github.com/anchore/chronicle) keys off for changelogs.

## Reporting problems

Include `hoard version` output in bug reports. Security issues go through
[SECURITY.md](SECURITY.md), not public issues.
