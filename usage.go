package main

// The usage text as data: command rows laid out by the ui.Table engine at
// the terminal's real width — the first output surface that responds to it
// — with section headers bold, prose dim, and the whole thing exactly its
// old hand-aligned self when piped.

import (
	"fmt"
	"io"
	"strings"

	"github.com/spiffcs/hoard/internal/ui"
)

// usageRow is one command: what you type, what it does. A continuation row
// (a wrapped invocation or description) is just another row.
type usageRow struct{ invocation, description string }

// usageSections are the command tables, one titled group each. They render
// as a single table so the description column aligns across every group.
var usageSections = []struct {
	title string
	rows  []usageRow
}{
	{"Collection commands:", []usageRow{
		{"add", "Add cards interactively by name"},
		{"add <scryfall-url> [--foil] [--qty N]", "Add one card by its Scryfall link"},
		{"add --file LIST | - [--binder B] [--again]", "Add a pasted/exported card list (or pipe one in)"},
		{"update-prices [--limit N]", "Refresh prices (Scryfall updates daily)"},
		{"movers [--since 30d] [--limit N]", "Biggest risers and sinkers you hold"},
		{"backfill-prices", "Load 90 days of past prices from MTGJSON"},
		{"unpriced", "Cards counting as $0.00, and why"},
		{"repair-finishes", "Fix cards stored as a finish they lack"},
		{"vacuum", "Delete orphaned printings nothing holds or watches"},
		{"market [--min N] [--limit N]", "Vendor prices vs TCGplayer's last-sold prices"},
		{"report [--top N] [--csv] [-o FILE]", "Dated valuation: totals, binders, top holdings"},
		{"watch", "Check price watches (no network; exit 3 = fired)"},
		{"watch add <name> --under N|--over N [--foil]", "Alert when a price crosses a threshold"},
		{"watch import <file>", "Import price watches in bulk (CSV or JSON)"},
		{"watch list | watch rm <id|name>", "Your watches, and removing one"},
		{"catalog [status|update]", "The local copy of Scryfall's card data"},
	}},
	{"Binder commands:", []usageRow{
		{"binder list", "Your binders, with counts and value"},
		{"binder new <name>", "Create a named binder"},
		{"binder rename <binder> <new-name>", "Rename a binder"},
		{"binder rm <binder>", "Remove an empty binder"},
	}},
	{"Deck commands:", []usageRow{
		{"deck add <archidekt-url>", "Import/refresh a deck from a link"},
		{"deck add --file <path> [--name NAME] [--source S]", "Import a text/exported decklist"},
		{"deck remove <name>", "Delete a deck"},
		{"deck repin <name> <set>", "Re-point a deck's cards at the set it came from"},
	}},
	{"Interop commands:", []usageRow{
		{"export [--binder B | --deck D | --all] [-o FILE]", "Holdings as CSV or JSON (everything by"},
		{"       [--format csv|json|moxfield|archidekt]", "default) in hoard's format or theirs"},
		{"import FILE [--binder B | --preserve-binders]", "Add a collection CSV export (ManaBox,"},
		{"       [--format F] [--dry-run]", "Moxfield, Delver Lens, hoard)"},
	}},
}

// printUsage lays the usage out for one stream: asked-for help goes to
// stdout at stdout's width, the error paths print to stderr at stderr's.
// Commands only — the longer story (browsing, --json, exit codes, where
// the database lives) belongs to the README and docs/, not to every typo.
func printUsage(w io.Writer, env ui.Env) {
	bold := env.Bold()

	var b strings.Builder
	b.WriteString(bold("hoard: catalog valuable MTG cards and decks in SQLite") + "\n\n")
	b.WriteString(bold("Usage:") + "\n")
	b.WriteString("  hoard [--db PATH] [--json] [command] [args]\n")

	t := ui.Table{Env: env, Cols: []ui.Col{
		// Min makes the invocation column shrinkable on a narrow terminal —
		// without one the layout engine could only give up and let rows run
		// past the edge.
		{Align: ui.Left, Min: 24},
		{Align: ui.Left, Flex: true, Min: 16},
	}}
	t.AddSpacer()
	t.Add(ui.C("  hoard"), ui.C("Browse the hoard"))
	for _, sec := range usageSections {
		t.AddSpacer()
		t.AddStyled(bold, ui.C(sec.title))
		for _, r := range sec.rows {
			t.Add(ui.C("  "+r.invocation), ui.C(r.description))
		}
	}
	b.WriteString(t.Render())
	fmt.Fprint(w, b.String())
}
