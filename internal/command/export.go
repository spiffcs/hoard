package command

// The export command: holdings as CSV, in hoard's canonical layout or the
// collection-import formats of Moxfield and Archidekt.

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/export"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/store"
)

// writeHoldingsJSON is the JSON member of the format table, matching the CSV
// writers' signature.
func writeHoldingsJSON(w io.Writer, rows []export.Row) error {
	return hoardjson.Write(w, hoardjson.FromExportRows(rows))
}

// NewCmdExport builds `hoard export`.
func NewCmdExport(a *app) *cobra.Command {
	var format, binder, deck, outPath string
	var all bool

	cmd := &cobra.Command{
		Use:     "export",
		GroupID: groupInterop,
		Short:   "Holdings as CSV or JSON, in hoard's format or theirs",
		Example: "hoard export [--binder B | --deck D | --all] [-o FILE]\n" +
			"       [--format csv|json|text|moxfield|archidekt]",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runExport(a.store, a.env, format, c.Flags().Changed("format"),
				binder, deck, outPath, all)
		},
	}
	cmd.Flags().StringVar(&format, "format", "csv",
		"output format: csv (canonical), json, text (a decklist 'deck add --file' reads), moxfield, or archidekt")
	cmd.Flags().StringVar(&binder, "binder", "", "export one binder (id, name, or unique fragment)")
	cmd.Flags().StringVar(&deck, "deck", "", "export one deck (id, name, or unique fragment)")
	cmd.Flags().BoolVar(&all, "all", false, "export every binder and deck (the default)")
	// See NewCmdReport on why -o hangs off a long name.
	cmd.Flags().StringVarP(&outPath, "output", "o", "", "write to FILE instead of stdout")
	return cli.JSONCapable(cmd)
}

func runExport(st *store.Store, env *cli.Env, format string, formatSet bool,
	binder, deck, outPath string, all bool) error {
	// The global --json is the same request as --format json; saying both
	// differently is the combination with no right answer.
	//
	// formatSet, not the value: --format csv --json is exactly as much of a
	// contradiction as --format moxfield --json, but testing format != "csv"
	// could not tell an explicit csv from the default it happens to equal, so
	// the one spelling a script is most likely to write — name the format,
	// inherit --json from a wrapper — was silently promoted to JSON instead
	// of refused. --format json --json is the same request twice and stands.
	if env.JSON {
		if formatSet && format != "json" {
			return cli.Usagef("--json conflicts with --format %s", format)
		}
		format = "json"
	}

	write := map[string]func(io.Writer, []export.Row) error{
		"csv":       export.WriteCanonical,
		"json":      writeHoldingsJSON,
		"text":      export.WriteText,
		"moxfield":  export.WriteMoxfield,
		"archidekt": export.WriteArchidekt,
	}[format]
	if write == nil {
		return cli.Usagef("unknown format %q (want csv, json, text, moxfield, or archidekt)", format)
	}
	if (binder != "" && deck != "") || (all && (binder != "" || deck != "")) {
		return cli.Usagef("choose one of --binder, --deck, or --all")
	}

	rows, err := action.Deps{Store: st}.ExportRows(binder, deck)
	if err != nil {
		return err
	}
	// A text decklist is read back by `hoard deck add --file`, which builds
	// exactly one deck from the file it is given. Writing several containers
	// into one file would produce something that restores as a single deck
	// holding everything — the same quiet lie this format exists to end — so
	// the scope has to be named rather than guessed at.
	if format == "text" && containers(rows) > 1 {
		return cli.Usagef("--format text writes one container's list; name it with --deck D or --binder B")
	}

	if outPath == "" {
		return write(env.Out, rows)
	}
	f, err := createOutput(outPath)
	if err != nil {
		return err
	}
	if err := write(f, rows); err != nil {
		f.Abort()
		return err
	}
	if err := f.Commit(); err != nil {
		return err
	}
	copies := 0
	for _, r := range rows {
		copies += r.Count
	}
	env.Report().Result("Exported %d cards to %s", copies, outPath)
	return nil
}

// containers counts the distinct binders and decks the rows came from.
func containers(rows []export.Row) int {
	seen := make(map[string]bool, 1)
	for _, r := range rows {
		seen[r.Container] = true
	}
	return len(seen)
}
