# Contributing to hoard

## Getting started

```sh
make tools     # bootstrap the pinned toolchain (binny → .tool/)
make build     # go build -o hoard ./cmd/hoard
make test      # go test ./...   (no network needed)
make help      # list every target
```

The Makefile is a shim: every target is a [Taskfile](Taskfile.yaml) task, and
`make <target>` forwards to `task <target>`. Tool versions are pinned in
[.binny.yaml](.binny.yaml), so CI and your machine run the same binaries.

## What CI gates on

Every PR runs [validations.yaml](.github/workflows/validations.yaml):

- `make static-analysis` — golangci-lint (config in [.golangci.yaml](.golangci.yaml)),
  gofmt, and a `go mod tidy` check
- `make test` and `make build`

Run both locally before pushing; they are the same commands CI runs.

## The Swift half

The card scanner (macOS helper + iPhone app under `scan/`) needs **macOS and
Xcode** and is **not** gated in regular CI — its Taskfile tasks carry
`platforms: [darwin]` and skip silently on Linux, so a green Linux run proves
nothing about the Swift side. The scanner gate is local:

```sh
make scan-test scan-check
```

plus the path-filtered [scan.yml](.github/workflows/scan.yml) workflow on
macOS runners. See [docs/ios-development.md](docs/ios-development.md) for the
iPhone app.

## Commit messages

Conventional-ish prefixes (`feat:`, `fix:`, `docs:`, `chore:`), which the
existing history follows and which [chronicle](https://github.com/anchore/chronicle)
keys off for changelogs.

## Reporting problems

Bug reports should include `hoard version` output — see the issue templates.
Security issues go through [SECURITY.md](SECURITY.md), not public issues.
