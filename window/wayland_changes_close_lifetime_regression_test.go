package window

import (
	"sync"
	"testing"
	"time"
)

// A compositor event channel must not outlive its backend. Otherwise a waiter
// without a caller deadline can be stranded after the window manager closes.
func TestWaylandWindowChangesCloseEndsPreexistingAndLateSubscriptions(t *testing.T) {
	for _, priorSubscription := range []bool{true, false} {
		t.Run(map[bool]string{true: "subscribed before close", false: "subscribed after close"}[priorSubscription], func(t *testing.T) {
			m := &WaylandWindowManager{}
			var events <-chan struct{}
			if priorSubscription {
				events = m.WindowChanges()
			}
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			if !priorSubscription {
				events = m.WindowChanges()
			}
			if events == nil {
				t.Fatal("manager did not provide a closable event stream")
			}
			select {
			case _, open := <-events:
				if open {
					t.Fatal("closed manager emitted a usable event")
				}
			case <-time.After(50 * time.Millisecond):
				t.Fatal("closed Wayland manager left subscribers waiting")
			}
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			m.notifyWindowChange()
			select {
			case _, open := <-events:
				if open {
					t.Fatal("closed manager emitted a late event")
				}
			default:
				t.Fatal("close did not keep event stream terminated")
			}
		})
	}
}

// WindowChanges, Close, and foreign-toplevel event dispatch can race. Every
// subscriber must receive a terminating close without panics from late sends.
func TestWaylandWindowChangesConcurrentSubscribeNotifyAndClose(t *testing.T) {
	const attempts = 32
	for trial := 0; trial < 4; trial++ {
		m := &WaylandWindowManager{}
		start := make(chan struct{})
		var group sync.WaitGroup
		for i := 0; i < attempts; i++ {
			group.Add(3)
			go func() { defer group.Done(); <-start; m.notifyWindowChange() }()
			go func() { defer group.Done(); <-start; _ = m.WindowChanges() }()
			go func() { defer group.Done(); <-start; _ = m.Close() }()
		}
		close(start)
		group.Wait()
		changes := m.WindowChanges()
		terminated := false
		for i := 0; i < 2; i++ {
			select {
			case _, open := <-changes:
				if !open {
					terminated = true
				}
			default:
				t.Fatalf("trial %d: change stream did not terminate", trial)
			}
			if terminated {
				break
			}
		}
		if !terminated {
			t.Fatalf("trial %d: change stream remained active after Close", trial)
		}
	}
}

func TestWaylandWindowChangesDropPendingNotificationsOnShutdown(t *testing.T) {
	m := &WaylandWindowManager{}
	events := m.WindowChanges()
	m.notifyWindowChange()
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case _, open := <-events:
		if open {
			t.Fatal("closing the compositor delivered a stale buffered window change")
		}
	default:
		t.Fatal("close left subscribed stream open")
	}
}
