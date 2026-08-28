package window

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"
)

func TestSwayWindowChangesUsesDedicatedSubscription(t *testing.T) {
	originalDial := swayDialContext
	t.Cleanup(func() {
		swayDialContext = originalDial
	})

	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = server.Close()
	})
	swayDialContext = func(
		context.Context,
		string,
		string,
	) (net.Conn, error) {
		return client, nil
	}

	serverErr := make(chan error, 1)
	go func() {
		messageType, payload, err := readSwayMessage(server)
		if err != nil {
			serverErr <- err
			return
		}
		if messageType != swayMsgSubscribe ||
			string(payload) != `["window"]` {
			serverErr <- fmt.Errorf(
				"subscribe = type %d payload %q",
				messageType,
				payload,
			)
			return
		}
		if err := writeSwayMessage(
			server,
			swayMsgSubscribe,
			`{"success":true}`,
		); err != nil {
			serverErr <- err
			return
		}
		if err := writeSwayMessage(
			server,
			1<<31|3,
			`{"change":"new"}`,
		); err != nil {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	manager := &SwayManager{sock: "test"}
	changes := manager.WindowChanges()
	select {
	case <-changes:
	case <-time.After(time.Second):
		t.Fatal("WindowChanges did not receive sway event")
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("subscription server: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSwayWindowChangesRejectsUnexpectedSubscriptionResponse(t *testing.T) {
	originalDial := swayDialContext
	t.Cleanup(func() {
		swayDialContext = originalDial
	})

	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = server.Close()
	})
	swayDialContext = func(
		context.Context,
		string,
		string,
	) (net.Conn, error) {
		return client, nil
	}

	serverErr := make(chan error, 1)
	go func() {
		messageType, _, err := readSwayMessage(server)
		if err != nil {
			serverErr <- err
			return
		}
		if messageType != swayMsgSubscribe {
			serverErr <- fmt.Errorf("subscribe request type = %d, want %d", messageType, swayMsgSubscribe)
			return
		}
		serverErr <- writeSwayMessage(server, swayMsgGetTree, `{"unexpected":true}`)
	}()

	manager := &SwayManager{sock: "test"}
	changes := manager.WindowChanges()
	select {
	case _, ok := <-changes:
		if ok {
			t.Fatal("WindowChanges emitted an event after an invalid subscription response")
		}
	case <-time.After(time.Second):
		t.Fatal("WindowChanges did not stop after an invalid subscription response")
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("subscription server: %v", err)
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestSwayWindowChangesCloseCancelsInitialDial(t *testing.T) {
	originalDial := swayDialContext
	t.Cleanup(func() {
		swayDialContext = originalDial
	})

	dialStarted := make(chan struct{})
	releaseDial := make(chan struct{})
	swayDialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		close(dialStarted)
		if ctx.Done() == nil {
			<-releaseDial
			return nil, errors.New("dial released")
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}

	manager := &SwayManager{sock: "test"}
	manager.WindowChanges()
	<-dialStarted

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- manager.Close()
	}()

	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		close(releaseDial)
		t.Fatal("Close did not cancel the initial event-subscription dial")
	}
}
