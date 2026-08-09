// Command hoard catalogs valuable Magic: The Gathering cards in a local SQLite
// database. Loose cards are added by their Scryfall page URL; whole decks are
// imported from a deck-list link (or a pasted/exported text list). The tool
// records how many of each card you own (across the collection and every deck)
// and their current market prices.
//
// Everything hoard does lives in internal/command. This file is the entry point
// and nothing else: os.Exit is the one thing that cannot be written anywhere
// testable, so it is the one thing kept here.
package main

import (
	"os"

	"github.com/spiffcs/hoard/internal/command"
)

func main() { os.Exit(command.Run(os.Args[1:])) }
