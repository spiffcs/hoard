#!/usr/bin/env bash
# sweep.sh — replay every checked-in fixture frame through the built scan
# helper and diff the extracted card list against its golden. This is the
# scanner's regression suite: run it after any change to the helper's
# detection, OCR filtering, or multi-card logic.
#
#   make scan scan-check          # build the helper, then sweep
#   ./scan/fixtures/sweep.sh --update   # regenerate goldens (deliberate!)
#
# The goldens pin the *decisions* (which cards, from which channel, with
# what collector read) rather than the full event: OCR candidate lists are
# long and jitter-prone, the card list is what the Go side acts on.
#
# The exception is the head of the candidate list. The Go side only tries the
# first few lines before giving up, so which readings sit at the front is a
# decision too — it is what makes a name recovered from rules text reachable
# at all. Three is enough to pin the ordering without dragging the jittery
# tail of the list into the goldens.
#
# Vision's output can shift across macOS releases. If a sweep fails right
# after an OS update, eyeball the diff — legitimate OCR drift gets a
# --update with the diff quoted in the commit; anything else is a
# regression in the helper.
set -u

dir=$(cd "$(dirname "$0")" && pwd)
helper=${HELPER:-"$dir/../../bin/hoard-scan.app/Contents/MacOS/hoard-scan"}

if [ ! -x "$helper" ]; then
    echo "helper not built at $helper — run: make scan" >&2
    exit 2
fi

update=false
[ "${1:-}" = "--update" ] && update=true

normalize() {
    python3 -c '
import json, sys
out = []
for ln in sys.stdin:
    ln = ln.strip()
    if not ln:
        continue
    try:
        ev = json.loads(ln)
    except ValueError:
        continue
    if ev.get("event") != "scan":
        continue
    for c in ev.get("cards", []):
        out.append({
            "name": c.get("name", ""),
            "source": c.get("source", ""),
            "set": c.get("setCode", ""),
            "number": c.get("collectorNumber", ""),
            "finish": c.get("finishHint", ""),
            "candidates": (c.get("candidates") or [])[:3],
        })
print(json.dumps(out, indent=2, sort_keys=True))
'
}

fail=0
for png in "$dir"/*.png; do
    name=$(basename "$png" .png)
    golden="$dir/$name.golden.json"
    got=$("$helper" --image "$png" --rotate 0 2>/dev/null | normalize)

    if $update; then
        printf '%s\n' "$got" >"$golden"
        echo "updated  $name"
        continue
    fi
    if [ ! -f "$golden" ]; then
        echo "MISSING  $name (no golden — run with --update)"
        fail=1
        continue
    fi
    if [ "$got" = "$(cat "$golden")" ]; then
        echo "ok       $name"
    else
        echo "FAIL     $name"
        diff <(cat "$golden") <(printf '%s\n' "$got") | sed 's/^/         /'
        fail=1
    fi
done
exit $fail
