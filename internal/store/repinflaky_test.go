package store

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
	"github.com/spiffcs/hoard/internal/scryfall"
)

var errReadFailedPartway = errors.New("simulated read failure partway through")

type flakyDriver struct {
	inner driver.Driver
	match string
	after int
}

var flakyOnce sync.Once

var flaky = &flakyDriver{match: "SELECT finish, condition, board", after: 1}

func (d *flakyDriver) Open(name string) (driver.Conn, error) {
	c, err := d.inner.Open(name)
	if err != nil {
		return nil, err
	}
	return flakyConn{Conn: c, d: d}, nil
}

type flakyConn struct {
	driver.Conn
	d *flakyDriver
}

func (c flakyConn) Prepare(query string) (driver.Stmt, error) {
	st, err := c.Conn.Prepare(query)
	if err != nil {
		return nil, err
	}
	if !strings.Contains(query, c.d.match) {
		return st, nil
	}
	return flakyStmt{Stmt: st, after: c.d.after}, nil
}

type flakyStmt struct {
	driver.Stmt
	after int
}

func (s flakyStmt) Query(args []driver.Value) (driver.Rows, error) {
	rows, err := s.Stmt.Query(args)
	if err != nil {
		return nil, err
	}
	return &flakyRows{Rows: rows, left: s.after}, nil
}

type flakyRows struct {
	driver.Rows
	left int
}

func (r *flakyRows) Next(dest []driver.Value) error {
	if r.left <= 0 {
		return errReadFailedPartway
	}
	r.left--
	return r.Rows.Next(dest)
}

// flakyWriter reopens path on a driver that fails the repin SELECT partway,
// and points the store's writer at it. Migration has already run on the real
// handle, so only the re-pin's own statements meet the failure.
func flakyWriter(t *testing.T, s *Store) {
	t.Helper()
	flakyOnce.Do(func() {
		probe, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			t.Fatalf("probing the sqlite driver: %v", err)
		}
		flaky.inner = probe.Driver()
		probe.Close()
		sql.Register("sqlite-flaky-repin", flaky)
	})
	db, err := sql.Open("sqlite-flaky-repin", s.path+writerPragmas)
	if err != nil {
		t.Fatalf("opening the flaky writer: %v", err)
	}
	db.SetMaxOpenConns(1)
	real := s.db
	s.db = db
	t.Cleanup(func() { s.db = real; db.Close() })
}

func TestRepinRefusesWhenTheReadFailsPartway(t *testing.T) {
	s := newTestStore(t)
	hob := scryfall.Card{ID: "we-hob", Set: "hob", CollectorNumber: "142",
		Name: "Wood Elves", ScryfallURL: "http://x"}
	cma := scryfall.Card{ID: "we-cma", Set: "cma", CollectorNumber: "154",
		Name: "Wood Elves", ScryfallURL: "http://x"}
	if err := s.UpsertPrintings([]scryfall.Card{hob, cma}); err != nil {
		t.Fatalf("UpsertPrintings: %v", err)
	}
	deckID, err := s.UpsertDeck(DeckMeta{Name: "Guided", Source: "text", SourceID: "guided"},
		[]Entry{
			{ScryfallID: "we-hob", Finish: finish.Nonfoil, Board: "main", Quantity: 1},
			{ScryfallID: "we-hob", Finish: finish.Foil, Board: "main", Quantity: 1},
			{ScryfallID: "we-hob", Finish: finish.Nonfoil, Board: "side", Quantity: 1},
		})
	if err != nil {
		t.Fatalf("UpsertDeck: %v", err)
	}

	flakyWriter(t, s)

	if _, err := s.RepointDeckPrintings(deckID, map[string]string{"we-hob": "we-cma"}); err == nil {
		t.Error("a read that failed partway was reported as success")
	}

	entries, err := s.DeckEntries(deckID)
	if err != nil {
		t.Fatalf("DeckEntries: %v", err)
	}
	total := 0
	for _, e := range entries {
		total += e.Quantity
		if e.Card.ScryfallID != "we-hob" {
			t.Errorf("%s moved despite the failure", e.Card.ScryfallID)
		}
	}
	if total != 3 {
		t.Errorf("copies after the failed re-pin = %d, want all 3 still there", total)
	}
}
