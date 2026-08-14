package action

import (
	"bytes"
	"context"
	"fmt"
	"github.com/spiffcs/hoard/internal/finish"
	"strings"

	"github.com/spiffcs/hoard/internal/collsource"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/resolve"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

var ErrPartial = fmt.Errorf("some items were skipped")

func (d Deps) resolver(p progress.Fn) *resolve.Resolver {
	if d.Resolver != nil && d.Resolver.Fetch != nil {
		return d.Resolver
	}
	var done, total int
	return &resolve.Resolver{
		Fetch: func(ctx context.Context, ids []scryfall.Identifier) ([]scryfall.Card, []scryfall.Identifier, error) {
			total += len(ids)
			start := done
			found, missing, err := scryfall.FetchCollectionProgress(ctx, ids,
				func(chunkDone, _ int, note string) {
					p.Emit(progress.Event{Step: "resolving cards",
						Done: int64(start + chunkDone), Total: int64(total),
						Unit: progress.UnitCards, Note: note})
				})
			done += len(ids)
			p.Emit(progress.Event{Step: "resolving cards",
				Done: int64(done), Total: int64(total), Unit: progress.UnitCards})
			return found, missing, err
		},
	}
}

type ImportOptions struct {
	Data []byte

	Display   string
	Format    string
	BinderRef string
	Preserve  bool
	DryRun    bool
	Again     bool
}

type ImportResult struct {
	Format          string
	Copies          int
	Resolved        int
	PerBinder       map[string]int
	Created         []string
	SkippedDeckRows int
	Refinished      int
	Dropped         map[string]int
	Unresolved      []string
	Gaps            GapReport
}

func missingBinderAdvice(err error, ref string) error {
	if err == nil || err.Error() != fmt.Sprintf("no binder matching %q", ref) {
		return err
	}
	return fmt.Errorf("%w. Create it with 'hoard binder new %q', or see 'hoard binder list'", err, ref)
}

func ImportCollection(ctx context.Context, d Deps, p progress.Fn, o ImportOptions) (ImportResult, error) {
	var res ImportResult
	hash := ContentHash(o.Data)
	if !o.DryRun {
		if err := RefuseReimport(d.Store, hash, o.Again); err != nil {
			return res, err
		}
	}

	coll, err := collsource.Parse(bytes.NewReader(o.Data), o.Format)
	if err != nil {
		return res, fmt.Errorf("%s: %w", o.Display, err)
	}
	res.Format = coll.Format
	res.Dropped = coll.Dropped

	keptRows := coll.Rows[:0]
	for _, r := range coll.Rows {
		if r.Kind == "deck" {
			res.SkippedDeckRows++
			continue
		}
		keptRows = append(keptRows, r)
	}
	coll.Rows = keptRows

	binders, err := d.Store.ListBinders()
	if err != nil {
		return res, err
	}

	if len(binders) == 0 {
		return res, fmt.Errorf("no default binder exists to import into; the database is missing its collection container")
	}
	targetID, targetName := binders[0].ID, binders[0].Name
	if o.BinderRef != "" {
		b, err := d.Store.BinderByRef(o.BinderRef)
		if err != nil {
			return res, missingBinderAdvice(err, o.BinderRef)
		}
		targetID, targetName = b.ID, b.Name
	}
	binderIDs := make(map[string]int64, len(binders))
	binderNames := make(map[int64]string, len(binders))
	for _, b := range binders {
		binderIDs[strings.ToLower(b.Name)] = b.ID
		binderNames[b.ID] = b.Name
	}

	for _, alias := range store.ReservedBinderNames {
		if _, taken := binderIDs[strings.ToLower(alias)]; !taken {
			binderIDs[strings.ToLower(alias)] = binders[0].ID
		}
	}

	rr, err := d.resolver(p).Resolve(ctx, resolve.Requests(coll.Rows))
	if err != nil {
		return res, err
	}
	res.Refinished, res.Unresolved = rr.Refinished, rr.Unresolved

	type addition struct {
		binder string
		card   scryfall.Card
		finish finish.Finish

		condition string
		qty       int
	}
	var adds []addition
	for i, r := range coll.Rows {
		m := rr.Matches[i]
		if !m.OK {
			continue
		}
		binder := ""
		if o.Preserve {
			binder = r.Binder
		}
		adds = append(adds, addition{binder: binder, card: m.Card, finish: m.Finish,
			condition: r.Condition, qty: r.Quantity})
	}
	res.Resolved = len(adds)

	spelling := make(map[string]string)
	res.PerBinder = make(map[string]int)
	cardAdds := make([]store.CardAdd, 0, len(adds))
	for _, a := range adds {
		dest, name := targetID, targetName
		newBinder := ""
		if a.binder != "" {
			key := strings.ToLower(a.binder)
			if id, ok := binderIDs[key]; ok {

				dest, name = id, binderNames[id]
			} else {
				canonical, seen := spelling[key]
				if !seen {
					canonical = strings.TrimSpace(a.binder)
					spelling[key] = canonical
					res.Created = append(res.Created, canonical)
				}
				dest, name, newBinder = 0, canonical, canonical
			}
		}
		cardAdds = append(cardAdds, store.CardAdd{
			ContainerID: dest, Binder: newBinder,
			Card: a.card, Finish: a.finish, Condition: a.condition, Quantity: a.qty,
		})
		res.Copies += a.qty
		res.PerBinder[name] += a.qty
	}
	if !o.DryRun && len(cardAdds) > 0 {
		receipt := &store.ImportReceipt{Hash: hash, File: o.Display, Cards: res.Copies}
		if _, err := d.Store.ApplyImport(receipt, res.Created, cardAdds); err != nil {
			return res, err
		}
	}
	if o.DryRun {
		if n := len(res.Unresolved); n > 0 {
			return res, fmt.Errorf("%d rows would not resolve: %w", n, ErrPartial)
		}
		return res, nil
	}

	if res.Gaps, err = FillGaps(ctx, d, p); err != nil {
		return res, err
	}
	if n := len(res.Unresolved); n > 0 {
		return res, fmt.Errorf("%d rows were skipped: %w", n, ErrPartial)
	}
	return res, nil
}
