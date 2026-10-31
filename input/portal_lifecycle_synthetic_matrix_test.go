package input

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func newSyntheticRevocableRemoteDesktop(t *testing.T) (*RemoteDesktopBackend, *portalSession, *eiClient) {
	t.Helper()
	connection, peer := unixConnectionPair(t)
	t.Cleanup(func() { _ = peer.Close() })
	client := &eiClient{conn: connection, messages: make(chan eiMessage, 8), done: make(chan struct{}), stop: make(chan struct{})}
	portal := &portalSession{path: dbus.ObjectPath("/org/freedesktop/portal/desktop/session/fixture"), signals: make(chan *dbus.Signal, 8)}
	sender := &eiSender{client: client, connection: 0xff00000000000010, seat: 0xff00000000000011, keyboard: 101, button: 102, scroll: 103, text: 104, keyboardDev: 0xff00000000000012, buttonDev: 0xff00000000000012, scrollDev: 0xff00000000000012, textDev: 0xff00000000000012}
	b := newRemoteDesktopBackend(sender, portal)
	t.Cleanup(func() { _ = b.Close() })
	return b, portal, client
}

func TestRemoteDesktopSyntheticSessionAndEISLifecycleFailuresRevokeAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name     string
		trigger  func(*portalSession, *eiClient)
		contains string
	}{
		{"portal closed", func(p *portalSession, _ *eiClient) {
			p.signals <- &dbus.Signal{Name: portalSessionInterface + ".Closed", Path: p.path}
		}, "portal session closed"},
		{"portal stream closed", func(p *portalSession, _ *eiClient) { close(p.signals) }, "portal session signal stream ended"},
		{"EIS connection disconnect", func(_ *portalSession, c *eiClient) { c.messages <- eiMessage{object: 0xff00000000000010, opcode: 0} }, "EIS session ended"},
		{"selected keyboard suspended", func(_ *portalSession, c *eiClient) { c.messages <- eiMessage{object: 0xff00000000000012, opcode: 8} }, "EIS session ended"},
		{"selected seat removed", func(_ *portalSession, c *eiClient) { c.messages <- eiMessage{object: 0xff00000000000011, opcode: 0} }, "EIS session ended"},
		{"EIS read stream terminated", func(_ *portalSession, c *eiClient) { c.setReadError(errors.New("malformed EIS frame")); close(c.done) }, "malformed EIS frame"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, p, c := newSyntheticRevocableRemoteDesktop(t)
			tc.trigger(p, c)
			select {
			case <-b.revoked:
			case <-time.After(2 * time.Second):
				t.Fatal("authorization remained usable after terminal event")
			}
			if err := b.stateError(); !errors.Is(err, ErrRemoteDesktopRevoked) || !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("wrong revocation: %v", err)
			}
			if err := b.KeyDown(context.Background(), "a"); !errors.Is(err, ErrRemoteDesktopRevoked) {
				t.Fatalf("revoked session accepted keyboard input: %v", err)
			}
			select {
			case <-b.writerDone:
			case <-time.After(2 * time.Second):
				t.Fatal("writer survived authorization loss")
			}
			// Cleanup remains idempotent even after concurrent read-loop and portal loss.
			if err := b.Close(); err != nil {
				t.Fatalf("revoked backend cleanup: %v", err)
			}
			if err := b.Close(); err != nil {
				t.Fatalf("repeated cleanup: %v", err)
			}
		})
	}
}

func TestRemoteDesktopSyntheticUnrelatedSessionSignalsDoNotRevokeAccess(t *testing.T) {
	b, p, c := newSyntheticRevocableRemoteDesktop(t)
	p.signals <- &dbus.Signal{Name: portalSessionInterface + ".Closed", Path: dbus.ObjectPath("/org/freedesktop/portal/desktop/session/other")}
	p.signals <- &dbus.Signal{Name: "org.example.Other.Event", Path: p.path}
	c.messages <- eiMessage{object: 1, opcode: 32}
	deadline := time.After(20 * time.Millisecond)
	select {
	case <-b.revoked:
		t.Fatal("unrelated signal revoked the authorized session")
	case <-deadline:
	}
	if err := b.stateError(); err != nil {
		t.Fatalf("unrelated signals invalidated session: %v", err)
	}
	p.signals <- &dbus.Signal{Name: portalSessionInterface + ".Closed", Path: p.path}
	select {
	case <-b.revoked:
	case <-time.After(time.Second):
		t.Fatal("session closure was ignored")
	}
}

func TestRemoteDesktopPreCanceledQueryDoesNotExposeAuthoritativeRegion(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := b.PointerCoordinateSpace(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled coordinate metadata returned %v", err)
	}
}

var _ net.Conn = (*net.UnixConn)(nil)
