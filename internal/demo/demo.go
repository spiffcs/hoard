// Package demo is everything `hoard demo` is made of, and nothing else is.
//
// The sample collection and its price history, the reader that seeds them, and
// the generator that produces them all live here rather than beside the code
// that runs a real hoard. That separation is the point of the directory: none
// of this ships a capability, none of it runs unless someone asks for the demo,
// and a reader looking at internal/ should be able to see at a glance which
// files are the product and which are the showroom.
//
// Two things deliberately stayed outside it. `hoard demo` itself is a command,
// so it sits with the other commands in internal/command — the demo database is
// opened and browsed by exactly the code that opens and browses a real one, and
// a private copy of that path would be a second answer to what the browser
// does. And the collection is applied by action.SeedHoard, which shares the
// merge planner: a demo assembled by some other route would be free to drift
// from what a merged hoard looks like, and going through the planner means a
// bug that would corrupt a real merge shows up in the demo first.
//
// # The documents
//
// Collection is a hoard interchange document — the same `kind: hoard` JSON that
// `hoard merge -o` writes and that hoard already knows how to apply. Nothing
// here is a special format invented for the demo. History is the one exception
// and says so in its own file: build data, not interchange.
//
// Compiled in rather than fetched. The file lives in the repository and is
// readable on GitHub like any other source, but a demo that needs the network
// is a demo that fails on a train — and the whole purpose is to be the first
// thing a new user runs.
//
// The card documents inside are Scryfall's, carried so the browser has rarity,
// colours, type lines and art without a lookup. That is the same data any
// hoard holds after its first add; see NOTICE for whose it is.
//
// # Regenerating
//
// The document is produced by hoard, not written by hand:
//
//	export HOARD_DB=/tmp/seed.db
//	hoard binder new "Trade"
//	hoard add <scryfall-url> --qty N [--binder Trade] [--foil]   # ...and so on
//	printf '4 Lightning Bolt\n...' | hoard deck add --file - --name "Kitchen Table"
//	HOARD_DB=/tmp/target.db hoard merge /tmp/seed.db --dry-run -o collection.json
//
// Then the history that goes with it, which is derived from the collection and
// so has to follow it:
//
//	task generate-demo-history   # go run ./internal/demo/gen ...
//
// Keep it slim. Every printing carries its full Scryfall document, so this is
// ~8 KB per card, and it is linked into the binary.
package demo

import _ "embed"

// Collection is the sample hoard, as an interchange document.
//
// Prices in it are frozen at the moment it was generated and will drift from
// the market — that is acceptable and worth knowing. The demo exists to show
// what a populated hoard looks like, not to quote anyone a price, and
// `hoard demo` says so before it opens.
//
//go:embed collection.json
var Collection []byte

// History is the sample collection's price history: ninety days of retail
// observations and buylist bids for the printings in Collection.
//
// It is here for the same reason the collection is compiled in rather than
// fetched. The movers view charts a card against its own past, so a database
// seeded from a document alone opens it empty — and filling it costs a ~150 MB
// MTGJSON download, which is not a thing a demo should do to someone who typed
// `hoard demo` to have a look around.
//
// Frozen like the prices, and it ages the same way: the movers windows are
// measured back from today, so once the whole series falls behind the window
// being asked for, that window has nothing to report. Regenerate this and
// collection.json together — see internal/demo/gen.
//
//go:embed history.json
var History []byte
