package window

import (
	"context"
	"net"
	"testing"
	"time"
)

// The first Sway subscribe reply must explicitly confirm success. An IPC
// socket that rejects subscription is not a usable event source, even if it
// sends later unrelated messages; the channel should terminate promptly.
func TestSwaySubscriptionRejectsDeniedOrMalformedAcks(t *testing.T) {
	for _, ack := range []string{`{"success":false}`, `{}`, `{"success":"true"}`, `not-json`} {
		t.Run(ack, func(t *testing.T) {
			original := swayDialContext
			t.Cleanup(func() { swayDialContext = original })
			client, server := net.Pipe()
			defer server.Close()
			swayDialContext = func(context.Context, string, string) (net.Conn, error) { return client, nil }
			done := make(chan struct{})
			go func() {
				defer close(done)
				if _, _, err := readSwayMessage(server); err != nil {
					return
				}
				_ = writeSwayMessage(server, swayMsgSubscribe, ack)
			}()
			m := &SwayManager{sock: "synthetic"}
			t.Cleanup(func() { _ = m.Close() })
			events := m.WindowChanges()
			select {
			case _, ok := <-events:
				if ok {
					t.Fatal("rejected subscription emitted event")
				}
			case <-time.After(200 * time.Millisecond):
				t.Fatal("denied or malformed subscribe reply left event stream open")
			}
			<-done
		})
	}
}

// Sway multiplexes IPC notifications using the top bit and distinct event
// opcodes. Workspace and output events cannot satisfy a window change watch.
func TestSwayWindowChangesIgnoresUnrelatedIPCEvents(t *testing.T) {
	original := swayDialContext
	t.Cleanup(func() { swayDialContext = original })
	client, server := net.Pipe()
	defer server.Close()
	swayDialContext = func(context.Context, string, string) (net.Conn, error) { return client, nil }
	workspaceSent := make(chan struct{})
	sendWindow := make(chan struct{})
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
		if err := writeSwayMessage(server, 1<<31|0, `{"change":"workspace"}`); err != nil {
			serverDone <- err
			return
		}
		close(workspaceSent)
		<-sendWindow
		serverDone <- writeSwayMessage(server, 1<<31|3, `{"change":"focus"}`)
	}()
	m := &SwayManager{sock: "synthetic"}
	events := m.WindowChanges()
	defer m.Close()
	<-workspaceSent
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("workspace event falsely reported as window change")
		}
		t.Fatal("subscription closed on unrelated event")
	case <-time.After(35 * time.Millisecond):
	}
	close(sendWindow)
	select {
	case _, ok := <-events:
		if !ok {
			t.Fatal("real window change lost")
		}
	case <-time.After(time.Second):
		t.Fatal("window change not delivered")
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}
