package mtgjson

import (
	"context"
	"testing"
)

const allPrintingsBody = `{
 "meta": {"date": "2026-08-22", "version": "5.3.0"},
 "data": {
  "M3C": {"code": "M3C", "cards": [
    {"uuid": "uuid-ck", "identifiers": {"scryfallId": "scry-1",
     "tcgplayerProductId": "111", "tcgplayerAlternativeFoilProductId": "553005",
     "cardKingdomFoilId": "222", "cardKingdomEtchedId": "333"},
     "purchaseUrls": {"cardKingdom": "https://mtgjson.com/links/aa",
                      "cardKingdomFoil": "https://mtgjson.com/links/bb"}},
    {"uuid": "uuid-tcg", "identifiers": {"scryfallId": "scry-2",
     "tcgplayerEtchedProductId": "600100"}},
    {"uuid": "uuid-none", "identifiers": {}}
  ]},
  "EMPTY": {"code": "EMPTY", "cards": []},
  "NEO": {"code": "NEO", "cards": [
    {"uuid": "uuid-neo", "identifiers": {"scryfallId": "scry-3",
     "tcgplayerProductId": "999"},
     "purchaseUrls": {"cardKingdom": "https://mtgjson.com/links/cc"}}
  ]}
 }
}`

func TestAllIdentifiersReadsEverySet(t *testing.T) {
	serve(t, map[string][]byte{"/AllPrintings.json.gz": gzipped(t, allPrintingsBody)})

	got, err := AllIdentifiers(context.Background(), Options{})
	if err != nil {
		t.Fatalf("AllIdentifiers: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("map has %d entries, want 3 (one per card carrying a Scryfall id)", len(got))
	}

	for _, want := range []struct{ sid, uuid string }{
		{"scry-1", "uuid-ck"}, {"scry-2", "uuid-tcg"}, {"scry-3", "uuid-neo"},
	} {
		if got[want.sid].UUID != want.uuid {
			t.Errorf("%s maps to %q, want %q", want.sid, got[want.sid].UUID, want.uuid)
		}
	}

	if _, ok := got["scry-3"]; !ok {
		t.Error("a set listed after an empty one was not read; the walk stopped early")
	}

	one := got["scry-1"]
	if one.CKURL != "https://mtgjson.com/links/aa" || one.CKFoilURL != "https://mtgjson.com/links/bb" {
		t.Errorf("scry-1 links = %+v, want both Card Kingdom URLs", one)
	}
	if one.TCGProductID != "111" || one.AltProductID != "553005" {
		t.Errorf("scry-1 tcg ids = %+v, want product 111 and alt-foil 553005", one)
	}
	if one.CKFoilID != "222" || one.CKEtchedID != "333" {
		t.Errorf("scry-1 ck ids = %+v, want foil 222 and etched 333", one)
	}
	if got["scry-2"].EtchedProductID != "600100" {
		t.Errorf("scry-2 etched product = %q, want 600100", got["scry-2"].EtchedProductID)
	}
	if got["scry-2"].CKURL != "" {
		t.Errorf("scry-2 CKURL = %q, want empty for a card with no purchase urls", got["scry-2"].CKURL)
	}
}
