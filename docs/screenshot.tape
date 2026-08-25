Output .tmp/screenshot-discard.gif

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 680
Set Padding 20
Set Theme "Catppuccin Mocha"
Set Framerate 24

Hide
Type `make build >/dev/null 2>&1`
Enter
Sleep 5s
Type `export PATH="$PWD:$PATH"`
Enter
Type `export HOARD_CARD_IMAGES=off`
Enter
Type `export HOARD_DB="$HOME/Library/Caches/hoard/demo.db"`
Enter
Type `hoard demo --reset >/dev/null 2>&1 </dev/null`
Enter
Sleep 3s
Type "clear"
Enter
Sleep 2s
Show

Type "hoard"
Enter
Sleep 5s
Screenshot docs/assets/browse.png
Sleep 2s
Type "q"
Sleep 1s
