//go:build windows

package session

import (
	"os"
	"os/signal"
	"syscall"
)

func (s *DirectSession) watchSignals() func() {
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGTERM, os.Interrupt)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case sig := <-signals:
				if sig == os.Interrupt {
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
