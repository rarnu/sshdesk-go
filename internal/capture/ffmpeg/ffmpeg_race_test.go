package ffmpeg

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Regression: stop() used to nil c.waitDone while the wait goroutine still
// closed it through the struct field, panicking with "close of nil channel"
// whenever the stream ended or the session detached around process exit.
func TestStopDoesNotRaceWaitGoroutine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stub")
	}
	stub := filepath.Join(t.TempDir(), "ffmpeg-stub")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		c, err := New(stub, ":0", 4, 2)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Capture(); err == nil {
			t.Fatal("Capture() unexpectedly succeeded against the stub")
		}
		c.Close()
	}
}
