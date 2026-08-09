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
			"       [--format csv|json|moxfield|archidekt]",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return runExport(a.store, a.env, format, binder, deck, outPath, all)
		},
	}
	cmd.Flags().StringVar(&format, "format", "csv",
		"output format: csv (canonical), json, moxfield, or archidekt")
	cmd.Flags().StringVar(&binder, "binder", "", "export one binder (id, name, or unique fragment)")
	cmd.Flags().StringVar(&deck, "deck", "", "export one deck (id, name, or unique fragment)")
	cmd.Flags().BoolVar(&all, "all", false, "export every binder and deck (the default)")
	// See NewCmdReport on why -o hangs off a long name.
	cmd.Flags().StringVarP(&outPath, "output", "o", "", "write to FILE instead of stdout")
	return cli.JSONCapable(cmd)
}

func runExport(st *store.Store, env *cli.Env, format, binder, deck, outPath string, all bool) error {
	// The global --json is the same request as --format json; saying both
	// differently is the one combination with no right answer.
	if env.JSON {
		if format != "csv" && format != "json" {
			return cli.Usagef("--json conflicts with --format %s", format)
		}
		format = "json"
	}

	write := map[string]func(io.Writer, []export.Row) error{
		"csv":       export.WriteCanonical,
		"json":      writeHoldingsJSON,
		"moxfield":  export.WriteMoxfield,
		"archidekt": export.WriteArchidekt,
	}[format]
	if write == nil {
		return cli.Usagef("unknown format %q (want csv, json, moxfield, or archidekt)", format)
	}
	if (binder != "" && deck != "") || (all && (binder != "" || deck != "")) {
		return cli.Usagef("choose one of --binder, --deck, or --all")
	}

	rows, err := action.Deps{Store: st}.ExportRows(binder, deck)
	if err != nil {
		return err
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
