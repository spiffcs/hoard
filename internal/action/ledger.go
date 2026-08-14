package action

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/spiffcs/hoard/internal/store"
)

func ContentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

type AlreadyImportedError struct {
	When  string
	Cards int
}

func (e *AlreadyImportedError) Error() string {
	return fmt.Sprintf(
		"this content was already imported on %s (%d cards); re-running would double every quantity.\nUse --again to add them anyway",
		e.When, e.Cards)
}

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

func humanWhen(stamp string) string {
	if t, err := time.Parse(time.RFC3339, stamp); err == nil {
		return t.Local().Format("2 Jan 2006 15:04")
	}
	return stamp
}
