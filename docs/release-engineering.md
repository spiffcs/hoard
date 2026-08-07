# Release engineering plan

How hoard gets from "private repo with a CI job" to "public repo people can
install a signed, notarized binary from." The reference implementation is
[spiffcs/triage](https://github.com/spiffcs/triage), the other Go TUI in this
account: same owner, same language, same shape of release. Where triage has
made a call, hoard follows it — the point is one house style across the org's
tools, not a bespoke setup per repo.

It is written to be executed top to bottom by an agent. Every stage lists the
files it touches, the exact content where that content is short enough to
inline, and the command that proves the stage landed. Stages are ordered by
dependency: Stage 0 blocks everything, A blocks B, B blocks C. E through G can
interleave.

---

## 1. Where we are

| Concern | triage | hoard today |
| --- | --- | --- |
| Repo visibility | public | **private** |
| Tags / releases | tagged, signed releases | **no tags, no releases** |
| Version in binary | `cmd.version` via ldflags, `triage version` | **none** — no `--version`, no version command |
| Build/release | `.goreleaser.yaml` + `release.yaml` | **none** — `make build` only |
| CI | `validations.yaml` (static analysis + unit) via reusable workflow | `ci.yml` (gofmt, vet, test, build) |
| Lint | golangci-lint v2 + gosimports + go-mod-tidy check | **gofmt + vet only** |
| Actions pinning | every `uses:` pinned to a commit SHA | `@v7` floating tags |
| macOS binaries | quill sign + Apple notarize | n/a |
| Supply chain | cosign keyless signing, CodeQL, zizmor, dependabot | **none** |
| Install path | `install.sh` on Cloudflare R2 | **none** — build from source |
| Repo meta | `SECURITY.md`, `RELEASE.md`, `CHANGELOG.md` | README + LICENSE + `docs/` |
| Toolchain | binny (`.binny.yaml`) + Taskfile, Makefile is a shim | hand-written Makefile (Go **and** Swift targets) |

hoard's advantage over triage: `modernc.org/sqlite` is **pure Go**. There is no
cgo anywhere in the module, so `CGO_ENABLED=0` cross-compiles to every target
from one ubuntu runner. No zig, no cross toolchain, no per-arch runner matrix.

hoard's complication: the repo also holds a Swift macOS helper and a SwiftUI
iPhone app (`scan/`). Neither ships in a Go release archive — but both get
first-class Taskfile targets so the build has one front door. See D1 and §5.

---

## 2. Decisions

Settled with the owner. Do not re-litigate mid-implementation; if one turns out
wrong, change it here first.

**D1 — Adopt binny + Taskfile, with the Swift and iOS targets moved in too.**
Triage's `Makefile` is a shim that bootstraps `binny`, which installs pinned
tool binaries into `.tool/`, and delegates every target to `Taskfile.yaml`.
hoard takes the same structure. The reason to do this rather than keep the
hand-written Makefile is pinning: today hoard's toolchain is "whatever golangci-lint
and goreleaser happen to be on the machine," and a release pipeline whose tools
float is a pipeline that breaks on someone else's laptop. `.binny.yaml` makes
every tool a version-controlled line.

The Swift side moves in with it. `scan`, `scan-test`, `scan-check`, `cardkit`,
`cardkit-score`, `scan-ios`, `scan-ios-install`, `scan-ios-test` all become
Taskfile tasks, so `task -l` lists the whole project — Go and native — rather
than half of it. `make <anything>` still works: the Makefile catch-all forwards
to task.

**D2 — Sign *and* notarize. quill for macOS, cosign for the checksums.**
Cosign keyless signing gives provenance (the artifact came from this workflow,
recorded in Rekor). Apple notarization gives macOS users a binary that opens
without a Gatekeeper fight. They solve different problems and hoard does both,
same as triage. Cost: five repo secrets and a Developer ID Application
certificate. See §8.1 for the exact secret setup — this is the one part of the
plan that needs Apple Developer portal work, and it is a hard dependency for the
release job, not a nice-to-have.

**D3 — Publish `install.sh` to Cloudflare R2.**
Same bucket and workflow shape as the org's other tools, so `hoard` installs
the way its siblings do. `release-install-script.yaml` is a reusable workflow
copied nearly verbatim; the three R2 secrets and the Cloudflare rewrite rule are
the setup (§10.2). The script itself only ever downloads from
`github.com/spiffcs/hoard/releases/download` — R2 hosts the script, not the
binaries.

**D4 — Branch is `master`, not `main`.**
Every triage workflow says `branches: [main]`. hoard's default branch is
`master`. Each copied file needs that substitution — it is the single most
likely silent mistake in this whole plan, because a workflow with the wrong
branch filter simply never runs and reports nothing.

**D5 — First tag is `v0.1.0`; `prerelease: auto` handles the rest.**
hoard has no tags. Pre-1.0 signals "the CLI surface can still move," which is
true. `prerelease: auto` marks anything with a `-rc1`-style suffix as a
prerelease with no config change.

---

## 3. What is explicitly out of scope

- **Shipping the Swift helper or the iPhone app in the release archives.**
  They get Taskfile targets (D1), not goreleaser build entries. The macOS helper
  is an `.app` bundle built by `build-scan.sh`; the iOS head goes through the App
  Store — `docs/app-store-release.md`. The README must say plainly that the
  release binary is the CLI, and that scanning needs a separate build on a Mac.
- **The macOS `scan.yml` workflow.** It stays manual-only for the reasons its own
  header comment gives (10× billing on macOS runners). Releasing does not change
  that math. Going public does change it — revisit separately, not here.
- **Homebrew tap / winget / Linux packages.** goreleaser can produce all of them.
  None on the first release; revisit once there is download traffic to justify
  the maintenance.

---

## 4. Stage 0 — Blockers before the repo goes public

Everything below assumes the repo will be public. These must be true *before*
flipping visibility, because a public repo is a permanent artifact — the
licensing exposure and the first impression both start the moment it flips.

**0.1 — Close the data-licensing P0s.** `docs/data-licensing.md` §8 lists three,
and they are release gates by that document's own framing:

1. Raise `chunkPause` to ≥500 ms in `internal/scryfall/scryfall.go` (around line
   249) and fix the stale comment — `/cards/collection` is 2/second, not
   10/second.
2. Raise the search pagination gap to ≥500 ms in the same file (around line 497)
   — `/cards/search` is 2/second.
3. Add the Wizards Fan Content Policy notice (the block quote in
   `docs/data-licensing.md` §7) to `README.md`, and to `hoard version` output
   once Stage A exists.

Verify the first two against the file before editing — line numbers drift. The
P1 items in that section (credits block, price disclaimer, extended User-Agent)
are cheap and clearly right; fold them into Stage F.

**0.2 — Audit for anything that should not be public.** Run at minimum:

```bash
git ls-files | grep -iE '\.db$|\.env|secret|token|credential|Signing\.xcconfig'
git log --all --oneline -S 'APPLE_' -- . | head
```

Also confirm the ignored-but-bulky corpora are actually ignored
(`scan/corpus/images/`, `scan/foil-dataset/images/`) and that
`scan/foil-corpus/cards/` — which *is* tracked on purpose — contains only
photographs the owner took.

**0.3 — Flip visibility.** `gh repo edit spiffcs/hoard --visibility public
--accept-visibility-change-consequences`. Only when 0.1 and 0.2 are done, and
only with the owner's explicit go-ahead in the same session.

---

## 5. Stage A — Toolchain migration (binny + Taskfile)

Do this first among the build changes: every later stage's verification command
(`make lint`, `make static-analysis`, `.tool/goreleaser`) assumes it.

**A.1 — `.binny.yaml`** at the repo root. Triage's list is exactly hoard's
needs; take it wholesale and bump to current versions with `.tool/binny update`
after the first install:

| tool | why hoard needs it |
| --- | --- |
| `binny` | manages itself; pinning the pinner |
| `task` | runs everything |
| `golangci-lint` | Stage D lint gate |
| `gosimports` | import grouping in `task format` |
| `goreleaser` | Stage B builds |
| `cosign` | signs `checksums.txt` |
| `quill` | signs + notarizes the macOS binaries (D2) |
| `chronicle` | changelog generation |
| `glow` | renders the changelog in the terminal |
| `gh` | triggering releases from the CLI |

**A.2 — `Taskfile.yaml`.** Start from triage's, then port every existing
Makefile target. The Go half maps one-to-one; the native half is hoard-only and
is the reason this migration is worth doing. Preserve the explanatory comments
currently in the Makefile — they carry real knowledge (why `scan-check` depends
on `cardkit` and not `scan`; why `cardkit-score` runs one process instead of
231; why the simulator is discovered rather than named) and they must not be
lost in the move.

Task groups:

- **High level** — `default`/`validations` → static-analysis; `static-analysis` →
  `check-go-mod-tidy` + `lint`; `test` → `unit`; `unit` → `go test ./...`;
  `build` → `go build -o hoard .`; `all` → `build` + `scan`.
- **Bootstrap** — `binny`, `tools`/`bootstrap`, `update-tools`, `list-tools`,
  `list-tool-updates`, `tmpdir`. Copy from triage unchanged.
- **Static analysis** — `format`, `lint`, `lint-fix`, `check-go-mod-tidy`. Copy,
  changing `gosimports -local github.com/anchore` to
  `-local github.com/spiffcs`.
- **Native / scan** (hoard-only) — `scan`, `scan-test`, `cardkit`, `scan-check`
  (deps: `cardkit`), `cardkit-score` (deps: `cardkit`), `scan-ios`,
  `scan-ios-install`, `scan-ios-test`.
- **Schema** — `generate-json-schema`, `generate-sqlite-schema`.
- **Changelog** — `unreleased`, `changelog`. Copy from triage.

Two porting details that will bite otherwise:

```yaml
  # Makefile: `make cardkit-score ARGS=--misses`
  # Taskfile: `task cardkit-score -- --misses`
  cardkit-score:
    desc: Score the labelled corpus in one process (~23s)
    deps: [cardkit]
    cmd: "./bin/cardkit-probe --score scan/corpus/manifest.tsv {{ .CLI_ARGS }}"

  # Makefile used $(shell ...) at parse time; Taskfile uses a var with `sh:`,
  # which is evaluated lazily — so this no longer runs xcrun on Linux.
  scan-ios-test:
    desc: Run ScanKit's unit tests on the iOS simulator
    platforms: [darwin]
    vars:
      SIM_NAME:
        sh: "xcrun simctl list devices available | grep -m1 -o 'iPhone [A-Za-z0-9 ]*' | sed 's/ *$$//'"
    cmd: >
      cd scan/hoard-scan && xcodebuild test -scheme hoard-scan-Package
      -destination 'platform=iOS Simulator,name={{ .SIM_NAME }}' CODE_SIGNING_ALLOWED=NO
```

`platforms: [darwin]` on every native task means they are **skipped silently**
on the Linux CI runner rather than failing. That is what makes a single
`task all` safe everywhere — but it also means a green run on Linux proves
nothing about the Swift half. Say so in a comment on each such task, and keep
the real native gate where it already is: local `make scan scan-test scan-check`,
plus the manual `scan.yml` workflow.

**A.3 — Replace the `Makefile` with triage's shim.** `OWNER = spiffcs`,
`PROJECT = hoard`. The catch-all `%:` target installs task on demand and
forwards, so every command in every existing doc — `make build`, `make scan`,
`make cardkit-score` — keeps working. Grep `docs/` and `README.md` for `make `
afterwards and confirm each named target still resolves via `task -l`.

Verify:

```bash
make tools && .tool/binny check
make -s help                 # should list every task, native ones included
make build && make test && make lint && make static-analysis
make scan && make scan-test && make scan-check   # macOS only
```

---

## 6. Stage B — Version plumbing

goreleaser's ldflags need somewhere to write to, and hoard has no version
symbol at all today. This is a code change, and it blocks Stage C.

**B.1 — Add `internal/version/version.go`:**

```go
// Package version carries the build identity stamped in by the release
// pipeline. The zero values are what a `go build` from a working tree gets;
// goreleaser overwrites them with -X ldflags at release time.
package version

var (
	// Version is the release tag, e.g. "v0.1.0". "dev" for local builds.
	Version = "dev"
	// GitCommit is the commit the binary was built from.
	GitCommit = "unknown"
	// BuildDate is an RFC3339 timestamp.
	BuildDate = "unknown"
)
```

**B.2 — Wire a `version` command into `main.go`'s dispatch switch** (the `case`
ladder starting around line 121), beside `catalog` and `binder`. It prints the
three values, the Go version and `runtime.GOOS/GOARCH`, and the Fan Content
notice from Stage 0.1. Accept `--version` and `-v` as top-level flags mapping to
the same code path, since that is what people type. Add the row to
`usage.go`'s `usageSections` — the usage table is data, so it is one entry.

**B.3 — Cover it in `main_test.go`.** One test that the command runs and its
output contains the version string. The existing tests there show the house
pattern for invoking `run()` with args and capturing output.

Verify:

```bash
make test && make build && ./hoard version
go build -ldflags "-X github.com/spiffcs/hoard/internal/version.Version=v0.0.1-test" -o /tmp/hoard-vt . && /tmp/hoard-vt version
```

The second command is the one that matters: if the ldflag path is wrong, the
build silently succeeds and still prints `dev`. Confirm it prints `v0.0.1-test`.

---

## 7. Stage C — `.goreleaser.yaml`

New file at the repo root. Triage's, adapted: Windows added, ldflags pointed at
hoard's `internal/version`, and the build split by OS because only the darwin
entry carries the quill hook.

```yaml
version: 2

project_name: hoard

builds:
  # modernc.org/sqlite is pure Go: no cgo anywhere in the module, so every
  # target cross-compiles from the ubuntu runner with no toolchain wrangling.
  - id: hoard-linux
    binary: hoard
    main: .
    env: [CGO_ENABLED=0]
    goos: [linux]
    goarch: [amd64, arm64]
    ldflags: &ldflags
      - -s -w
      - -X github.com/spiffcs/hoard/internal/version.Version={{ .Version }}
      - -X github.com/spiffcs/hoard/internal/version.GitCommit={{ .Commit }}
      - -X github.com/spiffcs/hoard/internal/version.BuildDate={{ .Date }}

  - id: hoard-windows
    binary: hoard
    main: .
    env: [CGO_ENABLED=0]
    goos: [windows]
    goarch: [amd64, arm64]
    ldflags: *ldflags

  # Split out for the post hook: quill signs the Mach-O binary with the
  # Developer ID cert and submits it to Apple's notary service. Snapshot builds
  # pass --dry-run/--ad-hoc so a local `goreleaser --snapshot` needs no
  # credentials and talks to nobody.
  - id: hoard-darwin
    binary: hoard
    main: .
    env: [CGO_ENABLED=0]
    goos: [darwin]
    goarch: [amd64, arm64]
    ldflags: *ldflags
    hooks:
      post:
        - cmd: .tool/quill sign-and-notarize "{{ .Path }}" --dry-run={{ .IsSnapshot }} --ad-hoc={{ .IsSnapshot }} -vv
          env:
            - QUILL_LOG_FILE=/tmp/quill-{{ .Target }}.log

archives:
  - id: default
    formats: [tar.gz]
    format_overrides:
      - goos: windows
        formats: [zip]
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"
    files:
      - README.md
      - LICENSE

checksum:
  name_template: "checksums.txt"
  algorithm: sha256

snapshot:
  version_template: "{{ incpatch .Version }}-next+{{ .ShortCommit }}"

# Keyless signing: cosign gets a short-lived Fulcio cert bound to the workflow's
# OIDC identity, and the signature lands in Rekor. Signing only checksums.txt
# transitively covers every archive it hashes.
signs:
  - cmd: .tool/cosign
    signature: "${artifact}.sigstore.json"
    args: ["sign-blob", "--bundle=${signature}", "--yes", "${artifact}"]
    artifacts: checksum

release:
  github:
    owner: spiffcs
    name: hoard
  draft: false
  prerelease: auto
  make_latest: true
  footer: |
    ---
    **Verification:**
    ```bash
    cosign verify-blob \
        --bundle checksums.txt.sigstore.json \
        --certificate-identity-regexp "^https://github.com/spiffcs/hoard/.*" \
        --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
        checksums.txt
    ```
```

`.tool/quill` and `.tool/cosign` are binny-installed (Stage A), which is why
Stage A comes first. quill signs and notarizes Mach-O binaries **from Linux** —
that is its reason to exist — so the release job stays on `ubuntu-latest`.

Verify locally:

```bash
make tools
.tool/goreleaser check
.tool/goreleaser release --snapshot --clean --skip=sign
ls dist/
./dist/hoard-darwin_darwin_arm64*/hoard version
```

A snapshot produces every archive without touching GitHub or Apple: quill runs
ad-hoc/dry-run, and `--skip=sign` skips cosign, which wants an OIDC token that
only exists in Actions. The binary must print a version like
`v0.0.1-next+abc1234` — if it prints `dev`, the ldflag path is mismatched.

---

## 8. Stage D — The release workflow

### 8.1 Apple credentials (do this before writing the workflow)

The release job hard-fails without these, so get them in place first. This needs
Apple Developer portal access and is the longest-lead item in the plan.

1. **Developer ID Application certificate.** Create it in the Apple Developer
   portal, install to the login keychain, then export as `.p12` *with the full
   chain* and a password.
2. **App Store Connect API key** for the notary service: an Issuer ID, a Key ID,
   and the `.p8` private key.
3. **Five repository secrets:**

   | secret | contents |
   | --- | --- |
   | `APPLE_DEVELOPER_ID_CERT_CHAIN` | base64 of the `.p12` |
   | `APPLE_SIGNING_CERT_PASSWORD` | the `.p12` export password |
   | `APPLE_NOTARY_ISSUER` | App Store Connect issuer UUID |
   | `APPLE_NOTARY_KEY_ID` | the key's ID |
   | `APPLE_NOTARY_KEY` | contents of the `.p8` |

4. **Verify before you need them**, locally, with the same tool the workflow
   uses: `.tool/quill submission list` with the three notary env vars set. It
   round-trips against Apple's API and fails loudly on a bad key. The workflow
   runs this same command as its own preflight step, for the same reason —
   a credential failure should surface in seconds, not after a full build.

This overlaps with the iPhone sprint's blocked signing work
(`docs/ios-development.md`): both need the Apple Developer account wired up.
Doing it here unblocks both.

### 8.2 `.github/actions/bootstrap/action.yaml`

Copy triage's composite action: `setup-go` (SHA-pinned), then `make tools` +
`.tool/binny list` + `.tool/binny check`, then `make ci-bootstrap-go`. Every
workflow below uses it, which is what keeps CI and local running the *same*
pinned tools.

### 8.3 `.github/workflows/release.yaml`

```yaml
name: "Release"

on:
  push:
    tags: ["v*"]
  workflow_dispatch:
    inputs:
      version:
        description: "Version to release (e.g., v0.1.0)"
        required: true
        type: string
      phase:
        description: "Release phase to run"
        required: false
        default: "full"
        type: choice
        options: [full, install-script-only]

permissions:
  contents: read

concurrency:
  group: ${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: false

jobs:
  # The same quality gate PRs run. Tags are effectively immutable (RELEASE.md),
  # so this is the last chance to stop a bad one becoming a release.
  validations:
    name: "Validations"
    if: ${{ github.event.inputs.phase != 'install-script-only' }}
    uses: ./.github/workflows/validations.yaml

  release:
    name: "Release"
    needs: validations
    if: ${{ github.event.inputs.phase != 'install-script-only' }}
    runs-on: ubuntu-latest
    permissions:
      contents: write  # create the GitHub release
      id-token: write  # keyless cosign signing
    environment: production
    steps:
      - name: Checkout
        uses: actions/checkout@<SHA>  # v7.0.0
        with:
          fetch-depth: 0            # goreleaser needs full history
          persist-credentials: false

      - name: Validate tag format
        env:
          VERSION: ${{ inputs.version || github.ref_name }}
        run: |
          if [[ ! "${VERSION}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?$ ]]; then
            echo "::error::Invalid tag format: ${VERSION} (expected vX.Y.Z or vX.Y.Z-suffix)"
            exit 1
          fi

      - name: Bootstrap environment
        uses: ./.github/actions/bootstrap

      - name: Validate Apple notarization credentials
        run: .tool/quill submission list
        env:
          QUILL_NOTARY_ISSUER: ${{ secrets.APPLE_NOTARY_ISSUER }}
          QUILL_NOTARY_KEY_ID: ${{ secrets.APPLE_NOTARY_KEY_ID }}
          QUILL_NOTARY_KEY: ${{ secrets.APPLE_NOTARY_KEY }}

      - name: Run GoReleaser
        uses: goreleaser/goreleaser-action@<SHA>  # v7.2.3
        with:
          distribution: goreleaser
          version: "~> v2"
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          QUILL_SIGN_P12: ${{ secrets.APPLE_DEVELOPER_ID_CERT_CHAIN }}
          QUILL_SIGN_PASSWORD: ${{ secrets.APPLE_SIGNING_CERT_PASSWORD }}
          QUILL_NOTARY_ISSUER: ${{ secrets.APPLE_NOTARY_ISSUER }}
          QUILL_NOTARY_KEY_ID: ${{ secrets.APPLE_NOTARY_KEY_ID }}
          QUILL_NOTARY_KEY: ${{ secrets.APPLE_NOTARY_KEY }}

  release-install-script:
    name: "Release install script"
    needs: [release]
    if: ${{ always() && (needs.release.result == 'success' || github.event.inputs.phase == 'install-script-only') }}
    uses: ./.github/workflows/release-install-script.yaml
    with:
      tag: ${{ github.event.inputs.version || github.ref_name }}
    secrets:
      R2_INSTALL_ACCESS_KEY_ID: ${{ secrets.R2_INSTALL_ACCESS_KEY_ID }}
      R2_INSTALL_SECRET_ACCESS_KEY: ${{ secrets.R2_INSTALL_SECRET_ACCESS_KEY }}
      R2_ENDPOINT: ${{ secrets.R2_ENDPOINT }}
```

Implementation notes:

- **`<SHA>` is not a placeholder to leave in.** Resolve each before committing:
  `gh api repos/actions/checkout/git/ref/tags/v7.0.0 --jq '.object.sha'`
  (dereference the tag object if it is annotated). Keep the `# v7.0.0` comment
  beside it — that comment is what dependabot reads to offer bumps.
- **`uses: ./.github/workflows/validations.yaml` requires that file to exist
  with `workflow_call:`** — Stage E. Build Stage E first or this fails on its
  first run with an unhelpful "invalid workflow file."
- **`environment: production`** gates the Apple secrets behind an environment,
  matching triage. Create it in repo settings; leave it without required
  reviewers unless the owner wants a manual approval on every release.
- The `phase: install-script-only` input exists to re-push the install script
  without re-cutting a release. Worth keeping — it costs one `if:` and saves a
  bad afternoon.

---

## 9. Stage E — CI as a reusable workflow

Triage names it `validations.yaml` and the release workflow calls it by path.
hoard's `ci.yml` did the same job under a different name.

**The rename is already done** (2026-08-06, uncommitted): the file is
`.github/workflows/validations.yaml`, its `name:` is `Validations`, the README
badge points at the new filename, and `scan.yml`'s header comment was updated to
match. The badge is the part that breaks silently — its URL contains the
workflow filename — so if a later rename ever happens, fix `README.md` in the
same commit. The job contents were left alone; everything below is still to do.

Changes beyond the rename:

**E.1 — Add `workflow_call:` and `workflow_dispatch:`** to the `on:` block,
keeping `push: branches: [master]` and `pull_request:`.

**E.2 — Add a concurrency group.** Three pushes to a PR currently mean three
full runs:

```yaml
concurrency:
  group: ${{ github.workflow }}-${{ github.event.pull_request.number || github.ref }}
  cancel-in-progress: true
```

**E.3 — Two jobs, both bootstrapped.** `Static-Analysis` running
`make static-analysis`, and `Unit-Test` running `make test` — each using
`./.github/actions/bootstrap`. This replaces the current inline gofmt/vet/test/build
steps with the same commands a developer runs locally, which is the whole
argument for Stage A.

**E.4 — Pin actions by SHA, and `persist-credentials: false`** on every
checkout. A floating tag is a mutable reference to code that runs with
repository write scope in the release job.

**E.5 — `.golangci.yaml`.** Triage's is a good starting point: errcheck, govet,
ineffassign, staticcheck, unused, with `check-type-assertions: true`. Expect the
first run to surface real findings — fix them in a commit separate from the
config, so review can see each half.

**E.6 — `.github/scripts/go-mod-tidy-check.sh`.** Copy verbatim, `chmod +x`.
Proves `go.mod`/`go.sum` are what `go mod tidy` would write.

Verify: `make static-analysis && make test`, then push a branch and confirm the
PR checks go green.

---

## 10. Stage F — Supply chain, security, and the install path

### 10.1 Copy-with-substitution from triage

- **`.github/dependabot.yaml`** — gomod + github-actions, weekly, 7-day
  cooldown, `chore(deps)` prefix. The github-actions ecosystem is what turns SHA
  pinning from a maintenance burden into a PR queue.
- **`.github/workflows/codeql.yaml`** — `main` → `master`. Weekly cron plus
  push/PR. Free on public repos.
- **`.github/workflows/validate-github-actions.yaml` + `.github/zizmor.yml`** —
  zizmor lints the workflows themselves (unpinned `uses`, credential
  persistence, template injection). Runs only on `.github/**` changes.
- **`SECURITY.md`** — project name and contact swapped. GitHub surfaces it in
  the Security tab and the "Report a vulnerability" flow.
- **`.chronicle.yaml`** — the two-comment defaults file.

### 10.2 `install.sh` and R2

**F.1 — `install.sh` at the repo root.** Copy triage's 229-line script;
substitute `PROJECT_NAME="hoard"` and `OWNER="spiffcs"`. It detects OS/arch,
downloads the archive and `checksums.txt` from the GitHub releases API, verifies
the SHA256, and verifies the cosign bundle when `cosign` is on PATH. Note it
handles darwin and linux only — Windows users take the release page, which is
what the README should say.

**F.2 — `.github/workflows/release-install-script.yaml`.** Copy triage's
reusable workflow unchanged. It is already project-agnostic: the R2 key is
`${{ github.event.repository.name }}/...`, so it writes to `hoard/<tag>/install.sh`
and `hoard/install.sh` with no edits.

**F.3 — R2 setup** (needs org access, not just repo access):

1. Confirm which bucket hoard belongs in. The workflow's default is
   `oss-prod-install`, shared with the org's other tools — if hoard goes
   somewhere else, pass `r2-bucket:` from the caller in `release.yaml`.
2. Add the three secrets: `R2_INSTALL_ACCESS_KEY_ID`,
   `R2_INSTALL_SECRET_ACCESS_KEY`, `R2_ENDPOINT`.
3. Add the Cloudflare rewrite rule for `/hoard` → `/hoard/install.sh`, matching
   the existing per-project rules. Without it the short URL 404s while the
   versioned path works — a confusing failure worth pre-empting.

Until step 3 is confirmed, the README should quote the full path, not the short
one. Fix it after the first release verifies which URL actually resolves.

---

## 11. Stage G — Repo polish

Everything here is prose, and prose is the part an agent drafts and a human
reads before it ships.

**G.1 — README install section.** The README currently has no install path at
all — it goes from the screenshot straight into usage. Add, right after the
badges: the `install.sh` one-liner, `go install github.com/spiffcs/hoard@latest`,
manual download from the releases page with the `cosign verify-blob`
incantation, and one sentence saying the release binary is the CLI — the card
scanner needs the Swift helper and the iPhone app built from source on a Mac
(link `docs/ios-development.md`).

Because of D2 there is **no** Gatekeeper workaround to document: the macOS
binaries are signed and notarized, so they open on a double-click. That is the
payoff for §8.1, and it is worth one sentence in the README.

**G.2 — Licensing prose** (P0.3 and the P1s from Stage 0): the Fan Content
notice, a credits section naming Scryfall / MTGJSON (with Zach Halpern's MIT
line) / tcgcsv and disclaiming affiliation, and the price disclaimer — one line
in the README and one in `docs/pricing.md`.

**G.3 — `CONTRIBUTING.md`.** Short. How to bootstrap (`make tools`), build
(`make build`), test (`make test`), what CI gates on, the fact that the Swift
half needs macOS + Xcode and is **not** gated in CI (see the `platforms:` caveat
in A.2), and that commits are conventional-ish (`feat:`/`fix:`) — which the
existing history already follows and which chronicle keys off.

**G.4 — `RELEASE.md`.** Triage's is genuinely good and mostly project-agnostic:
tag format, the "never retag, always patch-release" rule with the Go
checksum-db reasoning, the Sigstore verification model, and what each release
artifact is. Copy it and substitute the project name.

**G.5 — Issue and PR templates.** `.github/ISSUE_TEMPLATE/bug_report.yml`,
`feature_request.yml`, `config.yml`. Triage has none — this is hoard going a
step further, and it is cheap. The bug template should ask for `hoard version`
output, which is exactly why Stage B exists.

---

## 12. Stage H — Cut the first release

In order, stopping at the first failure:

1. **Dry run.** `.tool/goreleaser release --snapshot --clean --skip=sign` on a
   clean tree. Inspect `dist/`: six archives (3 OS × 2 arch, Windows as `.zip`),
   `checksums.txt`, and a binary that prints a real version.
2. **Confirm CI is green on `master`** — all of Stage E and F, not just the
   build job.
3. **Confirm the Apple preflight passes**: `.tool/quill submission list` with
   the notary env vars.
4. **Tag and push.**
   ```bash
   git tag -a v0.1.0 -m "Release v0.1.0"
   git push origin v0.1.0
   ```
   *(Committing and pushing is the owner's to run, not an agent's.)*
5. **Watch it.** `gh run watch`. The four likely first-time failures, in the
   order they'd hit: tag-format regex (fails fast, harmless); quill preflight
   (bad base64 on the `.p12` is the usual cause); notarization submit (Apple can
   take minutes — it is slow, not hung); cosign (needs `id-token: write`, and
   the error when it is missing is an opaque OIDC fetch failure rather than a
   permissions message).
6. **Verify what shipped, as a user would:**
   ```bash
   gh release view v0.1.0
   cosign verify-blob --bundle checksums.txt.sigstore.json \
     --certificate-identity-regexp "^https://github.com/spiffcs/hoard/.*" \
     --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
     checksums.txt
   # on a Mac, the notarization payoff — must print "accepted", with no prompt:
   spctl -a -vvv -t install ./hoard
   curl -sSfL https://<r2-host>/hoard/install.sh | sh -s -- -b /tmp/hoard-install
   /tmp/hoard-install/hoard version
   ```
7. **If it fails after the tag is pushed: do not delete and retag.** Fix forward
   and cut `v0.1.1`. `RELEASE.md` explains why — once the Go checksum database
   has seen a version, changing what it points at produces `SECURITY ERROR: sum
   mismatch` for anyone who fetched it.

---

## 13. Deferred, on purpose

- **Homebrew tap** — worth it at real download volume, not before.
- **Chronicle in the release pipeline** — triage generates changelogs with
  chronicle *locally* (`make changelog`, `make unreleased`) and lets goreleaser
  build the release notes. hoard matches that. Wire chronicle into the workflow
  only if the commit-derived notes prove inadequate.
- **Re-enabling `scan.yml` on push/PR** — the macOS billing math changes once
  the repo is public, but that is a separate decision with its own tradeoffs;
  see the header comment in that file.
- **Windows in `install.sh`** — triage's script is POSIX sh and handles
  darwin/linux. Windows users take the releases page.

---

## 14. Order of execution, condensed

```
Stage 0  licensing P0s + secret audit + go public       ← blocks everything
Stage A  binny + Taskfile + Makefile shim (Go & Swift)  ← blocks B, C, E
Stage B  internal/version + `hoard version` + tests     ← blocks C
Stage C  .goreleaser.yaml (+ quill hook) + snapshot     ← blocks D
Stage E  validations.yaml (renamed ✓), bootstrap        ← blocks D
         action, golangci, go-mod-tidy check
Stage D  Apple secrets (§8.1, long lead — start early)
         + .github/workflows/release.yaml
Stage F  dependabot, codeql, zizmor, SECURITY.md,       ← R2 setup needs org access
         install.sh, R2 workflow + bucket + rewrite
Stage G  README install + credits + CONTRIBUTING +
         RELEASE.md + issue templates
Stage H  tag v0.1.0, verify as a user would
```

§8.1 (Apple credentials) and §10.2 (R2 access) are the two items that depend on
people and portals rather than code. Start both on day one; everything else can
proceed in parallel while they land.
