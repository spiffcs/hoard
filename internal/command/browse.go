package command

// `hoard` with no arguments: the browser at a terminal, the summary document
// when piped. It replaces what used to be four separate read commands, so the
// thing reached for most often is what typing nothing gives.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/browse"
	"github.com/spiffcs/hoard/internal/decksource"
	"github.com/spiffcs/hoard/internal/export"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/market"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/tui"
	"github.com/spiffcs/hoard/internal/ui"
)

// openInBrowser hands a URL to the platform's opener — the detail view's
// vendor links. Start, not Run: the browser owns its own lifetime.
func openInBrowser(u string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", u).Start()
	case "windows":
		return exec.Command("cmd", "/c", "start", u).Start()
	}
	return exec.Command("xdg-open", u).Start()
}

// browseDeckAdd imports an acquired deck and shapes the browser's report.
// Both deck-import seams — a pasted link, an exported file — differ only in
// how they get the list, so everything after that is written once.
func browseDeckAdd(ctx context.Context, deps action.Deps, p progress.Fn, deck *decksource.Deck) (browse.OpReport, error) {
	// No dry run from the browser: its deck-import prompts commit, and there
	// is no rehearsal surface to report the result on.
	res, err := action.DeckAdd(ctx, deps, p, deck, action.DeckAddOptions{})
	if err != nil && !errors.Is(err, errPartial) {
		return browse.OpReport{}, err
	}
	r := browse.OpReport{Summary: fmt.Sprintf("imported deck %q (%s) · %d cards resolved",
		res.Name, res.Source, res.Resolved)}
	if res.Refinished > 0 {
		r.Summary += fmt.Sprintf(" · %d recorded as foil", res.Refinished)
	}
	if len(res.Unresolved) > 0 {
		r.Summary += fmt.Sprintf(" · %d unresolved", len(res.Unresolved))
		r.Report = append([]string{
			fmt.Sprintf("%d cards could not be resolved and were skipped:", len(res.Unresolved)), "",
		}, res.Unresolved...)
	}
	return r, nil
}

// cmdBrowse is what `hoard` with no arguments does: the browser at a terminal,
// the summary table when piped, so `hoard | grep` keeps working.
//
// The loop is the add handoff — `a` quits with a request, we run the cascade, then
// re-enter. Two bubbletea programs cannot share a terminal, so they take turns.
func cmdBrowse(ctx context.Context, st *store.Store, jsonOut bool) error {
	// --json means the summary document even at a terminal: asking for JSON is
	// asking for output, not for an interactive session.
	if jsonOut {
		return writeSummary(st, true)
	}
	if !stdoutIsTTY() {
		return writeSummary(st, false)
	}
	// One catalog handle for the whole session; the injected operations and
	// the embedded add cascade share it.
	cat := openCatalog()
	if cat != nil {
		defer cat.Close()
	}
	// Deps.Confirm bridges an op goroutine's blocking question (the catalog
	// download ask inside update-prices) to the browser's confirm surface.
	// Cap-1 channels on both legs: the ask never blocks on a pump re-arm
	// race, and the answer never blocks on a worker that gave up. A dead
	// context answers "no" — the same reading a piped CLI run gives.
	confirmCh := make(chan browse.ConfirmRequest, 1)
	deps := action.Deps{
		Store: st, Catalog: cat, CacheDir: pricing.DefaultCacheDir(), Resolver: cardResolver,
		Confirm: func(q string) bool {
			reply := make(chan bool, 1)
			select {
			case confirmCh <- browse.ConfirmRequest{Question: q, Reply: reply}:
			case <-ctx.Done():
				return false
			}
			select {
			case a := <-reply:
				return a
			case <-ctx.Done():
				return false
			}
		},
	}

	sum, err := browse.Run(ctx, st,
		browse.WithConfirm(confirmCh),
		browse.WithCardImage(fetchCardImage),
		browse.WithCatalogOffer(cat != nil && cat.CardCount() == 0),
		browse.WithDeckAddByURL(func(ctx context.Context, p progress.Fn, rawURL string) (browse.OpReport, error) {
			deck, ferr := decksource.Fetch(ctx, rawURL)
			if ferr != nil {
				return browse.OpReport{}, ferr
			}
			return browseDeckAdd(ctx, deps, p, deck)
		}),
		// The same capability from a file: providers that block fetching
		// still export, so the browser takes the export directly rather
		// than sending the reader to the CLI's 'deck add --file'.
		browse.WithDeckAddByFile(func(ctx context.Context, p progress.Fn, path string) (browse.OpReport, error) {
			deck, perr := importTextDeck(path, "", "")
			if perr != nil {
				return browse.OpReport{}, perr
			}
			return browseDeckAdd(ctx, deps, p, deck)
		}),
		browse.WithImportFile(func(ctx context.Context, p progress.Fn, path string, again bool) (browse.OpReport, error) {
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return browse.OpReport{}, rerr
			}
			res, ierr := action.ImportCollection(ctx, deps, p, action.ImportOptions{
				Data: data, Display: path, Format: "auto", Again: again,
			})
			// The ledger refusal is a question, not a failure: the browser
			// stages "import it again?" where the CLI prints --again advice.
			var already *action.AlreadyImportedError
			if errors.As(ierr, &already) {
				return browse.OpReport{AlreadyImported: fmt.Sprintf(
					"already imported on %s (%d cards)", already.When, already.Cards)}, nil
			}
			// A partial import still did its work; the outcome reports the
			// skips rather than erroring away a committed result.
			if ierr != nil && !errors.Is(ierr, errPartial) {
				return browse.OpReport{}, ierr
			}
			r := browse.OpReport{Summary: fmt.Sprintf("imported %d cards (%s format) · %d rows resolved",
				res.Copies, res.Format, res.Resolved)}
			var lines []string
			for _, name := range sortedKeys(res.PerBinder) {
				note := ""
				if slices.Contains(res.Created, name) {
					note = " (new binder)"
				}
				lines = append(lines, fmt.Sprintf("%d into %s%s", res.PerBinder[name], name, note))
			}
			if res.SkippedDeckRows > 0 {
				lines = append(lines, fmt.Sprintf(
					"skipped %d deck rows: decks come back via 'hoard deck add', not as loose cards",
					res.SkippedDeckRows))
			}
			if res.Refinished > 0 {
				lines = append(lines, fmt.Sprintf(
					"%d recorded as foil: the file said otherwise but the printing has no non-foil",
					res.Refinished))
			}
			for _, field := range sortedKeys(res.Dropped) {
				lines = append(lines, fmt.Sprintf("dropped %s on %d rows: hoard could not carry it",
					field, res.Dropped[field]))
			}
			if len(res.Unresolved) > 0 {
				r.Summary += fmt.Sprintf(" · %d skipped", len(res.Unresolved))
				lines = append(lines, fmt.Sprintf("%d cards could not be resolved and were skipped:",
					len(res.Unresolved)))
				for _, u := range res.Unresolved {
					lines = append(lines, "  - "+u)
				}
			}
			r.Report = lines
			return r, nil
		}),
		browse.WithExport(func(binderRef, deckRef, format, path string) (string, error) {
			write := map[string]func(io.Writer, []export.Row) error{
				"csv":       export.WriteCanonical,
				"json":      writeHoldingsJSON,
				"moxfield":  export.WriteMoxfield,
				"archidekt": export.WriteArchidekt,
			}[format]
			if write == nil {
				return "", fmt.Errorf("unknown format %q", format)
			}
			rows, rerr := action.Deps{Store: st}.ExportRows(binderRef, deckRef)
			if rerr != nil {
				return "", rerr
			}
			f, ferr := createOutput(path)
			if ferr != nil {
				return "", ferr
			}
			if werr := write(f, rows); werr != nil {
				f.Abort()
				return "", werr
			}
			if cerr := f.Commit(); cerr != nil {
				return "", cerr
			}
			return fmt.Sprintf("exported %s rows to %s", ui.Count(len(rows)), path), nil
		}),
		browse.WithReport(func(top, width int) ([]string, error) {
			d, derr := deps.Valuation(top)
			if derr != nil {
				return nil, derr
			}
			// The TUI supplies its own width; Detect still decides color, so
			// NO_COLOR reaches the report overlay too.
			env := ui.Detect(os.Stdout)
			env.Width, env.Clamp = width, true
			text := report.Valuation(env, d)
			return strings.Split(strings.TrimRight(text, "\n"), "\n"), nil
		}),
		browse.WithMarket(func(ctx context.Context, p progress.Fn, min float64) (market.Result, error) {
			return action.Market(ctx, deps, p, min)
		}),
		browse.WithMarketCached(func(min float64) (market.Result, bool) {
			res, ok, err := action.MarketCached(deps, min)
			return res, ok && err == nil
		}),
		browse.WithCardComps(func(id string) (map[string]market.Comp, bool) {
			comps, ok, err := action.CardComps(deps, id)
			return comps, ok && err == nil
		}),
		browse.WithOpenURL(openInBrowser),
		browse.WithPrintSearch(newSearcher(cat).SearchPrints),
		browse.WithUpdatePrices(func(ctx context.Context, p progress.Fn) (string, error) {
			res, err := action.UpdatePrices(ctx, deps, p)
			if err != nil {
				return "", err
			}
			if res.Total == 0 {
				return "no cards yet; nothing to update", nil
			}
			s := fmt.Sprintf("prices updated · %s printings", ui.Count(res.Found))
			if res.Gaps.Remaining > 0 {
				s += fmt.Sprintf(" · %d still unpriced", res.Gaps.Remaining)
			}
			return s, nil
		}),
		browse.WithRepairFinishes(func(ctx context.Context, p progress.Fn) (string, error) {
			res, err := action.RepairFinishes(ctx, deps, p)
			if err != nil {
				return "", err
			}
			switch {
			case res.Total == 0:
				return "no cards yet; nothing to repair", nil
			case len(res.Fixed) == 0 && len(res.Ambiguous) == 0:
				return "every finish already correct", nil
			case len(res.Ambiguous) > 0:
				return fmt.Sprintf("finishes repaired · %d fixed · %d ambiguous (see hoard repair-finishes)",
					len(res.Fixed), len(res.Ambiguous)), nil
			}
			return fmt.Sprintf("finishes repaired · %d fixed", len(res.Fixed)), nil
		}),
		browse.WithCatalogUpdate(func(ctx context.Context, p progress.Fn) (string, error) {
			res, err := action.CatalogUpdate(ctx, deps, p)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("catalog ready · %s cards", ui.Count(res.Cards)), nil
		}),
		browse.WithBackfill(func(ctx context.Context, p progress.Fn, days int) (string, error) {
			res, err := action.BackfillPrices(ctx, deps, p, days)
			if err != nil {
				return "", err
			}
			switch {
			case res.Printings == 0:
				return "nothing owned yet", nil
			case res.AlreadyToday != "":
				return "already backfilled today", nil
			case res.Inserted == 0 && res.BidInserted == 0:
				return "nothing to backfill · history already recorded", nil
			}
			summary := fmt.Sprintf("backfilled %s observations across %s printings",
				ui.Count(res.Inserted), ui.Count(res.Cards))
			if res.BidInserted > 0 {
				summary += fmt.Sprintf(" · %s buylist bids", ui.Count(res.BidInserted))
			}
			return summary, nil
		}),
		browse.WithWatchAddByName(func(ctx context.Context, p progress.Fn,
			name, op string, threshold float64) (string, error) {
			res, err := action.WatchAdd(ctx, deps, p,
				action.WatchAddOptions{Name: name, Op: op, Threshold: threshold})
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("watching %s (%s) %s %s",
				res.Card.Name, res.Finish, op, ui.Money(threshold)), nil
		}),
		browse.WithWatchImportFile(func(ctx context.Context, p progress.Fn, path string) (browse.OpReport, error) {
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return browse.OpReport{}, rerr
			}
			res, werr := action.WatchImport(ctx, deps, p,
				action.WatchImportOptions{Data: data, Display: path})
			// A partial import still stood its watches; the outcome reports
			// the skips rather than erroring away a committed result.
			if werr != nil && !errors.Is(werr, errPartial) {
				return browse.OpReport{}, werr
			}
			r := browse.OpReport{Summary: fmt.Sprintf("imported %d watches · %d new · %d adjusted",
				res.Created+res.Updated, res.Created, res.Updated)}
			var lines []string
			if res.Refinished > 0 {
				lines = append(lines, fmt.Sprintf(
					"%d watch the foil price: the file said otherwise but the printing has no non-foil",
					res.Refinished))
			}
			if len(res.Unresolved) > 0 {
				r.Summary += fmt.Sprintf(" · %d skipped", len(res.Unresolved))
				lines = append(lines, fmt.Sprintf("%d cards could not be resolved and were skipped:",
					len(res.Unresolved)))
				for _, u := range res.Unresolved {
					lines = append(lines, "  - "+u)
				}
			}
			r.Report = lines
			return r, nil
		}),
		browse.WithAddCascade(func() (tui.Child, error) {
			// Destinations re-read per invocation, so a binder created in
			// the browser appears in the cascade's picker.
			dests, derr := destinations(st)
			if derr != nil {
				return tui.Child{}, derr
			}
			return tui.NewChild(ctx, newSearcher(cat), storeAdder(st), linkScanner{}, "", dests), nil
		}))
	// The scan receipt outlives the alternate screen: unattended writes need
	// a durable record, and the status line dies with the program.
	printScanSummary(sum)
	return err
}

// writeSummary prints the hoard's totals, the output `hoard summary` used to
// produce. It is what a non-interactive `hoard` writes.
func writeSummary(st *store.Store, jsonOut bool) error {
	sum, err := action.Deps{Store: st}.Summary()
	if err != nil {
		return err
	}
	if jsonOut {
		return hoardjson.Write(os.Stdout, hoardjson.FromSummary(sum.Binder, sum.Decks))
	}
	fmt.Print(report.Summary(ui.Detect(os.Stdout), sum.Binder, sum.Decks))
	return nil
}

// stdoutIsTTY reports whether output is going to an interactive terminal rather
// than a pipe or a file.
func stdoutIsTTY() bool { return isTTY(os.Stdout) }
