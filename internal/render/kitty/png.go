package kitty

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"image"
)

// palettePNG encodes packed RGB24 pixels as a paletted PNG, matching the
// role of Pillow's compress_level=1 output. The packing is hand-rolled:
// 8-bit indexed color, one filter-None scanline per row, zlib at best
// speed deflating straight into the output buffer (the IDAT length and CRC
// are backpatched afterwards). Skipping image/png's per-row filter search
// and intermediate buffers makes encoding noticeably cheaper on
// full-screen frames at a small size cost.
func palettePNG(rgb []byte, width, height int) ([]byte, error) {
	return encodePalettedPNG(quantizePaletted(rgb, width, height))
}

var pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}

func encodePalettedPNG(img *image.Paletted) ([]byte, error) {
	width := img.Rect.Dx()
	height := img.Rect.Dy()
	out := bytes.NewBuffer(make([]byte, 0, 4096+len(img.Palette)*3+width*height/4))
	out.Write(pngSignature)

	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(height))
	ihdr[8] = 8 // bit depth
	ihdr[9] = 3 // color type: indexed palette
	writePNGChunk(out, "IHDR", ihdr[:])

	plte := make([]byte, 0, len(img.Palette)*3)
	for _, entry := range img.Palette {
		r, g, b, _ := entry.RGBA()
		plte = append(plte, uint8(r>>8), uint8(g>>8), uint8(b>>8))
	}
	writePNGChunk(out, "PLTE", plte)

	// The IDAT length precedes the data, so reserve the field and
	// backpatch it once the deflated size is known.
	out.Write([]byte{0, 0, 0, 0})
	out.WriteString("IDAT")
	idatStart := out.Len()
	deflater, err := zlib.NewWriterLevel(out, zlib.BestSpeed)
	if err != nil {
		return nil, err
	}
	filterNone := []byte{0}
	for y := 0; y < height; y++ {
		if _, err := deflater.Write(filterNone); err != nil {
			return nil, err
		}
		start := y * img.Stride
		if _, err := deflater.Write(img.Pix[start : start+width]); err != nil {
			return nil, err
		}
	}
	if err := deflater.Close(); err != nil {
		return nil, err
	}

	var field [4]byte
	idat := out.Bytes()[idatStart-4:]
	binary.BigEndian.PutUint32(field[:], uint32(len(idat)-4))
	copy(out.Bytes()[idatStart-8:idatStart-4], field[:])
	crc := crc32.NewIEEE()
	crc.Write(idat)
	binary.BigEndian.PutUint32(field[:], crc.Sum32())
	out.Write(field[:])

	writePNGChunk(out, "IEND", nil)
	return out.Bytes(), nil
}

func writePNGChunk(out *bytes.Buffer, kind string, data []byte) {
	var field [4]byte
	binary.BigEndian.PutUint32(field[:], uint32(len(data)))
	out.Write(field[:])
	out.WriteString(kind)
	out.Write(data)
	crc := crc32.NewIEEE()
	crc.Write([]byte(kind))
	crc.Write(data)
	binary.BigEndian.PutUint32(field[:], crc.Sum32())
	out.Write(field[:])
}
