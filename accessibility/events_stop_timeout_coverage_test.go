package accessibility

import (
	"testing"
	"time"
)

func TestStopEventsReturnsWhenSetupOrDispatcherOwnerStalls(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup bool
	}{
		{name: "setup owner", setup: true},
		{name: "dispatcher owner"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			canceled := make(chan struct{})
			ownerDone := make(chan struct{})
			backend := &dbusBackend{}
			if tc.setup {
				backend.eventStarting = &eventStart{done: ownerDone}
				backend.eventStartStop = func() { close(canceled) }
			} else {
				backend.eventDone = ownerDone
				backend.eventCancel = func() { close(canceled) }
			}

			returned := make(chan struct{})
			go func() {
				backend.stopEvents(nil)
				close(returned)
			}()
			select {
			case <-canceled:
			case <-time.After(time.Second):
				t.Fatal("shutdown did not cancel the stalled event owner")
			}
			select {
			case <-returned:
			case <-time.After(2 * eventCleanupTimeout):
				t.Fatal("shutdown remained blocked after the cleanup deadline")
			}
			select {
			case <-ownerDone:
				t.Fatal("stalled event owner completed unexpectedly")
			default:
			}
			close(ownerDone)
		})
	}
}
