#!/usr/bin/env bash
# fetch.sh — build a stratified corpus of card images for parser testing.
#
#   ./scan/corpus/fetch.sh [per-stratum]      # default 25
#
# Samples the local catalog across frame era × border colour, then pulls each
# card's image from Scryfall. Writes images/ (gitignored) and manifest.tsv
# (tracked), the latter carrying the known-good answer for every image so
# sweep.sh can score against it.
#
# Stratified rather than "a few from every set" on purpose: most sets in an era
# share one frame, so ~300 images cover every distinct layout where ~2,700
# would be mostly redundant and far slower to sweep.
set -u

here=$(cd "$(dirname "$0")" && pwd)
per=${1:-25}
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
python3 - "$catalog" "$per" >"$manifest.ids" <<'PY'
import sqlite3, sys, collections, random
con = sqlite3.connect(sys.argv[1]); per = int(sys.argv[2])
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
rnd = random.Random(20260803)   # fixed, so the corpus is reproducible
for (e, b), rs in sorted(buckets.items()):
    if len(rs) < 3: continue
    for r in rnd.sample(rs, min(per, len(rs))):
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
