package ui

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func benchCardImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 488, 680))
	for y := range 680 {
		for x := range 488 {
			img.Set(x, y, color.RGBA{
				R: uint8((x * 5) % 256), G: uint8((y * 3) % 256),
				B: uint8((x + y) % 256), A: 255,
			})
		}
	}
	return img
}

func benchCardJPEG(b *testing.B) []byte {
	b.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, benchCardImage(), nil); err != nil {
		b.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

func BenchmarkHalfblocks(b *testing.B) {
	img := benchCardImage()
	b.ResetTimer()
	for b.Loop() {
		if lines := Halfblocks(img, 42); len(lines) == 0 {
			b.Fatal("no lines rendered")
		}
	}
}

func BenchmarkJPEGDecode(b *testing.B) {
	data := benchCardJPEG(b)
	b.ResetTimer()
	for b.Loop() {
		if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
			b.Fatalf("decode: %v", err)
		}
	}
}

func BenchmarkDecodeThenHalfblocks(b *testing.B) {
	data := benchCardJPEG(b)
	b.ResetTimer()
	for b.Loop() {
		img, err := jpeg.Decode(bytes.NewReader(data))
		if err != nil {
			b.Fatalf("decode: %v", err)
		}
		_ = Halfblocks(img, 42)
	}
}
