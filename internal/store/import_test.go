package store

import (
	"database/sql"
	"github.com/spiffcs/hoard/internal/finish"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyImportCreatesBindersAndAddsAtomically(t *testing.T) {
	s := newTestStore(t)
	binders, err := s.ListBinders()
	if err != nil {
		t.Fatalf("ListBinders: %v", err)
	}
	defaultID := binders[0].ID

	created, err := s.ApplyImport(nil, []string{"Trade"}, []CardAdd{
		{ContainerID: defaultID, Card: ulamog(), Finish: finish.Nonfoil, Quantity: 2},
		{Binder: "Trade", Card: solRing(), Finish: finish.Foil, Quantity: 1},
	})
	if err != nil {
		t.Fatalf("ApplyImport: %v", err)
	}
	if _, ok := created["Trade"]; !ok {
		t.Fatalf("created = %v, want Trade", created)
	}
	totals, err := s.CollectionTotals()
	if err != nil {
		t.Fatalf("CollectionTotals: %v", err)
	}
	if totals.TotalCopies != 3 {
		t.Errorf("copies = %d, want 3", totals.TotalCopies)
	}
	rows, err := s.BinderByFinish(created["Trade"])
	if err != nil || len(rows) != 1 || rows[0].Finish != finish.Foil {
		t.Errorf("Trade rows = %+v (%v), want the foil Sol Ring", rows, err)
	}
}

func TestApplyImportIsAllOrNothing(t *testing.T) {
	s := newTestStore(t)
	binders, _ := s.ListBinders()
	defaultID := binders[0].ID

	_, err := s.ApplyImport(nil, []string{"Trade"}, []CardAdd{
		{ContainerID: defaultID, Card: ulamog(), Finish: finish.Nonfoil, Quantity: 2},
		{Binder: "Ghost", Card: solRing(), Finish: finish.Nonfoil, Quantity: 1},
	})
	if err == nil {
		t.Fatal("a bad batch committed, want an error")
	}
	totals, _ := s.CollectionTotals()
	if totals.TotalCopies != 0 {
		t.Errorf("copies = %d after a failed import, want 0", totals.TotalCopies)
	}
	after, _ := s.ListBinders()
	if len(after) != 1 {
		t.Errorf("binders = %d after a failed import, want just the default", len(after))
	}
}

func TestNewerSchemaIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	s.Close()

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "Upgrade hoard") {
		t.Errorf("opening a v99 file: err = %v, want a refusal telling the user to upgrade", err)
	}
}

func TestApplicationIDIsStamped(t *testing.T) {
	s := freshDB(t)
	var id int64
	if err := s.db.QueryRow(`PRAGMA application_id`).Scan(&id); err != nil {
		t.Fatalf("application_id: %v", err)
	}
	if id != applicationID {
		t.Errorf("application_id = %#x, want %#x (HORD)", id, applicationID)
	}
}

func TestBackupPrecedesTransforms(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hoard.db")
	seedRawDB(t, path, preVersioningDDL+`
INSERT INTO cards VALUES ('ulamog-id','uma','7','Ulamog, the Infinite Gyre',
                          10.0,25.0,'http://x','2020-01-01T00:00:00Z');`)

	s, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()

	entries, _ := os.ReadDir(dir)
	var backup string
	for _, e := range entries {
		if strings.Contains(e.Name(), ".bak-") {
			backup = filepath.Join(dir, e.Name())
		}
	}
	if backup == "" {
		t.Fatal("no backup written")
	}
	raw, err := sql.Open("sqlite", backup)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var v int
	if err := raw.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != 0 {
		t.Errorf("backup user_version = %d, want the untouched 0", v)
	}
	var hasAlt bool
	if err := raw.QueryRow(`
SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='card_prices_alt')`).Scan(&hasAlt); err != nil {
		t.Fatal(err)
	}
	if hasAlt {
		t.Error("backup contains a migrated table — it was taken after a transform ran")
	}
}
