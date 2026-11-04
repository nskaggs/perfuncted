//go:build linux

package window

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/gnomebridge"
)

// Closing a session from several owners must not double-close its event stream
// or leave a native event subscription alive.
func TestGnomeNativeManagerConcurrentCloseReleasesSubscriptionOnce(t *testing.T) {
	source := make(chan gnomebridge.WindowEvent)
	var releases atomic.Int32
	m := &GnomeNativeManager{
		bridge:     &gnomebridge.Client{},
		events:     make(chan Event, 1),
		stopEvents: make(chan struct{}),
		eventsDone: make(chan struct{}),
	}
	go m.forwardWindowEvents(source, func() { releases.Add(1) })
	var wg sync.WaitGroup
	failures := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			failures <- m.Close()
		}()
	}
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent close blocked waiting for active GNOME events")
	}
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatalf("close returned error: %v", err)
		}
	}
	if n := releases.Load(); n != 1 {
		t.Fatalf("subscription released %d times, want once", n)
	}
	if _, ok := <-m.WindowEvents(); ok {
		t.Fatal("event stream remains open after concurrent close")
	}
}
