package catalog

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/spiffcs/hoard/internal/finish"
)

func imageCard(id, name, set, num, image string) string {
	b, _ := json.Marshal(map[string]any{
		"id": id, "name": name, "set": set, "collector_number": num,
		"set_name": "Test Set", "released_at": "2024-01-01", "rarity": "rare",
		"finishes": []finish.Finish{finish.Nonfoil}, "border_color": "black",
		"scryfall_uri": "https://scryfall.com/card/" + set + "/" + num,
		"games":        []string{"paper"},
		"prices":       map[string]any{"usd": "1.00"},
		"image_uris":   map[string]any{"small": image + "-small", "normal": image},
	})
	return string(b)
}

func doubleFacedCard(id, name, set, num, front, back string) string {
	b, _ := json.Marshal(map[string]any{
		"id": id, "name": name, "set": set, "collector_number": num,
		"set_name": "Test Set", "released_at": "2024-01-01", "rarity": "rare",
		"finishes": []finish.Finish{finish.Nonfoil}, "border_color": "black",
		"scryfall_uri": "https://scryfall.com/card/" + set + "/" + num,
		"games":        []string{"paper"},
		"prices":       map[string]any{"usd": "1.00"},
		"card_faces": []map[string]any{
			{"name": "Front", "image_uris": map[string]any{"normal": front}},
			{"name": "Back", "image_uris": map[string]any{"normal": back}},
		},
	})
	return string(b)
}

func TestSetPrintsCarryTheArtScryfallPublished(t *testing.T) {
	serveBundle(t, "2024-05-01T00:00:00Z", []string{
		imageCard("nexus-id", "Cosmic Nexus", "eoe", "4",
			"https://cards.scryfall.io/normal/front/n/e/nexus-id.jpg"),
		doubleFacedCard("drake-id", "Voidwing Drake", "eoe", "5",
			"https://cards.scryfall.io/normal/front/d/r/drake-id.jpg",
			"https://cards.scryfall.io/normal/back/d/r/drake-id.jpg"),
	})

	c := openTemp(t)
	if err := c.Update(context.Background(), nil); err != nil {
		t.Fatalf("Update: %v", err)
	}

	prints, err := c.SetPrints(context.Background(), "eoe")
	if err != nil {
		t.Fatalf("SetPrints: %v", err)
	}
	if len(prints) != 2 {
		t.Fatalf("SetPrints returned %d printings, want the 2 in the bundle", len(prints))
	}

	got := map[string]string{}
	for _, p := range prints {
		got[p.Name] = p.ImageURI
	}
	want := map[string]string{
		"Cosmic Nexus":   "https://cards.scryfall.io/normal/front/n/e/nexus-id.jpg",
		"Voidwing Drake": "https://cards.scryfall.io/normal/front/d/r/drake-id.jpg",
	}
	for name, url := range want {
		if got[name] != url {
			t.Errorf("%s image = %q, want %q", name, got[name], url)
		}
	}
}
