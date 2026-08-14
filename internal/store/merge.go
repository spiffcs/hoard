package store

import (
	"fmt"
)

type MergePlan struct {
	Printings []SourcePrinting

	NewBinders []string
	Adds       []CardAdd
	Decks      []DeckMerge
	Watches    []WatchInput
}

type DeckMerge struct {
	Meta    DeckMeta
	Entries []Entry
}

type MergeResult struct {
	Printings      int
	Copies         int
	CreatedBinders []string
	Decks          int
	DeckCards      int
	Watches        int
}

func (s *Store) ApplyMerge(receipt *ImportReceipt, p MergePlan) (MergeResult, error) {
	res := MergeResult{Printings: len(p.Printings)}

	type binderPlan struct{ name, sourceID string }
	plans := make([]binderPlan, 0, len(p.NewBinders))
	for _, name := range p.NewBinders {
		trimmed, sid, err := s.validateNewBinderName(name)
		if err != nil {
			return res, err
		}
		plans = append(plans, binderPlan{trimmed, sid})
	}
	for _, a := range p.Adds {
		if err := validFinish(a.Finish); err != nil {
			return res, err
		}
	}
	for _, d := range p.Decks {
		for _, e := range d.Entries {
			if err := validFinish(e.Finish); err != nil {
				return res, err
			}
		}
	}
	for i := range p.Watches {
		p.Watches[i].normalize()
		if err := validateWatch(p.Watches[i]); err != nil {
			return res, err
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback()

	if err := mergePrintingsTx(tx, p.Printings); err != nil {
		return res, err
	}

	created := make(map[string]int64, len(plans))
	for _, bp := range plans {
		out, err := tx.Exec(insertBinderSQL, KindCollection, bp.name, bp.sourceID, now(), now())
		if err != nil {
			return res, fmt.Errorf("creating binder %q: %w", bp.name, err)
		}
		id, err := out.LastInsertId()
		if err != nil {
			return res, err
		}
		created[bp.name] = id
		res.CreatedBinders = append(res.CreatedBinders, bp.name)
	}

	stmt, err := tx.Prepare(entryAccumulateSQL)
	if err != nil {
		return res, err
	}
	defer stmt.Close()
	for _, a := range p.Adds {
		cid := a.ContainerID
		if cid == 0 {
			var ok bool
			if cid, ok = created[a.Binder]; !ok {
				return res, fmt.Errorf("add for %q names binder %q, which this merge does not create",
					a.Card.Name, a.Binder)
			}
		}

		if _, err := stmt.Exec(cid, a.Card.ID, a.Finish, orUnknown(a.Condition),
			"main", a.Quantity); err != nil {
			return res, fmt.Errorf("merging %s: %w", a.Card.Name, err)
		}
		res.Copies += a.Quantity
	}

	for _, d := range p.Decks {
		if _, err := upsertDeckTx(tx, d.Meta, d.Entries); err != nil {
			return res, err
		}
		res.Decks++
		for _, e := range d.Entries {
			res.DeckCards += e.Quantity
		}
	}

	for _, w := range p.Watches {

		if _, err := tx.Exec(watchUpsertSQL, w.ScryfallID, w.Display, w.Finish, w.Op,
			w.Threshold, w.Pct, w.MinMove, w.WindowDays, now()); err != nil {
			return res, fmt.Errorf("merging watch on %s: %w", w.Display, err)
		}
		res.Watches++
	}

	if receipt != nil {
		if _, err := tx.Exec(`
INSERT INTO import_ledger (hash, file, cards, imported_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(hash) DO UPDATE SET file=excluded.file, cards=excluded.cards,
    imported_at=excluded.imported_at`,
			receipt.Hash, receipt.File, receipt.Cards, now()); err != nil {
			return res, fmt.Errorf("recording merge receipt: %w", err)
		}
	}
	return res, tx.Commit()
}
