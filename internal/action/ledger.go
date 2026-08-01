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

// RefuseReimport rejects content the ledger has seen, unless again says the
// doubling is intentional. Imports add quantities, so a silent re-run doubles
// every count with no visible symptom.
func RefuseReimport(st *store.Store, hash string, again bool) error {
	when, cardCount, done, err := st.ImportedAt(hash)
	if err != nil {
		return err
	}
	if done && !again {
		return fmt.Errorf(
			"this content was already imported on %s (%d cards) — re-running would double every quantity.\nUse --again to add them anyway",
			when, cardCount)
	}
	return nil
}
