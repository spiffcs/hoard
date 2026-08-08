package main

import (
	"fmt"
	"io"
	"runtime"

	"github.com/spiffcs/hoard/internal/buildinfo"
	"github.com/spiffcs/hoard/internal/ui"
)

// printVersion answers `hoard version`: the build's identity, then the legal
// notices the Fan Content Policy and Scryfall's guidelines require the product
// to carry (docs/data-licensing.md §7). Commit and date read "unknown" on
// source builds — Resolve already folds the VCS revision into the version
// line, so nothing is lost there.
func printVersion(w io.Writer, env ui.Env) {
	dim := env.Dim()
	fmt.Fprintf(w, "hoard %s\n", buildinfo.Resolve())
	fmt.Fprintf(w, "  commit:   %s\n", buildinfo.GitCommit)
	fmt.Fprintf(w, "  built:    %s\n", buildinfo.BuildDate)
	fmt.Fprintf(w, "  go:       %s %s/%s\n\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	fmt.Fprintln(w, dim(buildinfo.FanContentNotice))
	fmt.Fprintln(w, dim(buildinfo.DataCredit))
}
