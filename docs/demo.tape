# The README recording, made with vhs (https://github.com/charmbracelet/vhs).
#
#   brew install vhs
#   vhs docs/demo.tape        # writes docs/assets/demo.gif
#
# Runs against a COPY of the real collection: rich enough to be worth showing,
# and the original is never opened, so a demo can never write to it.
#
# NOTE: the resulting GIF publishes the contents and total value of that
# collection. Watch it back before committing it.

Output docs/assets/demo.gif

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 720
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms
Set Framerate 24

# Setup, not filmed.
Hide
Type `make build >/dev/null 2>&1`
Enter
# Film the binary just built, not whatever `hoard` is installed on PATH. An
# installed copy can be arbitrarily old — even built from a commit no longer in
# this repository — and the recording would show its behaviour, not main's.
Type `export PATH="$PWD:$PATH"`
Enter
# Pin the image tier instead of letting hoard detect it. vhs inherits the
# environment of whatever terminal launched it, so recording from Ghostty (or
# kitty, or WezTerm) leaks TERM_PROGRAM through and hoard reasonably concludes it
# may send kitty graphics — which vhs renders via xterm.js, which cannot display
# them, so the card art came out as a block of purple garbage.
#
# halfblock rather than merely unsetting the leak: vhs sets no COLORTERM either,
# so detection on its own lands on ImageNone and the detail view shows no art at
# all. Naming the tier is the only way to actually film the card images.
Type `export HOARD_CARD_IMAGES=halfblock`
Enter
Type `export HOARD_DB="$(mktemp -d)/demo.db"`
Enter
Type `cp "$HOME/Library/Application Support/hoard/hoard.db" "$HOARD_DB"`
Enter
Type "clear"
Enter
Sleep 2s
Show

# The browser is what `hoard` with no arguments opens, and it is the half
#    no static example conveys.
Type "hoard"
Enter
Sleep 3s
Type B
Down@400ms 7
Sleep 700ms

# Card detail: the printing, its price history and its comps.
Enter
Sleep 4s
Enter
Sleep 4s
PageDown 2
Sleep 3s
Escape
Sleep 1s

# 4. tab cycles the four screens: holdings, movers, watches, market.
Type v
Sleep 3s
Type v
Sleep 3s
Type v
Sleep 3s
Type v
Sleep 1000ms

Escape
Sleep 500ms
Type "q"
Sleep 500ms
