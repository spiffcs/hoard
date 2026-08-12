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
# No card art in the recording. Detection cannot be trusted here in either
# direction — vhs inherits the launching terminal's environment, so recording
# from Ghostty leaks TERM_PROGRAM through and hoard sends kitty graphics that
# xterm.js renders as purple garbage; and vhs sets no COLORTERM, so plain
# detection lands on ImageNone anyway. So the tier is named explicitly.
#
# `off` rather than `halfblock`, deliberately. Halfblocks draw one pixel per
# column and two per row, so even at a generous width the card is a few thousand
# pixels of a 488x680 scan — legible as "a card", mush as an image. What a real
# user sees in Ghostty or kitty is the terminal scaling the full-resolution
# image, and no recording vhs can make will show that. Given the choice between
# filming something worse than the product and filming the layout without it,
# this films the layout: with art off, HELD, PRICE with all three sparklines,
# COMPS and LINKS fit on one screen with nothing scrolled off.
Type `export HOARD_CARD_IMAGES=off`
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
