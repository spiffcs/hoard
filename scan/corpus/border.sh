#!/usr/bin/env bash
# border.sh — dump the border reader's measurements over the corpus.
#
#   ./scan/corpus/border.sh            # score the reader's verdicts
#   ./scan/corpus/border.sh --dump     # one TSV row of raw numbers per image
#
# This is the one thing the corpus *can* say about the border. These are clean
# full-bleed scans, so the card fills the image and the card rect is therefore
# known exactly — which makes them the right place to fit the layout constants
# in CardLayout and the photometric gates in BorderGate, and the wrong place to
# learn anything about the perspective crop (see README.md).
#
# Fit here, then confirm on scan/fixtures/, which are real photographs.
set -u

here=$(cd "$(dirname "$0")" && pwd)
helper=${HELPER:-"$here/../../bin/hoard-scan.app/Contents/MacOS/hoard-scan"}
manifest="$here/manifest.tsv"
images="$here/images"
mode=${1:-}

if [ ! -x "$helper" ]; then
    echo "helper not built at $helper — run: make scan" >&2
    exit 2
fi

results=$(mktemp)
trap 'rm -f "$results"' EXIT

while IFS=$'\t' read -r sid era border name set num rel; do
    [ "$sid" = "id" ] && continue
    img="$images/$sid.png"
    [ -f "$img" ] || continue
    json=$("$helper" --border-probe "$img" --rotate 0 2>/dev/null)
    [ -z "$json" ] && json='{}'
    printf '%s\t%s\t%s\t%s\n' "$era" "$border" "$name" "$json" >>"$results"
done <"$manifest"

python3 - "$results" "$mode" <<'PY'
import json, sys, collections

rows = []
for line in open(sys.argv[1]):
    era, border, name, blob = line.rstrip("\n").split("\t", 3)
    try:
        d = json.loads(blob)
    except json.JSONDecodeError:
        d = {}
    rows.append((era, border, name, d))

if sys.argv[2] == "--dump":
    keys = ["color", "source", "abstain", "anchorKind", "t", "ringBottom", "ringTop", "innerBottom",
            "ringMAD", "innerMAD", "ringChroma", "patchDark", "patchBright",
            "patchSeparation", "patchDarkFraction", "patchChroma", "clipHigh", "clipLow",
            "cardHeightPx", "scaleAgreement", "thetaDegrees",
            "footerVMeasured", "titleVMeasured", "footerGlyphVMeasured",
            "footerLeftU", "footerRightU", "titleLeftU", "creditCandidateLeftU"]
    print("\t".join(["era", "border", "name"] + keys))
    for era, border, name, d in rows:
        vals = []
        for k in keys:
            v = d.get(k, "")
            vals.append("" if v is None else (f"{v:.4f}" if isinstance(v, float) else str(v)))
        print("\t".join([era, border, name] + vals))
    raise SystemExit

# Coverage and precision, never averaged into one number: the property that
# matters is "when it speaks it is right", not "it is usually right".
agg = collections.defaultdict(lambda: [0, 0, 0])       # n, spoke, correct
reasons = collections.Counter()
wrong = []
for era, border, name, d in rows:
    a = agg[(era, border)]
    a[0] += 1
    color = d.get("color")
    if not color:
        reasons[d.get("abstain", "?")] += 1
        continue
    a[1] += 1
    if color == border:
        a[2] += 1
    else:
        wrong.append((era, border, color, name, d.get("t", 0)))

print(f"{'era':10s} {'border':11s} {'n':>4s} {'spoke':>7s} {'correct':>8s} {'WRONG':>6s}")
tot = [0, 0, 0]
for (era, border), (n, spoke, ok) in sorted(agg.items()):
    tot = [tot[0] + n, tot[1] + spoke, tot[2] + ok]
    acc = f"{ok/spoke*100:6.0f}%" if spoke else "      -"
    print(f"{era:10s} {border:11s} {n:4d} {spoke/n*100:6.0f}% {acc:>8s} {spoke-ok:6d}")
acc = f"{tot[2]/tot[1]*100:6.0f}%" if tot[1] else "      -"
print(f"{'ALL':10s} {'':11s} {tot[0]:4d} {tot[1]/tot[0]*100:6.0f}% {acc:>8s} {tot[1]-tot[2]:6d}")

print("\nabstained because:")
for r, c in reasons.most_common():
    print(f"  {c:4d}  {r}")
if wrong:
    # Gold and silver are groups 2 and 3 — World Championship decks and
    # Un-sets, both declared non-goals. A wrong read there is still a wrong
    # read and is reported as one, but it is bounded: the Go side only ever
    # reorders on a border, and a card whose printings are all gold ignores a
    # "white" outright. The number that must stay zero is the white/black one.
    hard = [w for w in wrong if w[1] in ("white", "black")]
    print(f"\nWRONG: {len(hard)} on white/black, {len(wrong) - len(hard)} on gold/silver")
    for era, want, got, name, t in wrong:
        print(f"  [{era}] {name}: want {want}, got {got} (t={t:.3f})")
PY
