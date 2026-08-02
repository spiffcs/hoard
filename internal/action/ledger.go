package action

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

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
		"this content was already imported on %s (%d cards) — re-running would double every quantity.\nUse --again to add them anyway",
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
		return &AlreadyImportedError{When: when, Cards: cardCount}
	}
	return nil
}
