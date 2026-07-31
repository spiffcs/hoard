// Package collsource parses collection CSV exports from other tools —
// ManaBox, Moxfield, Delver Lens — and hoard's own canonical export, into one
// normalized shape the import command can resolve against Scryfall.
//
// The sibling of internal/decksource, which does the same for decklists. The
// difference is dispatch: decklists arrive by URL or explicit flag, while
// collection CSVs are recognized by their header row, since users rarely know
// or care which app's "Export" button produced the file.
package collsource

import (
	"github.com/spiffcs/hoard/internal/scryfall"
)

// Row is one normalized holding from a foreign export.
//
// Ident carries the single best resolution scheme the file offered (Scryfall
// ID, else set+number, else name); Name is kept alongside even then, so a
// set+number miss can fall back to a name lookup without re-parsing.
type Row struct {
	Quantity int
	Ident    scryfall.Identifier
	Name     string
	Finish   string // normal|foil|etched — hoard's spelling
	// Binder is the source's own container label (ManaBox's "Binder Name",
	// hoard's Container), empty for formats that have none. Only honored when
	// the import asks to preserve binders.
	Binder string
	// Kind is the container's kind in hoard's own canonical CSV — "binder" or
	// "deck" — and empty for every other format (and for canonical files that
	// predate the column, which held only binder rows). The importer skips
	// deck rows: pouring a deck's contents into a binder would count those
	// cards twice the moment the deck itself is re-imported.
	Kind string
}

// Collection is a parsed export file.
type Collection struct {
	Rows   []Row
	Format string // which parser handled the file
	// Dropped counts rows whose value in a column hoard cannot store carried
	// real information — a condition other than near-mint, a language other
	// than English, a purchase price. The import reports these so the loss is
	// visible instead of silent.
	Dropped map[string]int
}
