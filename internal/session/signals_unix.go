//go:build !windows

package session

import (
	"os"
	"os/signal"
	"syscall"
)

// watchSignals wires SIGHUP/SIGTERM to stop and SIGINT to interrupt; the
// returned stop function restores the default disposition.
func (s *DirectSession) watchSignals() func() {
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-signals:
				if sig == syscall.SIGINT {
					s.interrupted.Store(true)
				}
				s.requestStop()
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(signals)
		close(done)
	}
}
