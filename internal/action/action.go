package action

import (
	"github.com/spiffcs/hoard/internal/catalog"
	"github.com/spiffcs/hoard/internal/pricing"
	"github.com/spiffcs/hoard/internal/resolve"
	"github.com/spiffcs/hoard/internal/store"
)

type Deps struct {
	Store *store.Store

	Catalog *catalog.Catalog

	CacheDir string

	Confirm func(question string) bool

	Resolver *resolve.Resolver

	PriceBaseURL string

	TCGCSVBaseURL string
}

func (d Deps) pricer() *pricing.Fetcher {
	return pricing.New(d.Store, d.CacheDir).
		WithBaseURL(d.PriceBaseURL).
		WithTCGCSVBaseURL(d.TCGCSVBaseURL)
}

func (d Deps) confirm(question string) bool {
	if d.Confirm == nil {
		return false
	}
	return d.Confirm(question)
}
