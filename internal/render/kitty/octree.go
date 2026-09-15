package kitty

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"runtime"
	"sync"
)

// This is a self-contained octree color quantizer standing in for Pillow's
// Image.Quantize.FASTOCTREE with no dithering. The resulting palette and PNG
// bytes are not byte-identical to Pillow but visually and structurally
// equivalent: at most maxColors palette entries, each pixel mapped to the
// average color of its octree leaf.
//
// Nodes live in a shared slice addressed by uint32 index (0 is the nil
// sentinel, 1 the root) instead of pointers, so building the tree allocates
// amortized O(1) instead of one object per node. Subtrees detached by
// reduction go onto a free list and are reused; reuse never changes which
// nodes exist or the order reducible entries are appended, so the palette
// stays identical to a pointer-based implementation.

type octNode struct {
	children   [8]uint32
	childCount int
	leaf       bool
	count      int
	r, g, b    int
	index      int
}

type octree struct {
	nodes     []octNode
	free      []uint32
	leaves    int
	reducible [8][]uint32
	maxColors int
}

func newOctree(maxColors int) *octree {
	nodes := make([]octNode, 2, 2048)
	return &octree{nodes: nodes, maxColors: maxColors}
}

func (q *octree) newNode() uint32 {
	if n := len(q.free); n > 0 {
		index := q.free[n-1]
		q.free = q.free[:n-1]
		q.nodes[index] = octNode{}
		return index
	}
	q.nodes = append(q.nodes, octNode{})
	return uint32(len(q.nodes) - 1)
}

func colorIndex(r, g, b uint8, level int) int {
	shift := uint(7 - level)
	return int(((r>>shift)&1)<<2 | ((g>>shift)&1)<<1 | ((b >> shift) & 1))
}

func (q *octree) insert(r, g, b uint8) {
	// Index into q.nodes on every access: newNode may reallocate the slice.
	ni := uint32(1)
	for level := 0; level < 8; level++ {
		if q.nodes[ni].leaf {
			q.nodes[ni].accumulate(r, g, b)
			return
		}
		index := colorIndex(r, g, b, level)
		ci := q.nodes[ni].children[index]
		if ci == 0 {
			ci = q.newNode()
			q.nodes[ni].children[index] = ci
			if q.nodes[ni].childCount == 0 {
				q.reducible[level] = append(q.reducible[level], ni)
			}
			q.nodes[ni].childCount++
		}
		ni = ci
	}
	if !q.nodes[ni].leaf {
		q.nodes[ni].leaf = true
		q.leaves++
	}
	q.nodes[ni].accumulate(r, g, b)
	// Each reduction eliminates one internal node, so the loop always
	// terminates; single-child merges net zero leaves but move the collapse
	// one level closer to a branching node.
	for q.leaves > q.maxColors && q.reduce() {
	}
}

func (n *octNode) accumulate(r, g, b uint8) {
	n.count++
	n.r += int(r)
	n.g += int(g)
	n.b += int(b)
}

// collectStats sums the accumulated pixels and counts the leaves below a
// node, including the node itself when it is a leaf. Every visited node is
// pushed onto the free list; the caller detaches the subtree.
func (q *octree) collectStats(ni uint32) (count, r, g, b, leaves int) {
	node := &q.nodes[ni]
	if node.leaf {
		q.free = append(q.free, ni)
		return node.count, node.r, node.g, node.b, 1
	}
	for _, ci := range node.children {
		if ci == 0 {
			continue
		}
		childCount, childR, childG, childB, childLeaves := q.collectStats(ci)
		count += childCount
		r += childR
		g += childG
		b += childB
		leaves += childLeaves
	}
	q.free = append(q.free, ni)
	return count, r, g, b, leaves
}

// reduce merges the children of the deepest reducible node into itself.
// It reports whether any node was collapsed.
func (q *octree) reduce() bool {
	for level := 7; level >= 0; level-- {
		list := q.reducible[level]
		for len(list) > 0 {
			ni := list[len(list)-1]
			list = list[:len(list)-1]
			node := &q.nodes[ni]
			if node.leaf || node.childCount == 0 {
				continue
			}
			for i, ci := range node.children {
				if ci == 0 {
					continue
				}
				// collectStats only appends to q.free, never to q.nodes,
				// so the node pointer stays valid across the call.
				count, r, g, b, leaves := q.collectStats(ci)
				node.count += count
				node.r += r
				node.g += g
				node.b += b
				node.children[i] = 0
				q.leaves -= leaves
			}
			node.childCount = 0
			node.leaf = true
			q.leaves++
			q.reducible[level] = list
			return true
		}
		q.reducible[level] = list
	}
	return false
}

// palette collects the leaf colors in deterministic tree order.
func (q *octree) palette() color.Palette {
	var palette color.Palette
	var walk func(ni uint32)
	walk = func(ni uint32) {
		node := &q.nodes[ni]
		if node.leaf {
			count := max(1, node.count)
			node.index = len(palette)
			palette = append(palette, color.RGBA{
				R: uint8((node.r + count/2) / count),
				G: uint8((node.g + count/2) / count),
				B: uint8((node.b + count/2) / count),
				A: 0xFF,
			})
			return
		}
		for _, ci := range node.children {
			if ci != 0 {
				walk(ci)
			}
		}
	}
	walk(1)
	return palette
}

// lookup walks the tree to the leaf holding the color.
func (q *octree) lookup(r, g, b uint8) int {
	ni := uint32(1)
	for level := 0; level < 8 && !q.nodes[ni].leaf; level++ {
		next := q.nodes[ni].children[colorIndex(r, g, b, level)]
		if next == 0 {
			break
		}
		ni = next
	}
	return q.nodes[ni].index
}

// mapPixels writes palette indices for rows [y0, y1). Colors sharing the
// same r4g4b4 bucket share one lookup through a private cache, so rows are
// independent and several strips can run in parallel. The cache makes the
// mapping deterministic for a fixed strip layout.
func (q *octree) mapPixels(rgb, pix []byte, y0, y1, width int) {
	var cache [4096]uint8
	var filled [4096]bool
	for y := y0; y < y1; y++ {
		row := y * width
		for x := 0; x < width; x++ {
			offset := (row + x) * 3
			r, g, b := rgb[offset], rgb[offset+1], rgb[offset+2]
			key := uint16(r>>4)<<8 | uint16(g>>4)<<4 | uint16(b>>4)
			if !filled[key] {
				cache[key] = uint8(q.lookup(r, g, b))
				filled[key] = true
			}
			pix[row+x] = cache[key]
		}
	}
}

// quantizePaletted converts packed RGB24 pixels into a paletted image with
// at most 128 colors.
func quantizePaletted(rgb []byte, width, height int) *image.Paletted {
	tree := newOctree(128)
	for offset := 0; offset+2 < len(rgb); offset += 3 {
		tree.insert(rgb[offset], rgb[offset+1], rgb[offset+2])
	}
	palette := tree.palette()
	if len(palette) == 0 {
		palette = color.Palette{color.RGBA{A: 0xFF}}
	}
	img := image.NewPaletted(image.Rect(0, 0, width, height), palette)
	pixels := width * height
	const minParallelPixels = 1 << 16
	if workers := min(4, runtime.NumCPU()); pixels >= minParallelPixels && workers > 1 {
		var wg sync.WaitGroup
		for w := 0; w < workers; w++ {
			y0 := height * w / workers
			y1 := height * (w + 1) / workers
			wg.Add(1)
			go func() {
				defer wg.Done()
				tree.mapPixels(rgb, img.Pix, y0, y1, width)
			}()
		}
		wg.Wait()
	} else {
		tree.mapPixels(rgb, img.Pix, 0, height, width)
	}
	return img
}

// palettePNG encodes packed RGB24 pixels as a paletted PNG with fast
// compression, matching Pillow's compress_level=1 output role.
func palettePNG(rgb []byte, width, height int) ([]byte, error) {
	img := quantizePaletted(rgb, width, height)
	var buffer bytes.Buffer
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := encoder.Encode(&buffer, img); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
