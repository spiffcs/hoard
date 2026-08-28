# Updating

hoard tells you when a new release exists.

The upgrade goes through the installer you already use, which downloads from
this repository's releases and verifies the SHA-256.

## Where hoard mentions it

| Where | What happens |
| --- | --- |
| The browser, at startup | A y/n prompt. <kbd>y</kbd> opens the download page; any other key skips that release and does not ask again. |
| `:UpdateHoard` in the palette | Checks on demand and reports either way. |
| `hoard update` | Checks, then prints the command that upgrades this build. |
| `hoard version` | An `update:` line, when there is something to say. |

```console
$ hoard update
hoard 0.4.0 → v0.4.1

Upgrade with:
  curl -sSfL https://tools.aithirne.com/hoard/install.sh \
    | sh -s -- -b "$HOME/.local/bin" v0.4.1

Release notes:
  https://github.com/spiffcs/hoard/releases/tag/v0.4.1
```

## How often it checks

At most once a day. The answer is cached in `update.json` beside hoard's other
caches, so most launches make no request at all. The file is safe to delete;
losing it costs one extra check.

## Turning it off

```sh
HOARD_NO_UPDATE_CHECK=1 hoard
```

That suppresses the check everywhere, including `hoard version`. To stop only
the browser's startup prompt, keep `update.check` off in that hoard's settings:

```console
$ sqlite3 ~/Library/Application\ Support/hoard/hoard.db \
    "INSERT INTO settings (key, value) VALUES ('update.check', 'false')
     ON CONFLICT(key) DO UPDATE SET value = 'false'"
```

Declining a release at the prompt records it as `update.skipVersion`, so that
one is never offered again. A later release still is.

## Installing a specific release

Any published tag works, which is also how you go back if a release does not
suit you:

```sh
curl -sSfL https://tools.aithirne.com/hoard/install.sh \
  | sh -s -- -b "$HOME/.local/bin" v0.4.0
```

Add `-v` to verify the Sigstore signature as well, if you have cosign.
[RELEASE.md](../RELEASE.md) covers verifying a release by hand.
