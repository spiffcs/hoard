# Releasing

Releases are triggered by pushing a git tag. The release workflow validates the tag format, runs the quality gate, and uses GoReleaser to build and publish.

## Using the CLI

```bash
git tag -a v0.1.0 -m "Release v0.1.0"
git push origin v0.1.0
```

## Using the GitHub UI

1. Go to the [Releases page](../../releases/new)
2. Click "Choose a tag" and type your new version (e.g., `v0.1.0`)
3. Click "Create new tag: v0.1.0 on publish"
4. Add release notes and click "Publish release"

## Tag Format

The tag format must match `vX.Y.Z` or `vX.Y.Z-suffix` (e.g., `v0.1.0`, `v0.2.0-rc1`). A `-suffix` tag is marked as a prerelease automatically.

## When a Release Fails

If the release workflow fails after the tag is pushed, **do not delete and recreate the tag**. Instead:

1. **Fix the issue** in a new commit on `main`
2. **Bump the version** and create a new tag (e.g., if `v0.2.0` failed, release `v0.2.1`)
3. If the failed tag created a partial GitHub release, delete the release (not the tag) from the UI

### Why Not Retag?

Retagging is problematic:

- Once a version is fetched by anyone, it's recorded in the Go checksum database and module caches
- Changing what `v1.2.3` points to causes checksum mismatches and `SECURITY ERROR` / `sum mismatch` errors for users
- The Go proxy may have already cached the original tag's contents

**Treat tags as immutable.** A patch release is always safer than retagging.

## Security Model

Releases are signed using [Sigstore](https://www.sigstore.dev/) for cryptographic proof of artifact origin. This provides supply chain security without managing private keys. The macOS binaries are additionally signed with a Developer ID certificate and notarized by Apple (via [quill](https://github.com/anchore/quill)), so they open without a Gatekeeper prompt.

**Checksum signing:** Rather than signing each artifact individually, we sign `checksums.txt` which contains SHA256 hashes of all release artifacts. This transitively verifies all artifacts through a single signature.

### What Gets Published

Each release includes:
- `hoard_<version>_<os>_<arch>.tar.gz` - The binary archive (`.zip` for Windows)
- `checksums.txt` - SHA256 hashes of all archives
- `checksums.txt.sigstore.json` - Sigstore bundle (contains signature, certificate, and transparency log entry)

The release binary is the CLI only: the Swift scan helper and the iPhone app are built from source on a Mac and do not ship in release archives.

### Verifying a Release

Users can verify that artifacts were built by this repository's GitHub Actions workflow:

```bash
# Download the artifact and verification files
cosign verify-blob \
    --bundle checksums.txt.sigstore.json \
    --certificate-identity-regexp "^https://github.com/spiffcs/hoard/.*" \
    --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
    checksums.txt

# Then verify the artifact checksum
sha256sum -c checksums.txt --ignore-missing
```
