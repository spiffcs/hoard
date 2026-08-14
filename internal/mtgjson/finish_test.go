package mtgjson

import (
	"context"
	"github.com/spiffcs/hoard/internal/finish"
	"testing"
)

func TestTodayQuotesTranslateNormalOnTheWayIn(t *testing.T) {
	serve(t, map[string][]byte{"/AllPricesToday.json.gz": gzipped(t, quoteFile)})

	got, err := TodayQuotes(context.Background(), Options{}, map[string]bool{"uuid-legion": true})
	if err != nil {
		t.Fatalf("TodayQuotes: %v", err)
	}
	qs := got["uuid-legion"]
	if len(qs) == 0 {
		t.Fatal("no quotes decoded, so this proves nothing")
	}

	byKey := map[string]float64{}
	for _, q := range qs {
		if _, err := finish.Parse(q.Finish.String()); err != nil {
			t.Errorf("quote %+v does not carry a finish hoard recognises: %v", q, err)
		}
		byKey[q.Provider+"/"+q.Kind+"/"+q.Finish.String()] = q.Price
	}
	if byKey["cardkingdom/retail/nonfoil"] != 0.99 {
		t.Errorf("cardkingdom/retail/nonfoil = %v, want 0.99: MTGJSON's normal side is our nonfoil",
			byKey["cardkingdom/retail/nonfoil"])
	}
	if byKey["tcgplayer/retail/nonfoil"] != 0.42 {
		t.Errorf("tcgplayer/retail/nonfoil = %v, want 0.42", byKey["tcgplayer/retail/nonfoil"])
	}
}

func TestPriceHistoryTranslatesNormalOnTheWayIn(t *testing.T) {
	serve(t, map[string][]byte{"/AllPrices.json.gz": gzipped(t, archiveFileBody)})

	got, err := PriceHistory(context.Background(), Options{}, map[string]bool{"uuid-hist": true})
	if err != nil {
		t.Fatalf("PriceHistory: %v", err)
	}
	h := got["uuid-hist"]
	if len(h.Retail) == 0 && len(h.Bids) == 0 {
		t.Fatal("no observations decoded, so this proves nothing")
	}

	nonfoil := 0
	for _, o := range append(append([]Observation{}, h.Retail...), h.Bids...) {
		if _, err := finish.Parse(o.Finish.String()); err != nil {
			t.Errorf("observation %+v does not carry a finish hoard recognises: %v", o, err)
		}
		if o.Finish == finish.Nonfoil {
			nonfoil++
		}
	}
	if nonfoil == 0 {
		t.Error("no observation came back as nonfoil, so the normal side was dropped rather than translated")
	}
}
