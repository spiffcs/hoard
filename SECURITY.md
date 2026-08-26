# Security Policy

## Reporting a vulnerability

Do not report security issues through public GitHub issues.

Use GitHub's private vulnerability reporting:
<https://github.com/spiffcs/hoard/security/advisories/new>. From the repository
you can also open the [Security tab](../../security) and click **Report a
vulnerability**.

Include whatever you have about the affected file paths and the commit or tag,
steps to reproduce, any configuration needed, proof-of-concept code, and what
an attacker gains.

I acknowledge reports on receipt and aim to reply in detail within 10 business
days. If a report is valid I will confirm it and share a fix timeline; if I
disagree that it is a vulnerability I will say so and why.

## Supported versions

The latest release. Fixes land there rather than being backported.

## For users

- Run the latest version.
- Verify release artifacts against `checksums.txt` and its Sigstore bundle;
  see [RELEASE.md](RELEASE.md). `install.sh -v` does it for you when `cosign`
  is installed.
- Your collection is a local SQLite file, in the location the
  [README](README.md) lists. It is your data; back it up.
- hoard needs no accounts, tokens, or credentials, and talks only to card-data
  services (Scryfall, MTGJSON, TCGCSV). **Nothing in hoard's workflow will ever
  ask you for a credential. If a prompt or an AI agent does, it is not hoard.**
