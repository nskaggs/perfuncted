//go:build linux

package input

import (
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/bendahl/uinput"
)

// Shutdown can win while the lazy mouse factory is executing. Its acquired
// device must be rolled back, and a failed rollback must remain observable.
func TestUinputLazyMouseFactoryShutdownRetainsCleanupFailure(t *testing.T) {
	b := &UinputBackend{}
	cleanupErr := errors.New("synthetic late mouse close failure")
	mouse := &recordingMouse{closeErr: cleanupErr}
	factoryEntered := make(chan struct{})
	releaseFactory := make(chan struct{})
	oldFactory := createUinputMouse
	createUinputMouse = func(string, []byte) (uinput.Mouse, error) {
		close(factoryEntered)
		<-releaseFactory
		return mouse, nil
	}
	t.Cleanup(func() { createUinputMouse = oldFactory })

	creationDone := make(chan error, 1)
	go func() {
		creationDone <- b.withMouse(func(uinput.Mouse) error {
			return errors.New("mouse callback must not execute after shutdown")
		})
	}()
	<-factoryEntered
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- b.Close() }()
	deadline := time.Now().Add(3 * time.Second)
	for !b.operations.Closed() {
		if time.Now().After(deadline) {
			close(releaseFactory)
			t.Fatal("shutdown did not start")
		}
		runtime.Gosched()
	}
	close(releaseFactory)
	if err := <-creationDone; !errors.Is(err, errUinputBackendClosed) || !errors.Is(err, cleanupErr) {
		t.Fatalf("lost shutdown or mouse cleanup failure: %v", err)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("shutdown should not own factory's unpublished device: %v", err)
	}
	if b.mouse != nil {
		t.Fatal("closed backend published a late-created mouse")
	}
	if events := mouse.eventsSnapshot(); len(events) != 1 || events[0] != "close" {
		t.Fatalf("late mouse was not closed exactly once: %v", events)
	}
}
