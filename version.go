package main

import (
	"fmt"
	"io"

	"github.com/spiffcs/hoard/internal/buildinfo"
	"github.com/spiffcs/hoard/internal/ui"
)

// printVersion answers `hoard version`: the build's identity, then the legal
// notices the Fan Content Policy and Scryfall's guidelines require the product
// to carry (docs/data-licensing.md §7).
func printVersion(w io.Writer, env ui.Env) {
	dim := env.Dim()
	fmt.Fprintf(w, "hoard %s\n\n", buildinfo.Resolve())
	fmt.Fprintln(w, dim(buildinfo.FanContentNotice))
	fmt.Fprintln(w, dim(buildinfo.DataCredit))
}
