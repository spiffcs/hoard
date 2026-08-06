#!/usr/bin/env python3
"""Add a `lang` column to manifest.tsv, from Scryfall.

The corpus samples printings, and printings have languages. Set `rin` is
Rinascimento and its images are Italian cards; `ren` is Renaissance; `fbb` is
Foreign Black Border. The manifest's `name` column holds the *English* name,
because that is the identity the catalog is keyed on.

Without this column the scorer marks a perfect read of "Miniera a Cielo Aperto"
as a failure to read "Strip Mine". A fifth of all name misses were exactly that,
and it made pre-1998 black look 35 points worse than its white sibling for
reasons that had nothing to do with the scanner.

    ./scan/corpus/lang.py            # add the column
    ./scan/corpus/lang.py --check    # report what is there, change nothing

Written in Python rather than shell after a shell version truncated the manifest
to 25 rows: a counter inside a piped `while read` loop runs in a subshell, and
under `set -u` the abort left a partial file that had already been moved into
place. The rules that came out of that are enforced below — back up first, build
the whole thing in memory, verify the row count, and only then replace.
"""

import json
import shutil
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parent
MANIFEST = HERE / "manifest.tsv"
BACKUP = HERE / "manifest.tsv.bak"

# Scryfall asks for 50-100ms between requests. Theirs is a free service holding
# up a hobby project; the corpus is 231 cards and this runs once.
DELAY = 0.1
API = "https://api.scryfall.com/cards/"
# Scryfall rejects requests without these with a bare 400 — no message, no hint.
# `fetch.sh` never hit it because curl sends a User-Agent of its own; urllib's
# default is refused.
HEADERS = {
    "User-Agent": "hoard-corpus/1.0 (+https://github.com/spiffcs/hoard)",
    "Accept": "application/json",
}


def read_manifest():
    rows = MANIFEST.read_text().rstrip("\n").split("\n")
    return rows[0].split("\t"), [r.split("\t") for r in rows[1:]]


def language_of(card_id):
    """The printing's language, or None if Scryfall would not say.

    None rather than a default of "en": guessing English on a failed lookup
    would silently mislabel exactly the cards this column exists to find.
    """
    try:
        req = urllib.request.Request(API + card_id, headers=HEADERS)
        with urllib.request.urlopen(req, timeout=20) as r:
            return json.load(r).get("lang") or None
    except (urllib.error.URLError, json.JSONDecodeError, TimeoutError):
        return None


def main():
    header, rows = read_manifest()

    if "lang" in header:
        counts = {}
        for r in rows:
            counts[r[header.index("lang")]] = counts.get(r[header.index("lang")], 0) + 1
        print(f"manifest already has a lang column ({len(rows)} rows)")
        for lang, n in sorted(counts.items(), key=lambda kv: -kv[1]):
            print(f"  {lang:4} {n}")
        return 0

    if "--check" in sys.argv:
        print(f"no lang column; {len(rows)} rows would be fetched")
        return 0

    # Back up before touching anything. The manifest is tracked, so this is
    # belt and braces — but it is also the only ground truth the corpus has.
    shutil.copy2(MANIFEST, BACKUP)
    print(f"backed up to {BACKUP.name}", file=sys.stderr)

    out, failed = [], []
    for i, row in enumerate(rows, 1):
        lang = language_of(row[0])
        if lang is None:
            failed.append(row[0])
            lang = "?"
        out.append(row + [lang])
        if i % 25 == 0:
            print(f"  {i}/{len(rows)}", file=sys.stderr)
        time.sleep(DELAY)

    # Verify before replacing, not after. This is the check whose absence
    # truncated the manifest last time.
    if len(out) != len(rows):
        print(f"ABORT: built {len(out)} rows from {len(rows)}; manifest untouched",
              file=sys.stderr)
        return 1
    if len(failed) > len(rows) // 10:
        print(f"ABORT: {len(failed)} lookups failed, more than a tenth; "
              f"manifest untouched", file=sys.stderr)
        return 1

    text = "\t".join(header + ["lang"]) + "\n"
    text += "".join("\t".join(r) + "\n" for r in out)
    MANIFEST.write_text(text)

    counts = {}
    for r in out:
        counts[r[-1]] = counts.get(r[-1], 0) + 1
    print(f"added lang column to {len(out)} rows")
    for lang, n in sorted(counts.items(), key=lambda kv: -kv[1]):
        print(f"  {lang:4} {n}")
    if failed:
        print(f"  {len(failed)} lookups failed and are marked '?'", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
