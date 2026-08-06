#!/usr/bin/env bash
# sweep.sh — replay every checked-in fixture frame through the card reader and
# diff the extracted card list against its golden. This is the scanner's
# regression suite: run it after any change to detection, OCR filtering, or the
# border reader.
#
# The reader is cardkit-probe, which is CardKit — the same pipeline the iPhone
# app runs. It used to be the macOS helper's own reader; that reader existed for
# the Continuity Camera path and went with it, so the goldens now pin what the
# phone actually reads rather than what a second implementation did.
#
#   make cardkit scan-check             # build the reader, then sweep
#   ./scan/fixtures/sweep.sh --update   # regenerate goldens (deliberate!)
#
# The goldens pin the *decisions* (which cards, from which channel, with
# what collector read) rather than the full event: OCR candidate lists are
# long and jitter-prone, the card list is what the Go side acts on.
#
# The copyright year is in for the same reason as the card list: it is a
# decision, not a reading. On a card printed before collector numbers existed
# it is the only printing evidence there is, and it was silently absent for
# that whole era until the parser learned to accept a lone year.
#
# The other exception is the head of the candidate list. The Go side only tries the
# first few lines before giving up, so which readings sit at the front is a
# decision too — it is what makes a name recovered from rules text reachable
# at all. Three is enough to pin the ordering without dragging the jittery
# tail of the list into the goldens.
#
# Vision's output can shift across macOS releases. If a sweep fails right
# after an OS update, eyeball the diff — legitimate OCR drift gets a
# --update with the diff quoted in the commit; anything else is a
# regression in the reader.
set -u

dir=$(cd "$(dirname "$0")" && pwd)
helper=${HELPER:-"$dir/../../bin/cardkit-probe"}

if [ ! -x "$helper" ]; then
    echo "reader not built at $helper — run: make cardkit" >&2
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
            "year": c.get("copyrightYear", 0),
            "border": c.get("borderColor", ""),
            "borderSource": c.get("borderSource", ""),
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
