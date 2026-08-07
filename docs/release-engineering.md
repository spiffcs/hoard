# Release engineering plan

How hoard gets from "private repo with a CI job" to "public repo people can
install a signed binary from." The reference implementation is
[spiffcs/triage](https://github.com/spiffcs/triage), the other Go TUI in this
account: same owner, same language, same shape of release. This document says
what to copy, what to adapt, and what to deliberately leave behind.

It is written to be executed top to bottom by an agent. Every stage lists the
files it touches, the exact content where that content is short enough to
inline, and the command that proves the stage landed. Stages are ordered by
dependency: Stage 0 blocks everything, A blocks B, B blocks C. D through G can
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
| Supply chain | cosign keyless signing, CodeQL, zizmor, dependabot | **none** |
| Repo meta | `SECURITY.md`, `RELEASE.md`, `CHANGELOG.md`, `install.sh` | README + LICENSE + `docs/` |
| Toolchain pinning | binny (`.binny.yaml`) + Taskfile, Makefile is a shim | hand-written Makefile (Go **and** Swift targets) |

hoard's advantage over triage: `modernc.org/sqlite` is **pure Go**. There is no
cgo anywhere in the module, so `CGO_ENABLED=0` cross-compiles to every target
from one ubuntu runner. No zig, no cross toolchain, no per-arch runner matrix.

hoard's complication: the repo also holds a Swift macOS helper and a SwiftUI
iPhone app (`scan/`). Neither can ship in a Go release archive. See §3.

---

## 2. Decisions already made

These were weighed against triage's setup and settled. Do not re-litigate them
mid-implementation; if one turns out wrong, change it here first.

**D1 — Adopt goreleaser + cosign. Skip binny and Taskfile.**
Triage's `Makefile` is a shim that bootstraps `binny`, which installs pinned
tool binaries into `.tool/`, and delegates every target to `Taskfile.yaml`.
That indirection pays for itself across a dozen Anchore repos with a shared
toolchain. hoard has one repo and a Makefile that already carries Swift targets
(`scan`, `cardkit`, `scan-ios-test`) which have nothing to do with the Go
toolchain. Adopting binny would give hoard's build two homes and leave the Swift
half outside both. Instead: keep the Makefile as the single entry point, and pin
tool versions in the workflows that use them (goreleaser-action, golangci-lint
action, both by SHA). If hoard ever grows a second repo sharing this toolchain,
revisit.

**D2 — Sign checksums with cosign keyless. Do not notarize with quill yet.**
Cosign keyless signing costs nothing and needs no secrets: it uses the workflow's
OIDC identity, so `id-token: write` is the entire setup. Apple notarization
(triage's `quill sign-and-notarize` hook) needs five repo secrets and a Developer
ID certificate. hoard's Apple Developer account exists but the iOS sprint is
still blocked on signing (`docs/ios-development.md`), so wiring notarization now
would block the first release on an unrelated blocker. Consequence: macOS users
who download the tarball hit Gatekeeper quarantine. Mitigate with an `xattr`
line in the README install section (Stage F), and add quill in a later release
once the Apple side is unblocked.

**D3 — Ship `install.sh` from the repo, not from Cloudflare R2.**
Triage's `release-install-script.yaml` uploads `install.sh` to an R2 bucket for a
short vanity URL. The script itself only ever downloads from
`github.com/OWNER/REPO/releases/download`, so it works fine served from
`raw.githubusercontent.com`. Copy the script; skip the workflow and its three R2
secrets entirely.

**D4 — Branch is `master`, not `main`.**
Every triage workflow says `branches: [main]`. hoard's default branch is
`master`. Each copied file needs that substitution — it is the single most
likely silent mistake in this whole plan, because a workflow with the wrong
branch filter simply never runs and reports nothing.

**D5 — First tag is `v0.1.0`, prerelease semantics via `prerelease: auto`.**
hoard has no tags. Pre-1.0 signals "the CLI surface can still move," which is
true. `prerelease: auto` in goreleaser marks anything with a `-rc1`-style suffix
as a prerelease automatically, so `v0.2.0-rc1` behaves correctly with no config
change.

---

## 3. What is explicitly out of scope

- **The Swift scan helper and the iPhone app.** `bin/hoard-scan.app` and the iOS
  head are built by `build-scan.sh` / `build-scan-ios.sh` on macOS with Xcode.
  They are not in the goreleaser build matrix and not in the release archives.
  The iPhone app has its own distribution path — `docs/app-store-release.md`.
  The README must say plainly that the release binary is the CLI, and that
  scanning needs a separate build from source on a Mac.
- **The macOS `scan.yml` workflow.** It stays manual-only for the reasons its own
  header comment gives (10× billing on macOS runners). Releasing does not change
  that math. Do not re-enable it as part of this work.
- **Homebrew tap / winget / Linux packages.** goreleaser can produce all of them.
  None on the first release; revisit once there is download traffic to justify
  the maintenance.

---

## 4. Stage 0 — Blockers before the repo goes public

Everything below assumes the repo will be public. These items must be true
*before* flipping visibility, because a public repo is a permanent artifact —
the licensing exposure and the first impression both start the moment it flips.

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

Verify the first two against the file before editing — line numbers drift.
The P1 items in that same section (attribution/credits block, price disclaimer,
extended User-Agent) are cheap and clearly right; fold them into Stage F.

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
--accept-visibility-change-consequences`. Do this only when 0.1 and 0.2 are
done, and only with the owner's explicit go-ahead in the same session. Once
public, private-repo Actions minutes billing stops applying, which is also when
re-enabling `scan.yml` becomes worth reconsidering (separately, not here).

---

## 5. Stage A — Version plumbing

goreleaser's ldflags need somewhere to write to, and hoard has no version
symbol at all today. This is a code change, and it blocks Stage B.

**A.1 — Add `internal/version/version.go`:**

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

**A.2 — Wire a `version` command into `main.go`'s dispatch switch** (the `case`
ladder starting around line 121), beside `catalog` and `binder`. It prints the
three values, the Go version and `runtime.GOOS/GOARCH`, and the Fan Content
notice from Stage 0.1. Accept `--version` and `-v` as top-level flags mapping to
the same code path, since that is what people type. Add the row to
`usage.go`'s `usageSections` — the usage table is data, so it is one entry.

**A.3 — Cover it in `main_test.go`.** One test that the command runs and its
output contains the version string. The existing tests in that file show the
house pattern for invoking `run()` with args and capturing output.

Verify:

```bash
go test ./... && go build -o hoard . && ./hoard version
go build -ldflags "-X github.com/spiffcs/hoard/internal/version.Version=v0.0.1-test" -o /tmp/hoard-vt . && /tmp/hoard-vt version
```

The second command is the one that matters: if the ldflag path is wrong, the
build silently succeeds and still prints `dev`. Confirm it prints `v0.0.1-test`.

---

## 6. Stage B — `.goreleaser.yaml`

New file at the repo root. This is triage's, adapted: one build entry instead of
two (no per-OS split, because no notarization hook), Windows added, and the
ldflags pointed at hoard's `internal/version`.

```yaml
version: 2

project_name: hoard

builds:
  - id: hoard
    binary: hoard
    main: .
    # modernc.org/sqlite is pure Go: no cgo anywhere in the module, so every
    # target cross-compiles from the ubuntu runner with no toolchain wrangling.
    env:
      - CGO_ENABLED=0
    goos:
      - linux
      - darwin
      - windows
    goarch:
      - amd64
      - arm64
    ldflags:
      - -s -w
      - -X github.com/spiffcs/hoard/internal/version.Version={{ .Version }}
      - -X github.com/spiffcs/hoard/internal/version.GitCommit={{ .Commit }}
      - -X github.com/spiffcs/hoard/internal/version.BuildDate={{ .Date }}

archives:
  - id: default
    formats:
      - tar.gz
    format_overrides:
      - goos: windows
        formats:
          - zip
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
  - cmd: cosign
    signature: "${artifact}.sigstore.json"
    args:
      - "sign-blob"
      - "--bundle=${signature}"
      - "--yes"
      - "${artifact}"
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

Note the difference from triage: `cmd: cosign` rather than `.tool/cosign`,
because D1 drops binny. The release workflow installs cosign via
`sigstore/cosign-installer` instead.

Verify locally — this is the whole point of doing Stage B before Stage C:

```bash
brew install goreleaser      # or: go install github.com/goreleaser/goreleaser/v2@latest
goreleaser check
goreleaser release --snapshot --clean --skip=sign
ls dist/
./dist/hoard_darwin_arm64*/hoard version
```

A snapshot build produces every archive without touching GitHub. It must print
a version like `v0.0.1-next+abc1234` — if it prints `dev`, the ldflag path in
A.1/B is mismatched. `--skip=sign` because keyless cosign wants an OIDC token
that only exists in Actions.

---

## 7. Stage C — The release workflow

New file `.github/workflows/release.yaml`. Adapted from triage: the quill
notarization step is gone (D2), the R2 install-script job is gone (D3), and
cosign is installed rather than bootstrapped by binny.

```yaml
name: "Release"

on:
  push:
    tags:
      - "v*"
  workflow_dispatch:
    inputs:
      version:
        description: "Version to release (e.g., v0.1.0)"
        required: true
        type: string

permissions:
  contents: read

concurrency:
  group: ${{ github.workflow }}-${{ github.ref }}
  cancel-in-progress: false

jobs:
  # The same quality gate PRs run. A tag that cannot pass CI must not produce
  # a release, and tags are effectively immutable (see RELEASE.md), so this
  # gate is the last chance to catch it.
  validations:
    name: "Validations"
    uses: ./.github/workflows/ci.yml

  release:
    name: "Release"
    needs: validations
    runs-on: ubuntu-latest
    permissions:
      contents: write  # create the GitHub release
      id-token: write  # keyless cosign signing
    steps:
      - name: Checkout
        uses: actions/checkout@<SHA>  # v7.0.0
        with:
          fetch-depth: 0            # goreleaser needs full history for the changelog
          persist-credentials: false

      - name: Validate tag format
        env:
          VERSION: ${{ inputs.version || github.ref_name }}
        run: |
          if [[ ! "${VERSION}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?$ ]]; then
            echo "::error::Invalid tag format: ${VERSION} (expected vX.Y.Z or vX.Y.Z-suffix)"
            exit 1
          fi

      - uses: actions/setup-go@<SHA>  # v6.2.0
        with:
          go-version-file: go.mod

      - uses: sigstore/cosign-installer@<SHA>  # v3.x

      - name: Run GoReleaser
        uses: goreleaser/goreleaser-action@<SHA>  # v7.2.3
        with:
          distribution: goreleaser
          version: "~> v2"
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

Implementation notes for whoever writes this file:

- **`<SHA>` is not a placeholder to leave in.** Resolve each one before
  committing: `gh api repos/actions/checkout/git/ref/tags/v7.0.0 --jq
  '.object.sha'` (for a lightweight tag; for annotated tags dereference with
  `--jq '.object.sha'` then fetch the tag object). Keep the `# v7.0.0` comment
  beside it — that comment is what dependabot reads to offer bumps.
- **`uses: ./.github/workflows/ci.yml` requires ci.yml to accept
  `workflow_call`.** That is part of Stage D. Do Stage D first or this workflow
  fails on its first run with a confusing "invalid workflow file" error.
- **No `environment: production`.** Triage uses one to gate its Apple secrets.
  hoard has no release secrets beyond the auto-provided `GITHUB_TOKEN`, so an
  environment adds an approval prompt with nothing to protect. Add one later
  alongside quill.
- **`persist-credentials: false`** on every checkout. It is a zizmor finding
  otherwise (Stage E), and there is no step here that needs the token in
  `.git/config`.

---

## 8. Stage D — Harden CI

`.github/workflows/ci.yml` exists and is sound in shape. Four changes:

**D.1 — Make it callable.** Add `workflow_call:` to the `on:` block so the
release workflow can reuse it. Keep `push: branches: [master]` and
`pull_request:` as they are.

**D.2 — Add a concurrency group.** Three pushes to a PR currently mean three
full runs. Copy the triage pattern:

```yaml
concurrency:
  group: ${{ github.workflow }}-${{ github.event.pull_request.number || github.ref }}
  cancel-in-progress: true
```

**D.3 — Pin actions by SHA and stop persisting credentials.** `actions/checkout@v7`
and `actions/setup-go@v7` become SHAs with a version comment, and checkout gets
`persist-credentials: false`. A floating tag is a mutable reference to code that
runs with repository write scope in the release job.

**D.4 — Add lint and a tidy check.** The current job is gofmt + vet, which misses
the whole `staticcheck`/`errcheck` class. Add:

- `.golangci.yaml` at the root — triage's is a good starting point (errcheck,
  govet, ineffassign, staticcheck, unused; `check-type-assertions: true`).
  Expect a first run with real findings; fix them in a separate commit from
  the config so review can see each half.
- `.github/scripts/go-mod-tidy-check.sh` — copy triage's verbatim, `chmod +x`.
  It proves `go.mod`/`go.sum` are what `go mod tidy` would write.
- A `lint` step using `golangci/golangci-lint-action` pinned by SHA, plus a
  `go mod tidy check` step running the script.
- Matching `make lint` / `make tidy-check` targets, so the local loop and CI
  run the same thing. That is the existing Makefile's convention already.

Verify: `make test && make vet && make lint && .github/scripts/go-mod-tidy-check.sh`,
then push a branch and confirm the PR checks go green.

---

## 9. Stage E — Supply chain and security

All four are copy-with-substitution from triage. None require secrets.

**E.1 — `.github/dependabot.yaml`.** Triage's verbatim: gomod + github-actions,
weekly, 7-day cooldown, `chore(deps)` commit prefix. The github-actions
ecosystem is what turns SHA pinning from a maintenance burden into a PR queue.

**E.2 — `.github/workflows/codeql.yaml`.** Triage's, with `main` → `master`.
Weekly cron plus push/PR. `security-events: write` is required for the SARIF
upload; on a public repo Code Scanning is free.

**E.3 — `.github/workflows/validate-github-actions.yaml` + `.github/zizmor.yml`.**
zizmor lints the workflows themselves — unpinned `uses`, credential
persistence, template injection. Runs only on `.github/**` changes. The
`zizmor.yml` policy needs `spiffcs/*: any` swapped to whatever hoard's own local
actions are (hoard has none today, so the file can start empty or keep the
policy for future use).

**E.4 — `SECURITY.md`.** Triage's, with the project name and contact swapped.
GitHub surfaces it in the Security tab and in the "Report a vulnerability" flow.

---

## 10. Stage F — Repo polish

This is the "looks like a real project, not slop" stage. Everything here is
prose, and prose is the part an agent should draft and a human should read.

**F.1 — README install section.** The README currently has no install path at
all — it goes from the screenshot straight into usage. Add, right after the
badges:

- `curl -sSfL https://raw.githubusercontent.com/spiffcs/hoard/master/install.sh | sh -s -- -b /usr/local/bin`
- `go install github.com/spiffcs/hoard@latest`
- Manual download from the releases page, with the `cosign verify-blob`
  incantation from §6's footer.
- The macOS Gatekeeper note (D2): `xattr -d com.apple.quarantine ./hoard` after
  extracting a downloaded archive, with one sentence saying why (the binary is
  signed for provenance but not Apple-notarized yet).
- One sentence: the release binary is the CLI. The card scanner needs the Swift
  helper and the iPhone app built from source on a Mac — link
  `docs/ios-development.md`.

**F.2 — `install.sh`.** Copy triage's 229-line script; substitute
`PROJECT_NAME`/`OWNER`. It detects OS/arch, downloads the archive and
`checksums.txt` from the GitHub releases API, verifies the SHA256, and
optionally verifies the cosign bundle when `cosign` is on PATH. Test it against
the first real release in Stage G — not before, since it needs a release to
download.

**F.3 — Licensing prose (P0.3 and the P1s from Stage 0).** The Fan Content
notice, a credits section naming Scryfall / MTGJSON (with Zach Halpern's MIT
line) / tcgcsv and disclaiming affiliation, and the price disclaimer — one line
in the README and one in `docs/pricing.md`.

**F.4 — `CONTRIBUTING.md`.** Short. How to build (`make build`), how to test
(`make test`), what CI gates on, the fact that the Swift half needs macOS +
Xcode and is not gated in CI, and that commits are conventional-ish
(`feat:`/`fix:`) — which the existing git history already follows and which
chronicle-style changelog tooling keys off.

**F.5 — `RELEASE.md`.** Triage's is genuinely good and mostly project-agnostic:
tag format, the "never retag, always patch-release" rule with the Go checksum-db
reasoning, and the Sigstore verification model. Copy it, substitute the
project name, and cut the sections that describe things hoard does not do.

**F.6 — Issue and PR templates.** `.github/ISSUE_TEMPLATE/bug_report.yml`,
`feature_request.yml`, and `config.yml`. Triage has none — this is hoard going a
step further, and it is cheap. The bug template should ask for `hoard version`
output, which is exactly why Stage A exists.

---

## 11. Stage G — Cut the first release

In order, stopping at the first failure:

1. **Dry run.** `goreleaser release --snapshot --clean --skip=sign` on a clean
   tree. Inspect `dist/`: six archives (3 OS × 2 arch, Windows as `.zip`),
   `checksums.txt`, and a binary that prints a real version.
2. **Confirm CI is green on `master`** — all of Stage D and E, not just the
   build job.
3. **Tag and push.**
   ```bash
   git tag -a v0.1.0 -m "Release v0.1.0"
   git push origin v0.1.0
   ```
   *(Committing and pushing is the owner's to run, not an agent's.)*
4. **Watch it.** `gh run watch` on the release workflow. The two steps most
   likely to fail first time: the tag-format regex (fails fast, harmless) and
   the cosign signing step (needs `id-token: write` — if it is missing, the error
   is an opaque OIDC token fetch failure, not a permissions message).
5. **Verify what shipped**, as a user would:
   ```bash
   gh release view v0.1.0
   cosign verify-blob --bundle checksums.txt.sigstore.json \
     --certificate-identity-regexp "^https://github.com/spiffcs/hoard/.*" \
     --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
     checksums.txt
   curl -sSfL https://raw.githubusercontent.com/spiffcs/hoard/master/install.sh | sh -s -- -b /tmp/hoard-install
   /tmp/hoard-install/hoard version
   ```
6. **If it fails after the tag is pushed: do not delete and retag.** Fix
   forward and cut `v0.1.1`. `RELEASE.md` explains why — once the Go checksum
   database has seen a version, changing what it points at produces `SECURITY
   ERROR: sum mismatch` for anyone who fetched it.

---

## 12. Deferred, on purpose

Written down so they read as decisions rather than oversights:

- **Apple notarization via quill** (D2) — needs Developer ID cert + notary API
  key as repo secrets; blocked behind the same Apple account work as the iOS
  sprint.
- **Homebrew tap** — worth it at real download volume, not before.
- **Changelog automation** (triage uses `chronicle` + `glow`) — goreleaser's
  built-in commit-based changelog is adequate for v0.1.x. Revisit when releases
  get frequent enough that hand-reading commits stops scaling.
- **Re-enabling `scan.yml` on push/PR** — the macOS billing math changes once
  the repo is public, but that is a separate decision with its own tradeoffs;
  see the header comment in that file.
- **`install.sh` hosting on a vanity domain** (D3) — raw.githubusercontent.com
  works and costs nothing.

---

## 13. Order of execution, condensed

```
Stage 0  licensing P0s + secret audit + go public      ← blocks everything
Stage A  internal/version + `hoard version` + tests    ← blocks B
Stage B  .goreleaser.yaml + local snapshot dry run     ← blocks C
Stage D  ci.yml: workflow_call, concurrency, SHA pins, ← blocks C (workflow_call)
         golangci-lint, go-mod-tidy check
Stage C  .github/workflows/release.yaml
Stage E  dependabot, codeql, zizmor, SECURITY.md       ← independent
Stage F  README install + install.sh + credits +       ← independent
         CONTRIBUTING + RELEASE.md + issue templates
Stage G  tag v0.1.0, verify as a user would
```
