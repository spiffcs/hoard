package command

// `hoard movers`: the biggest risers and sinkers among the cards you hold.

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/action"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/report"
	"github.com/spiffcs/hoard/internal/store"
)

// NewCmdMovers is the plain shape: flags declared on the cobra command, read in
// RunE, both closures over the same locals.
func NewCmdMovers(a *app) *cobra.Command {
	var since string
	var limit int

	cmd := &cobra.Command{
		Use:     "movers",
		GroupID: groupCollection,
		Short:   "Biggest risers and sinkers you hold",
		Example: "hoard movers [--since 30d] [--limit N]",
		Long: "Compares today's price against the last one recorded on or before\n" +
			"the cutoff. Prices are observed when a refresh runs, not continuously.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return runMovers(a.store, a.env, since, limit)
		},
	}
	cmd.Flags().StringVar(&since, "since", "30d", "how far back to compare (e.g. 7d, 2w, 48h)")
	cmd.Flags().IntVar(&limit, "limit", report.DefaultMoverRows, "rows per section")
	return cli.JSONCapable(cmd)
}

// runMovers is the ported shape: flags arrive as values, and everything it
// writes goes to the Env it was handed rather than to os.Stdout. That second
// part is what makes the command testable — the output was previously
// unreachable, which is why this file only ever tested parseWindow.
func runMovers(st *store.Store, cmdEnv *cli.Env, since string, limit int) error {
	window, err := parseWindow(since)
	if err != nil {
		return err
	}

	out, env := cmdEnv.Out, cmdEnv.OutEnv
	jsonOut := cmdEnv.JSON
	cutoff := time.Now().UTC().Add(-window)
	m, err := action.Deps{Store: st}.Movers(cutoff.Format(time.RFC3339))
	if err != nil {
		return err
	}
	// An empty result means "nothing moved", which is indistinguishable from
	// "nothing was ever recorded" unless the difference is stated outright —
	// in prose for a reader, by recordedSince's absence for a script.
	if m.Observations == 0 {
		if jsonOut {
			return hoardjson.Write(out,
				hoardjson.FromMovers(cutoff.Format(time.RFC3339), "", nil))
		}
		fmt.Fprintln(out, env.Dim()("No price history recorded yet. Run hoard update-prices to start."))
		return nil
	}
	oldest, changes := m.Oldest, m.Changes
	if jsonOut {
		// The whole answer, not the display's top-N: --limit shapes the
		// tables, and a consumer slices its own.
		return hoardjson.Write(out,
			hoardjson.FromMovers(cutoff.Format(time.RFC3339), oldest, changes))
	}
	// A date, not "over the last 7d". Prices are observed when a refresh runs,
	// not continuously, so what is being compared is today's price against the
	// last one recorded on or before that date — which, on a hoard refreshed
	// every few weeks, may have been recorded well before it. Naming the date
	// says that; naming the window would imply the move happened inside it.
	fmt.Fprint(out, report.Movers(env, changes, limit, "since "+cutoff.Local().Format("2 Jan 2006")))

	// History that does not reach back as far as the window was asked to look is
	// worth saying: the answer is right for the data, and the data is younger
	// than the question.
	if t, err := time.Parse(time.RFC3339, oldest); err == nil && t.After(cutoff) {
		fmt.Fprintln(out, env.Dim()(fmt.Sprintf(
			"Prices have only been recorded since %s.", t.Local().Format("2 Jan 2006"))))
	}
	return nil
}

// parseWindow reads a lookback like 7d, 2w or 48h.
//
// Go's ParseDuration stops at hours, and nobody asks for price movement in
// hours-since-720. Days and weeks are spelled out here and everything else is
// handed to the standard parser.
func parseWindow(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("--since needs a value, e.g. 30d")
	}
	unit := time.Duration(0)
	switch s[len(s)-1] {
	case 'd':
		unit = 24 * time.Hour
	case 'w':
		unit = 7 * 24 * time.Hour
	}
	if unit > 0 {
		n, err := strconv.ParseFloat(s[:len(s)-1], 64)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid --since %q: want something like 7d, 2w or 48h", s)
		}
		return time.Duration(n * float64(unit)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid --since %q: want something like 7d, 2w or 48h", s)
	}
	return d, nil
}
