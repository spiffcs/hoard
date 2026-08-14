#!/usr/bin/env bash
set -u

here=$(cd "$(dirname "$0")" && pwd)
helper=${HELPER:-"$here/../../bin/cardkit-probe"}
manifest="$here/manifest.tsv"
images="$here/images"
mode=${1:-}

if [ ! -x "$helper" ]; then
    echo "reader not built at $helper — run: make cardkit" >&2
    exit 2
fi

results=$(mktemp)
trap 'rm -f "$results"' EXIT

while IFS=$'\t' read -r sid era border name set num rel; do
    [ "$sid" = "id" ] && continue
    img="$images/$sid.png"
    [ -f "$img" ] || continue
    json=$("$helper" --image "$img" --rotate 0 --border 2>/dev/null)
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
    keys = ["color", "source", "abstain", "anchorKind", "t", "standoff",
            "scaleAgreement", "cardHeightPx", "borderMS"]
    print("\t".join(["era", "border", "name"] + keys))
    for era, border, name, d in rows:
        vals = []
        for k in keys:
            v = d.get(k, "")
            vals.append("" if v is None else (f"{v:.4f}" if isinstance(v, float) else str(v)))
        print("\t".join([era, border, name] + vals))
    raise SystemExit

agg = collections.defaultdict(lambda: [0, 0, 0])
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
    hard = [w for w in wrong if w[1] in ("white", "black")]
    print(f"\nWRONG: {len(hard)} on white/black, {len(wrong) - len(hard)} on gold/silver")
    for era, want, got, name, t in wrong:
        print(f"  [{era}] {name}: want {want}, got {got} (t={t:.3f})")
PY
