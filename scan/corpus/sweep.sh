#!/usr/bin/env bash
# sweep.sh — replay the corpus through the helper and score it per stratum.
#
#   ./scan/corpus/sweep.sh            # summary per era × border
#   ./scan/corpus/sweep.sh --misses   # also list every card that failed
#
# Scores two things against manifest.tsv: did the *name* come back, and did the
# *collector number* come back correct. Reported per stratum so a frame era
# that parses badly is obvious rather than averaged away.
#
# English printings only, matching `cardkit-probe --score`. See
# docs/scanner-accuracy.md for why, and scan/corpus/lang.py for the column.
set -u

here=$(cd "$(dirname "$0")" && pwd)
helper=${HELPER:-"$here/../../bin/hoard-scan.app/Contents/MacOS/hoard-scan"}
manifest="$here/manifest.tsv"
images="$here/images"
show_misses=false
[ "${1:-}" = "--misses" ] && show_misses=true

if [ ! -x "$helper" ]; then
    echo "helper not built at $helper — run: make scan" >&2
    exit 2
fi
[ -f "$manifest" ] || { echo "no manifest — run ./scan/corpus/fetch.sh" >&2; exit 2; }

results=$(mktemp); trap 'rm -f "$results"' EXIT
tail -n +2 "$manifest" | while IFS=$'\t' read -r sid era border name set num rel lang; do
    # Non-English printings are scored apart, exactly as cardkit-probe --score
    # does. Their images are Italian, Spanish and Japanese cards while the
    # manifest holds the English name, so counting them as name failures says
    # nothing about the reader. Two scorers reporting different headline numbers
    # is worse than either number, so this skip has to match that one.
    [ "${lang:-en}" = "en" ] || continue
    img="$images/$sid.png"
    [ -f "$img" ] || continue
    "$helper" --image "$img" --rotate 0 2>/dev/null \
        | python3 "$here/score.py" "$era" "$border" "$name" "$num" >>"$results"
done

python3 - "$results" "$show_misses" <<'PY'
import sys, collections
rows = [l.rstrip("\n").split("\t") for l in open(sys.argv[1]) if l.strip()]
misses = sys.argv[2] == "true"
agg = collections.defaultdict(lambda: [0, 0, 0])   # total, name ok, number ok
for era, border, ok_name, ok_num, want, got in rows:
    a = agg[(era, border)]
    a[0] += 1; a[1] += ok_name == "1"; a[2] += ok_num == "1"
print(f"{'era':<11} {'border':<10} {'n':>4}  {'name':>6}  {'number':>7}")
print("-" * 44)
for (era, border), (n, nm, cn) in sorted(agg.items()):
    print(f"{era:<11} {border:<10} {n:>4}  {nm/n*100:5.0f}%  {cn/n*100:6.0f}%")
tot = sum(a[0] for a in agg.values())
nm = sum(a[1] for a in agg.values()); cn = sum(a[2] for a in agg.values())
if tot:
    print("-" * 44)
    print(f"{'ALL':<11} {'':<10} {tot:>4}  {nm/tot*100:5.0f}%  {cn/tot*100:6.0f}%")
if misses:
    print("\nmisses:")
    for era, border, ok_name, ok_num, want, got in rows:
        if ok_name != "1" or ok_num != "1":
            print(f"  [{era}/{border}] want {want!r} got {got!r}")
PY
