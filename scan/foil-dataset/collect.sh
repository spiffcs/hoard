#!/usr/bin/env bash
# collect.sh — pull labelled-corpus candidates out of a scanning session.
#
#   ./scan/foil-dataset/collect.sh <debug-dir> <source> <provenance>
#   ./scan/foil-dataset/collect.sh /tmp/scan-fixtures session 2026-08-03
#
# For each capture the session saved, this replays the frame through the built
# helper to get the same perspective-corrected card the scanner sees, then cuts
# the lower-left patch where the pre-8th-Edition foil star lives. Both land in
# images/, and a draft row per capture is appended to labels.tsv with
# finish=unknown.
#
# Labelling is deliberately manual. The helper's own finishHint only knows the
# modern set/language separator, which is the signal this corpus exists to
# replace on old frames — trusting it would label the interesting cases wrong.
set -u

dir=${1:?usage: collect.sh <debug-dir> <source> <provenance>}
source=${2:?usage: collect.sh <debug-dir> <source> <provenance>}
prov=${3:?usage: collect.sh <debug-dir> <source> <provenance>}

here=$(cd "$(dirname "$0")" && pwd)
helper=${HELPER:-"$here/../../bin/hoard-scan.app/Contents/MacOS/hoard-scan"}
images="$here/images"
labels="$here/labels.tsv"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

if [ ! -x "$helper" ]; then
    echo "helper not built at $helper — run: make scan" >&2
    exit 2
fi
mkdir -p "$images"
if [ ! -f "$labels" ]; then
    printf 'id\tsource\tprovenance\tname\tset\tnumber\tfinish\tmarker\tframe\tnotes\n' >"$labels"
fi

# The marker's home, as a fraction of the normalized card: the text box's
# lower-left corner. Generous on purpose — the star drifts with frame era and
# the crop's own alignment, and a patch that misses it is worse than a loose one.
MX0=0.04 MY0=0.76 MX1=0.42 MY1=0.99

added=0
for frame in "$dir"/capture-*-ocr.png; do
    [ -e "$frame" ] || continue
    stem=$(basename "$frame" -ocr.png)
    id="${prov//[^0-9A-Za-z]/}-$stem"

    rm -f "$tmp"/multi-rect-*.png
    HOARD_SCAN_DEBUG_DIR="$tmp" "$helper" --image "$frame" --rotate 0 >"$tmp/ev.json" 2>/dev/null
    [ -f "$tmp/multi-rect-0.png" ] || continue
    cp "$tmp/multi-rect-0.png" "$images/$id.png"

    w=$(sips -g pixelWidth "$images/$id.png" | awk '/pixelWidth/{print $2}')
    h=$(sips -g pixelHeight "$images/$id.png" | awk '/pixelHeight/{print $2}')
    ox=$(python3 -c "print(int($w*$MX0))")
    oy=$(python3 -c "print(int($h*$MY0))")
    cw=$(python3 -c "print(max(8,int($w*($MX1-$MX0))))")
    ch=$(python3 -c "print(max(8,int($h*($MY1-$MY0))))")
    sips -c "$ch" "$cw" --cropOffset "$oy" "$ox" "$images/$id.png" \
        --out "$images/$id-marker.png" >/dev/null 2>&1

    read -r name set number < <(python3 - "$tmp/ev.json" <<'PY'
import json, sys
try:
    ev = json.load(open(sys.argv[1]))
except Exception:
    print("- - -"); raise SystemExit
cards = ev.get("cards") or []
c = cards[0] if cards else {}
def f(v): return (v or "-").replace(" ", "_") or "-"
print(f(c.get("name") or ev.get("name")), f(c.get("setCode")), f(c.get("collectorNumber")))
PY
    )
    printf '%s\t%s\t%s\t%s\t%s\t%s\tunknown\tunknown\tunknown\t\n' \
        "$id" "$source" "$prov" "$name" "$set" "$number" >>"$labels"
    added=$((added + 1))
done

echo "added $added candidates to $labels"
echo "now label them: open images/<id>-marker.png and fill finish/marker/frame"
