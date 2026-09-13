package xshm

import (
	"testing"
)

func TestPixelLayoutValidate(t *testing.T) {
	valid := PixelLayout{BitsPerPixel: 32, RedMask: 0xFF0000, GreenMask: 0x00FF00, BlueMask: 0x0000FF}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	cases := []PixelLayout{
		{BitsPerPixel: 24, RedMask: 0xFF0000, GreenMask: 0x00FF00, BlueMask: 0x0000FF},
		{BitsPerPixel: 32, RedMask: 0x0000FF, GreenMask: 0x00FF00, BlueMask: 0xFF0000},
		{BitsPerPixel: 32, RedMask: 0xFF0000, GreenMask: 0x00FF00, BlueMask: 0x00FF00},
		{},
	}
	for index, layout := range cases {
		err := layout.Validate()
		if err == nil || err.Error() != "unsupported X11 pixel layout for accelerated capture" {
			t.Fatalf("case %d: Validate() error = %v, want the layout message", index, err)
		}
	}
}

func TestBGRXToRGB(t *testing.T) {
	// 2x2 BGRX pixels with 4 bytes of row padding (stride 12).
	src := []byte{
		10, 20, 30, 0, 40, 50, 60, 0, 0xEE, 0xEE, 0xEE, 0xEE,
		70, 80, 90, 0, 100, 110, 120, 0, 0xEE, 0xEE, 0xEE, 0xEE,
	}
	img, err := BGRXToRGB(src, 2, 2, 12)
	if err != nil {
		t.Fatalf("BGRXToRGB() error = %v", err)
	}
	want := []uint8{
		30, 20, 10, 0xFF, 60, 50, 40, 0xFF,
		90, 80, 70, 0xFF, 120, 110, 100, 0xFF,
	}
	for index, value := range want {
		if img.Pix[index] != value {
			t.Fatalf("pixel byte %d = %d, want %d", index, img.Pix[index], value)
		}
	}
}

func TestBGRXToRGBRejectsBadInput(t *testing.T) {
	if _, err := BGRXToRGB(make([]byte, 32), 0, 2, 8); err == nil {
		t.Fatal("zero width accepted")
	}
	if _, err := BGRXToRGB(make([]byte, 32), 2, 2, 7); err == nil {
		t.Fatal("stride smaller than the width accepted")
	}
	if _, err := BGRXToRGB(make([]byte, 8), 2, 2, 8); err == nil {
		t.Fatal("undersized buffer accepted")
	}
}

func TestScaleSameSizeReturnsInput(t *testing.T) {
	img, err := BGRXToRGB(make([]byte, 4*4*4), 4, 4, 16)
	if err != nil {
		t.Fatalf("BGRXToRGB() error = %v", err)
	}
	if got := Scale(img, 4, 4); got != img {
		t.Fatal("Scale() copied an already-sized image")
	}
}

func TestScaleDownscales(t *testing.T) {
	img, err := BGRXToRGB(make([]byte, 8*8*4), 8, 8, 32)
	if err != nil {
		t.Fatalf("BGRXToRGB() error = %v", err)
	}
	got := Scale(img, 4, 2)
	if got.Rect.Dx() != 4 || got.Rect.Dy() != 2 {
		t.Fatalf("Scale() size = %dx%d, want 4x2", got.Rect.Dx(), got.Rect.Dy())
	}
}
