package main

// The report command: a dated valuation of the whole hoard — totals, each
// binder, the most valuable holdings, and where every price came from — in a
// form worth handing to an insurer or a buyer.

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func cmdReport(st *store.Store, args []string, jsonOut bool) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	top := fs.Int("top", 10, "holdings to itemize in the text report")
	csvOut := fs.Bool("csv", false, "write the full itemized valuation as CSV")
	out := fs.String("o", "", "write to FILE instead of stdout")
	if _, err := parsePositionals(fs, args); err != nil {
		return err
	}
	if *csvOut && jsonOut {
		return fmt.Errorf("choose --csv or --json, not both")
	}

	d, err := action.Deps{Store: st}.Valuation(*top)
	if err != nil {
		return err
	}

	w := os.Stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	switch {
	case jsonOut:
		return hoardjson.Write(w, hoardjson.FromValuation(d))
	case *csvOut:
		owned, err := st.OwnedByFinish()
		if err != nil {
			return err
		}
		return report.ValuationCSV(w, d.AsOf, owned)
	default:
		// Detect on the actual destination: a report written to a file must
		// be plain text however colorful the terminal running the command.
		_, err := io.WriteString(w, report.Valuation(ui.Detect(w), d))
		return err
	}
}
