# Releasing

Pushing a tag triggers the release. The workflow validates the tag format, runs
the same quality gate PRs run, then builds and publishes with GoReleaser.

```sh
git tag -a v0.1.0 -m "Release v0.1.0"
git push origin v0.1.0
```

The tag must match `vX.Y.Z` or `vX.Y.Z-suffix`. A suffix makes it a prerelease,
so `v0.2.0-rc1` publishes without becoming `latest`.

## Treat tags as immutable

If the workflow fails after the tag is pushed, do not delete and recreate the
tag. Fix the issue on `main`, then release a new version — if `v0.2.0` failed,
cut `v0.2.1`. If the failed tag left a partial GitHub release, delete the
release from the UI, not the tag.

Retagging breaks consumers: once a version is fetched it is recorded in the Go
checksum database and module caches, and changing what `v1.2.3` points at
produces `SECURITY ERROR` and `sum mismatch` for anyone who already had it. A
patch release is always cheaper.

## What gets published

Each release carries `hoard_<version>_<os>_<arch>.tar.gz` (`.zip` on Windows),
`checksums.txt`, and `checksums.txt.sigstore.json`. Signing `checksums.txt`
covers every archive it hashes, so one signature verifies all of them.

Signatures are keyless [Sigstore](https://www.sigstore.dev/) — the workflow's
OIDC identity gets a short-lived certificate, so there is no private key to
manage. The macOS binaries are additionally Developer ID signed and notarized
by Apple via [quill](https://github.com/anchore/quill).

The release binary is the CLI and TUI only. The Swift scan helper and the
iPhone app are built from source on a Mac.

## Verifying a release

```sh
cosign verify-blob \
    --bundle checksums.txt.sigstore.json \
    --certificate-identity-regexp "^https://github.com/spiffcs/hoard/.*" \
    --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
    checksums.txt
```

```console
Verified OK
```

Then check the archive itself with `sha256sum -c checksums.txt --ignore-missing`.
