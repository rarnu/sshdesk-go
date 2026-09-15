package kitty

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

// TestPalettePNGRoundTrip validates the hand-rolled PNG packing against the
// standard decoder: the decoded image must be paletted, capped at 128
// colors, and reproduce every quantized pixel.
func TestPalettePNGRoundTrip(t *testing.T) {
	const width, height = 96, 64
	rgb := make([]byte, width*height*3)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			offset := (y*width + x) * 3
			rgb[offset] = uint8(x * 7)
			rgb[offset+1] = uint8(y * 11)
			rgb[offset+2] = uint8((x + y) * 5)
		}
	}
	data, err := palettePNG(rgb, width, height)
	if err != nil {
		t.Fatalf("palettePNG() error = %v", err)
	}
	if !bytes.HasPrefix(data, pngSignature) {
		t.Fatal("palettePNG() output lacks the PNG signature")
	}
	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("png.Decode() error = %v", err)
	}
	if decoded.Bounds().Dx() != width || decoded.Bounds().Dy() != height {
		t.Fatalf("decoded size = %dx%d, want %dx%d",
			decoded.Bounds().Dx(), decoded.Bounds().Dy(), width, height)
	}
	paletted, ok := decoded.(*image.Paletted)
	if !ok {
		t.Fatalf("decoded image is %T, want *image.Paletted", decoded)
	}
	if len(paletted.Palette) > 128 {
		t.Fatalf("decoded palette = %d colors, want <= 128", len(paletted.Palette))
	}
	want := quantizePaletted(rgb, width, height)
	if len(paletted.Palette) != len(want.Palette) {
		t.Fatalf("decoded palette = %d colors, want %d", len(paletted.Palette), len(want.Palette))
	}
	for i, entry := range want.Palette {
		if paletted.Palette[i] != entry {
			t.Fatalf("palette[%d] = %v, want %v", i, paletted.Palette[i], entry)
		}
	}
	if !bytes.Equal(paletted.Pix, want.Pix) {
		t.Fatal("decoded palette indices differ from the quantized pixels")
	}
}
