Output docs/assets/demo.gif

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 680
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 40ms
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
Sleep 4s

Tab
Sleep 1s
Down@400ms 4
Sleep 1500ms
Enter
Sleep 6s
Escape
Sleep 1s
Tab
Sleep 1500ms

Down@500ms 1
Sleep 3s
Type "b"
Sleep 5s
Type "b"
Sleep 2s

Type "B"
Sleep 3500ms
Type "B"
Sleep 1500ms
Up@500ms 1
Sleep 2s

Type ":"
Sleep 1s
Type "toggleraw"
Sleep 1500ms
Enter
Sleep 3500ms
Type ":"
Sleep 800ms
Type "toggleraw"
Sleep 1s
Enter
Sleep 1500ms

Type "v"
Sleep 4500ms
Type "v"
Sleep 4500ms

Type "q"
Sleep 1s
