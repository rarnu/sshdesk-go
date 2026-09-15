package gnome

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Regression: stop() used to nil s.waitDone while the wait goroutine still
// closed it through the struct field, panicking with "close of nil channel"
// whenever the stream ended around process exit.
func TestStreamStopDoesNotRaceWaitGoroutine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stub")
	}
	stub := filepath.Join(t.TempDir(), "gst-launch-stub")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		s := &streamCapture{
			executable:  stub,
			targetWidth: 4, targetHeight: 2,
		}
		if _, err := s.Capture(); err == nil {
			t.Fatal("Capture() unexpectedly succeeded against the stub")
		}
		s.Close()
	}
}
