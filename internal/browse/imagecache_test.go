package browse

import (
	"context"
	"image"
	"testing"

	"github.com/spiffcs/hoard/internal/store"
	"github.com/spiffcs/hoard/internal/ui"
)

func TestRenderCacheEvictsOldestBeyondItsCap(t *testing.T) {
	c := newRenderCache(3)
	keys := make([]imageKey, 5)
	for i := range keys {
		keys[i] = imageKey{id: string(rune('a' + i)), cols: 10}
		c.put(keys[i], imageEntry{lines: []string{keys[i].id}})
	}
	for _, k := range keys[:2] {
		if _, ok := c.get(k); ok {
			t.Errorf("%q survived past the cap; the cache would grow unbounded", k.id)
		}
	}
	for _, k := range keys[2:] {
		if e, ok := c.get(k); !ok || e.lines[0] != k.id {
			t.Errorf("%q was evicted early, got %v %v", k.id, e, ok)
		}
	}
}

func TestRenderCacheKeysOnEverythingThatChangesTheRender(t *testing.T) {
	c := newRenderCache(8)
	base := imageKey{id: "sf1", cols: 20, tier: ui.ImageHalfblock, imgID: 91, aspect: 2}
	c.put(base, imageEntry{lines: []string{"base"}})

	for name, k := range map[string]imageKey{
		"cols":   {id: "sf1", cols: 21, tier: ui.ImageHalfblock, imgID: 91, aspect: 2},
		"tier":   {id: "sf1", cols: 20, tier: ui.ImageKitty, imgID: 91, aspect: 2},
		"imgID":  {id: "sf1", cols: 20, tier: ui.ImageHalfblock, imgID: 92, aspect: 2},
		"aspect": {id: "sf1", cols: 20, tier: ui.ImageHalfblock, imgID: 91, aspect: 1.9},
		"card":   {id: "sf2", cols: 20, tier: ui.ImageHalfblock, imgID: 91, aspect: 2},
	} {
		if _, ok := c.get(k); ok {
			t.Errorf("a different %s hit the cache; the wrong image would be drawn", name)
		}
	}
}

func TestReopeningACardSkipsTheDecodeAndResample(t *testing.T) {
	m := newTestModel(t, testStore())
	m.imgTier = ui.ImageHalfblock

	var fetches int
	m.imageFetch = func(context.Context, string, string) (image.Image, error) {
		fetches++
		return image.NewRGBA(image.Rect(0, 0, 8, 12)), nil
	}
	open := func(id string) imageMsg {
		m.detail = &detail{card: store.CardDetail{}}
		m.detail.card.ScryfallID = id
		m.detail.card.ImageURI = "https://img/" + id + ".jpg"
		cmd := m.fetchDetailImage()
		if cmd == nil {
			t.Fatal("no fetch command")
		}
		msg, ok := cmd().(imageMsg)
		if !ok {
			t.Fatal("not an imageMsg")
		}
		return msg
	}

	first := open("sf1")
	if fetches != 1 || len(first.lines) == 0 {
		t.Fatalf("first open: %d fetches, %d lines", fetches, len(first.lines))
	}
	again := open("sf1")
	if fetches != 1 {
		t.Errorf("%d fetches for the same card twice, want 1 — every look "+
			"re-reads, re-decodes and re-resamples the image", fetches)
	}
	if len(again.lines) != len(first.lines) {
		t.Errorf("cached render returned %d lines, want the original %d",
			len(again.lines), len(first.lines))
	}
	if open("sf2"); fetches != 2 {
		t.Errorf("%d fetches after opening a different card, want 2", fetches)
	}
}
