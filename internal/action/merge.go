package action

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/spiffcs/hoard/internal/finish"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

type MergeOptions struct {
	Source string

	Target string

	ReplaceDecks bool

	ReplaceWatches bool
	DryRun         bool
	Again          bool
}

type MergeResult struct {
	Source string

	SourceVersion int

	Upgraded   bool
	BackupPath string

	Document []byte

	Printings int
	Copies    int
	PerBinder map[string]int
	Created   []string

	Decks     int
	DeckCards int

	SkippedDecks []string

	Watches int

	SkippedWatches []string

	FoldedInto string
}

func MergeHoard(d Deps, p progress.Fn, o MergeOptions) (MergeResult, error) {
	res := MergeResult{Source: o.Source, PerBinder: make(map[string]int)}

	if err := refuseSameFile(o.Source, o.Target); err != nil {
		return res, err
	}

	p.Emit(progress.Event{Step: "reading " + filepath.Base(o.Source)})
	if err := ensureSourceCurrent(d, o.Source, &res); err != nil {
		return res, err
	}

	src, err := store.OpenSource(o.Source)
	if err != nil {
		return res, err
	}
	defer src.Close()

	snap, err := src.Snapshot()
	if err != nil {
		return res, err
	}
	res.SourceVersion = snap.Version
	rows, err := Deps{Store: src}.ExportRows("", "")
	if err != nil {
		return res, err
	}

	var buf bytes.Buffer
	if err := hoardjson.Write(&buf, hoardjson.FromSnapshot(snap, rows)); err != nil {
		return res, err
	}
	res.Document = buf.Bytes()

	hash := ContentHash(res.Document)
	if !o.DryRun {
		if err := RefuseReimport(d.Store, hash, o.Again); err != nil {
			return res, err
		}
	}

	h, err := hoardjson.ReadHoard(bytes.NewReader(res.Document))
	if err != nil {
		return res, err
	}

	plan, err := planMerge(d.Store, h, o, &res)
	if err != nil {
		return res, err
	}
	if o.DryRun {
		return res, nil
	}

	p.Emit(progress.Event{Step: "merging"})
	receipt := &store.ImportReceipt{Hash: hash, File: o.Source, Cards: res.Copies + res.DeckCards}
	if _, err := d.Store.ApplyMerge(receipt, plan); err != nil {
		return res, err
	}
	return res, nil
}

func refuseSameFile(source, target string) error {
	si, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("reading database %q: %w", source, err)
	}
	ti, err := os.Stat(target)
	if err != nil {

		return nil //nolint:nilerr // absence here means "not the same file"
	}
	if os.SameFile(si, ti) {
		return fmt.Errorf("%s is the database you are merging into; merging a hoard into itself would double every quantity", source)
	}
	return nil
}

func ensureSourceCurrent(d Deps, path string, res *MergeResult) error {
	v, err := store.FileVersion(path)
	if err != nil {
		return err
	}
	res.SourceVersion = v
	want := store.SchemaVersion()
	if v == want {
		return nil
	}
	if v > want {
		return fmt.Errorf(
			"%s is schema v%d but this hoard understands v%d. Upgrade hoard; an older build must not write there",
			path, v, want)
	}

	backup := fmt.Sprintf("%s.premerge-v%d", path, v)
	if !d.confirm(fmt.Sprintf(
		"%s is schema v%d; this hoard is v%d.\n"+
			"It must be upgraded before it can be merged. A copy will be kept at\n"+
			"  %s\n"+
			"and restored if the upgrade fails.\nUpgrade %s now?",
		path, v, want, backup, filepath.Base(path))) {
		return fmt.Errorf("%s is schema v%d and was not upgraded; nothing was merged", path, v)
	}

	if err := copyFile(path, backup); err != nil {
		return fmt.Errorf("backing up %s before upgrading it: %w", path, err)
	}
	upgraded, err := store.Open(path)
	if err != nil {

		if rerr := copyFile(backup, path); rerr != nil {
			return fmt.Errorf(
				"upgrading %s failed: %w\nrestoring it from %s ALSO failed: %v\nthe backup still holds your data",
				path, err, backup, rerr)
		}
		return fmt.Errorf(
			"upgrading %s failed: %w\nit has been restored from %s and your data is intact; nothing was merged",
			path, err, backup)
	}
	if err := upgraded.Close(); err != nil {
		return err
	}
	res.Upgraded, res.BackupPath, res.SourceVersion = true, backup, want
	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dst)
}

func planMerge(st *store.Store, h *hoardjson.Hoard, o MergeOptions, res *MergeResult) (store.MergePlan, error) {
	var plan store.MergePlan

	cards := make(map[string]scryfall.Card, len(h.Printings))
	plan.Printings = make([]store.SourcePrinting, 0, len(h.Printings))
	for _, p := range h.Printings {
		c := scryfall.Card{
			ID: p.ScryfallID, Name: p.Name, Set: p.SetCode,
			CollectorNumber: p.Number, ScryfallURL: p.ScryfallURL,
			PriceUSD: p.PriceUsd, PriceUSDFoil: p.PriceUsdFoil,
			PriceUSDEtched: p.PriceUsdEtched, Raw: compactJSON(p.Raw),
		}
		cards[p.ScryfallID] = c
		plan.Printings = append(plan.Printings, store.SourcePrinting{
			Card: c, MTGJSONUUID: p.MTGJSONUUID, UpdatedAt: p.UpdatedAt,
		})
	}
	res.Printings = len(plan.Printings)

	binderTargets, err := binderDestinations(st, res)
	if err != nil {
		return plan, err
	}
	deckMeta, err := deckDestinations(h)
	if err != nil {
		return plan, err
	}

	newBinders := make(map[string]string)
	for _, c := range h.Containers {
		if c.Kind != "binder" {
			continue
		}
		if _, ok := binderTargets[strings.ToLower(c.Name)]; ok {
			continue
		}
		if _, seen := newBinders[strings.ToLower(c.Name)]; !seen {
			newBinders[strings.ToLower(c.Name)] = strings.TrimSpace(c.Name)
		}
	}

	deckEntries := make(map[string][]store.Entry)
	for _, row := range h.Holdings.Rows {
		card, ok := cards[row.Card.ScryfallID]
		if !ok {
			return plan, fmt.Errorf("the document holds %s (%s) but no catalog entry for it; the source database is inconsistent",
				row.Card.Name, row.Card.ScryfallID)
		}
		rowFinish, err := finish.Parse(row.Card.Finish)
		if err != nil {
			return plan, fmt.Errorf("the document holds %s (%s): %w",
				row.Card.Name, row.Card.ScryfallID, err)
		}
		if row.ContainerKind == "deck" {
			deckEntries[row.Container] = append(deckEntries[row.Container], store.Entry{
				ScryfallID: row.Card.ScryfallID, Finish: rowFinish,
				Condition: row.Card.Condition, Board: row.Board, Quantity: row.Count,
			})
			continue
		}
		add := store.CardAdd{
			Card: card, Finish: rowFinish,
			Condition: row.Card.Condition, Quantity: row.Count,
		}
		if id, ok := binderTargets[strings.ToLower(row.Container)]; ok {
			add.ContainerID = id.id
			add.Binder = id.name
		} else {
			add.Binder = newBinders[strings.ToLower(row.Container)]
			if add.Binder == "" {

				add.Binder = strings.TrimSpace(row.Container)
				newBinders[strings.ToLower(row.Container)] = add.Binder
			}
		}
		plan.Adds = append(plan.Adds, add)
		res.Copies += row.Count
		res.PerBinder[add.Binder] += row.Count
	}

	plan.NewBinders = sortedValues(newBinders)

	res.Created = plan.NewBinders

	for _, name := range sortedKeys(deckEntries) {
		meta, ok := deckMeta[name]
		if !ok {
			return plan, fmt.Errorf("the document holds cards in deck %q but declares no such deck", name)
		}
		if _, _, exists, err := st.DeckBySource(meta.Source, meta.SourceID); err != nil {
			return plan, err
		} else if exists && !o.ReplaceDecks {
			res.SkippedDecks = append(res.SkippedDecks, name)
			continue
		}
		plan.Decks = append(plan.Decks, store.DeckMerge{Meta: meta, Entries: deckEntries[name]})
		res.Decks++
		for _, e := range deckEntries[name] {
			res.DeckCards += e.Quantity
		}
	}

	standing, err := st.ListWatches()
	if err != nil {
		return plan, err
	}
	held := make(map[string]bool, len(standing))
	for _, w := range standing {
		held[watchKey(w.ScryfallID, w.Finish, w.Op)] = true
	}
	for _, w := range h.Watches {
		watchFinish, err := finish.Parse(w.Card.Finish)
		if err != nil {
			return plan, fmt.Errorf("watch %s: %w", w.Display, err)
		}
		if held[watchKey(w.Card.ScryfallID, watchFinish, w.Op)] && !o.ReplaceWatches {
			res.SkippedWatches = append(res.SkippedWatches, w.Display)
			continue
		}
		if _, ok := cards[w.Card.ScryfallID]; !ok {

			continue
		}
		plan.Watches = append(plan.Watches, store.WatchInput{
			ScryfallID: w.Card.ScryfallID, Display: w.Display,
			Finish: watchFinish, Op: w.Op, Threshold: w.Threshold,
		})
		res.Watches++
	}
	return plan, nil
}

type binderDest struct {
	id   int64
	name string
}

func binderDestinations(st *store.Store, res *MergeResult) (map[string]binderDest, error) {
	binders, err := st.ListBinders()
	if err != nil {
		return nil, err
	}
	if len(binders) == 0 {
		return nil, fmt.Errorf("no default binder exists to merge into; the database is missing its collection container")
	}
	out := make(map[string]binderDest, len(binders)+len(store.ReservedBinderNames))
	for _, b := range binders {
		out[strings.ToLower(b.Name)] = binderDest{b.ID, b.Name}
	}

	for _, alias := range store.ReservedBinderNames {
		if _, taken := out[strings.ToLower(alias)]; !taken {
			out[strings.ToLower(alias)] = binderDest{binders[0].ID, binders[0].Name}
			res.FoldedInto = binders[0].Name
		}
	}
	return out, nil
}

func deckDestinations(h *hoardjson.Hoard) (map[string]store.DeckMeta, error) {
	out := make(map[string]store.DeckMeta)
	for _, c := range h.Containers {
		if c.Kind != "deck" {
			continue
		}
		if _, dup := out[c.Name]; dup {
			return nil, fmt.Errorf(
				"the source hoard has two decks named %q; rename one there before merging, as their cards cannot be told apart",
				c.Name)
		}
		out[c.Name] = store.DeckMeta{
			Name: c.Name, Source: c.Source, SourceID: c.SourceID,
			SourceURL: c.SourceURL, Format: c.Format,
		}
	}
	return out, nil
}

func compactJSON(raw []byte) []byte {
	if len(raw) == 0 {
		return nil
	}
	var out bytes.Buffer
	if err := json.Compact(&out, raw); err != nil {
		return raw
	}
	return out.Bytes()
}

func watchKey(id string, fin finish.Finish, op string) string {
	return id + "\x00" + fin.String() + "\x00" + op
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, k := range sortedKeys(m) {
		out = append(out, m[k])
	}
	return out
}
