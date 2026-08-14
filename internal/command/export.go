package command

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/export"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/store"
)

func writeHoldingsJSON(w io.Writer, rows []export.Row) error {
	return hoardjson.Write(w, hoardjson.FromExportRows(rows))
}

type onceString struct {
	value string
	count int
}

func (s *onceString) String() string { return s.value }

func (s *onceString) Type() string { return "string" }

func (s *onceString) Set(v string) error {
	s.value = v
	s.count++
	return nil
}

func NewCmdExport(a *app) *cobra.Command {
	var format, outPath string
	var binder, deck onceString
	var all bool

	cmd := &cobra.Command{
		Use:     "export",
		GroupID: groupInterop,
		Short:   "Holdings as CSV or JSON, in hoard's format or theirs",
		Example: "hoard export [--binder B | --deck D | --all] [-o FILE]\n" +
			"       [--format csv|json|text|moxfield|archidekt]",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {

			for _, f := range []struct {
				name string
				v    *onceString
			}{{"binder", &binder}, {"deck", &deck}} {
				if f.v.count > 1 {
					return cli.Usagef(
						"--%s given more than once; an export names one container — choose one, or --all for every binder and deck",
						f.name)
				}
			}
			return runExport(a.store, a.env, format, c.Flags().Changed("format"),
				binder.value, deck.value, outPath, all)
		},
	}
	cmd.Flags().StringVar(&format, "format", "csv",
		"output format: csv (canonical), json, text (a decklist 'deck add --file' reads), moxfield, or archidekt")
	cmd.Flags().Var(&binder, "binder", "export one binder (id, name, or unique fragment)")
	cmd.Flags().Var(&deck, "deck", "export one deck (id, name, or unique fragment)")
	cmd.Flags().BoolVar(&all, "all", false, "export every binder and deck (the default)")

	cmd.Flags().StringVarP(&outPath, "output", "o", "", "write to FILE instead of stdout")
	return cli.JSONCapable(cmd)
}

func runExport(st *store.Store, env *cli.Env, format string, formatSet bool,
	binder, deck, outPath string, all bool) error {

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

func containers(rows []export.Row) int {
	seen := make(map[string]bool, 1)
	for _, r := range rows {
		seen[r.Container] = true
	}
	return len(seen)
}
