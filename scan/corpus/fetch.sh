#!/usr/bin/env bash
# fetch.sh — build a stratified corpus of card images for parser testing.
#
#   ./scan/corpus/fetch.sh [per-stratum] [era]   # default 25, all eras
#
# The optional era limits which strata *grow* — `fetch.sh 40 pre1998` deepens
# the pre-1998 rows and leaves every other stratum exactly as it is, rather
# than pulling ~500 images nobody asked about. Pinned rows are always kept, so
# the manifest never shrinks whatever the filter says.
#
# Samples the local catalog across frame era × border colour, then pulls each
# card's image from Scryfall. Writes images/ (gitignored) and manifest.tsv
# (tracked), the latter carrying the known-good answer for every image so
# sweep.sh can score against it.
#
# Stratified rather than "a few from every set" on purpose: most sets in an era
# share one frame, so ~300 images cover every distinct layout where ~2,700
# would be mostly redundant and far slower to sweep.
#
# The corpus grows as a superset: rows already in the manifest are pinned, so
# raising the per-stratum count only ever appends and the published baseline
# keeps scoring the same images. Re-running with a smaller count is a no-op.
set -u

here=$(cd "$(dirname "$0")" && pwd)
per=${1:-25}
only=${2:-}
images="$here/images"
manifest="$here/manifest.tsv"
catalog=${HOARD_CATALOG:-"$HOME/Library/Caches/hoard/catalog/catalog.db"}

if [ ! -f "$catalog" ]; then
    echo "no catalog at $catalog — run hoard and let it build one" >&2
    exit 2
fi
mkdir -p "$images"

# Era boundaries are frame changes, not set boundaries:
#   pre-1998  no collector number printed at all
#   1998-2003 collector numbers arrive, old frame
#   2003-2015 the 8th Edition frame
#   2015+     the M15 frame
python3 - "$catalog" "$per" "$manifest" "$only" >"$manifest.ids" <<'PY'
import sqlite3, sys, collections, random, os
con = sqlite3.connect(sys.argv[1]); per = int(sys.argv[2]); manifest = sys.argv[3]
only = sys.argv[4]
rows = con.execute("""
SELECT scryfall_id, name, set_code, collector_number, released_at, border_color
FROM cards WHERE released_at != '' AND border_color != ''
""").fetchall()
def era(d):
    y = d[:4]
    if y < '1998': return 'pre1998'
    if y < '2003': return '1998-2002'
    if y < '2015': return '2003-2014'
    return '2015+'
buckets = collections.defaultdict(list)
for r in rows:
    buckets[(era(r[4]), r[5])].append(r)
# Rows already in the manifest are pinned, in the order they appear there, so a
# bigger corpus is a superset of the one the README's baseline was measured on.
# New rows are drawn per stratum from a seed derived from the stratum itself:
# a bucket's order then depends on nothing but its own contents, so raising
# `per` can neither reshuffle a bucket nor disturb any other one. (A shared
# generator could not promise that — rnd.sample draws a number of times that
# depends on how many rows it is asked for, so every later bucket moved too.)
pinned = collections.defaultdict(list)
if os.path.exists(manifest):
    with open(manifest) as fh:
        next(fh, None)                          # header
        for line in fh:
            f = line.rstrip("\n").split("\t")   # id, era, border, ...
            if len(f) >= 3:
                pinned[(f[1], f[2])].append(f[0])

for (e, b), rs in sorted(buckets.items()):
    if len(rs) < 3: continue
    was = {sid: i for i, sid in enumerate(pinned[(e, b)])}
    keep = sorted((r for r in rs if r[0] in was), key=lambda r: was[r[0]])
    rest = [r for r in rs if r[0] not in was]
    random.Random("hoard-corpus:%s:%s" % (e, b)).shuffle(rest)
    want = per if (not only or only == e) else len(keep)
    for r in keep + rest[:max(0, want - len(keep))]:
        print("\t".join([e, b] + [str(x) for x in r]))
PY

printf 'id\tera\tborder\tname\tset\tnumber\treleased\n' >"$manifest"
n=0; miss=0
while IFS=$'\t' read -r era border sid name set num rel bc; do
    out="$images/$sid.png"
    if [ ! -f "$out" ]; then
        url=$(curl -s "https://api.scryfall.com/cards/$sid" \
            | python3 -c 'import json,sys
d=json.load(sys.stdin)
u=(d.get("image_uris") or {}).get("png") or ""
if not u:
    f=(d.get("card_faces") or [{}])[0]
    u=(f.get("image_uris") or {}).get("png") or ""
print(u)' 2>/dev/null)
        sleep 0.12                       # Scryfall asks for 50-100ms between calls
        if [ -z "$url" ]; then miss=$((miss + 1)); continue; fi
        curl -s "$url" -o "$out" || { miss=$((miss + 1)); continue; }
        sleep 0.12
    fi
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$sid" "$era" "$border" "$name" "$set" "$num" "$rel" >>"$manifest"
    n=$((n + 1))
done <"$manifest.ids"
rm -f "$manifest.ids"
echo "corpus: $n images in $images ($miss skipped)"
