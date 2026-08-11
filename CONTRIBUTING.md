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

## Commit messages

Conventional-ish prefixes (`feat:`, `fix:`, `docs:`, `chore:`), which the
existing history follows and which
[chronicle](https://github.com/anchore/chronicle) keys off for changelogs.

## Reporting problems

Include `hoard version` output in bug reports. Security issues go through
[SECURITY.md](SECURITY.md), not public issues.
