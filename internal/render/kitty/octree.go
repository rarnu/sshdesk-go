package kitty

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
)

// This is a self-contained octree color quantizer standing in for Pillow's
// Image.Quantize.FASTOCTREE with no dithering. The resulting palette and PNG
// bytes are not byte-identical to Pillow but visually and structurally
// equivalent: at most maxColors palette entries, each pixel mapped to the
// average color of its octree leaf.

type octNode struct {
	children   [8]*octNode
	childCount int
	leaf       bool
	count      int
	r, g, b    int
	index      int
}

type octree struct {
	root      *octNode
	leaves    int
	reducible [8][]*octNode
	maxColors int
}

func newOctree(maxColors int) *octree {
	return &octree{root: &octNode{}, maxColors: maxColors}
}

func colorIndex(r, g, b uint8, level int) int {
	shift := uint(7 - level)
	return int(((r>>shift)&1)<<2 | ((g>>shift)&1)<<1 | ((b >> shift) & 1))
}

func (q *octree) insert(r, g, b uint8) {
	node := q.root
	for level := 0; level < 8; level++ {
		if node.leaf {
			node.accumulate(r, g, b)
			return
		}
		index := colorIndex(r, g, b, level)
		child := node.children[index]
		if child == nil {
			child = &octNode{}
			node.children[index] = child
			if node.childCount == 0 {
				q.reducible[level] = append(q.reducible[level], node)
			}
			node.childCount++
		}
		node = child
	}
	if !node.leaf {
		node.leaf = true
		q.leaves++
	}
	node.accumulate(r, g, b)
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

// subtreeStats sums the accumulated pixels and counts the leaves below a
// node, including the node itself when it is a leaf.
func subtreeStats(node *octNode) (count, r, g, b, leaves int) {
	if node.leaf {
		return node.count, node.r, node.g, node.b, 1
	}
	for _, child := range node.children {
		if child == nil {
			continue
		}
		childCount, childR, childG, childB, childLeaves := subtreeStats(child)
		count += childCount
		r += childR
		g += childG
		b += childB
		leaves += childLeaves
	}
	return count, r, g, b, leaves
}

// reduce merges the children of the deepest reducible node into itself.
// It reports whether any node was collapsed.
func (q *octree) reduce() bool {
	for level := 7; level >= 0; level-- {
		list := q.reducible[level]
		for len(list) > 0 {
			node := list[len(list)-1]
			list = list[:len(list)-1]
			if node.leaf || node.childCount == 0 {
				continue
			}
			for i, child := range node.children {
				if child == nil {
					continue
				}
				count, r, g, b, leaves := subtreeStats(child)
				node.count += count
				node.r += r
				node.g += g
				node.b += b
				// Detached nodes must look stale if they still sit in a
				// reducible list.
				child.childCount = 0
				child.leaf = true
				node.children[i] = nil
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
	var walk func(node *octNode)
	walk = func(node *octNode) {
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
		for _, child := range node.children {
			if child != nil {
				walk(child)
			}
		}
	}
	walk(q.root)
	return palette
}

// lookup walks the tree to the leaf holding the color.
func (q *octree) lookup(r, g, b uint8) int {
	node := q.root
	for level := 0; level < 8 && !node.leaf; level++ {
		next := node.children[colorIndex(r, g, b, level)]
		if next == nil {
			break
		}
		node = next
	}
	return node.index
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
	for pixel := 0; pixel < width*height; pixel++ {
		offset := pixel * 3
		img.Pix[pixel] = uint8(tree.lookup(rgb[offset], rgb[offset+1], rgb[offset+2]))
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
