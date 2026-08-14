package command

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

func NewCmdMovers(a *app) *cobra.Command {
	var since string
	var limit int

	cmd := &cobra.Command{
		Use:     "movers",
		GroupID: groupCollection,
		Short:   "Biggest risers and sinkers you hold",
		Example: "hoard movers [--since 30d] [--limit N]",

		Long: "Compares today's price against the last one recorded on\n" +
			"or before the cutoff. Prices are observed when a refresh\n" +
			"runs, not continuously.\n\n" +
			"The net holds out sets released in the last " +
			strconv.Itoa(store.DefaultSettlingDays) + " days: a\n" +
			"market price averages completed sales, and a set with\n" +
			"none has an average over nothing. The rows stay listed.\n" +
			"Set " + store.SettlingDaysEnv + " to change the window,\n" +
			"or to 0 to count every set.",
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return runMovers(a.store, a.env, since, limit)
		},
	}
	cmd.Flags().StringVar(&since, "since", "30d", "how far back to compare (e.g. 7d, 2w, 48h)")
	cmd.Flags().IntVar(&limit, "limit", report.DefaultMoverRows, "rows per section")
	return cli.JSONCapable(cmd)
}

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

		return hoardjson.Write(out,
			hoardjson.FromMovers(cutoff.Format(time.RFC3339), oldest, changes))
	}

	fmt.Fprint(out, report.Movers(env, changes, limit,
		"since "+cutoff.Local().Format("2 Jan 2006"), cutoff))

	if t, err := time.Parse(time.RFC3339, oldest); err == nil && t.After(cutoff) {
		fmt.Fprintln(out, env.Dim()(fmt.Sprintf(
			"Prices have only been recorded since %s.", t.Local().Format("2 Jan 2006"))))
	}
	return nil
}

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
