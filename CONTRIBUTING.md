# Contributing to hoard

hoard is a collection tracker for Magic: The Gathering on the command line.
Three things you might be here for:

**Obtaining it.** Install with the script, with `go install`, or from a signed
release archive. All three are in the [README](README.md#install). `hoard demo`
opens the browser on a sample collection without touching your data.

**Giving feedback.** Bug reports and enhancement requests both go to the
[issue tracker](https://github.com/spiffcs/hoard/issues); what makes each one
cheap to answer is under
[Bug reports and feature requests](#bug-reports-and-feature-requests) below.

Security issues are the one exception. Please report those privately through
[SECURITY.md](SECURITY.md), never as a public issue.

**Contributing a change.** Fork the repository, work on a branch, and open a
pull request against `main`. There is no CLA to sign. Start with
[Requirements for a contribution](#requirements-for-a-contribution); the rest of
this document is what makes a patch easy to say yes to.

## Getting started

```sh
make tools     # bootstrap the pinned toolchain (binny → .tool/)
make build     # go build -o hoard ./cmd/hoard
make test      # go test ./...   (no network needed)
make help      # list every target
```

## Requirements for a contribution

Every PR runs [validations.yaml](.github/workflows/validations.yaml), which is
`make build` plus these two:

```sh
make static-analysis   # golangci-lint + gofmt + a go mod tidy check
make test              # go test ./...   (no network needed)
```
That is the automated gate. The rest of this list is checked at review.
[Before you ask for a review](#before-you-ask-for-a-review).

**Style is whatever the linter says.** gofmt, plus the golangci-lint set in
[.golangci.yaml](.golangci.yaml). We use errcheck, gosec, govet, ineffassign,
staticcheck, unused. There is no prose style guide to read; the config is the
standard. Lint runs `--tests=false` and over **both** `GOOS=darwin` and
`GOOS=linux`, so a bare `golangci-lint run` reports findings CI does not and
misses ones it does. Use the make target.

Personally I like to follow the [google style guide](https://google.github.io/styleguide/go/) for Golang but ymmv.

**`go mod tidy` leaves the module files unchanged.** CI checks this separately.

**A behaviour change comes with a test that failed first.** New features and bug
fixes alike. If you did not write a test, revert your change, keep the test, and 
watch it go red before you send it. A test that passes against the bug is a claim 
of coverage rather than real coverage. It is worse than none because it can mislead 
the next person who comes looking.

**Commit messages use conventional-ish prefixes** — `feat:`, `fix:`, `docs:`,
`chore:` — which the existing history follows and which
[chronicle](https://github.com/anchore/chronicle) keys off for changelogs. Say
*why*, since the diff already says what; the history is the reference.

**You have the right to submit it.** Contributions are accepted under the
[MIT license](LICENSE) covering the rest of the code. Card imagery is the
exception — it is Fan Content belonging to Wizards of the Coast, carved out in
[NOTICE](NOTICE), so adding any means updating that file. This applies to
generated code too: if it reproduces someone else's licensed work, it is their
work, whatever produced it.

Participation is under the [Code of Conduct](CODE_OF_CONDUCT.md). Further
repository-specific traps — the generated images that go stale silently, the
Swift scanner's local-only gate — are collected in [AGENTS.md](AGENTS.md);
it is addressed to coding agents but accurate for humans.

## The Swift scanner

The card scanner is a macOS helper and an iPhone app, both under `scan/` — needs
macOS and Xcode. Its Taskfile tasks carry `platforms: [darwin]` and skip
silently on Linux, so a green CI run proves nothing about the Swift side.

I don't want to pay for mac runners at the moment.

Its workflow, [scan.yml](.github/workflows/scan.yml), is **switched off** behind
an `if: false` guard: macOS runners bill at 10x. Leave it off. The gate is
local:

```sh
make scan-test scan-check
```

`scan-check` replays 28 camera frames through the reader and diffs the result
against checked-in goldens. It needs `oras` (`brew install oras`) to fetch them —
deliberately not in the pinned toolchain, because that is bootstrapped by every
CI job including the release, and this is a darwin-only check CI never runs.

**The frames are not in git** and they are ~58MB.
Charging every contributor for them on clone was unfair when the check needs
macOS, Xcode and a Swift build and CI never runs it. `make scan-fixtures`
downloads them once (it is a dependency of `scan-check`, so you rarely call it
yourself) and verifies the archive against the tracked
[`scan/fixtures/frames.oci`](scan/fixtures/frames.oci) before extracting.

See [scan/hoard-scan-ios/README.md](scan/hoard-scan-ios/README.md) for the
iPhone app.

## Before you ask for a review

Contributing code has never been easier. Reading it has not gotten any cheaper at all,
and that asymmetry can cause maintainership burden. A patch costs its author
minutes; it costs a reviewer longer and that longer is spent
whether or not the change was ready.

Spend the cheap resource to protect the expensive one. Everything below is
something you can check without involving anyone else.

**Run the gates.** They are the same commands CI runs, so a
failure here is a failure you were going to get at some point.

```sh
make static-analysis
make test
```
**Prove the new tests fail without your change.** Revert the fix, keep the test, and
watch it go red. 

**Exercise what no test sees.** A change to the browser or the card scanner needs
a real run. No unit test knows what the screen or the camera did. A change to a
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
- **Say why, not what.** The diff already says what. Your description should say 
  the reason it is correct, the alternative you rejected, the things you are unsure about.
- **Show the evidence.** The output before and after, the test that fails
  without the change, and screenshots all help.
- **Flag your own doubts.** "I am not sure this is the right layer" saves more
  time than it costs, every time. Nobody is annoyed by it.
- **Separate mechanical changes.** A rename, a reformat, or a regenerated file
  in with real logic buries the part that needs thought. Put it in its own
  commit, and say which one it is.

None of this is a barrier to entry. A small change from someone who has never
been here before is welcome and will be reviewed. This document is just to help
set expectations of what contributors can expect from the process.

## AI-assisted contributions

Welcome. hoard has its share of AI tooling influences and it would be dishonest
to hold contributors to a different standard.

Three small conditions:

1. **You understand every line and can defend it in review.** "The model wrote
   it" is not an answer to "why does this work?". If you cannot explain a change,
   you cannot maintain it, and neither can anyone else.
2. **The tests are real.** Break your own change and watch the test fail before
   you send it. A test that passes against the bug is worse than no test — it is
   a claim of coverage that is not there.
3. **You have the right to submit it.** The same requirement as any other
   contribution. Generated code that reproduces someone else's licensed work is
   their work, whatever produced it.

## Bug reports and feature requests

Both go to the [issue tracker](https://github.com/spiffcs/hoard/issues), and
both have a template that asks for what is needed:
[bug report](https://github.com/spiffcs/hoard/issues/new?template=bug_report.yml),
[feature request](https://github.com/spiffcs/hoard/issues/new?template=feature_request.yml).

For an enhancement, the useful part is the workflow you are trying to finish and
the workaround you are using today. What should hoard do is usually easier to
work out from those than from the proposal on its own.

You do not need a patch, a diagnosis, or a reproduction reduced to its minimum.
A report that says only what you saw is worth filing.

**Security issues do not go here.** Report those privately, through
[SECURITY.md](SECURITY.md).
