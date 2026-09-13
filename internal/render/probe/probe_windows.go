//go:build windows

package probe

// IoctlPixelSize always reports unknown pixel geometry on Windows.
func IoctlPixelSize(int) (int, int) { return 0, 0 }

// ProbeGraphics always reports an unavailable probe on Windows, matching the
// Python implementation which only probes on POSIX terminals.
func ProbeGraphics(int, int, int, int, float64) GraphicsProbe {
	return GraphicsProbe{}
}
