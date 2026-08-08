package main

// The export command: holdings as CSV, in hoard's canonical layout or the
// collection-import formats of Moxfield and Archidekt.

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/export"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

// writeHoldingsJSON is the JSON member of the format table, matching the CSV
// writers' signature.
func writeHoldingsJSON(w io.Writer, rows []export.Row) error {
	return hoardjson.Write(w, hoardjson.FromExportRows(rows))
}

func cmdExport(st *store.Store, args []string, jsonOut bool) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	format := fs.String("format", "csv", "output format: csv (canonical), json, moxfield, or archidekt")
	binder := fs.String("binder", "", "export one binder (id, name, or unique fragment)")
	deck := fs.String("deck", "", "export one deck (id, name, or unique fragment)")
	all := fs.Bool("all", false, "export every binder and deck (the default)")
	out := fs.String("o", "", "write to FILE instead of stdout")
	if _, err := parsePositionals(fs, args); err != nil {
		return err
	}

	// The global --json is the same request as --format json; saying both
	// differently is the one combination with no right answer.
	if jsonOut {
		if *format != "csv" && *format != "json" {
			return fmt.Errorf("--json conflicts with --format %s", *format)
		}
		*format = "json"
	}

	write := map[string]func(io.Writer, []export.Row) error{
		"csv":       export.WriteCanonical,
		"json":      writeHoldingsJSON,
		"moxfield":  export.WriteMoxfield,
		"archidekt": export.WriteArchidekt,
	}[*format]
	if write == nil {
		return fmt.Errorf("unknown format %q (want csv, json, moxfield, or archidekt)", *format)
	}
	if (*binder != "" && *deck != "") || (*all && (*binder != "" || *deck != "")) {
		return fmt.Errorf("choose one of --binder, --deck, or --all")
	}

	rows, err := action.Deps{Store: st}.ExportRows(*binder, *deck)
	if err != nil {
		return err
	}

	if *out == "" {
		return write(os.Stdout, rows)
	}
	f, err := createOutput(*out)
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
	ui.NewReport().Result("Exported %d cards to %s", copies, *out)
	return nil
}
