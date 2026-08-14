Output docs/assets/demo.gif

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 720
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms
Set Framerate 24

Hide
Type `make build >/dev/null 2>&1`
Enter
Type `export PATH="$PWD:$PATH"`
Enter
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

Type "hoard"
Enter
Sleep 3s
Down@400ms 7
Sleep 700ms

Enter
Sleep 4s
Enter
Sleep 4s
PageDown 2
Sleep 3s
Escape
Sleep 1s

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
