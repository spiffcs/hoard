package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSetLookupsUseAnIndexOnSetCode(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "idx.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	var name string
	err = st.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='index' AND tbl_name='cards'
		 AND sql LIKE '%set_code%'`).Scan(&name)
	if err != nil {
		t.Fatalf("no index on cards(set_code); every set the browser opens "+
			"scans the whole cards table: %v", err)
	}

	var plan string
	rows, err := st.db.Query(`EXPLAIN QUERY PLAN
SELECT c.scryfall_id FROM card_entries e
JOIN cards c ON c.scryfall_id = e.scryfall_id
WHERE c.set_code = ?`, "mh2")
	if err != nil {
		t.Fatalf("EXPLAIN: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scanning plan: %v", err)
		}
		plan += detail + "\n"
	}
	if !strings.Contains(plan, "SEARCH c USING INDEX "+name) {
		t.Errorf("set lookup does not seek through %s:\n%s", name, plan)
	}
}
