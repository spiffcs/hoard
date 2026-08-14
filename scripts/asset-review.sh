#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

OUT=.tmp/asset-review.html
MASTER=docs/assets/logo-master.png
LOGO=docs/assets/hoard-logo.png
BROWSE=docs/assets/browse.png
SOCIAL=docs/assets/social-preview.png
DEMO=docs/assets/demo.gif
ICON=scan/hoard-scan-ios/Resources/Assets.xcassets/AppIcon.appiconset/icon-1024.png

LOGO_W=$(sed -n 's/.*hoard-logo\.png" width="\([0-9]*\)".*/\1/p' README.md | head -1)
: "${LOGO_W:=190}"

mkdir -p .tmp

facts() {
  local dims
  dims=$(sips -g pixelWidth -g pixelHeight "$1" 2>/dev/null |
    awk '/pixelWidth/{w=$2} /pixelHeight/{h=$2} END{if (w) print w"x"h}')
  [ -z "$dims" ] && dims="animated"
  printf '%s · %s' "$dims" "$(du -h "$1" | cut -f1 | tr -d ' ')"
}

STALE=""
freshness() {
  local built=$1 source=$2 how=$3
  if [ ! -e "$built" ]; then
    STALE="${STALE}  MISSING  $built — run: $how"$'\n'
  elif [ "$source" -nt "$built" ]; then
    STALE="${STALE}  STALE    $built is older than $source — run: $how"$'\n'
  fi
}
freshness "$DEMO"   docs/demo.tape           "vhs docs/demo.tape"
freshness "$BROWSE" docs/screenshot.tape     "vhs docs/screenshot.tape"
freshness "$SOCIAL" docs/social-preview.html "make social-preview"
freshness "$LOGO"   "$MASTER"                "make logo"
freshness "$ICON"   "$MASTER"                "make logo"

cat > "$OUT" <<EOF
<!doctype html><meta charset=utf-8><title>hoard · asset review</title>
<style>
 body{margin:0;font:15px/1.5 -apple-system,sans-serif;background:#eef1f5;color:#1f2328}
 header{background:#0d1117;color:#e6edf3;padding:20px 32px}
 header h1{margin:0;font-size:21px} header p{margin:4px 0 0;color:#9ba6b6;font-size:13px}
 section{background:#fff;margin:20px 32px;border-radius:10px;border:1px solid #d8dee6;overflow:hidden}
 h2{margin:0;padding:14px 20px;font-size:15px;border-bottom:1px solid #e6eaf0;background:#f7f9fc}
 h2 span{float:right;font:12px ui-monospace,monospace;color:#6a7480;font-weight:400}
 .ask{padding:10px 20px;background:#fffbe6;border-bottom:1px solid #f0e4b0;font-size:13px;color:#5b4a00}
 .priv{background:#ffeef0;border-bottom-color:#ffc9d0;color:#82061e;font-weight:600}
 .stale{margin:20px 32px 0;padding:14px 20px;border-radius:10px;background:#fff4e5;
        border:1px solid #f0c893;color:#7a4a00;font:12px/1.7 ui-monospace,monospace;white-space:pre}
 .body{padding:20px}
 .split{display:flex;flex-wrap:wrap}
 .half{flex:1 1 320px;padding:26px;text-align:center}
 .lt{background:#fff;color:#1f2328}.dk{background:#0d1117;color:#e6edf3}
 .cap{font:11px ui-monospace,monospace;opacity:.6;margin-top:10px}
 .wall{background:linear-gradient(135deg,#7d8ba6,#a99cb0 60%,#c2a68f);padding:26px;
       display:flex;gap:26px;align-items:flex-start;justify-content:center;flex-wrap:wrap}
 .wall img{border-radius:22.4%;box-shadow:0 6px 16px rgba(0,0,0,.35)}
 figure{margin:0;text-align:center}
 figcaption{font:11px ui-monospace,monospace;color:#fff;margin-top:8px}
 img.fit{max-width:100%;display:block;border:1px solid #d8dee6;border-radius:6px}
 .stack{display:flex;flex-direction:column;gap:18px;align-items:flex-start}
</style>
<header><h1>hoard · asset review</h1>
<p>Every image the project ships, at the size it is actually seen. Rebuild: make asset-review</p></header>
EOF

if [ -n "$STALE" ]; then
  { printf '<div class="stale">Out of date:\n\n'; printf '%s' "$STALE"; printf '</div>\n'; } >> "$OUT"
fi

cat >> "$OUT" <<EOF
<section><h2>1 · Logo in the README <span>$(facts "$LOGO")</span></h2>
<div class=ask>Does it read on <b>both</b> themes, and is ${LOGO_W}px right beside the title?</div>
<div class=split>
 <div class="half lt"><img src="../$LOGO" width=$LOGO_W><div style="font:800 30px -apple-system">hoard</div><div class=cap>light · width=$LOGO_W</div></div>
 <div class="half dk"><img src="../$LOGO" width=$LOGO_W><div style="font:800 30px -apple-system">hoard</div><div class=cap>dark · width=$LOGO_W</div></div>
</div></section>

<section><h2>2 · iPhone app icon <span>$(facts "$ICON")</span></h2>
<div class=ask>Does the mark still read at 40px? This icon is hand-authored full-bleed artwork, not generated — there is no knob, so fixing it means redrawing it and copying the new 1024×1024 over <code>icon-1024.png</code>.</div>
<div class=wall>
 <figure><img src="../$ICON" width=180 height=180><figcaption>180</figcaption></figure>
 <figure><img src="../$ICON" width=120 height=120><figcaption>120 · home screen</figcaption></figure>
 <figure><img src="../$ICON" width=80 height=80><figcaption>80</figcaption></figure>
 <figure><img src="../$ICON" width=60 height=60><figcaption>60 · settings</figcaption></figure>
 <figure><img src="../$ICON" width=40 height=40><figcaption>40 · spotlight</figcaption></figure>
</div></section>

<section><h2>3 · Social preview card <span>$(facts "$SOCIAL")</span></h2>
<div class=ask>Upload at Settings → General → Social preview. Does it still say something at 280px?</div>
<div class=body><div class=stack>
 <div><img class=fit src="../$SOCIAL" width=640><div class=cap>640 · GitHub repo page</div></div>
 <div><img class=fit src="../$SOCIAL" width=440><div class=cap>440 · Slack / X card</div></div>
 <div><img class=fit src="../$SOCIAL" width=280><div class=cap>280 · compact unfurl</div></div>
</div></div></section>

<section><h2>4 · Browser screenshot <span>$(facts "$BROWSE")</span></h2>
<div class="ask priv">PRIVACY — this publishes real card names and a real total. Read it before committing.</div>
<div class=body><img class=fit src="../$BROWSE"></div></section>

<section><h2>5 · Demo recording <span>$(facts "$DEMO")</span></h2>
<div class="ask priv">PRIVACY — the whole collection and its value, animated. Watch it end to end.</div>
<div class=body><img class=fit src="../$DEMO"></div></section>
EOF

if [ -n "$STALE" ]; then
  printf '\nOut of date:\n%s\n' "$STALE"
fi
echo "wrote $OUT"
[ "${NO_OPEN:-}" = "1" ] || open "$OUT"
