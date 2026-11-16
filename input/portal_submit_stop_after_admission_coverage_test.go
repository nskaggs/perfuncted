//go:build linux

package input

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRemoteDesktopSubmitReportsStopAfterAdmissionWithoutReceipt(t *testing.T) {
	backend := syntheticQueuedPortal()
	result := make(chan error, 1)
	ran := false
	go func() {
		result <- backend.submit(context.Background(), func() error {
			ran = true
			return nil
		})
	}()
	var command remoteDesktopCommand
	select {
	case command = <-backend.commands:
	case <-time.After(time.Second):
		t.Fatal("submit did not hand off its command")
	}
	close(backend.stop)
	select {
	case err := <-result:
		if !errors.Is(err, ErrRemoteDesktopUnavailable) {
			t.Fatalf("stopped backend returned %v, want unavailable", err)
		}
	case <-time.After(time.Second):
		t.Fatal("submit remained blocked after backend stop")
	}
	if ran {
		t.Fatal("stopped backend executed an unacknowledged command")
	}
	if len(command.done) != 0 {
		t.Fatalf("unobserved writer receipt queue contains %d values", len(command.done))
	}
}
