package kitty

import "sync"

// bufferPool recycles the large transient byte slices of the encode path:
// tile crops, paletted pixel indices, PNG payloads, and base64 text.
var bufferPool = sync.Pool{New: func() any { return []byte(nil) }}

// getBuffer returns a zero-length slice with room for size bytes.
func getBuffer(size int) []byte {
	buf := bufferPool.Get().([]byte)
	if cap(buf) < size {
		return make([]byte, 0, size)
	}
	return buf[:0]
}

// putBuffer returns a buffer obtained from getBuffer. Oversized buffers
// are dropped so a single full-screen frame cannot pin the pool.
func putBuffer(buf []byte) {
	const maxRetained = 32 << 20
	if cap(buf) == 0 || cap(buf) > maxRetained {
		return
	}
	bufferPool.Put(buf[:0])
}
