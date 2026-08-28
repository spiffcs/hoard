package store

import (
	"testing"

	"github.com/spiffcs/hoard/internal/scryfall"
)

func TestCardDetailExposesTheBackFace(t *testing.T) {
	s := newTestStore(t)
	mk := func(id, raw string) scryfall.Card {
		c := ulamog()
		c.ID = id
		c.Raw = []byte(raw)
		return c
	}
	cards := []scryfall.Card{
		mk("transform", `{"layout":"transform","card_faces":[
		  {"name":"Growing Rites of Itlimoc","mana_cost":"{2}{G}",
		   "type_line":"Legendary Enchantment","oracle_text":"Look at the top four cards.",
		   "flavor_text":"The rites begin.","artist":"Front Artist",
		   "image_uris":{"normal":"https://img/rites.jpg"}},
		  {"name":"Itlimoc, Cradle of the Sun","type_line":"Legendary Land",
		   "oracle_text":"{T}: Add {G} for each creature you control.",
		   "flavor_text":"The sun answers.","artist":"Back Artist",
		   "image_uris":{"normal":"https://img/itlimoc.jpg"}}]}`),
		mk("mdfc", `{"layout":"modal_dfc","card_faces":[
		  {"name":"Front Beast","mana_cost":"{1}{G}","type_line":"Creature — Beast",
		   "power":"2","toughness":"2",
		   "image_uris":{"normal":"https://img/beast.jpg"}},
		  {"name":"Back Horror","mana_cost":"{1}{B}","type_line":"Creature — Horror",
		   "power":"3","toughness":"4","loyalty":"",
		   "image_uris":{"normal":"https://img/horror.jpg"}}]}`),
		mk("split", `{"layout":"split","image_uris":{"normal":"https://img/split.jpg"},
		  "card_faces":[{"name":"Fire","type_line":"Instant"},
		                {"name":"Ice","type_line":"Instant"}]}`),
		mk("single", `{"layout":"normal","type_line":"Artifact",
		  "image_uris":{"normal":"https://img/single.jpg"}}`),
	}
	if err := s.UpsertPrintings(cards); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	get := func(id string) CardDetail {
		t.Helper()
		d, err := s.CardDetail(id)
		if err != nil {
			t.Fatalf("CardDetail(%s): %v", id, err)
		}
		return d
	}

	tf := get("transform")
	if tf.Back == nil {
		t.Fatal("a transform card must carry its back face")
	}
	if tf.Back.Name != "Itlimoc, Cradle of the Sun" {
		t.Errorf("back name = %q", tf.Back.Name)
	}
	if tf.Back.TypeLine != "Legendary Land" {
		t.Errorf("back type line = %q", tf.Back.TypeLine)
	}
	if tf.Back.OracleText != "{T}: Add {G} for each creature you control." {
		t.Errorf("back oracle text = %q", tf.Back.OracleText)
	}
	if tf.Back.FlavorText != "The sun answers." {
		t.Errorf("back flavor text = %q", tf.Back.FlavorText)
	}
	if tf.Back.Artist != "Back Artist" {
		t.Errorf("back artist = %q", tf.Back.Artist)
	}
	if tf.Back.ImageURI != "https://img/itlimoc.jpg" {
		t.Errorf("back image = %q", tf.Back.ImageURI)
	}
	if tf.Back.ManaCost != "" {
		t.Errorf("back mana cost = %q, want empty: the front's cost must not leak onto a face that has none", tf.Back.ManaCost)
	}

	if tf.TypeLine != "Legendary Enchantment" || tf.ImageURI != "https://img/rites.jpg" {
		t.Errorf("the front face must still read from face 0: type %q, image %q", tf.TypeLine, tf.ImageURI)
	}
	if tf.OracleText != "Look at the top four cards." {
		t.Errorf("front oracle text = %q", tf.OracleText)
	}

	md := get("mdfc")
	if md.Back == nil {
		t.Fatal("a modal double-faced card must carry its back face")
	}
	if md.Back.Power != "3" || md.Back.Toughness != "4" {
		t.Errorf("back P/T = %q/%q, want 3/4", md.Back.Power, md.Back.Toughness)
	}
	if md.Back.ManaCost != "{1}{B}" {
		t.Errorf("back mana cost = %q, want {1}{B}", md.Back.ManaCost)
	}
	if md.Power != "2" || md.Toughness != "2" {
		t.Errorf("front P/T = %q/%q, want the face-0 values 2/2", md.Power, md.Toughness)
	}

	if sp := get("split"); sp.Back != nil {
		t.Errorf("a split card has one image and nothing to flip to, got back face %+v", sp.Back)
	}
	if one := get("single"); one.Back != nil {
		t.Errorf("a single-faced card must have no back face, got %+v", one.Back)
	}
}
