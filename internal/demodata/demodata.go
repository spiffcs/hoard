// Package demodata carries the sample collection `hoard demo` opens.
//
// It is a hoard interchange document — the same `kind: hoard` JSON that
// `hoard merge -o` writes and that hoard already knows how to apply. Nothing
// here is a special format invented for the demo, which is the point: the demo
// database is seeded through the merge path every other hoard uses, so it
// cannot drift from what a real database looks like.
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
// Keep it slim. Every printing carries its full Scryfall document, so this is
// ~8 KB per card, and it is linked into the binary.
package demodata

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
