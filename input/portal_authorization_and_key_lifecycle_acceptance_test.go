package input

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestPortalRequestRejectsMalformedTypedResponsesAndMethodFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    []any
		failure string
	}{
		{name: "malformed response code", body: []any{"authorized", map[string]dbus.Variant{}}},
		{name: "malformed result map", body: []any{uint32(0), "not a result map"}},
		{name: "portal method failure", failure: "synthetic request rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, portal := startSyntheticPortalBus(t)
			portal.mu.Lock()
			portal.responseBody = tc.body
			portal.createFailure = tc.failure
			portal.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			name, err := portalUniqueName(client.Names())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := portalCreateSession(ctx, client, name); !errors.Is(err, ErrRemoteDesktopUnavailable) {
				t.Fatalf("malformed or failed portal request returned %v", err)
			}
		})
	}
}

func TestPortalCreateSessionAcceptsObjectPathAndVersionErrorsFailClosed(t *testing.T) {
	client, portal := startSyntheticPortalBus(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	portal.mu.Lock()
	portal.result = map[string]dbus.Variant{"session_handle": dbus.MakeVariant(dbus.ObjectPath(syntheticSessionPath))}
	portal.mu.Unlock()
	name, err := portalUniqueName(client.Names())
	if err != nil {
		t.Fatal(err)
	}
	path, err := portalCreateSession(ctx, client, name)
	if err != nil || path != syntheticSessionPath {
		t.Fatalf("object-path session handle = %q, %v", path, err)
	}
	portal.mu.Lock()
	portal.versionFailure = "synthetic property failure"
	portal.mu.Unlock()
	if err := requireRemoteDesktopV2(ctx, client); !errors.Is(err, ErrRemoteDesktopUnavailable) {
		t.Fatalf("portal version query error = %v", err)
	}
	portal.mu.Lock()
	portal.versionFailure = ""
	portal.getVersion = "2"
	portal.mu.Unlock()
	if err := requireRemoteDesktopV2(ctx, client); !errors.Is(err, ErrRemoteDesktopUnavailable) {
		t.Fatalf("wrong portal version type = %v", err)
	}
}

func TestPortalSessionLifecycleSubscriptionRequiresLiveOwner(t *testing.T) {
	client, portal := startSyntheticPortalBus(t)
	if _, err := portal.conn.ReleaseName(remoteDesktopBus); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := portalRequest(ctx, client, ":1.1", "CreateSession", []any{map[string]dbus.Variant{}}); !errors.Is(err, ErrRemoteDesktopUnavailable) {
		t.Fatalf("request without portal owner returned %v", err)
	}
	session := &portalSession{conn: client, path: dbus.ObjectPath(syntheticSessionPath)}
	if err := session.subscribeClosed(ctx); !errors.Is(err, ErrRemoteDesktopUnavailable) {
		t.Fatalf("lifecycle subscription without portal owner returned %v", err)
	}
}

func TestPortalTypeHonorsExplicitModifiedKeyHoldAndRelease(t *testing.T) {
	backend, server := newAuthorizedEISOperationFixture(t)
	ctx := context.Background()
	if err := backend.Type(ctx, "{ctrl+a down}{a up}"); err != nil {
		t.Fatalf("explicit modified hold/release: %v", err)
	}
	for i, want := range []struct {
		key   uint32
		state uint32
	}{{29, 1}, {30, 1}, {29, 0}, {30, 0}} {
		request, err := readSyntheticEISFrame(server)
		if err != nil {
			t.Fatal(err)
		}
		frame, frameErr := readSyntheticEISFrame(server)
		if frameErr != nil || request.object != 103 || request.opcode != 1 || binary.NativeEndian.Uint32(request.body[:4]) != want.key || binary.NativeEndian.Uint32(request.body[4:8]) != want.state || frame.object != 0xff00000000000012 || frame.opcode != 3 {
			t.Fatalf("transition %d = %+v frame=%+v err=%v want key/state %+v", i, request, frame, frameErr, want)
		}
	}
	if len(backend.heldKeys) != 0 {
		t.Fatalf("completed explicit key sequence left keys held: %v", backend.heldKeys)
	}
	if err := backend.Type(ctx, "{shift+left up}"); err != nil {
		t.Fatalf("temporary modifier around explicit release: %v", err)
	}
	for _, want := range []struct {
		key   uint32
		state uint32
	}{{42, 1}, {105, 0}, {42, 0}} {
		request, err := readSyntheticEISFrame(server)
		if err != nil {
			t.Fatal(err)
		}
		frame, frameErr := readSyntheticEISFrame(server)
		if frameErr != nil || request.object != 103 || request.opcode != 1 || binary.NativeEndian.Uint32(request.body[:4]) != want.key || binary.NativeEndian.Uint32(request.body[4:8]) != want.state || frame.object != 0xff00000000000012 || frame.opcode != 3 {
			t.Fatalf("modified release = %+v frame=%+v err=%v want key/state %+v", request, frame, frameErr, want)
		}
	}
	if len(backend.heldKeys) != 0 {
		t.Fatalf("modified release left temporary keys held: %v", backend.heldKeys)
	}
}
