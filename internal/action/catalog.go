package action

import (
	"context"
	"fmt"
	"time"

	"github.com/spiffcs/hoard/internal/catalog"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/ui"
)

// CatalogStatus reports the local catalog's state, consulting Scryfall's
// bundle listing at most once per check interval.
func CatalogStatus(ctx context.Context, d Deps) (catalog.Status, error) {
	if d.Catalog == nil {
		return catalog.Status{}, fmt.Errorf("no writable cache directory for the catalog")
	}
	return d.Catalog.CheckStatus(ctx), nil
}

// CatalogUpdateResult is what a rebuild produced.
type CatalogUpdateResult struct {
	Cards int
	Bytes int64
	Took  time.Duration
}

// CatalogUpdate rebuilds the local catalog from Scryfall's current bundle,
// reporting byte progress against the known download size.
func CatalogUpdate(ctx context.Context, d Deps, p progress.Fn) (CatalogUpdateResult, error) {
	if d.Catalog == nil {
		return CatalogUpdateResult{}, fmt.Errorf("no writable cache directory for the catalog")
	}
	start := time.Now()
	if err := d.Catalog.Update(ctx, p); err != nil {
		return CatalogUpdateResult{}, err
	}
	return CatalogUpdateResult{
		Cards: d.Catalog.CardCount(),
		Bytes: d.Catalog.Bytes(),
		Took:  time.Since(start).Round(time.Second),
	}, nil
}

// EnsureCatalog offers to build or refresh the catalog and reports whether
// its prices can be trusted afterwards. It asks before downloading: 77 MB
// starting because somebody typed a command is a surprise on a metered link,
// and Deps.Confirm's nil-declines default keeps scheduled runs safe.
//
// The return value matters because a declined confirm is silent success
// otherwise: a scheduled refresh would serve prices as old as the catalog
// while reporting success, forever. Only prices go stale this way — identity
// and finishes do not — so repair-finishes and the add cascade use an
// out-of-date catalog happily.
//
// A catalog that will not build is not a reason to abandon the caller's
// command; the failure is narrated and everything falls through to the API.
func EnsureCatalog(ctx context.Context, d Deps, p progress.Fn) (pricesUsable bool) {
	cat := d.Catalog
	if cat == nil {
		return false
	}
	st := cat.CheckStatus(ctx)
	switch {
	case st.Empty():
		if !d.confirm(fmt.Sprintf(
			"No local card catalog yet. Download it now (%s)?", downloadSize(ctx, cat))) {
			p.Emit(progress.Event{
				Note: "using the Scryfall API; run 'hoard catalog update' to make this fast."})
			return false
		}
	case st.Checked && st.Stale:
		if !d.confirm(fmt.Sprintf(
			"A newer card catalog is available (yours is from %s). Update it (%s)?",
			st.SourceUpdated.Local().Format("2 Jan"), downloadSize(ctx, cat))) {
			p.Emit(progress.Event{
				Note: "catalog prices would be out of date, so using the Scryfall API instead."})
			return false
		}
	default:
		// Either current, or the freshness check was skipped as recent
		// enough.
		return !st.Empty()
	}
	if _, err := CatalogUpdate(ctx, d, p); err != nil {
		p.Emit(progress.Event{
			Note: fmt.Sprintf("catalog update failed, using the Scryfall API: %v", err)})
		return false
	}
	return true
}

// downloadSize describes the transfer a rebuild would cost, or "unknown
// size" when the listing cannot be read — a download prompt must say what it
// is about to spend.
func downloadSize(ctx context.Context, cat *catalog.Catalog) string {
	if n := cat.DownloadSize(ctx); n > 0 {
		return ui.Bytes(n)
	}
	return "unknown size"
}
