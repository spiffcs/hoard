package main

// The export command: holdings as CSV, in hoard's canonical layout or the
// collection-import formats of Moxfield and Archidekt.

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/spiffcs/hoard/internal/export"
	"github.com/spiffcs/hoard/internal/store"
)

func cmdExport(st *store.Store, args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	format := fs.String("format", "csv", "output format: csv (canonical), moxfield, or archidekt")
	binder := fs.String("binder", "", "export one binder (id, name, or unique fragment)")
	deck := fs.String("deck", "", "export one deck (id, name, or unique fragment)")
	all := fs.Bool("all", false, "export every binder and deck (the default)")
	out := fs.String("o", "", "write to FILE instead of stdout")
	if _, err := parsePositionals(fs, args); err != nil {
		return err
	}

	write := map[string]func(io.Writer, []export.Row) error{
		"csv":       export.WriteCanonical,
		"moxfield":  export.WriteMoxfield,
		"archidekt": export.WriteArchidekt,
	}[*format]
	if write == nil {
		return fmt.Errorf("unknown format %q (want csv, moxfield, or archidekt)", *format)
	}
	if (*binder != "" && *deck != "") || (*all && (*binder != "" || *deck != "")) {
		return fmt.Errorf("choose one of --binder, --deck, or --all")
	}

	rows, err := exportRows(st, *binder, *deck)
	if err != nil {
		return err
	}

	if *out == "" {
		return write(os.Stdout, rows)
	}
	f, err := os.Create(*out)
	if err != nil {
		return err
	}
	if err := write(f, rows); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	copies := 0
	for _, r := range rows {
		copies += r.Count
	}
	fmt.Printf("Exported %d cards to %s\n", copies, *out)
	return nil
}

// exportRows collects the requested holdings; empty refs mean everything —
// every binder, then every deck.
func exportRows(st *store.Store, binderRef, deckRef string) ([]export.Row, error) {
	if binderRef != "" {
		b, err := st.BinderByRef(binderRef)
		if err != nil {
			return nil, err
		}
		return binderRows(st, b.ID, b.Name)
	}
	if deckRef != "" {
		d, err := st.DeckByRef(deckRef)
		if err != nil {
			return nil, err
		}
		return deckRows(st, d.ID, d.Name)
	}

	var rows []export.Row
	binders, err := st.ListBinders()
	if err != nil {
		return nil, err
	}
	for _, b := range binders {
		br, err := binderRows(st, b.ID, b.Name)
		if err != nil {
			return nil, err
		}
		rows = append(rows, br...)
	}
	decks, err := st.ListDecks()
	if err != nil {
		return nil, err
	}
	for _, d := range decks {
		dr, err := deckRows(st, d.ID, d.Name)
		if err != nil {
			return nil, err
		}
		rows = append(rows, dr...)
	}
	return rows, nil
}

func binderRows(st *store.Store, id int64, name string) ([]export.Row, error) {
	list, err := st.BinderByFinish(id)
	if err != nil {
		return nil, err
	}
	rows := make([]export.Row, len(list))
	for i, r := range list {
		rows[i] = export.Row{
			Count:           r.Quantity,
			Name:            r.Name,
			Set:             r.SetCode,
			CollectorNumber: r.CollectorNumber,
			Finish:          r.Finish,
			ScryfallID:      r.ScryfallID,
			Container:       name,
			// BinderByFinish sums across boards, and binder entries only ever
			// hold 'main' — there is no per-row board to report.
			Board:    "main",
			PriceUSD: r.Price(),
		}
	}
	return rows, nil
}

func deckRows(st *store.Store, id int64, name string) ([]export.Row, error) {
	entries, err := st.DeckEntries(id)
	if err != nil {
		return nil, err
	}
	rows := make([]export.Row, len(entries))
	for i, e := range entries {
		rows[i] = export.Row{
			Count:           e.Quantity,
			Name:            e.Card.Name,
			Set:             e.Card.SetCode,
			CollectorNumber: e.Card.CollectorNumber,
			Finish:          e.Finish,
			ScryfallID:      e.Card.ScryfallID,
			Container:       name,
			Board:           e.Board,
			PriceUSD:        e.Price(),
		}
	}
	return rows, nil
}
