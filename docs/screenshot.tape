# A still of the browser for the README, made with vhs.
#
#   mkdir -p .tmp && vhs docs/screenshot.tape    # writes docs/assets/browse.png
#
# A GIF is not enough on its own: pkg.go.dev strips it, some mirrors drop it,
# and a reader who blocks animation sees nothing at all. The still is what those
# readers get, so it has to stand alone — hence the pause before the shot, to
# let prices and the footer finish painting.
#
# Same note as demo.tape: this runs against a COPY of the real collection, so
# the original is never opened, but the PNG publishes real card names and a real
# total. Look at it before committing it.

# Only the still is wanted. vhs always writes its Output, so it is aimed at the
# gitignored .tmp/ rather than at docs/assets/ next to the file worth keeping.
Output .tmp/screenshot-discard.gif

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 720
Set Padding 20
Set Theme "Catppuccin Mocha"
Set Framerate 24

# Setup, not shown.
Hide
Type `make build >/dev/null 2>&1`
Enter
# Shoot the binary just built, not whatever `hoard` is installed on PATH.
Type `export PATH="$PWD:$PATH"`
Enter
Type `export HOARD_DB="$(mktemp -d)/shot.db"`
Enter
Type `cp "$HOME/Library/Application Support/hoard/hoard.db" "$HOARD_DB"`
Enter
Type "clear"
Enter
Sleep 2s
Show

# Bare `hoard` opens the browser on holdings, left pane on the sets lens — the
# view the README is selling. vhs will not overwrite an existing Screenshot
# target: delete docs/assets/browse.png first or this run writes nothing.
Type "hoard"
Enter
Sleep 5s
Screenshot docs/assets/browse.png
