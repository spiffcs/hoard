package store

// The cheapest question a reader can ask a SQLite file: has anyone else
// committed since I last looked.
//
// `PRAGMA data_version` answers it from the database header, so its cost is
// flat in the size of the hoard — measured at 6µs on a 30MB file and 5µs on
// a 141MB one. That property is why the browser can afford to ask twice a
// second forever, and why nothing here needs a file watcher, a new
// dependency or a schema change.

import "fmt"

// DataVersion reports SQLite's data_version counter for this connection.
//
// The counter moves when *another* connection commits. It deliberately does
// not move for this connection's own reads or its own writes, which is the
// property that makes it usable as a change signal: a browser that edits
// its own hoard never sees its own edit here, so it can never chase itself
// round a refresh loop.
//
// The value is meaningful only by comparison with an earlier reading on the
// same connection — it is a change detector, not a version number, and it
// carries no meaning across a reopen.
func (s *Store) DataVersion() (int64, error) {
	var v int64
	if err := s.db.QueryRow(`PRAGMA data_version`).Scan(&v); err != nil {
		return 0, fmt.Errorf("reading data_version: %w", err)
	}
	return v, nil
}
