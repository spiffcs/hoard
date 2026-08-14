#!/usr/bin/env bash
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
pinned = collections.defaultdict(list)
if os.path.exists(manifest):
    with open(manifest) as fh:
        next(fh, None)
        for line in fh:
            f = line.rstrip("\n").split("\t")
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
        sleep 0.12
        if [ -z "$url" ]; then miss=$((miss + 1)); continue; fi
        curl -s "$url" -o "$out" || { miss=$((miss + 1)); continue; }
        sleep 0.12
    fi
    printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$sid" "$era" "$border" "$name" "$set" "$num" "$rel" >>"$manifest"
    n=$((n + 1))
done <"$manifest.ids"
rm -f "$manifest.ids"
echo "corpus: $n images in $images ($miss skipped)"
