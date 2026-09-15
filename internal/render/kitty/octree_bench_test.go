package kitty

import (
	"testing"
)

// benchFrame synthesizes a full-screen 2056x1187 RGB24 frame resembling a
// terminal desktop: smooth gradient background, solid color blocks, and
// high-frequency text-like stripes.
func benchFrame(width, height int) []byte {
	rgb := make([]byte, width*height*3)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			offset := (y*width + x) * 3
			// Gradient background.
			r := uint8((x * 200) / width)
			g := uint8((y * 180) / height)
			b := uint8(60 + ((x+y)*120)/(width+height))
			// Solid window blocks.
			if x > width/4 && x < width*3/4 && y > height/5 && y < height*4/5 {
				r, g, b = 30, 34, 42
			}
			// Text-like high-frequency stripes inside the block.
			if x > width/4+20 && x < width*3/4-20 && y > height/5+10 && y < height*4/5-10 {
				if y%14 < 8 && x%8 < 5 {
					r, g, b = 200+uint8(x%56), 200+uint8(y%56), 180
				}
			}
			rgb[offset] = r
			rgb[offset+1] = g
			rgb[offset+2] = b
		}
	}
	return rgb
}

func BenchmarkQuantizePaletted(b *testing.B) {
	const width, height = 2056, 1187
	rgb := benchFrame(width, height)
	b.SetBytes(int64(len(rgb)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		img := quantizePaletted(rgb, width, height)
		if img == nil {
			b.Fatal("quantizePaletted returned nil")
		}
	}
}

func BenchmarkPalettePNG(b *testing.B) {
	const width, height = 2056, 1187
	rgb := benchFrame(width, height)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, err := palettePNG(rgb, width, height)
		if err != nil {
			b.Fatal(err)
		}
		if len(data) == 0 {
			b.Fatal("palettePNG returned empty output")
		}
	}
}

func BenchmarkPalettePNGPooled(b *testing.B) {
	const width, height = 2056, 1187
	rgb := benchFrame(width, height)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		data, release, err := palettePNGPooled(rgb, width, height)
		if err != nil {
			b.Fatal(err)
		}
		if len(data) == 0 {
			b.Fatal("palettePNGPooled returned empty output")
		}
		release()
	}
}
