package action

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/spiffcs/hoard/internal/store"
)

// ContentHash is the import ledger's identity for a batch of cards: the
// bytes, not the filename — a renamed copy of an already-imported export is
// still the same cards, and a re-pasted list is still the same paste.
func ContentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// AlreadyImportedError is the ledger's refusal, typed so a frontend can
// recognize it (errors.As) and offer its own run-again affordance — the TUI
// stages a confirm where the CLI prints --again advice. Error reproduces
// the CLI message byte-exactly; the root import tests lock it.
type AlreadyImportedError struct {
	When  string
	Cards int
}

func (e *AlreadyImportedError) Error() string {
	return fmt.Sprintf(
		"this content was already imported on %s (%d cards); re-running would double every quantity.\nUse --again to add them anyway",
		e.When, e.Cards)
}

// RefuseReimport rejects content the ledger has seen, unless again says the
// doubling is intentional. Imports add quantities, so a silent re-run doubles
// every count with no visible symptom.
func RefuseReimport(st *store.Store, hash string, again bool) error {
	when, cardCount, done, err := st.ImportedAt(hash)
	if err != nil {
		return err
	}
	if done && !again {
		return &AlreadyImportedError{When: humanWhen(when), Cards: cardCount}
	}
	return nil
}

// humanWhen renders the ledger's stored stamp the way the rest of the CLI
// renders dates. The ledger writes RFC 3339 in UTC, and this refusal was the
// only place a user ever saw one: everything else — the valuation's "prices as
// of 10 Aug 2026", the catalog's "Built 10 Aug 12:22" — is local and human.
// West of Greenwich an evening import came back stamped with tomorrow's date,
// so the guard's most important property, that it is telling the truth about
// what you already did, was the first thing a reader had reason to doubt.
//
// Local, because the question it answers is "did I already run this?" and the
// clock a reader checks that against is their own. With the time, not just the
// date, because a re-import attempt usually follows the original by minutes:
// "8 Aug 2026" twice over is a refusal that cannot distinguish this morning's
// import from the one being attempted now.
//
// Formatted here rather than through report.asOfDate, which does this exact
// parse-or-passthrough for the valuation's stamp: that function is unexported
// and internal/report imports nothing from action, so reuse would mean either
// exporting a date helper from a rendering package or adding one to internal/ui
// for a single caller. Two literals of a format string is the smaller cost, and
// this is where the second one belongs.
//
// A stamp that will not parse is shown raw rather than dropped: a ledger row
// written by some future version is still evidence the content was imported,
// and the refusal is worth more than its date is.
func humanWhen(stamp string) string {
	if t, err := time.Parse(time.RFC3339, stamp); err == nil {
		return t.Local().Format("2 Jan 2006 15:04")
	}
	return stamp
}
