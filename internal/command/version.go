package command

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/spiffcs/hoard/internal/buildinfo"
	"github.com/spiffcs/hoard/internal/cli"
	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/release"
	"github.com/spiffcs/hoard/internal/ui"
)

func printVersion(w io.Writer, env ui.Env, st release.Status) {
	dim, bold := env.Dim(), env.Bold()

	version := st.Current
	if version == "" {
		version = buildinfo.Resolve()
	}

	fmt.Fprintln(w, bold("hoard "+version))
	fmt.Fprintf(w, "  %s   %s\n", dim("commit:"), buildinfo.GitCommit)
	fmt.Fprintf(w, "  %s    %s\n", dim("built:"), buildinfo.BuildDate)
	fmt.Fprintf(w, "  %s       %s %s/%s\n",
		dim("go:"), runtime.Version(), runtime.GOOS, runtime.GOARCH)
	if line := updateLine(env, st); line != "" {
		fmt.Fprintf(w, "  %s   %s\n", dim("update:"), line)
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, dim(buildinfo.FanContentNotice))
	fmt.Fprintln(w, dim(buildinfo.DataCredit))
}

func updateLine(env ui.Env, st release.Status) string {
	if st.Shape == release.ShapeDev || st.Latest == "" {
		return ""
	}
	if !st.Available() {
		return env.Dim()("up to date")
	}
	return env.Accent()(st.Latest) + env.Dim()(" available · run 'hoard update'")
}

func versionDocument(st release.Status) hoardjson.Document {
	version := st.Current
	if version == "" {
		version = buildinfo.Resolve()
	}
	doc := hoardjson.Document{
		SchemaVersion: hoardjson.SchemaVersion,
		Kind:          hoardjson.KindVersion,
		Version: &hoardjson.Version{
			Version:  version,
			Commit:   buildinfo.GitCommit,
			Built:    buildinfo.BuildDate,
			Go:       runtime.Version(),
			Platform: runtime.GOOS + "/" + runtime.GOARCH,
		},
	}
	if st.Available() {
		doc.Version.Update = &hoardjson.Update{
			Latest: st.Latest,
			URL:    release.ReleaseURL(st.Latest),
		}
	}
	return doc
}

func NewCmdVersion(a *app) *cobra.Command {
	return cli.NoStore(cli.JSONCapable(&cobra.Command{
		Use:   "version",
		Short: "This build's version, and the legal notices",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			st := cachedStatus(c.Context())
			if a.env.JSON {
				return hoardjson.Write(a.env.Out, versionDocument(st))
			}
			printVersion(a.env.Out, a.env.OutEnv, st)
			return nil
		},
	}))
}

// cachedStatus answers from the cache when it is fresh and otherwise refreshes
// it, with a short timeout: neither version nor update may hang on a network
// problem, and a failed check simply means no update line.
func cachedStatus(ctx context.Context) release.Status {
	st := release.Status{Current: currentBuild(), Shape: installShape()}
	if !updateCheckAllowed() {
		return st
	}
	dir := release.DefaultCacheDir()
	if c, err := release.LoadCache(dir); err == nil && c.Fresh(time.Now()) {
		st.Latest = c.LatestSeen
		return st
	}
	latest, err := latestRelease(ctx, versionCheckTimeout)
	if err != nil {
		return st
	}
	st.Latest = latest
	_ = release.SaveCache(dir, release.Cache{LastChecked: time.Now(), LatestSeen: latest})
	return st
}
