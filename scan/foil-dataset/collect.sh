#!/usr/bin/env bash
# collect.sh — pull labelled-corpus candidates out of a scanning session.
#
#   ./scan/foil-dataset/collect.sh <debug-dir> <source> <provenance>
#   ./scan/foil-dataset/collect.sh /tmp/scan-fixtures session 2026-08-03
#
# For each capture the session saved, this replays the frame through the reader
# to get the same perspective-corrected card the scanner sees, then cuts the
# lower-left patch where the pre-8th-Edition foil star lives. Both land in
# images/, and a draft row per capture is appended to labels.tsv with
# finish=unknown.
#
# The reader is cardkit-probe (CardKit — what the iPhone app runs), driven in
# --flatten mode: it locates the card in every image of a directory and writes
# the corrected crop, which is precisely the "same pixels the reader sees"
# property this corpus depends on. It replaced a per-frame --image call against
# the macOS helper, which wrote its crops via HOARD_SCAN_DEBUG_DIR; that helper
# had its own reader for the Continuity Camera path and both were removed.
#
# Labelling is deliberately manual. The reader's own finishHint only knows the
# modern set/language separator, which is the signal this corpus exists to
# replace on old frames — trusting it would label the interesting cases wrong.
set -u

dir=${1:?usage: collect.sh <debug-dir> <source> <provenance>}
source=${2:?usage: collect.sh <debug-dir> <source> <provenance>}
prov=${3:?usage: collect.sh <debug-dir> <source> <provenance>}

here=$(cd "$(dirname "$0")" && pwd)
helper=${HELPER:-"$here/../../bin/cardkit-probe"}
images="$here/images"
labels="$here/labels.tsv"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

if [ ! -x "$helper" ]; then
    echo "reader not built at $helper — run: make cardkit" >&2
    exit 2
fi
mkdir -p "$images" "$tmp/frames" "$tmp/flat"
if [ ! -f "$labels" ]; then
    printf 'id\tsource\tprovenance\tname\tset\tnumber\tfinish\tmarker\tframe\tnotes\n' >"$labels"
fi

# The marker's home, as a fraction of the normalized card: the text box's
# lower-left corner. Generous on purpose — the star drifts with frame era and
# the crop's own alignment, and a patch that misses it is worse than a loose one.
MX0=0.04 MY0=0.76 MX1=0.42 MY1=0.99

# --flatten reads a whole directory and keys its output by position in the
# sorted file list, so the frames are staged under names that sort the same way
# the crops will be numbered. Copying rather than globbing in place keeps any
# other file in the debug directory from shifting the numbering.
n=0
for frame in "$dir"/capture-*-ocr.png; do
    [ -e "$frame" ] || continue
    n=$((n + 1))
    printf -v idx '%04d' "$n"
    cp "$frame" "$tmp/frames/$idx.png"
    basename "$frame" -ocr.png >"$tmp/frames/$idx.stem"
done
if [ "$n" -eq 0 ]; then
    echo "no capture-*-ocr.png frames in $dir" >&2
    exit 2
fi

# One line per image: "<index>\tset=..\tnum=..\tfinish=..\tyear=..\trecovered=..\t<title>".
# A frame with no card located prints "NO CARD LOCATED" and writes no crop, so
# the crop's existence is the check rather than the line's shape.
"$helper" --flatten "$tmp/frames" "$tmp/flat" >"$tmp/read.tsv" 2>/dev/null || true

added=0
for i in $(seq 1 "$n"); do
    printf -v idx '%04d' "$i"
    printf -v crop '%02d' "$i"
    [ -f "$tmp/flat/$crop.png" ] || continue
    id="${prov//[^0-9A-Za-z]/}-$(cat "$tmp/frames/$idx.stem")"
    cp "$tmp/flat/$crop.png" "$images/$id.png"

    w=$(sips -g pixelWidth "$images/$id.png" | awk '/pixelWidth/{print $2}')
    h=$(sips -g pixelHeight "$images/$id.png" | awk '/pixelHeight/{print $2}')
    ox=$(python3 -c "print(int($w*$MX0))")
    oy=$(python3 -c "print(int($h*$MY0))")
    cw=$(python3 -c "print(max(8,int($w*($MX1-$MX0))))")
    ch=$(python3 -c "print(max(8,int($h*($MY1-$MY0))))")
    sips -c "$ch" "$cw" --cropOffset "$oy" "$ox" "$images/$id.png" \
        --out "$images/$id-marker.png" >/dev/null 2>&1

    read -r name set number < <(python3 - "$tmp/read.tsv" "$i" <<'PY'
import sys
want = sys.argv[2]
for line in open(sys.argv[1]):
    f = line.rstrip("\n").split("\t")
    if not f or f[0] != want:
        continue
    kv = dict(p.split("=", 1) for p in f[1:] if "=" in p)
    title = f[-1] if "=" not in f[-1] else ""
    def c(v): return (v or "-").replace(" ", "_") or "-"
    print(c(title), c(kv.get("set")), c(kv.get("num")))
    break
else:
    print("- - -")
PY
    )
    printf '%s\t%s\t%s\t%s\t%s\t%s\tunknown\tunknown\tunknown\t\n' \
        "$id" "$source" "$prov" "$name" "$set" "$number" >>"$labels"
    added=$((added + 1))
done

echo "added $added candidates to $labels"
echo "now label them: open images/<id>-marker.png and fill finish/marker/frame"
