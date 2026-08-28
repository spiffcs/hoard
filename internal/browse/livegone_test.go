package browse

import (
	"fmt"
	"strings"
	"testing"

	"github.com/spiffcs/hoard/internal/store"
)

func TestLiveRefreshRetiresWhenTheDatabaseIsReplaced(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	m = poll(m)

	st.dataVersionErr = fmt.Errorf("/tmp/all.db: %w", store.ErrDatabaseReplaced)

	next, cmd := m.Update(livePollMsg{})
	m = next.(Model)

	if cmd != nil {
		t.Error("the poll rescheduled itself after the database underneath it was replaced")
	}
	if !m.statusErr || !strings.Contains(m.status, "replaced") {
		t.Errorf("status = %q (statusErr %v), want it to report the database was replaced",
			m.status, m.statusErr)
	}

	reads := st.dataVersionReads
	m = poll(m)
	if st.dataVersionReads != reads {
		t.Errorf("kept polling a database that is gone: %d further reads",
			st.dataVersionReads-reads)
	}
}

func TestAnOrdinaryPollErrorDoesNotRetireLiveRefresh(t *testing.T) {
	st := testStore()
	m := newTestModel(t, st)
	m = poll(m)

	st.dataVersionErr = fmt.Errorf("database is locked")

	next, cmd := m.Update(livePollMsg{})
	m = next.(Model)

	if cmd == nil {
		t.Error("a transient poll error retired live refresh")
	}
	if m.statusErr {
		t.Errorf("a transient poll error raised an error status: %q", m.status)
	}
}
