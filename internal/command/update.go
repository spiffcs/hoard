package command

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/buildinfo"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/release"
)

const (
	// The check is a courtesy, never a reason to wait. version keeps a shorter
	// leash than update, because update is what the user asked for.
	versionCheckTimeout = 3 * time.Second
	updateCheckTimeout  = 10 * time.Second

	noCheckEnv = "HOARD_NO_UPDATE_CHECK"
)

var (
	latestRelease = release.Latest
	installShape  = release.CurrentShape
	currentBuild  = buildinfo.Resolve
)

func updateCheckAllowed() bool { return os.Getenv(noCheckEnv) == "" }

func NewCmdUpdate(a *app) *cobra.Command {
	return cli.NoStore(&cobra.Command{
		Use:   "update",
		Short: "Check for a newer hoard and show how to upgrade",

		Long: "Checks whether a newer hoard has been released, and\n" +
			"prints the command that upgrades this build.\n\n" +
			"hoard never replaces its own binary. The upgrade\n" +
			"runs through the installer you already use, which\n" +
			"verifies what it downloads.",
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return runUpdate(c.Context(), a)
		},
	})
}

func runUpdate(ctx context.Context, a *app) error {
	w, env := a.env.Out, a.env.OutEnv
	dim, bold, accent := env.Dim(), env.Bold(), env.Accent()

	shape := installShape()
	current := currentBuild()

	if shape == release.ShapeDev {
		fmt.Fprintf(w, "hoard %s is a dev build, built from source.\n", bold(current))
		fmt.Fprintln(w, dim("There is nothing to upgrade to; you are ahead of the release."))
		return nil
	}

	if !updateCheckAllowed() {
		fmt.Fprintf(w, "hoard %s\n", bold(current))
		fmt.Fprintln(w, dim("Update checks are off ("+noCheckEnv+" is set)."))
		return nil
	}

	latest, err := latestRelease(ctx, updateCheckTimeout)
	if err != nil {
		return fmt.Errorf("checking for a newer hoard: %w", err)
	}

	st := release.Status{Current: current, Latest: latest, Shape: shape}
	if !st.Available() {
		fmt.Fprintf(w, "hoard %s is up to date.\n", bold(current))
		return nil
	}

	fmt.Fprintf(w, "hoard %s → %s\n\n", current, accent(latest))
	fmt.Fprintln(w, "Upgrade with:")
	fmt.Fprintln(w, indent(release.Advice(shape, latest)))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Release notes:")
	fmt.Fprintln(w, indent(release.ReleaseURL(latest)))
	return nil
}

func indent(s string) string { return "  " + s }
