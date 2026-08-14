#!/usr/bin/env bash
set -u

dir=$(cd "$(dirname "$0")" && pwd)
helper=${HELPER:-"$dir/../../bin/cardkit-probe"}
frames="$dir/frames"

if [ ! -x "$helper" ]; then
    echo "reader not built at $helper — run: make cardkit" >&2
    exit 2
fi

if [ ! -d "$frames" ] || [ -z "$(ls "$frames"/*.png 2>/dev/null)" ]; then
    echo "no fixture frames in $frames — run: make scan-fixtures" >&2
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
for png in "$frames"/*.png; do
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
