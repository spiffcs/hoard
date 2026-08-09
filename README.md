<p align="center">
    <img src="docs/assets/hoard-logo.png" width="271" alt="A cavern with a mountain of gold — the hoard logo">
</p>

# hoard

[![Validations](https://github.com/spiffcs/hoard/actions/workflows/validations.yaml/badge.svg)](https://github.com/spiffcs/hoard/actions/workflows/validations.yaml)

**CLI for managing a Magic: The Gathering collection**.

Your cards live in a single SQLite file on your machine. `hoard` opens an
interactive browser; `hoard help` lists every command.

## Install

```sh
curl -sSfL https://tools.aithirne.com/hoard/install.sh | sh -s -- -b /usr/local/bin
```

Or install with Go (1.26+):

```sh
go install github.com/spiffcs/hoard/cmd/hoard@latest
```

Or download an archive from the [releases page](https://github.com/spiffcs/hoard/releases)
(Windows users: this is the path — the install script covers macOS and Linux only).
Every release can be verified against its Sigstore bundle:

```sh
cosign verify-blob \
    --bundle checksums.txt.sigstore.json \
    --certificate-identity-regexp "^https://github.com/spiffcs/hoard/.*" \
    --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
    checksums.txt
```

The macOS binaries are signed and notarized, so they open without a Gatekeeper
fight. The release binary is the CLI; the card scanner is a separate Swift
helper and iPhone app built from source on a Mac — see
[docs/ios-development.md](docs/ios-development.md).

Or build from source (Go 1.26+):

```sh
git clone https://github.com/spiffcs/hoard && cd hoard
make build          # → ./hoard
```

## Scripting and your data

Every card carries a Scryfall id and an MTGJSON uuid, so a document joins straight against either
ecosystem's bulk data. `hoard`, `unpriced`, `movers`, `market`, `report`, `watch` and `export`
all take `--json` for machine-readable output.

## Decks and binders

Cards are organized into binders (`hoard binder new trades`) and decks. Decks import from an
Archidekt link or a pasted text decklist (`hoard deck add --file list.txt`), and collection
CSV exports from ManaBox, Moxfield and Delver Lens import with `hoard import`.

## Scanning cards (macOS + iPhone)

On macOS, `hoard add` can drive a paired iPhone as a hands-free card scanner: cards held up to
the phone are recognized, priced and committed to a binder, with foil detection and a review
queue for uncertain reads. The phone app lives in `scan/hoard-scan-ios`.

## Database

| OS | Default location |
|---|---|
| macOS | `~/Library/Application Support/hoard/hoard.db` |
| Linux | `$XDG_DATA_HOME/hoard/hoard.db` (else `~/.local/share/hoard/hoard.db`) |
| Windows | `%AppData%\hoard\hoard.db` |

Override with `--db PATH` (anywhere on the command line) or an environment variable `$HOARD_DB`.
Schema upgrades back up the old file alongside first (`hoard.db.bak-v1-20260729`).

The card catalog and price downloads live separately in
your cache directory and are always safe to delete.

## Development

```sh
make tools               # install the pinned toolchain into .tool/
make build               # go build -o hoard ./cmd/hoard
make test                # go test ./...   (no network needed)
make static-analysis     # golangci-lint + go mod tidy check
make help                # list every target, the Swift scanner's included
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full development workflow and
[RELEASE.md](RELEASE.md) for how releases are cut and verified.

## License and legal

Code is [MIT](LICENSE) — © 2026 Christopher Phillips. Card imagery in this repository remains
the property of Wizards of the Coast (see the scope note in [LICENSE](LICENSE)).

hoard is unofficial Fan Content permitted under the Fan Content Policy. Not approved/endorsed
by Wizards. Portions of the materials used are property of Wizards of the Coast. ©Wizards of
the Coast LLC.

Card data courtesy of [Scryfall](https://scryfall.com), [MTGJSON](https://mtgjson.com)
(MIT, © 2018–Present Zach Halpern) and [TCGCSV](https://tcgcsv.com). hoard is not
affiliated with, endorsed by, or sponsored by any of them. All prices are daily estimates
from third-party aggregators, not quotes, with absolutely no guarantee — see stores for
final prices.
