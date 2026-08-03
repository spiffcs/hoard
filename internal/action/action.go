// Package action holds hoard's capabilities as plain functions: one per
// operation, orchestrating the domain packages, reporting progress through
// internal/progress, and returning a result struct the caller renders.
//
// This is the parity layer. The CLI's cmd* functions and the browse TUI are
// both thin frontends over these calls — a capability that exists only in
// one of them is a bug tracked in docs/parity.md, not an architecture. The
// package sits above every domain package and nothing imports it except
// frontends, so no import cycle is possible; browse in particular does NOT
// import it (main injects closures, keeping browse's zero-network rule).
package action

import (
	"github.com/spiffcs/hoard/internal/catalog"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/resolve"
	"github.com/spiffcs/hoard/internal/store"
)

// Deps is everything an operation needs that the frontend owns. Built once
// per command invocation; the progress callback is a per-call parameter
// instead, because a UI constructs a fresh bridge per operation.
type Deps struct {
	Store *store.Store

	// Catalog is the local card catalog, nil when unavailable — a supported
	// state, not an error: operations fall through to the Scryfall API.
	Catalog *catalog.Catalog

	// CacheDir is where MTGJSON bundles cache (pricing.DefaultCacheDir()).
	CacheDir string

	// Confirm asks a yes/no question before spending bandwidth. Nil
	// declines, which is also what a non-interactive stdin does — the safe
	// default is "don't download".
	Confirm func(question string) bool

	// Resolver is the shared card-resolution pipeline; its Fetch field
	// remains the test seam it has always been.
	Resolver *resolve.Resolver

	// PriceBaseURL overrides the MTGJSON file root the pricing reads go
	// through — the action tests' seam, empty in the real program.
	PriceBaseURL string
}

// pricer builds the pricing fetcher every price-touching action reads
// through, honoring the test seam.
func (d Deps) pricer() *pricing.Fetcher {
	return pricing.New(d.Store, d.CacheDir).WithBaseURL(d.PriceBaseURL)
}

// confirm resolves the Confirm dependency with its nil-declines default.
func (d Deps) confirm(question string) bool {
	if d.Confirm == nil {
		return false
	}
	return d.Confirm(question)
}
