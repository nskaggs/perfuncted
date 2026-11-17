package window

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

type swaySubscriptionFaultConn struct {
	net.Conn
	deadlineCalls atomic.Int32
	failDeadline  int32
	writeErr      error
}

func (c *swaySubscriptionFaultConn) SetDeadline(deadline time.Time) error {
	if c.deadlineCalls.Add(1) == c.failDeadline {
		return errors.New("synthetic subscription deadline failure")
	}
	return c.Conn.SetDeadline(deadline)
}

func (c *swaySubscriptionFaultConn) Write(p []byte) (int, error) {
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	return c.Conn.Write(p)
}

func awaitSwaySubscriptionStop(t *testing.T, manager *SwayManager) {
	t.Helper()
	select {
	case <-manager.eventDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Sway subscription did not terminate after transport failure")
	}
}

func TestSwaySubscriptionTransportFailuresAndEventCoalescing(t *testing.T) {
	for _, tc := range []struct {
		name         string
		failDeadline int32
		writeErr     error
		acknowledge  bool
	}{
		{name: "initial deadline failure", failDeadline: 1},
		{name: "subscribe write failure", writeErr: errors.New("synthetic subscribe write failure")},
		{name: "deadline reset failure", failDeadline: 2, acknowledge: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			originalDial := swayDialContext
			t.Cleanup(func() { swayDialContext = originalDial })
			client, server := net.Pipe()
			t.Cleanup(func() { _ = server.Close() })
			faultConn := &swaySubscriptionFaultConn{
				Conn:         client,
				failDeadline: tc.failDeadline,
				writeErr:     tc.writeErr,
			}
			swayDialContext = func(context.Context, string, string) (net.Conn, error) {
				return faultConn, nil
			}

			manager := &SwayManager{sock: "synthetic"}
			events := manager.WindowChanges()
			t.Cleanup(func() { _ = manager.Close() })
			serverDone := make(chan error, 1)
			if tc.acknowledge {
				go func() {
					if _, _, err := readSwayMessage(server); err != nil {
						serverDone <- err
						return
					}
					serverDone <- writeSwayMessage(server, swayMsgSubscribe, `{"success":true}`)
				}()
			}
			awaitSwaySubscriptionStop(t, manager)
			select {
			case _, ok := <-events:
				if ok {
					t.Fatal("failed Sway subscription published an event")
				}
			default:
				t.Fatal("failed Sway subscription left its event stream open")
			}
			if tc.acknowledge {
				if err := <-serverDone; err != nil {
					t.Fatalf("synthetic Sway peer: %v", err)
				}
			}
		})
	}

	t.Run("pending window changes coalesce", func(t *testing.T) {
		originalDial := swayDialContext
		t.Cleanup(func() { swayDialContext = originalDial })
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		swayDialContext = func(context.Context, string, string) (net.Conn, error) {
			return client, nil
		}

		manager := &SwayManager{sock: "synthetic"}
		events := manager.WindowChanges()
		t.Cleanup(func() { _ = manager.Close() })
		serverDone := make(chan error, 1)
		go func() {
			if _, _, err := readSwayMessage(server); err != nil {
				serverDone <- err
				return
			}
			if err := writeSwayMessage(server, swayMsgSubscribe, `{"success":true}`); err != nil {
				serverDone <- err
				return
			}
			for range 4 {
				if err := writeSwayMessage(server, 1<<31|3, `{"change":"focus"}`); err != nil {
					serverDone <- err
					return
				}
			}
			_ = server.Close()
			serverDone <- nil
		}()

		awaitSwaySubscriptionStop(t, manager)
		if err := <-serverDone; err != nil {
			t.Fatalf("synthetic Sway peer: %v", err)
		}
		count := 0
		for range events {
			count++
		}
		if count != 1 {
			t.Fatalf("pending window changes were not coalesced: received %d hints", count)
		}
	})
}
