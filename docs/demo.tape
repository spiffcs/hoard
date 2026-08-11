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
Type `export HOARD_DB="$(mktemp -d)/demo.db"`
Enter
Type `cp "$HOME/Library/Application Support/hoard/hoard.db" "$HOARD_DB"`
Enter
Type "clear"
Enter
Sleep 2s
Show

# 1. A dated valuation of the whole collection.
Type "hoard report"
Enter
Sleep 5s
Type "clear"
Enter
Sleep 500ms

# 2. The browser is what `hoard` with no arguments opens, and it is the half
#    no static example conveys.
Type "hoard"
Enter
Sleep 3s
Down@400ms 6
Sleep 1500ms

# 3. Card detail: the printing, its price history and its comps.
Enter
Sleep 4s
Escape
Sleep 1s

# 4. tab cycles the four screens: holdings, movers, watches, market.
Tab
Sleep 3s
Tab
Sleep 3s
Tab
Sleep 3s
Tab
Sleep 1500ms

# 5. `/` filters any screen. The grammar takes name, set, rarity, type,
#    artist, colour, cmc, price, value, qty, finish, board, layout, setname.
Type "/"
Sleep 700ms
Type "rarity:mythic"
Sleep 700ms
Enter
Sleep 3500ms

Escape
Sleep 1s
Type "q"
Sleep 1s
