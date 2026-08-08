# Security Policy

## Supported Versions

| Version | Supported          |
| ------- | ------------------ |
| latest  | :white_check_mark: |

## Reporting a Vulnerability

We take security vulnerabilities seriously. If you discover a security issue, please report it responsibly.

**Please do NOT report security vulnerabilities through public GitHub issues.**

Instead, please report them via GitHub's private vulnerability reporting:

1. Go to the [Security tab](../../security) of this repository
2. Click "Report a vulnerability"
3. Fill out the form with details about the vulnerability

### What to include

- Type of issue (e.g., buffer overflow, SQL injection, cross-site scripting, etc.)
- Full paths of source file(s) related to the issue
- Location of the affected source code (tag/branch/commit or direct URL)
- Any special configuration required to reproduce the issue
- Step-by-step instructions to reproduce the issue
- Proof-of-concept or exploit code (if possible)
- Impact of the issue, including how an attacker might exploit it

### Response Timeline

- We will acknowledge receipt of your vulnerability report
- We will provide a more detailed response within 10 business days
- We will work with you to understand and resolve the issue promptly

## Security Best Practices for Users

- Always use the latest version of hoard
- Verify downloaded release artifacts against `checksums.txt` and its Sigstore bundle (see [RELEASE.md](RELEASE.md)); `install.sh -v` does this for you when `cosign` is installed
- Your collection lives in a local SQLite file (see the README for its location); it is your data so back it up
- hoard talks only to card-data services (Scryfall, MTGJSON, TCGCSV) and requires no accounts, tokens, or credentials
