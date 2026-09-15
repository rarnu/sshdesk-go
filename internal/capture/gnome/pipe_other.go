//go:build !linux

package gnome

import "os"

func growPipe(*os.File, int) {}
