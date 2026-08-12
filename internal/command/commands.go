package command

// The command tree. One place that says what hoard can do, in the order the
// help should read it.
//
// Commands are built as closures over *app rather than handed a store, because
// the store cannot exist until --db has been resolved and the tree has to exist
// before that to parse --db at all. The root's PersistentPreRunE fills the app
// in once flags are parsed; crane threads a *[]crane.Option through its
// NewCmdXxx constructors for the same reason.
//
// Every command is a NewCmdXxx constructor in its own file, named for the
// command, holding that command's flags, its RunE and its private helpers. This
// file is the list — read it to see what hoard does; open a command's file to
// see how. The migration seam that let unported commands parse their own argv
// is gone: nothing sets DisableFlagParsing, so pflag handles `--`, interleaved
// positionals and shorthand runs everywhere.

import (
	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/store"
)

// app is what commands close over. Its fields are filled in by the root's
// PersistentPreRunE, so a RunE built at tree-construction time still sees the
// store that was opened later.
type app struct {
	store *store.Store
	env   *cli.Env
	// dbPath is the file store was opened on. Only `merge` needs it — to
	// refuse merging a hoard into itself — and it is empty when a command
	// runs without a store.
	dbPath string
}

// The one-line description, and the only copy of it in the Go tree. It is the
// root's Short, which means it is also the NAME line of every generated man
// page — and cobra writes that line as "hoard - <Short>". A Short beginning
// "hoard:" therefore produced "hoard - hoard: ...", so the program name is
// deliberately absent here; the help prints it on the Usage line directly below.
//
// KEEP IT UNDER 60 CHARACTERS. The help renderer writes this line verbatim
// rather than wrapping it, and TestUsageFitsANarrowTerminal asserts no help
// line exceeds 60 columns. This is prose, so it is the one string here a
// rewrite is likely to lengthen without anyone thinking about the terminal.
//
// The same sentence is the README's first line, the social card's subtitle and
// the GitHub description. It leads with the category rather than the selling
// point on purpose: a one-liner is read to answer "what kind of thing is this",
// and what makes hoard worth choosing — daily prices, a file you own — belongs
// in the paragraph under it, which is where the README puts it.
const tagline = "a terminal collection tracker for Magic: The Gathering"

// Command groups, in the order the root help reads them.
const (
	groupCollection = "collection"
	groupBinder     = "binder"
	groupDeck       = "deck"
	groupInterop    = "interop"
)

func commandGroups() []*cobra.Group {
	return []*cobra.Group{
		{ID: groupCollection, Title: "Collection commands:"},
		{ID: groupBinder, Title: "Binder commands:"},
		{ID: groupDeck, Title: "Deck commands:"},
		{ID: groupInterop, Title: "Interop commands:"},
	}
}

func rootCommand(a *app) *cobra.Command {
	// Declaration order, not alphabetical. The tree is written in the order the
	// help should read it — add before update-prices before movers — and
	// sorting would scatter that into "add, backfill-prices, binder".
	cobra.EnableCommandSorting = false

	// -v/--version is a flag as well as a subcommand because both spellings
	// predate the tree and scripts use them. It is answered in RunE rather
	// than through cobra's built-in --version so it prints hoard's own notice
	// block instead of a one-line template.
	var versionFlag bool

	root := &cobra.Command{
		Use:   "hoard [command]",
		Short: tagline,
		// No Args validator on purpose. cobra.NoArgs would reject an unknown
		// first word with a bare "unknown command"; leaving it off routes the
		// same case through cobra's own check, which appends a suggestion —
		// `hoard mover` answers "Did you mean this? movers". Bare `hoard` still
		// reaches RunE, which opens the browser: it replaces what used to be
		// four separate read commands, so the thing you reach for most often is
		// what you get by typing nothing.
		RunE: func(c *cobra.Command, _ []string) error {
			if versionFlag {
				printVersion(a.env.Out, a.env.OutEnv)
				return nil
			}
			return cmdBrowse(c.Context(), a.store, a.env.JSON)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
		// The global flags are listed in the root's own help; repeating
		// "[flags]" in the usage line says nothing the reader does not know.
		DisableFlagsInUseLine: true,
	}
	root.Flags().BoolVarP(&versionFlag, "version", "v", false, "print this build's version and legal notices")
	cli.JSONCapable(root)
	root.AddGroup(commandGroups()...)

	root.AddCommand(
		// ---- Collection ----
		NewCmdAdd(a),

		NewCmdUpdatePrices(a),

		NewCmdMovers(a),

		NewCmdBackfillPrices(a),

		NewCmdUnpriced(a),

		NewCmdGuessed(a),

		NewCmdRepairFinishes(a),

		NewCmdVacuum(a),

		NewCmdMarket(a),

		NewCmdReport(a),

		NewCmdWatch(a),

		NewCmdCatalog(a),

		// ---- Binders ----
		NewCmdBinder(a),

		// ---- Decks ----
		NewCmdDeck(a),

		// ---- Interop ----
		NewCmdExport(a),

		NewCmdImport(a),

		NewCmdMerge(a),

		// Ungrouped, with schema and version: none of the four groups is about
		// a hoard you do not have yet.
		NewCmdDemo(a),

		NewCmdSchema(a),

		NewCmdVersion(a),
	)

	return root
}
