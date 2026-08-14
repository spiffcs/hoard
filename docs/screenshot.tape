Output .tmp/screenshot-discard.gif

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 720
Set Padding 20
Set Theme "Catppuccin Mocha"
Set Framerate 24

Hide
Type `make build >/dev/null 2>&1`
Enter
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

Type "hoard"
Enter
Sleep 5s
Screenshot docs/assets/browse.png
