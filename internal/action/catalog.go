package action

import (
	"context"
	"fmt"
	"time"

	"github.com/spiffcs/hoard/internal/catalog"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/ui"
)

func CatalogStatus(ctx context.Context, d Deps) (catalog.Status, error) {
	if d.Catalog == nil {
		return catalog.Status{}, fmt.Errorf("no writable cache directory for the catalog")
	}
	return d.Catalog.CheckStatus(ctx), nil
}

type CatalogUpdateResult struct {
	Cards int
	Bytes int64
	Took  time.Duration
}

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

		return !st.Empty()
	}
	if _, err := CatalogUpdate(ctx, d, p); err != nil {
		p.Emit(progress.Event{
			Note: fmt.Sprintf("catalog update failed, using the Scryfall API: %v", err)})
		return false
	}
	return true
}

func downloadSize(ctx context.Context, cat *catalog.Catalog) string {
	if n := cat.DownloadSize(ctx); n > 0 {
		return ui.Bytes(n)
	}
	return "unknown size"
}
