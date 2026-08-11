package command

// `hoard binder list` is a discovery step, not a report: --binder on export,
// import and add takes "an id, a name, or a unique fragment", so a caller has
// to find out what binders exist before it can name one. These pin that the
// finding-out has a machine path.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/store"
)

// binderStore is a hoard with the built-in binder and one the user made, which
// is the case the ids matter in: with a single binder any name works, and the
// question of which handle is stable never comes up.
func binderStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "hoard.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.CreateBinder("Trade Stock"); err != nil {
		t.Fatalf("CreateBinder: %v", err)
	}
	return st
}

type binderDoc struct {
	SchemaVersion string `json:"schemaVersion"`
	Kind          string `json:"kind"`
	Binders       struct {
		Rows []struct {
			ID            int64   `json:"id"`
			Name          string  `json:"name"`
			IsDefault     bool    `json:"isDefault"`
			DistinctCards int     `json:"distinctCards"`
			TotalCopies   int     `json:"totalCopies"`
			ValueUsd      float64 `json:"valueUsd"`
		} `json:"rows"`
	} `json:"binders"`
}

func readBinders(t *testing.T, st *store.Store, args ...string) binderDoc {
	t.Helper()
	out, err := execCmd(context.Background(), st, args, true)
	if err != nil {
		t.Fatalf("hoard %v --json: %v", args, err)
	}
	var doc binderDoc
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("hoard %v --json emitted invalid JSON (%v): %q", args, err, out)
	}
	return doc
}

// The reported friction, stated as a test: before this, `hoard binder --json`
// answered "hoard binder has no JSON output" and the only way to learn a
// binder's id was to read a formatted table.
func TestBinderListJSONCarriesEveryBinderWithItsID(t *testing.T) {
	st := binderStore(t)
	doc := readBinders(t, st, "binder", "list")

	if doc.Kind != "binders" {
		t.Errorf("kind = %q, want %q", doc.Kind, "binders")
	}
	if len(doc.Binders.Rows) != 2 {
		t.Fatalf("rows = %d, want the default binder and Trade Stock: %+v",
			len(doc.Binders.Rows), doc.Binders.Rows)
	}
	for _, r := range doc.Binders.Rows {
		if r.ID == 0 {
			t.Errorf("binder %q has no id, which is the field the document exists for", r.Name)
		}
		if r.Name == "" {
			t.Errorf("binder #%d has no name", r.ID)
		}
	}
	// The default binder is marked rather than merely first, because `binder
	// rm` refuses it and a caller should not have to infer that from position.
	if !doc.Binders.Rows[0].IsDefault {
		t.Errorf("the first row is not marked isDefault: %+v", doc.Binders.Rows[0])
	}
	if doc.Binders.Rows[1].IsDefault {
		t.Errorf("a user-created binder is marked isDefault: %+v", doc.Binders.Rows[1])
	}
}

// The bare group form lists, as it did before the port, so it emits the same
// document. A caller that types the shorthand should not get a different answer
// from one that types the subcommand.
func TestBinderBareFormEmitsTheSameDocumentAsList(t *testing.T) {
	st := binderStore(t)
	bare, err := execCmd(context.Background(), st, []string{"binder"}, true)
	if err != nil {
		t.Fatalf("hoard binder --json: %v", err)
	}
	listed, err := execCmd(context.Background(), st, []string{"binder", "list"}, true)
	if err != nil {
		t.Fatalf("hoard binder list --json: %v", err)
	}
	if bare != listed {
		t.Errorf("the shorthand and the subcommand disagree:\n%s\nvs\n%s", bare, listed)
	}
}

// The point of carrying the id is that it survives a name changing underneath a
// script; the name is the handle that cannot. This walks that: rename the
// binder, and the row keeps its id.
func TestBinderIDSurvivesARename(t *testing.T) {
	st := binderStore(t)
	before := readBinders(t, st, "binder", "list")
	var id int64
	for _, r := range before.Binders.Rows {
		if r.Name == "Trade Stock" {
			id = r.ID
		}
	}
	if id == 0 {
		t.Fatalf("no Trade Stock binder to rename: %+v", before.Binders.Rows)
	}

	if _, err := execCmd(context.Background(), st,
		[]string{"binder", "rename", "Trade Stock", "Trades"}, false); err != nil {
		t.Fatalf("binder rename: %v", err)
	}

	after := readBinders(t, st, "binder", "list")
	for _, r := range after.Binders.Rows {
		if r.ID == id {
			if r.Name != "Trades" {
				t.Errorf("binder #%d is named %q, want the new name", id, r.Name)
			}
			return
		}
	}
	t.Errorf("binder #%d is gone after a rename; the id is not a stable handle: %+v",
		id, after.Binders.Rows)
}

// The annotation does not descend, and it must not: new, rename and rm report
// an action rather than a result, and there is no document for one.
func TestBinderMutatorsStillRefuseJSON(t *testing.T) {
	for _, args := range [][]string{
		{"binder", "new", "Spare"},
		{"binder", "rename", "Trade Stock", "Trades"},
		{"binder", "rm", "Trade Stock"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			st := binderStore(t)
			_, err := execCmd(context.Background(), st, args, true)
			if err == nil {
				t.Fatalf("hoard %v --json succeeded; it has no document to emit", args)
			}
			if !strings.Contains(err.Error(), "has no JSON output") {
				t.Errorf("refusal is not the --json policy's: %v", err)
			}
		})
	}
}
