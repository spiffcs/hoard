package action

// Merging another hoard's database into this one.
//
// The route is deliberately not a SQLite splice. The candidate is read out as
// the interchange document, the document is read back, and the plan is built
// from what was read — so the data model is the contract between two hoards,
// and a field the model cannot carry fails visibly here rather than silently
// on the far side. It also means the format is the same one `-o` hands the
// user, rather than a private path nothing else exercises.
//
// Nothing in here touches the network. Import re-resolves every row through
// Scryfall because it is reading a foreign CSV that carries names and set
// codes; a merge is reading a hoard, which already holds the full catalog for
// everything in it.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spiffcs/hoard/internal/hoardjson"
	"github.com/spiffcs/hoard/internal/progress"
	"github.com/spiffcs/hoard/internal/scryfall"
	"github.com/spiffcs/hoard/internal/store"
)

// MergeOptions is one merge as requested.
type MergeOptions struct {
	// Source is the database to merge in.
	Source string
	// Target is the database being merged into, for the same-file guard.
	Target string
	// ReplaceDecks overwrites a deck the target already has from the same
	// origin, rather than leaving the target's copy alone.
	ReplaceDecks bool
	// ReplaceWatches overwrites a standing watch's threshold instead of
	// keeping the target's.
	ReplaceWatches bool
	DryRun         bool
	Again          bool
}

// MergeResult is everything the merge did and declined to do.
type MergeResult struct {
	Source string
	// SourceVersion is the schema the candidate was read at, after any upgrade.
	SourceVersion int
	// Upgraded records that the candidate was migrated first, and where its
	// backup went.
	Upgraded   bool
	BackupPath string

	// Document is the interchange document the merge was planned from, kept so
	// the frontend can write it out.
	Document []byte

	Printings int
	Copies    int
	PerBinder map[string]int
	Created   []string

	Decks     int
	DeckCards int
	// SkippedDecks are decks the target already has from the same origin,
	// left untouched.
	SkippedDecks []string

	Watches int
	// SkippedWatches are watches the target already stands on the same
	// printing, finish and direction.
	SkippedWatches []string

	// FoldedInto names the target binder that the candidate's default binder
	// merged into, when the fold happened. It is the one surprise in the
	// binder rules worth stating outright.
	FoldedInto string
}

// MergeHoard folds another hoard's database into this one: holdings, decks and
// watches, in one transaction.
//
// It takes no context because it needs none: there is no network call and no
// long poll to cancel, only local reads and a single transaction. Interrupting
// it leaves the transaction unfinished, which is the same nothing an
// interrupted import leaves.
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

	// The ledger's identity is the document's bytes, so re-merging a candidate
	// that has not changed is refused. It catches the common repeat and not
	// the dangerous one: edit the candidate at all and the hash moves, so a
	// second merge adds its copies again. --dry-run before a re-merge is the
	// only real check.
	hash := ContentHash(res.Document)
	if !o.DryRun {
		if err := RefuseReimport(d.Store, hash, o.Again); err != nil {
			return res, err
		}
	}

	// Read the document back and plan from that. The round trip costs
	// microseconds and makes "the model carries everything a merge needs" a
	// fact this path proves on every run instead of an assumption.
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

// refuseSameFile stops a hoard being merged into itself, which would double
// every quantity in it. os.SameFile rather than path equality: the two names
// can differ and still be one file.
func refuseSameFile(source, target string) error {
	si, err := os.Stat(source)
	if err != nil {
		return fmt.Errorf("reading database %q: %w", source, err)
	}
	ti, err := os.Stat(target)
	if err != nil {
		// A target that cannot be stat'd is not this function's problem; the
		// store is already open on it.
		return nil //nolint:nilerr // absence here means "not the same file"
	}
	if os.SameFile(si, ti) {
		return fmt.Errorf("%s is the database you are merging into; merging a hoard into itself would double every quantity", source)
	}
	return nil
}

// ensureSourceCurrent brings the candidate up to this hoard's schema, asking
// first.
//
// Reading a database at an older schema means migrating it, and migrating is a
// write to a file the user pointed at rather than opened. So it is asked for,
// backed up, and rolled back on failure: a merge that damages the database it
// was only supposed to read is the worst outcome this command has.
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
		// Put the candidate back exactly as it was. The user pointed us at
		// this file to read it; they must not lose it because we could not.
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

// copyFile writes src over dst through a temporary file in dst's directory, so
// an interrupted copy cannot leave a half-written database at the destination.
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

// planMerge decides every write, resolving each conflict against what the
// target already holds. It reads the target but writes nothing, so a dry run
// is this function and nothing else.
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

	// Binders the candidate has and the target does not are created even when
	// they hold nothing: an empty binder is organization the user built, and
	// it has no holdings row to be inferred from.
	newBinders := make(map[string]string) // lowered name -> spelling
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
		if row.ContainerKind == "deck" {
			deckEntries[row.Container] = append(deckEntries[row.Container], store.Entry{
				ScryfallID: row.Card.ScryfallID, Finish: row.Card.Finish,
				Condition: row.Card.Condition, Board: row.Board, Quantity: row.Count,
			})
			continue
		}
		add := store.CardAdd{
			Card: card, Finish: row.Card.Finish,
			Condition: row.Card.Condition, Quantity: row.Count,
		}
		if id, ok := binderTargets[strings.ToLower(row.Container)]; ok {
			add.ContainerID = id.id
			add.Binder = id.name
		} else {
			add.Binder = newBinders[strings.ToLower(row.Container)]
			if add.Binder == "" {
				// A holdings row naming a binder no container row declared.
				// Create it rather than dropping the cards.
				add.Binder = strings.TrimSpace(row.Container)
				newBinders[strings.ToLower(row.Container)] = add.Binder
			}
		}
		plan.Adds = append(plan.Adds, add)
		res.Copies += row.Count
		res.PerBinder[add.Binder] += row.Count
	}

	plan.NewBinders = sortedValues(newBinders)
	// Reported from the plan, not from what was applied, so a dry run names
	// the binders it would create.
	res.Created = plan.NewBinders

	// Decks are all-or-nothing per deck: UpsertDeck replaces a deck's contents
	// wholesale, so merging into one the target already has would discard
	// repins and assessed conditions the user did by hand there.
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
		if held[watchKey(w.Card.ScryfallID, w.Card.Finish, w.Op)] && !o.ReplaceWatches {
			res.SkippedWatches = append(res.SkippedWatches, w.Display)
			continue
		}
		if _, ok := cards[w.Card.ScryfallID]; !ok {
			// watches reference cards; a watch whose printing never made it
			// into the catalog would break the foreign key.
			continue
		}
		plan.Watches = append(plan.Watches, store.WatchInput{
			ScryfallID: w.Card.ScryfallID, Display: w.Display,
			Finish: w.Card.Finish, Op: w.Op, Threshold: w.Threshold,
		})
		res.Watches++
	}
	return plan, nil
}

type binderDest struct {
	id   int64
	name string
}

// binderDestinations maps every name a candidate might use for a binder onto
// the target binder it lands in — the same resolution an import does, so a
// hoard export round-trips the same way whichever command reads it.
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
	// The reserved aliases resolve to the default binder however it is named
	// now — which is why two hoards' default binders necessarily become one.
	// Worth saying out loud rather than letting it be discovered later.
	for _, alias := range store.ReservedBinderNames {
		if _, taken := out[strings.ToLower(alias)]; !taken {
			out[strings.ToLower(alias)] = binderDest{binders[0].ID, binders[0].Name}
			res.FoldedInto = binders[0].Name
		}
	}
	return out, nil
}

// deckDestinations indexes the document's decks by name, which is all a
// holdings row carries.
//
// Two decks sharing a name are refused rather than guessed at. hoard cannot
// address such a pair by name anywhere else either, and silently merging one
// deck's cards into another is a worse answer than asking for a rename.
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

// compactJSON squeezes the whitespace out of a card document before it is
// stored.
//
// The document is written indented for a human reading a `-o` file, and Go's
// encoder indents an embedded RawMessage along with everything else — so a
// document read back carries a pretty-printed card. Storing that would put
// kilobytes of spaces in every merged row and make a merged printing's
// raw_json differ from a fetched one's for no reason. Invalid input is passed
// through untouched: the store is where a bad document should fail, not here.
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

func watchKey(id, finish, op string) string { return id + "\x00" + finish + "\x00" + op }

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
