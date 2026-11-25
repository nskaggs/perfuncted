package input

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/testutil"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func newSyntheticRevocableRemoteDesktop(t *testing.T) (*RemoteDesktopBackend, *portalSession, *eiClient) {
	t.Helper()
	connection, peer := unixConnectionPair(t)
	t.Cleanup(func() { _ = peer.Close() })
	client := &eiClient{conn: connection, messages: make(chan eiMessage, 8), done: make(chan struct{}), stop: make(chan struct{})}
	portal := &portalSession{owner: ":1.44", path: dbus.ObjectPath("/org/freedesktop/portal/desktop/session/fixture"), signals: make(chan *dbus.Signal, 8)}
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
			p.signals <- &dbus.Signal{Name: portalSessionInterface + ".Closed", Path: p.path, Sender: p.owner}
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
	p.signals <- &dbus.Signal{Name: portalSessionInterface + ".Closed", Path: dbus.ObjectPath("/org/freedesktop/portal/desktop/session/other"), Sender: p.owner}
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
	p.signals <- &dbus.Signal{Name: portalSessionInterface + ".Closed", Path: p.path, Sender: p.owner}
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

// Session capability discovery must reflect actual EIS objects. Advertising
// click when only pointer motion was granted leads callers to assume a
// permission they never obtained and to send protocol object-zero frames.
func TestRemoteDesktopAdvertisesOnlyActuallyBoundEISCapabilities(t *testing.T) {
	if ops := (&RemoteDesktopBackend{}).SupportedOperations(); len(ops) != 0 {
		t.Errorf("incomplete portal advertised nonexistent operations: %v", ops)
	}
	for _, tc := range []struct {
		name    string
		disable func(*eiSender)
		absent  []string
	}{
		{"keyboard object unavailable", func(s *eiSender) { s.keyboard = 0 }, []string{"keyboard"}},
		{"keyboard device unavailable", func(s *eiSender) { s.keyboardDev = 0 }, []string{"keyboard"}},
		{"button object unavailable", func(s *eiSender) { s.button = 0 }, []string{"click", "drag"}},
		{"button device unavailable", func(s *eiSender) { s.buttonDev = 0 }, []string{"click", "drag"}},
		{"pointer object unavailable", func(s *eiSender) { s.pointers[0].pointer = 0 }, []string{"pointer", "click", "drag", "pointer-coordinate-space"}},
		{"pointer device unavailable", func(s *eiSender) { s.pointers[0].device = 0 }, []string{"pointer", "click", "drag", "pointer-coordinate-space"}},
		{"zero-width pointer region", func(s *eiSender) { s.pointers[0].regions[0].width = 0 }, []string{"pointer", "click", "drag", "pointer-coordinate-space"}},
		{"zero-height pointer region", func(s *eiSender) { s.pointers[0].regions[0].height = 0 }, []string{"pointer", "click", "drag", "pointer-coordinate-space"}},
		{"scroll object unavailable", func(s *eiSender) { s.scroll = 0 }, []string{"scroll"}},
		{"scroll device unavailable", func(s *eiSender) { s.scrollDev = 0 }, []string{"scroll"}},
		{"fully authorized", func(*eiSender) {}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, _ := newSyntheticRevocableRemoteDesktop(t)
			b.sender.pointers = []eiPointerDevice{{device: 501, pointer: 502, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}}
			tc.disable(b.sender)
			ops := b.SupportedOperations()
			for _, name := range tc.absent {
				if slices.Contains(ops, name) {
					t.Errorf("missing EIS authorization advertised %q: %v", name, ops)
				}
			}
			if tc.name == "fully authorized" {
				for _, name := range []string{"keyboard", "pointer", "click", "drag", "scroll", "pointer-coordinate-space"} {
					if !slices.Contains(ops, name) {
						t.Errorf("fully authorized EIS missing %q in %v", name, ops)
					}
				}
				for _, name := range []string{"sync", "pointer-location"} {
					if slices.Contains(ops, name) {
						t.Errorf("unsupported %q advertised in %v", name, ops)
					}
				}
			}
		})
	}
}

func TestRemoteDesktopIncompleteEISPointerOrScrollAuthorizationRefusesCommands(t *testing.T) {
	for _, tc := range []struct {
		name      string
		disable   func(*eiSender)
		operation func(*RemoteDesktopBackend) error
	}{
		{"missing pointer object metadata", func(s *eiSender) { s.pointers[0].pointer = 0 }, func(b *RemoteDesktopBackend) error {
			_, err := b.PointerCoordinateSpace(context.Background())
			return err
		}},
		{"missing pointer device metadata", func(s *eiSender) { s.pointers[0].device = 0 }, func(b *RemoteDesktopBackend) error {
			_, err := b.PointerCoordinateSpace(context.Background())
			return err
		}},
		{"missing scroll device", func(s *eiSender) { s.scrollDev = 0 }, func(b *RemoteDesktopBackend) error { return b.ScrollDown(context.Background(), 3) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, _ := newSyntheticRevocableRemoteDesktop(t)
			b.sender.pointers = []eiPointerDevice{{device: 501, pointer: 502, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}}
			tc.disable(b.sender)
			if err := tc.operation(b); !errors.Is(err, ErrNotSupported) {
				t.Fatalf("missing EIS authorization returned %v, want typed unsupported error", err)
			}
			if err := b.stateError(); err != nil {
				t.Fatalf("missing capability revoked otherwise healthy session: %v", err)
			}
		})
	}
}

func TestRemoteDesktopDiagnosticsDoNotClaimMissingAuthorization(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	b.sender.pointers = []eiPointerDevice{{device: 501, pointer: 502, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}}
	b.sender.keyboard = 0
	b.sender.pointers[0].pointer = 0
	details := strings.Join(b.Diagnostics(), " ")
	if strings.Contains(details, "granted keyboard and pointer") || !strings.Contains(details, "keyboard: unavailable") || !strings.Contains(details, "pointer: unavailable") {
		t.Fatalf("portal diagnostics claim absent EIS authorization: %s", details)
	}
}

func TestRemoteDesktopDiagnosticsDoNotAdvertiseClosedOrRevokedPortal(t *testing.T) {
	for _, tc := range []struct {
		name      string
		terminate func(*RemoteDesktopBackend, *portalSession) error
	}{
		{"closed", func(b *RemoteDesktopBackend, _ *portalSession) error { return b.Close() }},
		{"revoked", func(b *RemoteDesktopBackend, p *portalSession) error {
			p.signals <- &dbus.Signal{Name: portalSessionInterface + ".Closed", Path: p.path, Sender: p.owner}
			select {
			case <-b.revoked:
				return nil
			case <-time.After(time.Second):
				return errors.New("revocation watcher did not close portal")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, p, _ := newSyntheticRevocableRemoteDesktop(t)
			if err := tc.terminate(b, p); err != nil {
				t.Fatal(err)
			}
			details := strings.Join(b.Diagnostics(), " ")
			if strings.Contains(details, "session established") || !strings.Contains(details, "unavailable") {
				t.Fatalf("inactive portal still claims active device authorization: %q", details)
			}
			if len(b.SupportedOperations()) != 0 {
				t.Fatalf("inactive portal advertised operations: %v", b.SupportedOperations())
			}
		})
	}
}

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
		if frameErr != nil || request.object != eiServerIDFloor+103 || request.opcode != 1 || binary.NativeEndian.Uint32(request.body[:4]) != want.key || binary.NativeEndian.Uint32(request.body[4:8]) != want.state || frame.object != 0xff00000000000012 || frame.opcode != 3 {
			t.Fatalf("transition %d = %+v frame=%+v err=%v want key/state %+v", i, request, frame, frameErr, want)
		}
	}
	if len(backend.heldKeys) != 0 {
		t.Fatalf("completed explicit key sequence left keys held: %v", backend.heldKeys)
	}
	// An explicit release must be owned, even when temporary modifiers
	// accompany it. Hold left first, then verify shift wraps its release.
	if err := backend.KeyDown(ctx, "left"); err != nil {
		t.Fatalf("establish owned left key: %v", err)
	}
	for range 2 {
		if _, err := readSyntheticEISFrame(server); err != nil {
			t.Fatal(err)
		}
	}
	if err := backend.Type(ctx, "{shift+left up}"); err != nil {
		t.Fatalf("temporary modifier around owned explicit release: %v", err)
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
		if frameErr != nil || request.object != eiServerIDFloor+103 || request.opcode != 1 || binary.NativeEndian.Uint32(request.body[:4]) != want.key || binary.NativeEndian.Uint32(request.body[4:8]) != want.state || frame.object != 0xff00000000000012 || frame.opcode != 3 {
			t.Fatalf("modified release = %+v frame=%+v err=%v want key/state %+v", request, frame, frameErr, want)
		}
	}
	if len(backend.heldKeys) != 0 {
		t.Fatalf("modified release left temporary keys held: %v", backend.heldKeys)
	}
}

// Exercise the actual serialized writer and connected Unix EIS socket. Each
// command must publish its state change only after frame dispatch succeeds.
func TestRemoteDesktopAuthorizedSyntheticEISCommandsAndHeldState(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	b.sender.pointers = []eiPointerDevice{{device: 701, pointer: 702, regions: []eiRegion{{x: 10, y: 20, width: 200, height: 150, scale: 1}}}}
	ctx := context.Background()
	if err := b.KeyDown(ctx, "ctrl"); err != nil {
		t.Fatal(err)
	}
	ctrl, err := remoteKeyCode("ctrl")
	if err != nil {
		t.Fatal(err)
	}
	if !b.heldKeys[ctrl] {
		t.Fatal("acknowledged ctrl down did not record held key")
	}
	if lintShadow26 := b.Type(ctx, "{ctrl+a}"); lintShadow26 != nil {
		t.Fatalf("held modifier must survive combination: %v", lintShadow26)
	}
	if !b.heldKeys[ctrl] {
		t.Fatal("combination released caller-held ctrl")
	}
	if lintShadow32 := b.KeyUp(ctx, "ctrl"); lintShadow32 != nil {
		t.Fatal(lintShadow32)
	}
	if b.heldKeys[ctrl] {
		t.Fatal("ctrl keyup left held key behind")
	}
	if lintShadow38 := b.Type(ctx, "plain text {enter}"); lintShadow38 != nil {
		t.Fatalf("EIS text and key combination: %v", lintShadow38)
	}
	if lintShadow41 := b.TypeLiteral(ctx, "literal{ctrl+x} café"); lintShadow41 != nil {
		t.Fatalf("literal Unicode text must not interpret syntax: %v", lintShadow41)
	}
	if lintShadow44 := b.MouseMove(ctx, 44, 65); lintShadow44 != nil {
		t.Fatal(lintShadow44)
	}
	if !b.hasPointer || b.pointerX != 44 || b.pointerY != 65 {
		t.Fatalf("pointer location state not recorded: %d,%d has=%v", b.pointerX, b.pointerY, b.hasPointer)
	}
	if lintShadow50 := b.MouseDown(ctx, 1); lintShadow50 != nil {
		t.Fatal(lintShadow50)
	}
	if !b.buttons[0x110] {
		t.Fatal("left mouse button not recorded as held")
	}
	if lintShadow56 := b.MouseUp(ctx, 1); lintShadow56 != nil {
		t.Fatal(lintShadow56)
	}
	if b.buttons[0x110] {
		t.Fatal("button release not recorded")
	}
	if lintShadow62 := b.MouseClick(ctx, 60, 70, 3); lintShadow62 != nil {
		t.Fatal(lintShadow62)
	}
	if b.buttons[0x111] || b.pointerX != 60 || b.pointerY != 70 {
		t.Fatalf("click left held input or lost location %+v", b.buttons)
	}
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"scroll up", func() error { return b.ScrollUp(ctx, 2) }}, {"scroll down", func() error { return b.ScrollDown(ctx, 3) }},
		{"scroll left", func() error { return b.ScrollLeft(ctx, 1) }}, {"scroll right", func() error { return b.ScrollRight(ctx, 5) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if lintShadow76 := tc.run(); lintShadow76 != nil {
				t.Fatal(lintShadow76)
			}
		})
	}
	space, err := b.PointerCoordinateSpace(ctx)
	if err != nil || space.Kind != CoordinateSpaceLogical || space.ScaleX != 1 || space.ScaleY != 1 {
		t.Fatalf("EIS coordinate space %+v %v", space, err)
	}
	if diagnostics := strings.Join(b.Diagnostics(), " "); !strings.Contains(diagnostics, "EIS") || !strings.Contains(diagnostics, "pointer") {
		t.Fatalf("authorized backend lacks diagnostics: %q", diagnostics)
	}
	if _, _, err := b.PointerLocation(ctx); err == nil {
		t.Fatal("EIS incorrectly fabricated absolute pointer position")
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.KeyDown(ctx, "a"); !errors.Is(err, ErrRemoteDesktopUnavailable) {
		t.Fatalf("closed portal accepted input: %v", err)
	}
}
func TestRemoteDesktopAuthorizedValidationDoesNotMutateEISState(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	b.sender.pointers = []eiPointerDevice{{device: 501, pointer: 502, regions: []eiRegion{{x: 0, y: 0, width: 80, height: 60, scale: 1}}}}
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"unsupported key", func() error { return b.KeyDown(ctx, "no-such-key") }},
		{"invalid text syntax", func() error { return b.Type(ctx, "abc{unclosed") }},
		{"unknown named key at tail", func() error { return b.Type(ctx, "text{no-such-key}") }},
		{"outside allowed pointer region", func() error { return b.MouseMove(ctx, 100, 100) }},
		{"negative absolute pointer", func() error { return b.MouseMove(ctx, -1, 0) }},
		{"pointer left of region", func() error { return b.MouseClick(ctx, -1, 10, 1) }},
		{"invalid button", func() error { return b.MouseClick(ctx, 10, 10, 0) }},
		{"invalid button down", func() error { return b.MouseDown(ctx, 8) }},
		{"invalid button up", func() error { return b.MouseUp(ctx, -1) }},
		{"negative scroll", func() error { return b.ScrollDown(ctx, -1) }},
		{"oversized scroll", func() error { return b.ScrollUp(ctx, 1<<25) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); err == nil {
				t.Fatal("unauthorized or malformed EIS operation accepted")
			}
			if b.hasPointer || len(b.buttons) != 0 || len(b.heldKeys) != 0 {
				t.Fatalf("invalid operation mutated input state: pointer=%v buttons=%v keys=%v", b.hasPointer, b.buttons, b.heldKeys)
			}
			if stateErr := b.stateError(); stateErr != nil {
				t.Fatalf("local validation error revoked healthy session: %v", stateErr)
			}
		})
	}
	if err := b.ScrollDown(ctx, 0); err != nil {
		t.Fatal(err)
	}
}

func syntheticQueuedPortal() *RemoteDesktopBackend {
	return &RemoteDesktopBackend{sender: &eiSender{}, portal: &portalSession{}, commands: make(chan remoteDesktopCommand), stop: make(chan struct{}), revoked: make(chan struct{})}
}
func TestRemoteDesktopSubmitRejectsCanceledAndUnavailableQueue(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*RemoteDesktopBackend)
		ctx   func() (context.Context, context.CancelFunc)
		want  error
	}{
		{"caller canceled before queue", func(*RemoteDesktopBackend) {}, func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, cancel
		}, context.Canceled},
		{"caller deadline while queue blocked", func(*RemoteDesktopBackend) {}, func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 10*time.Millisecond)
		}, context.DeadlineExceeded},
		{"backend stopped before queue", func(b *RemoteDesktopBackend) { close(b.stop) }, func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), time.Second)
		}, ErrRemoteDesktopUnavailable},
		{"authorization revoked before queue", func(b *RemoteDesktopBackend) {
			b.revokedState.Store(&remoteStateError{err: ErrRemoteDesktopRevoked})
			close(b.revoked)
		}, func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), time.Second)
		}, ErrRemoteDesktopRevoked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := syntheticQueuedPortal()
			tc.setup(b)
			ctx, cancel := tc.ctx()
			defer cancel()
			executed := false
			err := b.submit(ctx, func() error { executed = true; return nil })
			if !errors.Is(err, tc.want) || executed {
				t.Fatalf("queue accepted forbidden mutation: %v executed=%v", err, executed)
			}
		})
	}
}
func TestRemoteDesktopSubmitForwardsExactlyOneSerializedCommandReceipt(t *testing.T) {
	for _, failure := range []error{nil, errors.New("synthetic EIS transport failure")} {
		b := syntheticQueuedPortal()
		observed := make(chan struct{})
		completed := make(chan struct{})
		go func() {
			defer close(completed)
			command := <-b.commands
			if command.ctx.Err() != nil {
				command.done <- command.ctx.Err()
				return
			}
			if err := command.run(); err != nil {
				command.done <- err
				return
			}
			close(observed)
			command.done <- failure
		}()
		calls := 0
		err := b.submit(context.Background(), func() error { calls++; return nil })
		<-completed
		select {
		case <-observed:
		default:
			t.Fatal("accepted command not executed")
		}
		if calls != 1 || !errors.Is(err, failure) {
			t.Fatalf("command dispatch result=%v calls=%d want=%v", err, calls, failure)
		}
	}
}
func TestRemoteDesktopSubmitCancellationAfterAdmissionDoesNotAcknowledgeOperation(t *testing.T) {
	b := syntheticQueuedPortal()
	ctx, cancel := context.WithCancel(context.Background())
	received := make(chan struct{})
	release := make(chan struct{})
	completed := make(chan struct{})
	go func() {
		defer close(completed)
		command := <-b.commands
		close(received)
		<-release
		command.done <- nil // malicious/late provider success, caller is canceled
	}()
	result := make(chan error, 1)
	go func() { result <- b.submit(ctx, func() error { return nil }) }()
	<-received
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("late provider success acknowledged despite cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled submit was blocked behind accepted request")
	}
	close(release)
	<-completed
}
func TestRemoteDesktopSubmitRejectsNilContextAndPartialState(t *testing.T) {
	b := syntheticQueuedPortal()
	if err := b.submit(absentPortalCommandContext, func() error { return nil }); err == nil {
		t.Fatal("nil context accepted")
	}
	if err := (&RemoteDesktopBackend{}).submit(context.Background(), func() error { return nil }); !errors.Is(err, ErrRemoteDesktopUnavailable) {
		t.Fatalf("partial portal queued input: %v", err)
	}
	if _, err := b.PointerCoordinateSpace(absentPortalCommandContext); err == nil {
		t.Fatal("nil query context accepted")
	}
}

// An intentionally absent context exercises the public nil-context boundary.
var absentPortalCommandContext context.Context

func TestRemoteDesktopCoordinateQueryRespectsCanceledContext(t *testing.T) {
	b := &RemoteDesktopBackend{sender: &eiSender{}, portal: &portalSession{}, commands: make(chan remoteDesktopCommand), stop: make(chan struct{}), revoked: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := b.PointerCoordinateSpace(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("coordinate query on canceled ctx=%v want context.Canceled", err)
	}
}

func TestRemoteDesktopStateRevocationHasPriorityOverIncompleteSession(t *testing.T) {
	b := &RemoteDesktopBackend{}
	b.revokedState.Store(&remoteStateError{err: ErrRemoteDesktopRevoked})
	if err := b.stateError(); !errors.Is(err, ErrRemoteDesktopRevoked) {
		t.Fatalf("revoked state=%v", err)
	}
}

// Portal permissions are granular. If a session authorizes pointer motion
// but not button injection, a requested click must fail before moving the
// pointer or altering any held-input state.
func TestRemoteDesktopClickWithoutButtonCapabilityNeverMovesPointer(t *testing.T) {
	backend, _, _ := newSyntheticRevocableRemoteDesktop(t)
	backend.sender.pointers = []eiPointerDevice{{device: 501, pointer: 502, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}}
	backend.sender.button = 0
	if backend.hasPointer {
		t.Fatal("synthetic pointer unexpectedly initialized")
	}
	err := backend.MouseClick(context.Background(), 25, 30, 1)
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("click lacking EIS button device returned %v", err)
	}
	if backend.hasPointer || backend.pointerX != 0 || backend.pointerY != 0 || len(backend.buttons) != 0 {
		t.Fatalf("partial click moved pointer or held button: moved=%t pos=(%d,%d) buttons=%v", backend.hasPointer, backend.pointerX, backend.pointerY, backend.buttons)
	}
	if err := backend.stateError(); err != nil {
		t.Fatalf("local permission rejection retired healthy portal: %v", err)
	}
}

func TestRemoteDesktopMissingEISObjectsFailClosedWithoutChangingInputState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		disable func(*eiSender)
		op      func(context.Context, *RemoteDesktopBackend) error
	}{
		{"keyboard object down", func(s *eiSender) { s.keyboard = 0 }, func(ctx context.Context, b *RemoteDesktopBackend) error { return b.KeyDown(ctx, "a") }},
		{"keyboard device up", func(s *eiSender) { s.keyboardDev = 0 }, func(ctx context.Context, b *RemoteDesktopBackend) error { return b.KeyUp(ctx, "a") }},
		{"button object down", func(s *eiSender) { s.button = 0 }, func(ctx context.Context, b *RemoteDesktopBackend) error { return b.MouseDown(ctx, 1) }},
		{"button device up", func(s *eiSender) { s.buttonDev = 0 }, func(ctx context.Context, b *RemoteDesktopBackend) error { return b.MouseUp(ctx, 1) }},
		{"pointer object move", func(s *eiSender) { s.pointers[0].pointer = 0 }, func(ctx context.Context, b *RemoteDesktopBackend) error { return b.MouseMove(ctx, 30, 40) }},
		{"pointer device move", func(s *eiSender) { s.pointers[0].device = 0 }, func(ctx context.Context, b *RemoteDesktopBackend) error { return b.MouseMove(ctx, 30, 40) }},
		{"missing key during type", func(s *eiSender) { s.keyboard = 0 }, func(ctx context.Context, b *RemoteDesktopBackend) error { return b.Type(ctx, "first {enter}") }},
		{"missing key device during type", func(s *eiSender) { s.keyboardDev = 0 }, func(ctx context.Context, b *RemoteDesktopBackend) error { return b.Type(ctx, "first {enter}") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, _ := newSyntheticRevocableRemoteDesktop(t)
			b.sender.pointers = []eiPointerDevice{{device: 501, pointer: 502, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}}
			tc.disable(b.sender)
			if err := tc.op(context.Background(), b); !errors.Is(err, ErrNotSupported) {
				t.Fatalf("missing EIS capability accepted: %v", err)
			}
			if b.hasPointer || len(b.heldKeys) > 0 || len(b.buttons) > 0 {
				t.Fatalf("missing EIS device recorded partial action: pointer=%t keys=%v buttons=%v", b.hasPointer, b.heldKeys, b.buttons)
			}
			if err := b.stateError(); err != nil {
				t.Fatalf("missing EIS capability revoked session instead of returning local validation: %v", err)
			}
		})
	}
}

// An unrelated peer can emit a Closed signal with the exact portal session
// object path. D-Bus match rules are delivered to a shared signal channel,
// so matching path and member alone must not revoke a granted session.
func TestRemoteDesktopPortalClosedSignalRejectsUntrustedBusSender(t *testing.T) {
	client, provider := startSyntheticPortalBus(t)
	attacker, err := dbusutil.ConnectContext(context.Background(), provider.busAddress)
	if err != nil {
		t.Fatal(err)
	}
	defer attacker.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	p := &portalSession{conn: client, path: dbus.ObjectPath(syntheticSessionPath), resourcesDone: make(chan struct{})}
	if err := p.subscribeClosed(ctx); err != nil {
		t.Fatal(err)
	}
	// Introduce a second broad subscriber, as occurs when multiple request
	// handlers share this godbus connection.
	broad := []dbus.MatchOption{dbus.WithMatchInterface(portalSessionInterface), dbus.WithMatchMember("Closed")}
	if err := client.AddMatchSignalContext(ctx, broad...); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.RemoveMatchSignalContext(context.Background(), broad...) }()
	connection, peer := unixConnectionPair(t)
	defer peer.Close()
	eis := &eiClient{conn: connection, messages: make(chan eiMessage, 8), done: make(chan struct{}), stop: make(chan struct{})}
	sender := &eiSender{client: eis, connection: 0xff00000000000010, seat: 0xff00000000000011, keyboard: 101, button: 102, scroll: 103, text: 104, keyboardDev: 0xff00000000000012, buttonDev: 0xff00000000000012, scrollDev: 0xff00000000000012, textDev: 0xff00000000000012}
	b := newRemoteDesktopBackend(sender, p)
	defer func() { _ = b.Close() }()
	if err := attacker.Emit(p.path, portalSessionInterface+".Closed"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-b.revoked:
		t.Fatal("an unrelated D-Bus peer revoked live portal authorization")
	case <-time.After(45 * time.Millisecond):
	}
	if err := b.stateError(); err != nil {
		t.Fatalf("forged closure changed healthy session state: %v", err)
	}
	if err := provider.conn.Emit(p.path, portalSessionInterface+".Closed"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-b.revoked:
	case <-time.After(time.Second):
		t.Fatal("authoritative portal session closure was ignored")
	}
	if err := b.stateError(); !errors.Is(err, ErrRemoteDesktopRevoked) {
		t.Fatalf("provider closure not recognized: %v", err)
	}
}

// Multiple callers may retire a portal session concurrently after desktop
// authorization is revoked. Teardown must send one session Close and publish
// the same completed result to every waiter without racing or hanging.
func TestPortalSessionConcurrentCloseSendsOneAuthorizedClose(t *testing.T) {
	client, provider := startSyntheticPortalBus(t)
	p := &portalSession{
		conn:          client,
		path:          dbus.ObjectPath(syntheticSessionPath),
		resourcesDone: make(chan struct{}),
	}
	if err := p.subscribeClosed(context.Background()); err != nil {
		t.Fatal(err)
	}
	const callers = 16
	var wg sync.WaitGroup
	result := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result <- p.close()
		}()
	}
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent portal teardown blocked")
	}
	close(result)
	for err := range result {
		if err != nil {
			t.Fatalf("portal teardown failed: %v", err)
		}
	}
	provider.mu.Lock()
	closes := provider.closedSessions
	provider.mu.Unlock()
	if closes != 1 {
		t.Fatalf("session Close dispatched %d times, want one", closes)
	}
}

// An already canceled command must not reveal key validation results or
// attempt any EIS input, regardless of whether the key name is valid.
func TestRemoteDesktopDirectKeyRejectsCanceledContextBeforeKeyValidation(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name   string
		invoke func(context.Context, string) error
	}{
		{"down", b.KeyDown},
		{"up", b.KeyUp},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, key := range []string{"not-a-real-key", "a"} {
				if err := tc.invoke(canceled, key); !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled %s(%q) gave %v", tc.name, key, err)
				}
			}
			if err := tc.invoke(nil, "not-a-real-key"); err == nil || !strings.Contains(err.Error(), "nil operation context") {
				t.Fatalf("nil context %s revealed input validation: %v", tc.name, err)
			}
		})
	}
}

func TestRemoteDesktopEISCapabilitiesRequireBoundDevicesAndRegions(t *testing.T) {
	tests := []struct {
		name    string
		sender  eiSender
		want    []string
		pointer bool
	}{
		{"no bound devices", eiSender{}, []string{"keyboard: unavailable", "pointer: unavailable", "button: unavailable", "scroll: unavailable", "text: unavailable", "absolute pointer regions: 0"}, false},
		{"object IDs without device bindings", eiSender{keyboard: 1, pointer: 2, button: 3, scroll: 4, text: 5, pointers: []eiPointerDevice{{device: 8, pointer: 9, regions: []eiRegion{{width: 0, height: 10}}}}}, []string{"keyboard: unavailable", "pointer: unavailable", "button: unavailable", "scroll: unavailable", "text: unavailable", "absolute pointer regions: 0"}, false},
		{"fully authorized", eiSender{keyboard: 1, keyboardDev: 2, button: 3, buttonDev: 4, scroll: 5, scrollDev: 6, text: 7, textDev: 8, pointers: []eiPointerDevice{{device: 9, pointer: 10, regions: []eiRegion{{width: 100, height: 100}, {width: 0, height: 50}, {width: 50, height: 50}}}}}, []string{"keyboard: available", "pointer: available", "button: available", "scroll: available", "text: available", "absolute pointer regions: 2"}, true},
		{"unbound pointer regions do not authorize", eiSender{pointers: []eiPointerDevice{{device: 11, regions: []eiRegion{{width: 100, height: 100}}}, {pointer: 12, regions: []eiRegion{{width: 50, height: 50}}}}}, []string{"pointer: unavailable", "absolute pointer regions: 0"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := &RemoteDesktopBackend{sender: &tt.sender, portal: &portalSession{}, commands: make(chan remoteDesktopCommand), stop: make(chan struct{}), revoked: make(chan struct{})}
			diagnostics := strings.Join(backend.Diagnostics(), "\n")
			for _, want := range tt.want {
				if !strings.Contains(diagnostics, want) {
					t.Errorf("diagnostics %q missing %q", diagnostics, want)
				}
			}
			info, err := backend.PointerCoordinateSpace(context.Background())
			if tt.pointer {
				if err != nil || info.Kind != CoordinateSpaceLogical || info.ScaleX != 1 || info.ScaleY != 1 {
					t.Fatalf("authorized coordinate space = %+v, %v", info, err)
				}
			} else if !errors.Is(err, ErrNotSupported) {
				t.Fatalf("unauthorized coordinate space = %+v, %v", info, err)
			}
			if got := backend.SupportedOperations(); (len(got) > 0) != tt.pointer {
				t.Fatalf("supported operations = %v, pointer authorized = %v", got, tt.pointer)
			}
		})
	}
}

func TestRemoteDesktopEISPointerCoordinateSpaceRejectsInvalidContextsAndState(t *testing.T) {
	sender := &eiSender{pointers: []eiPointerDevice{{device: 1, pointer: 2, regions: []eiRegion{{width: 100, height: 100}}}}}
	backend := &RemoteDesktopBackend{sender: sender, portal: &portalSession{}, commands: make(chan remoteDesktopCommand), stop: make(chan struct{}), revoked: make(chan struct{})}
	if _, err := backend.PointerCoordinateSpace(nil); err == nil || !strings.Contains(err.Error(), "nil context") { //nolint:staticcheck // Explicitly verify rejection of nil contexts.
		t.Fatalf("nil context was not rejected: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.PointerCoordinateSpace(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled context returned %v", err)
	}
	close(backend.stop)
	if _, err := backend.PointerCoordinateSpace(context.Background()); !errors.Is(err, ErrRemoteDesktopUnavailable) {
		t.Fatalf("closed session coordinate space returned %v", err)
	}
	if got := backend.SupportedOperations(); len(got) != 0 {
		t.Fatalf("closed session advertises operations: %v", got)
	}
	if got := fmt.Sprint(backend.Diagnostics()); !strings.Contains(got, "unavailable") || strings.Contains(got, "session established") {
		t.Fatalf("closed session diagnostics: %q", got)
	}
}

// Even without a parser error, a terminated EIS transport must revoke the
// portal-backed sender. A lost event source cannot keep granting fresh input.
func TestRemoteDesktopUnexpectedEISTerminationRevokesInput(t *testing.T) {
	for _, tc := range []struct {
		name      string
		terminate func(*eiClient)
		reason    string
	}{
		{"transport closes without recorded read error", func(c *eiClient) { close(c.done) }, "EIS stream closed"},
		{"event channel unexpectedly closes", func(c *eiClient) { close(c.messages) }, "EIS event stream closed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, client := newSyntheticRevocableRemoteDesktop(t)
			tc.terminate(client)
			select {
			case <-b.revoked:
			case <-time.After(time.Second):
				t.Fatal("lost EIS stream retained input authorization")
			}
			if err := b.stateError(); !errors.Is(err, ErrRemoteDesktopRevoked) || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("unexpected EIS revocation: %v", err)
			}
			select {
			case <-b.writerDone:
			case <-time.After(time.Second):
				t.Fatal("EIS stream termination left writer running")
			}
			if err := b.KeyDown(context.Background(), "a"); !errors.Is(err, ErrRemoteDesktopRevoked) {
				t.Fatalf("terminated EIS source accepted key: %v", err)
			}
			if err := b.Close(); err != nil {
				t.Fatalf("idempotent cleanup of terminated EIS stream: %v", err)
			}
		})
	}
}

func TestSyntheticPortalFailsClosedAcrossAuthorizationStages(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*syntheticPortal)
		want      error
	}{
		{"session denied", func(p *syntheticPortal) { p.code = 1 }, ErrRemoteDesktopDenied},
		{"session malformed", func(p *syntheticPortal) {
			p.result = map[string]dbus.Variant{"session_handle": dbus.MakeVariant(uint32(12))}
		}, ErrRemoteDesktopUnavailable},
		{"select devices denied", func(p *syntheticPortal) { p.selectCode = 1 }, ErrRemoteDesktopDenied},
		{"start denied", func(p *syntheticPortal) { p.startCode = 1 }, ErrRemoteDesktopDenied},
		{"no keyboard", func(p *syntheticPortal) { p.deviceMask = portalDevicePointer }, ErrRemoteDesktopDenied},
		{"no pointer", func(p *syntheticPortal) { p.deviceMask = portalDeviceKeyboard }, ErrRemoteDesktopDenied},
		{"no devices", func(p *syntheticPortal) { p.deviceMask = 0 }, ErrRemoteDesktopDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, p := startSyntheticPortalBus(t)
			p.mu.Lock()
			p.deviceMask = portalDeviceKeyboard | portalDevicePointer
			tc.configure(p)
			p.mu.Unlock()
			rt := env.FromEnviron([]string{
				"WAYLAND_DISPLAY=/tmp/synthetic-wayland-socket",
				"DBUS_SESSION_BUS_ADDRESS=" + p.busAddress,
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			inputter, err := OpenRemoteDesktopContext(ctx, rt, RemoteDesktopOptions{})
			if inputter != nil {
				t.Fatalf("failed grant returned input backend %T", inputter)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("OpenRemoteDesktopContext=%v, want %v", err, tc.want)
			}
		})
	}
}

// A portal session may fail after capability advertisement but before sender
// initialization. Every operation must fail with a typed error, never panic.
func TestRemoteDesktopIncompleteBackendFailsClosedWithoutPanic(t *testing.T) {
	for _, makeBackend := range []struct {
		name       string
		newBackend func() *RemoteDesktopBackend
	}{
		{"nil", func() *RemoteDesktopBackend { return nil }},
		{"zero-value", func() *RemoteDesktopBackend { return &RemoteDesktopBackend{} }},
	} {
		t.Run(makeBackend.name, func(t *testing.T) {
			b := makeBackend.newBackend()
			ops := []struct {
				name string
				run  func() error
			}{
				{"move", func() error { return b.MouseMove(context.Background(), 1, 2) }},
				{"click", func() error { return b.MouseClick(context.Background(), 1, 2, 1) }},
				{"down", func() error { return b.MouseDown(context.Background(), 1) }},
				{"up", func() error { return b.MouseUp(context.Background(), 1) }},
				{"key-down", func() error { return b.KeyDown(context.Background(), "a") }},
				{"key-up", func() error { return b.KeyUp(context.Background(), "a") }},
				{"literal", func() error { return b.TypeLiteral(context.Background(), "abc") }},
				{"type", func() error { return b.Type(context.Background(), "abc") }},
				{"scroll", func() error { return b.ScrollDown(context.Background(), 1) }},
				{"coordinate", func() error { _, err := b.PointerCoordinateSpace(context.Background()); return err }},
			}
			for _, op := range ops {
				t.Run(op.name, func(t *testing.T) {
					defer func() {
						if v := recover(); v != nil {
							t.Errorf("incomplete backend panicked: %v", v)
						}
					}()
					err := op.run()
					if !errors.Is(err, ErrRemoteDesktopUnavailable) {
						t.Errorf("error=%v, want unavailable", err)
					}
				})
			}
		})
	}
}

func TestRemoteDesktopInvalidButtonAndEmptyScrollHaveDefinedResults(t *testing.T) {
	b := &RemoteDesktopBackend{}
	for _, button := range []int{-1, 0, 4, 255} {
		if err := b.MouseClick(context.Background(), 0, 0, button); err == nil {
			t.Errorf("click invalid button %d accepted", button)
		}
		if err := b.MouseDown(context.Background(), button); err == nil {
			t.Errorf("down invalid button %d accepted", button)
		}
	}
	if err := b.ScrollDown(context.Background(), 0); !errors.Is(err, ErrRemoteDesktopUnavailable) {
		t.Errorf("scroll zero with unavailable backend=%v", err)
	}
}

// EIS object zero identifies the global protocol, not a device. Destroying
// an unrelated object or sending an unrelated opcode must not revoke an
// otherwise authorized session. A selected device pause or destruction must.
func TestPortalLifecycleIgnoresGlobalAndUnrelatedObjects(t *testing.T) {
	sender := &eiSender{
		seat: 11, keyboard: 12, keyboardDev: 13,
		button: 14, buttonDev: 15,
		scroll: 16, scrollDev: 17,
		text: 18, textDev: 19,
		pointers: []eiPointerDevice{{pointer: 20, device: 21}},
	}
	b := &RemoteDesktopBackend{sender: sender}
	for _, tc := range []struct {
		name             string
		msg              eiMessage
		selectedPaused   bool
		lifecycleFailure bool
	}{
		{"global destroy is not device revocation", eiMessage{object: 0, opcode: 0}, false, false},
		{"global pause is not device revocation", eiMessage{object: 0, opcode: 8}, false, false},
		{"unrelated device destruction", eiMessage{object: 200, opcode: 0}, false, false},
		{"unrelated device pause", eiMessage{object: 200, opcode: 8}, false, false},
		{"selected keyboard pause", eiMessage{object: 13, opcode: 8}, true, true},
		{"selected button destroyed", eiMessage{object: 15, opcode: 0}, true, true},
		{"selected scroll pause", eiMessage{object: 17, opcode: 8}, true, true},
		{"selected text destroyed", eiMessage{object: 19, opcode: 0}, true, true},
		{"selected pointer pause", eiMessage{object: 21, opcode: 8}, true, true},
		{"selected pointer destroyed", eiMessage{object: 21, opcode: 0}, true, true},
		{"selected device unrelated opcode", eiMessage{object: 13, opcode: 7}, false, false},
		{"seat destruction", eiMessage{object: 11, opcode: 0}, false, true},
		{"keyboard object destruction", eiMessage{object: 12, opcode: 0}, false, true},
		{"button object destruction", eiMessage{object: 14, opcode: 0}, false, true},
		{"scroll object destruction", eiMessage{object: 16, opcode: 0}, false, true},
		{"text object destruction", eiMessage{object: 18, opcode: 0}, false, true},
		{"pointer object destruction", eiMessage{object: 20, opcode: 0}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := b.isSelectedDevicePaused(tc.msg); got != tc.selectedPaused {
				t.Fatalf("selected-device paused=%v want=%v", got, tc.selectedPaused)
			}
			if got := b.isEISLifecycleFailure(tc.msg); got != tc.lifecycleFailure {
				t.Fatalf("lifecycle failure=%v want=%v", got, tc.lifecycleFailure)
			}
		})
	}
}

// The EIS transport closes during an explicit local shutdown. The lifecycle
// watcher must distinguish this expected termination from a peer revocation:
// a normal Close must not convert the retired session to a remote error.
func TestPortalLifecycleReaderExitDuringLocalCloseDoesNotRevoke(t *testing.T) {
	done := make(chan struct{})
	close(done)
	client := &eiClient{done: done, messages: make(chan eiMessage), stop: make(chan struct{})}
	client.setReadError(errors.New("local EIS socket closed"))
	backend := &RemoteDesktopBackend{
		sender:  &eiSender{client: client},
		portal:  &portalSession{signals: make(chan *dbus.Signal)},
		revoked: make(chan struct{}),
		stop:    make(chan struct{}),
	}
	backend.closeStarted.Store(true)
	finished := make(chan struct{})
	go func() { backend.watchLifecycle(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("local-close EIS reader termination stranded lifecycle watcher")
	}
	if state := backend.revokedState.Load(); state != nil {
		t.Fatalf("local shutdown treated as external revocation: %v", state.err)
	}
	select {
	case <-backend.revoked:
		t.Fatal("local shutdown published remote revocation")
	default:
	}
}

// Use a real connected EIS Unix socket to verify that local authorization
// rejection sends *no* frames at all, including object-zero protocol frames.
func TestRemoteDesktopMissingCapabilityNeverSendsEISWireFrames(t *testing.T) {
	cases := []struct {
		name    string
		disable func(*eiSender)
		call    func(*RemoteDesktopBackend) error
	}{
		{"keyboard object", func(s *eiSender) { s.keyboard = 0 }, func(b *RemoteDesktopBackend) error { return b.KeyDown(context.Background(), "a") }},
		{"keyboard device", func(s *eiSender) { s.keyboardDev = 0 }, func(b *RemoteDesktopBackend) error { return b.Type(context.Background(), "prefix {enter}") }},
		{"button object", func(s *eiSender) { s.button = 0 }, func(b *RemoteDesktopBackend) error { return b.MouseClick(context.Background(), 25, 30, 1) }},
		{"button device", func(s *eiSender) { s.buttonDev = 0 }, func(b *RemoteDesktopBackend) error { return b.MouseDown(context.Background(), 1) }},
		{"pointer object", func(s *eiSender) { s.pointers[0].pointer = 0 }, func(b *RemoteDesktopBackend) error { return b.MouseMove(context.Background(), 25, 30) }},
		{"pointer device", func(s *eiSender) { s.pointers[0].device = 0 }, func(b *RemoteDesktopBackend) error { return b.MouseMove(context.Background(), 25, 30) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, peer := unixConnectionPair(t)
			t.Cleanup(func() { _ = peer.Close() })
			client := &eiClient{conn: conn, messages: make(chan eiMessage, 8), done: make(chan struct{}), stop: make(chan struct{})}
			sender := &eiSender{client: client, connection: 0xff00000000000010, seat: 0xff00000000000011, keyboard: 101, button: 102, scroll: 103, text: 104, keyboardDev: 0xff00000000000012, buttonDev: 0xff00000000000012, scrollDev: 0xff00000000000012, textDev: 0xff00000000000012}
			sender.pointers = []eiPointerDevice{{device: 501, pointer: 502, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}}
			portal := &portalSession{path: dbus.ObjectPath("/org/freedesktop/portal/desktop/session/fixture"), signals: make(chan *dbus.Signal, 8)}
			b := newRemoteDesktopBackend(sender, portal)
			t.Cleanup(func() { _ = b.Close() })
			tc.disable(sender)
			if err := tc.call(b); !errors.Is(err, ErrNotSupported) {
				t.Fatalf("missing EIS capability did not produce typed refusal: %v", err)
			}
			if err := peer.SetReadDeadline(time.Now().Add(30 * time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 64)
			n, err := peer.Read(buf)
			if n != 0 {
				t.Fatalf("denied EIS request sent %d bytes: %x", n, buf[:n])
			}
			var netErr net.Error
			if !errors.As(err, &netErr) || !netErr.Timeout() {
				t.Fatalf("expected clean EIS socket with no frames, got %v", err)
			}
		})
	}
}

// The session owner is authenticated at subscription. If lifecycle state is
// malformed or partially initialized, an unrelated peer must not be able to
// forge a session-close event by knowing the predictable portal object path.
func TestRemoteDesktopClosedSignalWithoutAuthenticatedOwnerFailsClosed(t *testing.T) {
	if (&RemoteDesktopBackend{}).isAuthorizedPortalSessionClose(&dbus.Signal{Name: portalSessionInterface + ".Closed"}) {
		t.Fatal("uninitialized portal accepted lifecycle event")
	}
	path := dbus.ObjectPath("/org/freedesktop/portal/desktop/session/authorized")
	backend := &RemoteDesktopBackend{portal: &portalSession{path: path}}
	for _, sender := range []string{"", ":1.44", ":1.66"} {
		forged := &dbus.Signal{Sender: sender, Path: path, Name: portalSessionInterface + ".Closed"}
		if backend.isAuthorizedPortalSessionClose(forged) {
			t.Fatalf("accepted lifecycle event from %q without authenticated portal owner", sender)
		}
	}
}

// A successful D-Bus response without a session handle is not an authorized
// session. The missing handle must abort before any input devices are selected.
func TestRemoteDesktopRejectsSuccessfulPortalResponseWithoutSessionHandle(t *testing.T) {
	_, portal := startSyntheticPortalBus(t)
	portal.mu.Lock()
	portal.result = map[string]dbus.Variant{}
	portal.mu.Unlock()
	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=/tmp/perfuncted-test-portal-wayland.sock",
		"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	backend, err := OpenRemoteDesktopContext(ctx, runtime, RemoteDesktopOptions{})
	if backend != nil || !errors.Is(err, ErrRemoteDesktopUnavailable) || !strings.Contains(err.Error(), "omitted session handle") {
		t.Fatalf("portal session handle omission was accepted: backend=%T err=%v", backend, err)
	}
	portal.mu.Lock()
	creates, closes := portal.called, portal.closedSessions
	portal.mu.Unlock()
	if creates != 1 || closes != 0 {
		t.Fatalf("incorrect unacquired session ownership: requests=%d closes=%d", creates, closes)
	}
}

func TestSyntheticPortalPersistentGrantRequiresValidRestoreToken(t *testing.T) {
	for _, tc := range []struct {
		name  string
		token string
		want  string
	}{
		{"missing persistent grant", "", "did not grant persistent permission"},
		{"embedded NUL cannot pass D-Bus", "bad\x00token", "context deadline exceeded"},
		{"newline", "bad\ntoken", "malformed restore token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, p := startSyntheticPortalBus(t)
			p.mu.Lock()
			p.deviceMask = portalDeviceKeyboard | portalDevicePointer
			p.persistToken = tc.token
			p.mu.Unlock()
			stateDir := privatePortalTokenFixtureDir(t)
			rt := env.FromEnviron([]string{
				"WAYLAND_DISPLAY=/tmp/fixture-wayland",
				"DBUS_SESSION_BUS_ADDRESS=" + p.busAddress,
				"XDG_STATE_HOME=" + stateDir,
			})
			ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
			defer cancel()
			backend, err := OpenRemoteDesktopContext(ctx, rt, RemoteDesktopOptions{PersistPermission: true})
			if backend != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("persistent grant %q => backend=%v err=%v want %q", tc.name, backend, err, tc.want)
			}
			tokenPath := filepath.Join(stateDir, "perfuncted", portalRestoreTokenFile)
			if _, err := os.Stat(tokenPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid restore token was persisted: %v", err)
			}
			p.mu.Lock()
			options := p.selectOptions
			p.mu.Unlock()
			if options["persist_mode"].Value() != portalPersistForever {
				t.Fatalf("persistent authorization was not explicitly requested: %+v", options)
			}
		})
	}
}

func TestPortalRestoreTokenStoreNeverUsesUntrustedRelativeStatePath(t *testing.T) {
	rt := env.FromEnviron([]string{
		"XDG_STATE_HOME=relative-state",
		"WAYLAND_DISPLAY=/tmp/test-wayland",
	})
	_, err := OpenRemoteDesktopContext(context.Background(), rt, RemoteDesktopOptions{PersistPermission: true})
	// Startup must fail early, and never silently write permission state in cwd.
	if err == nil {
		t.Fatal("remote input unexpectedly authorized without a session bus")
	}
	if _, err := lockPortalTokenStore(rt); err == nil {
		t.Fatal("relative token-store path accepted")
	}
}

func TestSyntheticPortalPersistentGrantConsumesAndRotatesRestoreToken(t *testing.T) {
	_, portal := startSyntheticPortalBus(t)
	stateDir := privatePortalTokenFixtureDir(t)
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=/tmp/fixture-wayland",
		"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress,
		"XDG_STATE_HOME=" + stateDir,
	})
	store, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.store("previous-grant"); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	eisClient, eisServer := unixConnectionPair(t)
	t.Cleanup(func() { _ = eisServer.Close() })
	file, err := eisClient.File()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	_ = eisClient.Close()
	portal.mu.Lock()
	portal.deviceMask = portalDeviceKeyboard | portalDevicePointer
	portal.persistToken = "rotated-grant"
	portal.eisFD = dbus.UnixFD(file.Fd())
	portal.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	peerDone := make(chan error, 1)
	go func() { peerDone <- serveSyntheticAuthorizedEISPeer(eisServer) }()
	inputter, err := OpenRemoteDesktopContext(ctx, rt, RemoteDesktopOptions{PersistPermission: true})
	if err != nil {
		t.Fatalf("persistent portal authorization: %v", err)
	}
	backend, ok := inputter.(*RemoteDesktopBackend)
	if !ok {
		t.Fatalf("unexpected inputter type %T", inputter)
	}
	portal.mu.Lock()
	options := portal.selectOptions
	portal.mu.Unlock()
	if options["persist_mode"].Value() != portalPersistForever {
		t.Fatalf("persistent grant mode not requested: %+v", options)
	}
	if options["restore_token"].Value() != "previous-grant" {
		t.Fatalf("prior restore token not offered to the portal: %+v", options["restore_token"])
	}
	select {
	case peerErr := <-peerDone:
		if peerErr != nil {
			t.Fatalf("synthetic EIS handshake: %v", peerErr)
		}
	case <-ctx.Done():
		t.Fatalf("synthetic EIS handshake did not finish: %v", ctx.Err())
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("close authorized backend: %v", err)
	}

	path := filepath.Join(stateDir, "perfuncted", portalRestoreTokenFile)
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("rotated token file mode: stat=%v err=%v", info, err)
	}
	rotated, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	got, err := rotated.consume()
	closeErr := rotated.Close()
	if err != nil || closeErr != nil || got != "rotated-grant" {
		t.Fatalf("stored restore token = %q, consume err=%v close err=%v", got, err, closeErr)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("consuming rotated token did not remove its file: %v", err)
	}
}

// Even unsupported queries must report their operation lifetime truthfully:
// canceled callers and revoked authorization take precedence over capability.
func TestRemoteDesktopPointerLocationHonorsCancellationAndAuthorization(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{"canceled", ctx, context.Canceled},
		{"live unsupported", context.Background(), ErrNotSupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x, y, err := b.PointerLocation(tc.ctx)
			if x != 0 || y != 0 || !errors.Is(err, tc.want) {
				t.Fatalf("position (%d,%d) error %v, want %v", x, y, err, tc.want)
			}
		})
	}
	if _, _, err := b.PointerLocation(nil); err == nil {
		t.Fatal("nil context accepted")
	}
	if err := b.revoke(errors.New("synthetic authorization retired")); !errors.Is(err, ErrRemoteDesktopRevoked) {
		t.Fatalf("revocation should report lost authorization, got %v", err)
	}
	if x, y, err := b.PointerLocation(context.Background()); x != 0 || y != 0 || !errors.Is(err, ErrRemoteDesktopRevoked) {
		t.Fatalf("revoked pointer query (%d,%d): %v", x, y, err)
	}
}

// An already-canceled caller must not enter the portal command queue even
// when its unbuffered receiver is ready at the same instant. Select alone
// does not give canceled contexts priority over other ready channels.
func TestRemoteDesktopPreCanceledOperationNeverEntersReadyCommandQueue(t *testing.T) {
	for i := 0; i < 128; i++ {
		b := syntheticQueuedPortal()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		receiverReady := make(chan struct{})
		stopReceiver := make(chan struct{})
		completed := make(chan struct{})
		var accepted atomic.Bool
		go func() {
			defer close(completed)
			close(receiverReady)
			select {
			case cmd := <-b.commands:
				accepted.Store(true)
				cmd.done <- nil
			case <-stopReceiver:
			}
		}()
		<-receiverReady
		err := b.submit(ctx, func() error { return nil })
		close(stopReceiver)
		<-completed
		if accepted.Load() {
			t.Fatalf("iteration %d: canceled caller entered the ready command queue", i)
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("iteration %d: expected cancellation, got %v", i, err)
		}
	}
}

// Canceled portal operations lose authority before validating text, mouse
// coordinates or button numbers. Invalid arguments may not supersede the
// cancellation, and none of these operations may enqueue EIS frames.
func TestRemoteDesktopPreCanceledPointerAndLiteralPreflight(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name string
		call func(context.Context) error
	}{
		{"literal invalid UTF8", func(ctx context.Context) error { return b.TypeLiteral(ctx, string([]byte{0xff})) }},
		{"mouse move invalid region", func(ctx context.Context) error { return b.MouseMove(ctx, -999, -999) }},
		{"mouse click invalid button", func(ctx context.Context) error { return b.MouseClick(ctx, -999, -999, 19) }},
		{"mouse down invalid button", func(ctx context.Context) error { return b.MouseDown(ctx, 19) }},
		{"mouse up invalid button", func(ctx context.Context) error { return b.MouseUp(ctx, 19) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled operation returned %v", err)
			}
			if err := tc.call(nil); err == nil || err.Error() != "input/remote-desktop: nil operation context" {
				t.Fatalf("nil context returned %v", err)
			}
		})
	}
}

// Authorization can disappear after the caller's preflight but before the
// unbuffered writer queue accepts its command. The waiting submit must
// unblock without executing input or reporting successful dispatch.
func TestRemoteDesktopQueuedInputAbortsOnLateAuthorityLoss(t *testing.T) {
	for _, tc := range []struct {
		name   string
		retire func(*RemoteDesktopBackend)
		want   error
	}{
		{name: "portal revoked while waiting", retire: func(b *RemoteDesktopBackend) {
			b.revokedState.Store(&remoteStateError{err: ErrRemoteDesktopRevoked})
			close(b.revoked)
		}, want: ErrRemoteDesktopRevoked},
		{name: "backend stopped while waiting", retire: func(b *RemoteDesktopBackend) { close(b.stop) }, want: ErrRemoteDesktopUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := syntheticQueuedPortal()
			// No receiver is permitted until the retirement signal has arrived.
			// A submit that ignores that signal will hang rather than complete.
			result := make(chan error, 1)
			invoked := make(chan struct{}, 1)
			go func() { result <- b.submit(context.Background(), func() error { invoked <- struct{}{}; return nil }) }()
			tc.retire(b)
			select {
			case err := <-result:
				if !errors.Is(err, tc.want) {
					t.Fatalf("retirement returned %v, expected %v", err, tc.want)
				}
			case <-time.After(time.Second):
				t.Fatal("retired portal command remained blocked on writer")
			}
			select {
			case <-invoked:
				t.Fatal("unauthorized input ran")
			default:
			}
		})
	}
}

// Cancellation must win over a successful writer receipt when the writer
// reports success at the instant its request's authorization expires.
func TestRemoteDesktopQueuedInputLateCanceledReceipt(t *testing.T) {
	b := syntheticQueuedPortal()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	received := make(chan remoteDesktopCommand, 1)
	go func() { received <- <-b.commands }()
	result := make(chan error, 1)
	go func() { result <- b.submit(ctx, func() error { return nil }) }()
	var cmd remoteDesktopCommand
	select {
	case cmd = <-received:
	case <-time.After(time.Second):
		t.Fatal("writer never accepted input")
	}
	cancel()
	cmd.done <- nil
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled receipt falsely succeeded: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled caller blocked on writer acknowledgement")
	}
}

// The same private bus connection can have multiple MatchSignal rules: a
// response handler must not treat an unrelated member as permission denial
// or an authoritative grant even when it arrives on the expected request path.
func TestPortalRequestIgnoresUnrelatedSignalMemberOnSameConnection(t *testing.T) {
	client, provider := startSyntheticPortalBus(t)
	provider.mu.Lock()
	provider.emitUnrelated = true
	provider.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	other := []dbus.MatchOption{
		dbus.WithMatchSender(remoteDesktopBus),
		dbus.WithMatchInterface(portalRequestInterface),
		dbus.WithMatchMember("Unexpected"),
	}
	if err := client.AddMatchSignalContext(ctx, other...); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.RemoveMatchSignalContext(context.Background(), other...) }()
	name, err := portalUniqueName(client.Names())
	if err != nil {
		t.Fatal(err)
	}
	handle, err := portalCreateSession(ctx, client, name)
	if err != nil || handle != syntheticSessionPath {
		t.Fatalf("unrelated member must not override authorized session: handle=%q error=%v", handle, err)
	}
}

// A bus peer must not authorize the caller by fabricating a Request.Response
// at the portal's returned object path. Although normal sessions use narrow
// match rules, godbus dispatches the union of all rules to each signal channel.
func TestPortalRequestRejectsSamePathResponseFromUntrustedBusSender(t *testing.T) {
	client, portal := startSyntheticPortalBus(t)
	pathReady := make(chan dbus.ObjectPath, 1)
	portal.mu.Lock()
	portal.skip = true
	portal.requestPaths = pathReady
	portal.mu.Unlock()
	attacker, err := dbusutil.ConnectContext(context.Background(), portal.busAddress)
	if err != nil {
		t.Fatal(err)
	}
	defer attacker.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Millisecond)
	defer cancel()
	wideRule := []dbus.MatchOption{
		dbus.WithMatchInterface(portalRequestInterface), dbus.WithMatchMember("Response"),
	}
	if lintShadow33 := client.AddMatchSignalContext(ctx, wideRule...); lintShadow33 != nil {
		t.Fatal(lintShadow33)
	}
	defer func() { _ = client.RemoveMatchSignalContext(context.Background(), wideRule...) }()
	name, err := portalUniqueName(client.Names())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := portalCreateSession(ctx, client, name); done <- err }()
	var path dbus.ObjectPath
	select {
	case path = <-pathReady:
	case <-ctx.Done():
		t.Fatalf("portal method not dispatched: %v", ctx.Err())
	}
	forged := map[string]dbus.Variant{"session_handle": dbus.MakeVariant(syntheticSessionPath)}
	if err := attacker.Emit(path, portalRequestInterface+".Response", uint32(0), forged); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("untrusted peer forged a successful RemoteDesktop authorization")
		}
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, ErrRemoteDesktopUnavailable) {
			t.Fatalf("untrusted response leaked unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("forged response caused portal request to hang")
	}
}

// Every device and interface selected by a live portal EIS session is part
// of its authorization. A compositor pause or removal must retire the whole
// sender; unrelated EIS events must not revoke a different authorization.
func TestPortalSelectedEISDeviceAndInterfaceLossRevokesAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message eiMessage
	}{
		{"keyboard device removed", eiMessage{object: 0xff00000000000021, opcode: 0}},
		{"button device paused", eiMessage{object: 0xff00000000000022, opcode: 8}},
		{"scroll device removed", eiMessage{object: 0xff00000000000023, opcode: 0}},
		{"text device paused", eiMessage{object: 0xff00000000000024, opcode: 8}},
		{"pointer device removed", eiMessage{object: 0xff00000000000025, opcode: 0}},
		{"pointer device paused", eiMessage{object: 0xff00000000000025, opcode: 8}},
		{"keyboard interface removed", eiMessage{object: 101, opcode: 0}},
		{"button interface removed", eiMessage{object: 102, opcode: 0}},
		{"scroll interface removed", eiMessage{object: 103, opcode: 0}},
		{"text interface removed", eiMessage{object: 104, opcode: 0}},
		{"pointer interface removed", eiMessage{object: 105, opcode: 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, client := newSyntheticRevocableRemoteDesktop(t)
			b.sender.keyboardDev = 0xff00000000000021
			b.sender.buttonDev = 0xff00000000000022
			b.sender.scrollDev = 0xff00000000000023
			b.sender.textDev = 0xff00000000000024
			b.sender.pointers = []eiPointerDevice{{device: 0xff00000000000025, pointer: 105, regions: []eiRegion{{width: 128, height: 128, scale: 1}}}}
			// An event on an unrelated device or a global control object
			// cannot revoke a live portal session.
			client.messages <- eiMessage{object: 0, opcode: 0}
			client.messages <- eiMessage{object: 0xff00000000000090, opcode: 8}
			client.messages <- eiMessage{object: 105, opcode: 3}
			select {
			case <-b.revoked:
				t.Fatal("unrelated EIS event revoked session")
			case <-time.After(5 * time.Millisecond):
			}
			if err := b.stateError(); err != nil {
				t.Fatalf("unrelated event invalidated authorization: %v", err)
			}
			client.messages <- tc.message
			select {
			case <-b.revoked:
			case <-time.After(time.Second):
				t.Fatal("selected device lifecycle loss did not revoke portal")
			}
			if err := b.stateError(); !errors.Is(err, ErrRemoteDesktopRevoked) || !strings.Contains(err.Error(), "EIS session ended") {
				t.Fatalf("wrong revocation after %s: %v", tc.name, err)
			}
			if err := b.KeyDown(context.Background(), "a"); !errors.Is(err, ErrRemoteDesktopRevoked) {
				t.Fatalf("revoked EIS sender accepted input: %v", err)
			}
			select {
			case <-b.writerDone:
			case <-time.After(time.Second):
				t.Fatal("EIS writer remained active after revocation")
			}
		})
	}
}

// Startup is a series of authorization checkpoints, not a best-effort
// fallback. Each failed checkpoint must reject the session, and any session
// already acquired from the trusted portal service must be explicitly closed.
func TestSyntheticPortalStartupStageFailuresDoNotExposeInputAuthority(t *testing.T) { //nolint:gocyclo // Individual authorization checkpoints are one explicitly verified cleanup matrix.
	for _, tc := range []struct {
		name      string
		configure func(*testing.T, *syntheticPortal, env.Runtime)
		persist   bool
		want      error
		message   string
		attempts  int
		closes    int
	}{
		{name: "unsupported portal version", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) { p.getVersion = uint32(1) }, want: ErrRemoteDesktopUnavailable, attempts: 0, closes: 0},
		{name: "wrong version type", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) { p.getVersion = "version 2" }, want: ErrRemoteDesktopUnavailable, attempts: 0, closes: 0},
		{name: "version property read fails", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) {
			p.versionFailure = "synthetic properties failure"
		}, want: ErrRemoteDesktopUnavailable, attempts: 0, closes: 0},
		{name: "insecure restore token directory", configure: func(t *testing.T, _ *syntheticPortal, rt env.Runtime) {
			private := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted")
			if err := os.MkdirAll(private, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(private, 0777); err != nil {
				t.Fatal(err)
			}
		}, persist: true, message: "secure restore-token store", attempts: 0, closes: 0},
		{name: "malformed saved restore token", configure: func(t *testing.T, _ *syntheticPortal, rt env.Runtime) {
			lock, err := lockPortalTokenStore(rt)
			if err != nil {
				t.Fatal(err)
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted", portalRestoreTokenFile)
			if err := os.WriteFile(path, []byte("untrusted credential without newline"), 0600); err != nil {
				t.Fatal(err)
			}
		}, persist: true, message: "consume restore token", attempts: 0, closes: 0},
		{name: "create session rejected", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) { p.createFailure = "synthetic create failed" }, want: ErrRemoteDesktopUnavailable, attempts: 1, closes: 0},
		{name: "invalid session handle", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) {
			p.result = map[string]dbus.Variant{"session_handle": dbus.MakeVariant("")}
		}, want: ErrRemoteDesktopUnavailable, message: "invalid session handle", attempts: 1, closes: 0},
		{name: "select devices denied", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) { p.selectCode = 1 }, want: ErrRemoteDesktopDenied, attempts: 1, closes: 1},
		{name: "start denied", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) { p.startCode = 1 }, want: ErrRemoteDesktopDenied, attempts: 1, closes: 1},
		{name: "missing pointer authority", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) { p.deviceMask = portalDeviceKeyboard }, want: ErrRemoteDesktopDenied, message: "did not authorize keyboard and pointer", attempts: 1, closes: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, portal := startSyntheticPortalBus(t)
			root := privatePortalTokenFixtureDir(t)
			rt := env.FromEnviron([]string{
				"WAYLAND_DISPLAY=/tmp/pf-test-fake-wayland.sock",
				"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress,
				"XDG_STATE_HOME=" + root,
			})
			portal.mu.Lock()
			if tc.configure != nil {
				tc.configure(t, portal, rt)
			}
			portal.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			backend, err := OpenRemoteDesktopContext(ctx, rt, RemoteDesktopOptions{PersistPermission: tc.persist})
			if backend != nil || err == nil {
				if backend != nil {
					_ = backend.Close()
				}
				t.Fatalf("failed checkpoint granted input: backend=%v err=%v", backend, err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("checkpoint err=%v, want %v", err, tc.want)
			}
			if tc.message != "" && !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("checkpoint err=%v, want %q", err, tc.message)
			}
			portal.mu.Lock()
			requests, closes := portal.called, portal.closedSessions
			portal.mu.Unlock()
			if requests != tc.attempts || closes != tc.closes {
				t.Fatalf("resource ownership mismatch: createRequests=%d, closeCalls=%d, want %d,%d; error=%v", requests, closes, tc.attempts, tc.closes, err)
			}
		})
	}
}

// An EIS writer may report success just as the compositor revokes its portal
// authorization. An accepted command receipt must never be returned as success
// once its session has been revoked.
func TestPortalSubmitRejectsSuccessfulReceiptAfterAuthorizationRevoked(t *testing.T) {
	b := syntheticQueuedPortal()
	received := make(chan remoteDesktopCommand, 1)
	go func() { received <- <-b.commands }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- b.submit(ctx, func() error { return nil }) }()
	var command remoteDesktopCommand
	select {
	case command = <-received:
	case <-ctx.Done():
		t.Fatal("command was not admitted")
	}
	b.revokedState.Store(&remoteStateError{err: ErrRemoteDesktopRevoked})
	close(b.revoked)
	command.done <- nil
	select {
	case err := <-result:
		if !errors.Is(err, ErrRemoteDesktopRevoked) {
			t.Fatalf("revoked portal returned successful receipt: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("revoked request did not finish")
	}
}

// A revoked authorization remains authoritative even if the queued writer
// completed with an unrelated I/O error. Both failure causes are useful to
// callers diagnosing why a previously authorized command failed.
func TestPortalSubmitJoinsTransportErrorWithLateRevocation(t *testing.T) {
	b := syntheticQueuedPortal()
	received := make(chan remoteDesktopCommand, 1)
	go func() { received <- <-b.commands }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- b.submit(ctx, func() error { return nil }) }()
	var command remoteDesktopCommand
	select {
	case command = <-received:
	case <-ctx.Done():
		t.Fatal("command was not admitted")
	}
	transportFailure := errors.New("synthetic EIS write rejected")
	b.revokedState.Store(&remoteStateError{err: ErrRemoteDesktopRevoked})
	// Do not close the revoked notification yet: dispatch receipt must observe
	// the retired state independently of the channel notification.
	command.done <- transportFailure
	select {
	case err := <-result:
		if !errors.Is(err, ErrRemoteDesktopRevoked) || !errors.Is(err, transportFailure) {
			t.Fatalf("lost revoked/transport error causes: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("revoked transport error did not finish")
	}
}

func TestRemoteDesktopSubmitWakesWhenQueuedBackendStopsOrRevokes(t *testing.T) {
	for _, tc := range []struct {
		name string
		wake func(*RemoteDesktopBackend)
		want error
	}{
		{
			name: "authorization revoked",
			wake: func(b *RemoteDesktopBackend) {
				b.revokedState.Store(&remoteStateError{err: ErrRemoteDesktopRevoked})
				close(b.revoked)
			},
			want: ErrRemoteDesktopRevoked,
		},
		{
			name: "backend stopped",
			wake: func(b *RemoteDesktopBackend) { close(b.stop) },
			want: ErrRemoteDesktopUnavailable,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := syntheticQueuedPortal()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			wait := &observedEISWaitContext{parent: ctx, started: make(chan struct{})}
			result := make(chan error, 1)
			go func() { result <- backend.submit(wait, func() error { return nil }) }()
			select {
			case <-wait.started:
			case <-ctx.Done():
				t.Fatal("submit did not enter its queue wait")
			}
			tc.wake(backend)
			select {
			case err := <-result:
				if !errors.Is(err, tc.want) {
					t.Fatalf("submit after %s returned %v, want %v", tc.name, err, tc.want)
				}
			case <-ctx.Done():
				t.Fatalf("submit did not wake after %s", tc.name)
			}
		})
	}
}

func TestPortalSubscribeClosedCleansSignalChannelWhenMatchIsRejected(t *testing.T) {
	client, _ := startSyntheticPortalBus(t)
	portal := &portalSession{
		conn: client,
		path: dbus.ObjectPath("/invalid object path"),
	}

	if err := portal.subscribeClosed(context.Background()); err == nil {
		t.Fatal("D-Bus accepted an invalid session path match")
	}
	if portal.owner == "" || len(portal.match) != 4 {
		t.Fatalf("invalid match did not pass owner resolution and rule construction: owner=%q rules=%d", portal.owner, len(portal.match))
	}
	if portal.signals != nil {
		t.Fatal("failed D-Bus match left a registered signal channel")
	}
}

// Reject invalid typed-input contexts before parsing or dispatch: otherwise
// a nil operation context could be mistaken for background authorization.
func TestRemoteDesktopTypeNilContextRejectsWithoutEISFrames(t *testing.T) {
	b, peer := newWireObservedRemoteDesktop(t)
	err := b.Type(nil, "private{enter}")
	if err == nil || !strings.Contains(err.Error(), "nil operation context") {
		t.Fatalf("nil context accepted: %v", err)
	}
	assertNoEIWireRequest(t, peer)
	if len(b.heldKeys) != 0 || len(b.buttons) != 0 || b.hasPointer {
		t.Fatalf("rejected type modified input state: keys=%v buttons=%v pointer=%t", b.heldKeys, b.buttons, b.hasPointer)
	}
	if err := b.stateError(); err != nil {
		t.Fatalf("local invalid context revoked healthy session: %v", err)
	}
}

// Scroll clicks are converted to EIS fixed-point deltas. Even a small click
// count must not permit a huge coordinate to wrap a signed 32-bit frame.
func TestRemoteDesktopScrollRejectsScaledCoordinateOverflowBeforeEISFrames(t *testing.T) {
	b, peer := newWireObservedRemoteDesktop(t)
	for _, tc := range []struct {
		name string
		x, y int
	}{
		{"positive horizontal", math.MaxInt32/120 + 1, 0},
		{"negative horizontal", math.MinInt32/120 - 1, 0},
		{"positive vertical", 0, math.MaxInt32/120 + 1},
		{"negative vertical", 0, math.MinInt32/120 - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := b.scroll(context.Background(), tc.x, tc.y, 1)
			if err == nil || !strings.Contains(err.Error(), "overflows") {
				t.Fatalf("out-of-range scroll accepted: %v", err)
			}
			assertNoEIWireRequest(t, peer)
			if len(b.heldKeys) != 0 || len(b.buttons) != 0 || b.hasPointer {
				t.Fatalf("rejected scroll modified state: keys=%v buttons=%v pointer=%t", b.heldKeys, b.buttons, b.hasPointer)
			}
			if err := b.stateError(); err != nil && !errors.Is(err, ErrRemoteDesktopUnavailable) {
				t.Fatalf("invalid scroll revoked authorized backend: %v", err)
			}
		})
	}
}

// Cancellation is authoritative before parsing or validating a command.
// The rejected command must neither dispatch input nor disclose whether an
// otherwise unauthorised payload was syntactically valid.
func TestRemoteDesktopTypePreCanceledRejectsBeforeParsingAndValidation(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, payload := range []string{"{", "{not-a-real-key}", "private text"} {
		err := b.Type(ctx, payload)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled Type(%q) returned %v instead of cancellation", payload, err)
		}
	}
}

func TestRemoteDesktopTypeNilContextRejectsBeforeParsing(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	if err := b.Type(nil, "{"); err == nil || err.Error() != "input/remote-desktop: nil operation context" {
		t.Fatalf("nil-context Type returned %v", err)
	}
}

func TestRemoteDesktopTypePreflightRejectsLateUnauthorizedActions(t *testing.T) {
	tests := []struct {
		name    string
		sender  eiSender
		actions []keySend
		want    string
	}{
		{"invalid UTF8 after key", eiSender{keyboard: 1, keyboardDev: 2, text: 3, textDev: 4}, []keySend{{key: "enter"}, {text: string([]byte{0xff})}}, "UTF-8"},
		{"missing text device after key", eiSender{keyboard: 1, keyboardDev: 2, text: 3}, []keySend{{key: "enter"}, {text: "hello"}}, "text input"},
		{"missing text object", eiSender{keyboard: 1, keyboardDev: 2, textDev: 4}, []keySend{{key: "enter"}, {text: "hello"}}, "text input"},
		{"missing keyboard device after text", eiSender{text: 1, textDev: 2, keyboard: 3}, []keySend{{text: "hello"}, {key: "enter"}}, "keyboard input"},
		{"unknown key after text", eiSender{text: 1, textDev: 2, keyboard: 3, keyboardDev: 4}, []keySend{{text: "hello"}, {key: "nonexistent-key-name"}}, "unknown key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := &RemoteDesktopBackend{sender: &tt.sender}
			got, err := backend.validatedTypeKeycodes(tt.actions)
			if err == nil || !strings.Contains(err.Error(), tt.want) || got != nil {
				t.Fatalf("preflight returned codes=%v error=%v, want error containing %q and no codes", got, err, tt.want)
			}
		})
	}
}

func TestRemoteDesktopTypePreflightAndHeldKeyOwnership(t *testing.T) {
	sender := &eiSender{keyboard: 1, keyboardDev: 2, text: 3, textDev: 4}
	shift, err := remoteKeyCode("shift")
	if err != nil {
		t.Fatal(err)
	}
	backend := &RemoteDesktopBackend{sender: sender, heldKeys: map[uint32]bool{shift: true}}
	tests := []struct {
		name    string
		actions []keySend
		want    string
	}{
		{"stray modifier release", []keySend{{key: "ctrl", up: true}}, "not held"},
		{"stray ordinary release", []keySend{{key: "enter", up: true}}, "not held"},
		{"duplicate press", []keySend{{key: "shift", down: true}}, "already held"},
		{"tap held modifier", []keySend{{key: "shift"}}, "already held"},
		{"late duplicate in batch", []keySend{{key: "enter", down: true}, {text: "hello"}, {key: "enter", down: true}}, "already held"},
		{"late stray release in batch", []keySend{{text: "hello"}, {key: "enter", up: true}}, "not held"},
		{"release then tap is allowed", []keySend{{key: "shift", up: true}, {key: "shift"}}, ""},
		{"press then release is allowed", []keySend{{key: "enter", down: true}, {key: "enter", up: true}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			codes, err := backend.validatedTypeKeycodes(tt.actions)
			if err != nil {
				t.Fatalf("preflight: %v", err)
			}
			err = backend.validateTypeHeldSequence(tt.actions, codes)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("valid sequence rejected: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("sequence error = %v, want %q", err, tt.want)
			}
			if !backend.heldKeys[shift] || len(backend.heldKeys) != 1 {
				t.Fatalf("preflight mutated held-key ownership: %v", backend.heldKeys)
			}
		})
	}
}

// A submitted operation may be canceled after the writer has admitted it.
// The writer must retire its deadline callback, preserve both cancellation
// and a real device failure, and continue accepting later authorized work.
func TestRemoteDesktopWriterCancellationRetiresDeadlineForNextOperation(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	released := make(chan struct{})
	errorFromDevice := errors.New("synthetic device write failure")
	first := make(chan error, 1)
	go func() {
		first <- b.submit(ctx, func() error {
			close(started)
			<-released
			return errorFromDevice
		})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("writer did not admit first request")
	}
	cancel()
	close(released)
	select {
	case err := <-first:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled request accepted: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled request never returned")
	}
	// The first caller can return before its writer finishes draining. A second
	// command is serialized behind the first and must not inherit its deadline.
	nextCtx, nextCancel := context.WithTimeout(context.Background(), time.Second)
	defer nextCancel()
	ran := false
	if err := b.submit(nextCtx, func() error { ran = true; return nil }); err != nil || !ran {
		t.Fatalf("writer failed to recover from canceled operation: executed=%v err=%v", ran, err)
	}
	if b.stateError() != nil {
		t.Fatalf("cancellation revoked portal authority: %v", b.stateError())
	}
}

// Inspect the writer receipt directly: submit can return on ctx.Done before
// observing command.done, but the writer must retain the underlying device
// failure when it records a terminal canceled operation.
func TestRemoteDesktopWriterReceiptPreservesDeviceFailureAndCancellation(t *testing.T) {
	b, _, _ := newSyntheticRevocableRemoteDesktop(t)
	deviceFailure := errors.New("synthetic keyboard failure")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	command := remoteDesktopCommand{ctx: ctx, done: make(chan error, 1), run: func() error {
		close(started)
		<-release
		return deviceFailure
	}}
	select {
	case b.commands <- command:
	case <-time.After(time.Second):
		t.Fatal("command not admitted")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("writer did not run command")
	}
	cancel()
	close(release)
	select {
	case err := <-command.done:
		if !errors.Is(err, context.Canceled) || !errors.Is(err, deviceFailure) {
			t.Fatalf("writer receipt discarded error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("writer never acknowledged canceled operation")
	}
	nextCtx, nextCancel := context.WithTimeout(context.Background(), time.Second)
	defer nextCancel()
	if err := b.submit(nextCtx, func() error { return nil }); err != nil {
		t.Fatalf("failed operation poisoned writer: %v", err)
	}
}

// Absent optional interfaces are represented by object ID zero. Object zero
// is a global EIS protocol object and must never be confused with a device
// teardown merely because an optional device was not offered by the server.
func TestRemoteDesktopUnboundOptionalDeviceDoesNotRevokeOnGlobalObjectZero(t *testing.T) {
	backend, _, client := newSyntheticRevocableRemoteDesktop(t)
	backend.sender.scroll, backend.sender.scrollDev = 0, 0
	backend.sender.text, backend.sender.textDev = 0, 0
	if backend.isEISLifecycleFailure(eiMessage{object: 0, opcode: 0}) {
		t.Fatal("global EIS object zero misidentified as unbound device removal")
	}
	client.messages <- eiMessage{object: 0, opcode: 0}
	select {
	case <-backend.revoked:
		t.Fatal("unrelated global EIS event revoked live portal authorization")
	case <-time.After(40 * time.Millisecond):
	}
	if err := backend.stateError(); err != nil {
		t.Fatalf("global event invalidated authorized session: %v", err)
	}
	client.messages <- eiMessage{object: backend.sender.keyboardDev, opcode: 8}
	select {
	case <-backend.revoked:
	case <-time.After(time.Second):
		t.Fatal("real selected device suspension did not revoke access")
	}
}

func TestRemoteDesktopZeroDistanceScrollDoesNotAcknowledgeCanceledOrNilOperation(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{name: "nil operation context", ctx: nil, want: ErrRemoteDesktopUnavailable},
		{name: "pre-canceled operation", ctx: func() context.Context { ctx, cancel := context.WithCancel(context.Background()); cancel(); return ctx }(), want: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, _ := newSyntheticRevocableRemoteDesktop(t)
			for _, run := range []func(context.Context, int) error{b.ScrollUp, b.ScrollDown, b.ScrollLeft, b.ScrollRight} {
				err := run(tc.ctx, 0)
				if tc.ctx == nil {
					if err == nil {
						t.Fatal("nil context zero-scroll reported success")
					}
				} else if !errors.Is(err, tc.want) {
					t.Fatalf("canceled zero scroll reported success: %v", err)
				}
			}
			if len(b.heldKeys) != 0 || len(b.buttons) != 0 {
				t.Fatalf("zero scroll changed input state: keys=%v buttons=%v", b.heldKeys, b.buttons)
			}
		})
	}
}

type emptyReplyPortal struct {
	conn *dbus.Conn
	code uint32
}

func (p *emptyReplyPortal) CreateSession(sender dbus.Sender, options map[string]dbus.Variant) (string, *dbus.Error) {
	token, ok := options["handle_token"].Value().(string)
	if !ok {
		return "", dbus.MakeFailedError(errors.New("missing request token"))
	}
	path := portalRequestPath(string(sender), token)
	results := map[string]dbus.Variant{"session_handle": dbus.MakeVariant(syntheticSessionPath)}
	if err := p.conn.Emit(path, portalRequestInterface+".Response", p.code, results); err != nil {
		return "", dbus.MakeFailedError(err)
	}
	return "", nil
}

func TestPortalEmptyStringReplyUsesPredictedRequestPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		code uint32
		want error
	}{
		{name: "authorized", code: 0},
		{name: "denied", code: 1, want: ErrRemoteDesktopDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, portal := startSyntheticPortalBus(t)
			if err := portal.conn.Export(&emptyReplyPortal{conn: portal.conn, code: tc.code}, remoteDesktopPath, remoteDesktopInterface); err != nil {
				t.Fatal(err)
			}
			name, err := portalUniqueName(client.Names())
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			handle, err := portalCreateSession(ctx, client, name)
			if tc.want != nil {
				if handle != "" || !errors.Is(err, tc.want) {
					t.Fatalf("denied response returned handle=%q err=%v", handle, err)
				}
			} else if err != nil || handle != syntheticSessionPath {
				t.Fatalf("authorized response returned handle=%q err=%v", handle, err)
			}
		})
	}
}

const syntheticSessionPath = "/org/freedesktop/portal/desktop/session/fixture"

type syntheticPortalSession struct {
	portal *syntheticPortal
}

func (s *syntheticPortalSession) Close() *dbus.Error {
	s.portal.mu.Lock()
	s.portal.closedSessions++
	s.portal.mu.Unlock()
	return nil
}

func (p *syntheticPortal) SelectDevices(_ dbus.ObjectPath, options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	p.mu.Lock()
	p.selectOptions = options
	p.mu.Unlock()
	return p.reply("SelectDevices", map[string]dbus.Variant{}), nil
}
func (p *syntheticPortal) Start(_ dbus.ObjectPath, _ string, _ map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	p.mu.Lock()
	response := make(map[string]dbus.Variant)
	hook := p.startHook
	if p.startResponse != nil {
		for key, value := range p.startResponse {
			response[key] = value
		}
	} else {
		response["devices"] = dbus.MakeVariant(p.deviceMask)
		if p.persistToken != "" {
			response["restore_token"] = dbus.MakeVariant(p.persistToken)
		}
	}
	p.mu.Unlock()
	if hook != nil {
		hook()
	}
	return p.reply("Start", response), nil
}
func (p *syntheticPortal) ConnectToEIS(_ dbus.ObjectPath, _ map[string]dbus.Variant) (dbus.UnixFD, *dbus.Error) {
	p.mu.Lock()
	fd, err := p.eisFD, p.connectErr
	p.mu.Unlock()
	return fd, err
}
func (p *syntheticPortal) reply(method string, results map[string]dbus.Variant) dbus.ObjectPath {
	path := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/fixture/" + method)
	p.mu.Lock()
	code := uint32(0)
	if method == "SelectDevices" {
		code = p.selectCode
	}
	if method == "Start" {
		code = p.startCode
	}
	customBody := p.responseBody != nil
	responseBody := append([]any(nil), p.responseBody...)
	p.mu.Unlock()
	go func() {
		time.Sleep(10 * time.Millisecond)
		if customBody {
			_ = p.conn.Emit(path, portalRequestInterface+".Response", responseBody...)
		} else {
			_ = p.conn.Emit(path, portalRequestInterface+".Response", code, results)
		}
	}()
	return path
}

func TestSyntheticPortalFullAuthorizedEISLifecycle(t *testing.T) { //nolint:gocyclo // Full protocol or mock state transitions are asserted in one transaction.
	client, portal := startSyntheticPortalBus(t)
	_ = client
	// Simulate a Wayland-capable runtime without connecting to a real display.
	runtimeDir, err := os.MkdirTemp("/var/tmp", "pf-eis-runtime-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	fakeWayland := filepath.Join(runtimeDir, "wayland-1")
	listener, err := net.Listen("unix", fakeWayland)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	eisClient, eisServer := unixConnectionPair(t)
	defer eisServer.Close()
	file, err := eisClient.File()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	_ = eisClient.Close()

	portal.mu.Lock()
	portal.eisFD = dbus.UnixFD(file.Fd())
	portal.deviceMask = portalDeviceKeyboard | portalDevicePointer
	portal.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	peerDone := make(chan error, 1)
	go func() { peerDone <- serveSyntheticAuthorizedEISPeer(eisServer) }()

	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=" + fakeWayland,
		"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress,
	})
	inputter, err := OpenRemoteDesktopContext(ctx, rt, RemoteDesktopOptions{})
	if err != nil {
		t.Fatalf("full portal + EIS startup: %v", err)
	}
	backend, ok := inputter.(*RemoteDesktopBackend)
	if !ok {
		t.Fatalf("unexpected inputter type %T", inputter)
	}
	defer backend.Close()
	select {
	case peerErr := <-peerDone:
		if peerErr != nil {
			t.Fatalf("synthetic EIS peer: %v", peerErr)
		}
	case <-ctx.Done():
		t.Fatalf("EIS peer setup timed out: %v", ctx.Err())
	}
	portal.mu.Lock()
	options := portal.selectOptions
	portal.mu.Unlock()
	if options["types"].Value() != portalDeviceKeyboard|portalDevicePointer {
		t.Fatalf("portal grant request missing keyboard/pointer: %+v", options)
	}
	if options["persist_mode"].Value() != portalPersistNever {
		t.Fatalf("unexpected persistent consent requested: %+v", options)
	}
	if keyErr := backend.KeyDown(ctx, "a"); keyErr != nil {
		t.Fatalf("send key through authorized session: %v", keyErr)
	}
	key, err := readSyntheticEISFrame(eisServer)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := readSyntheticEISFrame(eisServer)
	if err != nil {
		t.Fatal(err)
	}
	if key.object != eiServerIDFloor+103 || key.opcode != 1 || frame.object != 0xff00000000000012 || frame.opcode != 3 {
		t.Fatalf("authorized key not delivered: %+v %+v", key, frame)
	}
	if err := backend.KeyUp(ctx, "a"); err != nil {
		t.Fatalf("release key: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := readSyntheticEISFrame(eisServer); err != nil {
			t.Fatal(err)
		}
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("closing authorized session: %v", err)
	}
}

func readSyntheticEISFrame(conn *net.UnixConn) (testEIRequest, error) {
	header := make([]byte, eiHeaderSize)
	if _, err := io.ReadFull(conn, header); err != nil {
		return testEIRequest{}, err
	}
	n := int(binary.NativeEndian.Uint32(header[8:12]))
	if n < eiHeaderSize || n > eiMaximumMessage || n%4 != 0 {
		return testEIRequest{}, fmt.Errorf("invalid EI frame length %d", n)
	}
	body := make([]byte, n-eiHeaderSize)
	if _, err := io.ReadFull(conn, body); err != nil {
		return testEIRequest{}, err
	}
	return testEIRequest{object: binary.NativeEndian.Uint64(header[:8]), opcode: binary.NativeEndian.Uint32(header[12:16]), body: body}, nil
}

func serveSyntheticAuthorizedEISPeer(conn *net.UnixConn) error { //nolint:gocyclo // Synthetic handshake peer asserts a complete protocol exchange.
	if err := conn.SetDeadline(time.Now().Add(4 * time.Second)); err != nil {
		return err
	}
	if _, err := conn.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		return err
	}
	gotFinish := false
	for i := 0; i < 24; i++ {
		request, err := readSyntheticEISFrame(conn)
		if err != nil {
			return err
		}
		if request.object != 0 {
			return fmt.Errorf("handshake object %d", request.object)
		}
		if request.opcode == 1 {
			gotFinish = true
			break
		}
	}
	if !gotFinish {
		return fmt.Errorf("EIS handshake never completed")
	}
	const connID = uint64(0xff00000000000010)
	const seatID = uint64(0xff00000000000011)
	const devID = uint64(0xff00000000000012)
	stream := eiRequest(0, 2, append(append(eiU32(9), eiU64(connID)...), eiU32(1)...))
	stream = append(stream, eiRequest(connID, 1, eiU64(seatID))...)
	for i, name := range []string{"ei_pointer_absolute", "ei_button", "ei_keyboard", "ei_scroll", "ei_text"} {
		stream = append(stream, eiRequest(seatID, 2, append(eiU64(uint64(1<<i)), eiString(name)...))...)
	}
	stream = append(stream, eiRequest(seatID, 3, nil)...)
	if _, writeErr := conn.Write(stream); writeErr != nil {
		return writeErr
	}
	bind, err := readSyntheticEISFrame(conn)
	if err != nil {
		return err
	}
	if bind.object != seatID || bind.opcode != 1 {
		return fmt.Errorf("incorrect seat binding: %+v", bind)
	}
	callback, err := readSyntheticEISFrame(conn)
	if err != nil {
		return err
	}
	if callback.object != connID || callback.opcode != 0 {
		return fmt.Errorf("incorrect callback: %+v", callback)
	}
	callbackID := binary.NativeEndian.Uint64(callback.body[:8])

	stream = eiRequest(seatID, 4, append(eiU64(devID), eiU32(1)...))
	for i, name := range []string{"ei_pointer_absolute", "ei_button", "ei_keyboard", "ei_scroll", "ei_text"} {
		stream = append(stream, eiRequest(devID, 5, append(eiU64(eiServerIDFloor+101+uint64(i)), eiString(name)...))...)
	}
	region := append(append(append(append(eiU32(0), eiU32(0)...), eiU32(1920)...), eiU32(1080)...), eiF32(1)...)
	stream = append(stream, eiRequest(devID, 4, region)...)
	stream = append(stream, eiRequest(devID, 6, nil)...)
	stream = append(stream, eiRequest(devID, 7, eiU32(9))...)
	stream = append(stream, eiRequest(callbackID, 0, eiU64(0))...)
	if _, writeErr := conn.Write(stream); writeErr != nil {
		return writeErr
	}
	start, err := readSyntheticEISFrame(conn)
	if err != nil {
		return err
	}
	if start.object != devID || start.opcode != 1 {
		return fmt.Errorf("device emulation not started: %+v", start)
	}
	return nil
}

type cancelDuringPortalPreflight struct {
	context.Context //nolint:containedctx // deterministic canceled-provider boundary, not a stored operation.
	cancel          context.CancelFunc
	once            sync.Once
}

func (c *cancelDuringPortalPreflight) Err() error {
	err := c.Context.Err()
	c.once.Do(c.cancel)
	return err
}

// A zero-distance scroll is a no-op, but its return is still an acknowledgement
// of the caller operation. Cancellation at admission must win over success.
func TestPortalCanceledZeroScrollDoesNotAcknowledgeSuccess(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(*RemoteDesktopBackend, context.Context) error
	}{
		{"up", func(b *RemoteDesktopBackend, ctx context.Context) error { return b.ScrollUp(ctx, 0) }},
		{"down", func(b *RemoteDesktopBackend, ctx context.Context) error { return b.ScrollDown(ctx, 0) }},
		{"left", func(b *RemoteDesktopBackend, ctx context.Context) error { return b.ScrollLeft(ctx, 0) }},
		{"right", func(b *RemoteDesktopBackend, ctx context.Context) error { return b.ScrollRight(ctx, 0) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := syntheticQueuedPortal()
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &cancelDuringPortalPreflight{Context: base, cancel: cancel}
			if err := tc.run(b, ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("scroll acknowledged a canceled operation: %v", err)
			}
		})
	}
}

// The EIS authorized coordinate system is session-private; a canceled query
// may not disclose its successful metadata even if authority was valid at entry.
func TestPortalCanceledCoordinateMetadataDoesNotPublish(t *testing.T) {
	b := syntheticQueuedPortal()
	b.sender.pointers = []eiPointerDevice{{device: 11, pointer: 22, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelDuringPortalPreflight{Context: base, cancel: cancel}
	space, err := b.PointerCoordinateSpace(ctx)
	if !errors.Is(err, context.Canceled) || space.Kind != "" {
		t.Fatalf("canceled metadata query returned %+v, %v", space, err)
	}
}

type failingPortalEntropyReader struct{ err error }

func (r failingPortalEntropyReader) Read([]byte) (int, error) { return 0, r.err }

func TestPortalRequestTokenRejectsUnavailableEntropy(t *testing.T) {
	// A portal request must not proceed with a predictable token when the
	// cryptographic source fails. Restore the process-global reader in cleanup.
	original := rand.Reader
	failure := io.ErrUnexpectedEOF
	rand.Reader = failingPortalEntropyReader{err: failure}
	t.Cleanup(func() { rand.Reader = original })

	token, err := portalHandleToken()
	if token != "" || !errors.Is(err, failure) || !strings.Contains(err.Error(), "create request token") {
		t.Fatalf("portalHandleToken() = (%q, %v), want empty token and wrapped entropy error", token, err)
	}
}

// Neither portal session creation nor an ordinary portal request may begin
// D-Bus authorization with an unguessable handle unavailable. Both must fail
// at the entropy boundary even when no connection is present.
func TestPortalSessionAndRequestRejectMissingEntropyBeforeBusUse(t *testing.T) {
	original := rand.Reader
	failure := io.ErrUnexpectedEOF
	rand.Reader = failingPortalEntropyReader{err: failure}
	t.Cleanup(func() { rand.Reader = original })

	if session, err := portalCreateSession(context.Background(), nil, ""); session != "" || !errors.Is(err, failure) {
		t.Fatalf("session creation with failed entropy = (%q, %v), want no session and wrapped entropy error", session, err)
	}
	if results, err := portalRequest(context.Background(), nil, "", "CreateSession", nil); results != nil || !errors.Is(err, failure) {
		t.Fatalf("portal request with failed entropy = (%v, %v), want no results and wrapped entropy error", results, err)
	}
}

func TestOpenRemoteDesktopRejectsUnavailableStartupInputs(t *testing.T) {
	base := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=/tmp/synthetic-wayland.sock",
		"DBUS_SESSION_BUS_ADDRESS=not-a-dbus-address",
	})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		rt   env.Runtime
		want error
	}{
		{name: "nil context", rt: base, want: ErrRemoteDesktopUnavailable},
		{name: "canceled context", ctx: canceled, rt: base, want: context.Canceled},
		{name: "missing Wayland display", ctx: context.Background(), rt: env.FromEnviron([]string{"DBUS_SESSION_BUS_ADDRESS=not-a-dbus-address"}), want: ErrRemoteDesktopUnavailable},
		{name: "invalid D-Bus address", ctx: context.Background(), rt: base, want: ErrRemoteDesktopUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inputter, err := OpenRemoteDesktopContext(tc.ctx, tc.rt, RemoteDesktopOptions{})
			if inputter != nil {
				t.Fatalf("unavailable startup returned inputter %T", inputter)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("OpenRemoteDesktopContext error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestSyntheticPortalStartupFailuresCloseAcquiredSession(t *testing.T) {
	_, portal := startSyntheticPortalBus(t)
	baseRuntime := func() env.Runtime {
		return env.FromEnviron([]string{
			"WAYLAND_DISPLAY=/tmp/synthetic-wayland.sock",
			"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress,
			"XDG_STATE_HOME=" + privatePortalTokenFixtureDir(t),
		})
	}
	testCases := []struct {
		name      string
		configure func(*testing.T)
		options   RemoteDesktopOptions
		want      error
		contains  string
	}{
		{
			name: "device authorization omitted",
			configure: func(*testing.T) {
				portal.startResponse = map[string]dbus.Variant{}
			},
			want: ErrRemoteDesktopDenied,
		},
		{
			name: "device authorization malformed",
			configure: func(*testing.T) {
				portal.startResponse = map[string]dbus.Variant{"devices": dbus.MakeVariant("keyboard and pointer")}
			},
			want: ErrRemoteDesktopDenied,
		},
		{
			name: "persistent permission omitted",
			configure: func(*testing.T) {
				portal.startResponse = map[string]dbus.Variant{"devices": dbus.MakeVariant(portalDeviceKeyboard | portalDevicePointer)}
			},
			options: RemoteDesktopOptions{PersistPermission: true},
			want:    ErrRemoteDesktopDenied,
		},
		{
			name: "persistent permission malformed",
			configure: func(*testing.T) {
				portal.startResponse = map[string]dbus.Variant{
					"devices":       dbus.MakeVariant(portalDeviceKeyboard | portalDevicePointer),
					"restore_token": dbus.MakeVariant(uint32(7)),
				}
			},
			options: RemoteDesktopOptions{PersistPermission: true},
			want:    ErrRemoteDesktopDenied,
		},
		{
			name: "EIS connection denied",
			configure: func(*testing.T) {
				portal.connectErr = dbus.MakeFailedError(errors.New("synthetic EIS refusal"))
			},
			want: ErrRemoteDesktopUnavailable,
		},
		{
			name: "EIS descriptor is not a socket",
			configure: func(t *testing.T) {
				_, writeEnd, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = writeEnd.Close() })
				portal.eisFD = dbus.UnixFD(writeEnd.Fd())
			},
			want:     ErrRemoteDesktopUnavailable,
			contains: "connect EIS descriptor",
		},
		{
			name: "EIS handshake rejected",
			configure: func(t *testing.T) {
				client, peer := unixConnectionPair(t)
				file, err := client.File()
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = file.Close() })
				t.Cleanup(func() { _ = client.Close() })
				t.Cleanup(func() { _ = peer.Close() })
				portal.eisFD = dbus.UnixFD(file.Fd())
				if _, err := peer.Write(eiRequest(0, 0, eiU32(0))); err != nil {
					t.Fatal(err)
				}
			},
			contains: "server advertised handshake version zero",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			beforeClose := 0
			func() {
				portal.mu.Lock()
				defer portal.mu.Unlock()
				portal.deviceMask = portalDeviceKeyboard | portalDevicePointer
				portal.persistToken = ""
				portal.startResponse = nil
				portal.connectErr = nil
				portal.eisFD = 0
				beforeClose = portal.closedSessions
				tc.configure(t)
			}()

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			inputter, err := OpenRemoteDesktopContext(ctx, baseRuntime(), tc.options)
			if inputter != nil {
				t.Fatalf("failed startup returned inputter %T", inputter)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("OpenRemoteDesktopContext error = %v, want %v", err, tc.want)
			}
			if tc.contains != "" && (err == nil || !strings.Contains(err.Error(), tc.contains)) {
				t.Fatalf("OpenRemoteDesktopContext error = %v, want %q", err, tc.contains)
			}
			if err == nil {
				t.Fatal("invalid portal grant unexpectedly opened an input backend")
			}
			portal.mu.Lock()
			closedSessions := portal.closedSessions
			portal.mu.Unlock()
			if closedSessions != beforeClose+1 {
				t.Fatalf("failed startup closed %d portal sessions, want exactly one", closedSessions-beforeClose)
			}
		})
	}
}

func TestOpenRemoteDesktopRejectsAuthorizedNonUnixEISDescriptor(t *testing.T) {
	_, portal := startSyntheticPortalBus(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	server, err := listener.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	tcp, ok := client.(*net.TCPConn)
	if !ok {
		t.Fatalf("local synthetic connection has type %T", client)
	}
	file, err := tcp.File()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	portal.mu.Lock()
	portal.deviceMask = portalDeviceKeyboard | portalDevicePointer
	portal.eisFD = dbus.UnixFD(file.Fd())
	portal.mu.Unlock()
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=/tmp/synthetic-wayland.sock",
		"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	inputter, err := OpenRemoteDesktopContext(ctx, rt, RemoteDesktopOptions{})
	if inputter != nil {
		t.Fatalf("non-Unix EIS descriptor returned inputter %T", inputter)
	}
	if !errors.Is(err, ErrRemoteDesktopUnavailable) || err == nil || !strings.Contains(err.Error(), "EIS descriptor is not a Unix socket") {
		t.Fatalf("non-Unix EIS descriptor error = %v", err)
	}
	portal.mu.Lock()
	closed := portal.closedSessions
	portal.mu.Unlock()
	if closed != 1 {
		t.Fatalf("invalid EIS transport left %d portal sessions closed, want one", closed)
	}
}

type syntheticPortal struct {
	conn              *dbus.Conn
	mu                sync.Mutex
	code              uint32
	result            map[string]dbus.Variant
	responseBody      []any
	createFailure     string
	startResponse     map[string]dbus.Variant
	startHook         func()
	connectErr        *dbus.Error
	skip              bool
	emitUnrelated     bool                 // deliver a different portal signal before Response
	requestPaths      chan dbus.ObjectPath // optional test observation of method-return path
	createMethodDelay time.Duration        // delay method completion after emitting the Response
	releaseOwner      bool
	getVersion        any
	versionFailure    string
	busAddress        string
	deviceMask        uint32
	selectCode        uint32
	startCode         uint32
	persistToken      string
	eisFD             dbus.UnixFD
	selectOptions     map[string]dbus.Variant
	called            int
	closedSessions    int
}

func (p *syntheticPortal) CreateSession(options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	token, _ := options["handle_token"].Value().(string)
	path := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/fixture/" + token)
	p.mu.Lock()
	code, result, skip := p.code, p.result, p.skip
	failure, customBody := p.createFailure, p.responseBody != nil
	responseBody := append([]any(nil), p.responseBody...)
	unrelated := p.emitUnrelated
	paths := p.requestPaths
	methodDelay := p.createMethodDelay
	releaseName := p.releaseOwner
	p.called++
	p.mu.Unlock()
	if failure != "" {
		return "", dbus.MakeFailedError(errors.New(failure))
	}
	if paths != nil {
		select {
		case paths <- path:
		default:
		}
	}
	if !skip && releaseName {
		if unrelated {
			_ = p.conn.Emit(path, portalRequestInterface+".Unexpected", uint32(1), map[string]dbus.Variant{})
		}
		if customBody {
			if err := p.conn.Emit(path, portalRequestInterface+".Response", responseBody...); err != nil {
				return "", dbus.MakeFailedError(err)
			}
		} else if err := p.conn.Emit(path, portalRequestInterface+".Response", code, result); err != nil {
			return "", dbus.MakeFailedError(err)
		}
		if _, err := p.conn.ReleaseName(remoteDesktopBus); err != nil {
			return "", dbus.MakeFailedError(err)
		}
	} else if !skip {
		go func() {
			time.Sleep(10 * time.Millisecond)
			if unrelated {
				// Different signal, same trusted service and request path.
				// A session with another listener can receive both messages.
				_ = p.conn.Emit(path, portalRequestInterface+".Unexpected", uint32(1), map[string]dbus.Variant{})
				time.Sleep(25 * time.Millisecond)
			}
			if customBody {
				_ = p.conn.Emit(path, portalRequestInterface+".Response", responseBody...)
			} else {
				_ = p.conn.Emit(path, portalRequestInterface+".Response", code, result)
			}
		}()
	}
	if methodDelay > 0 {
		time.Sleep(methodDelay)
	}
	return path, nil
}

func (p *syntheticPortal) Get(iface, key string) (dbus.Variant, *dbus.Error) {
	if iface != remoteDesktopInterface || key != "version" {
		return dbus.Variant{}, dbus.MakeFailedError(errors.New("unknown property"))
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.versionFailure != "" {
		return dbus.Variant{}, dbus.MakeFailedError(errors.New(p.versionFailure))
	}
	return dbus.MakeVariant(p.getVersion), nil
}

func startSyntheticPortalBus(t *testing.T) (*dbus.Conn, *syntheticPortal) {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("dbus-daemon unavailable")
	}
	dir, err := os.MkdirTemp("", "pf-portal-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "bus")
	address := "unix:path=" + socket
	proc := exec.Command("dbus-daemon", "--session", "--nofork", "--address="+address)
	if startErr := proc.Start(); startErr != nil {
		t.Fatal(startErr)
	}
	t.Cleanup(func() { _ = proc.Process.Kill(); _ = proc.Wait() })
	var service *dbus.Conn
	for i := 0; i < 100; i++ {
		service, err = dbusutil.SessionBusAddressContext(context.Background(), address)
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("private dbus connection: %v", err)
	}
	t.Cleanup(func() { _ = service.Close() })
	reply, err := service.RequestName(remoteDesktopBus, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("own portal service: %v %v", reply, err)
	}
	mock := &syntheticPortal{conn: service, busAddress: address, getVersion: uint32(2), result: map[string]dbus.Variant{"session_handle": dbus.MakeVariant("/org/freedesktop/portal/desktop/session/fixture")}}
	if exportErr := service.Export(mock, remoteDesktopPath, remoteDesktopInterface); exportErr != nil {
		t.Fatal(exportErr)
	}
	if exportErr := service.Export(mock, remoteDesktopPath, "org.freedesktop.DBus.Properties"); exportErr != nil {
		t.Fatal(exportErr)
	}
	if exportErr := service.Export(&syntheticPortalSession{portal: mock}, dbus.ObjectPath(syntheticSessionPath), portalSessionInterface); exportErr != nil {
		t.Fatal(exportErr)
	}
	client, err := dbusutil.SessionBusAddressContext(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client, mock
}

func TestSyntheticPortalVersionAndSessionAuthorization(t *testing.T) {
	client, portal := startSyntheticPortalBus(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := requireRemoteDesktopV2(ctx, client); err != nil {
		t.Fatalf("version 2 rejected: %v", err)
	}
	name, err := portalUniqueName(client.Names())
	if err != nil {
		t.Fatal(err)
	}
	path, err := portalCreateSession(ctx, client, name)
	if err != nil {
		t.Fatalf("authorized portal session: %v", err)
	}
	if path != "/org/freedesktop/portal/desktop/session/fixture" {
		t.Fatalf("session path %q", path)
	}
	portal.mu.Lock()
	calls := portal.called
	portal.getVersion = uint32(1)
	portal.mu.Unlock()
	if calls != 1 {
		t.Fatalf("create calls=%d", calls)
	}
	if err := requireRemoteDesktopV2(ctx, client); !errors.Is(err, ErrRemoteDesktopUnavailable) {
		t.Fatalf("outdated version accepted: %v", err)
	}
}

func TestSyntheticPortalRefusesDeniedOrMalformedAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name   string
		code   uint32
		result map[string]dbus.Variant
		want   error
	}{
		{"denied", 1, map[string]dbus.Variant{}, ErrRemoteDesktopDenied},
		{"missing session", 0, map[string]dbus.Variant{}, ErrRemoteDesktopUnavailable},
		{"malformed session", 0, map[string]dbus.Variant{"session_handle": dbus.MakeVariant(uint32(8))}, ErrRemoteDesktopUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, portal := startSyntheticPortalBus(t)
			portal.mu.Lock()
			portal.code = tc.code
			portal.result = tc.result
			portal.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			name, err := portalUniqueName(client.Names())
			if err != nil {
				t.Fatal(err)
			}
			_, err = portalCreateSession(ctx, client, name)
			if !errors.Is(err, tc.want) {
				t.Fatalf("authorization error = %v want %v", err, tc.want)
			}
		})
	}
}

func TestSyntheticPortalCanceledRequestDoesNotHang(t *testing.T) {
	client, portal := startSyntheticPortalBus(t)
	portal.mu.Lock()
	portal.skip = true
	portal.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	name, err := portalUniqueName(client.Names())
	if err != nil {
		t.Fatal(err)
	}
	_, err = portalCreateSession(ctx, client, name)
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(fmt.Sprint(err), "timed out") {
		t.Fatalf("unanswered portal request returned %v, want bounded timeout", err)
	}
}

func TestPortalRequestRejectsInvalidOptionsWithoutDBusRoundTrip(t *testing.T) {
	if _, err := portalRequest(context.Background(), nil, ":1.0", "CreateSession", nil); err == nil {
		t.Fatal("empty options accepted")
	}
	if _, err := portalRequest(context.Background(), nil, ":1.0", "CreateSession", []any{"bad"}); err == nil {
		t.Fatal("invalid options accepted")
	}
	for _, tc := range []struct {
		names []string
		valid bool
	}{
		{[]string{"org.freedesktop.portal.Desktop"}, false},
		{[]string{"org.freedesktop.portal.Desktop", ":1.25"}, true},
	} {
		got, err := portalUniqueName(tc.names)
		if tc.valid && (err != nil || got != ":1.25") {
			t.Fatalf("name %q error %v", got, err)
		}
		if !tc.valid && err == nil {
			t.Fatal("accepted non-unique D-Bus names")
		}
	}
}

func TestOpenRemoteDesktopRejectsSessionBusWithoutUnixFDs(t *testing.T) {
	_, portal := startSyntheticPortalBus(t)
	address, negotiationRejected := startPortalUnixFDRejectingProxy(t, portal.busAddress)
	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=/tmp/synthetic-wayland.sock",
		"DBUS_SESSION_BUS_ADDRESS=" + address,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	inputter, err := OpenRemoteDesktopContext(ctx, runtime, RemoteDesktopOptions{})
	if inputter != nil {
		t.Fatalf("session bus without descriptor passing returned inputter %T", inputter)
	}
	if err == nil || !errors.Is(err, ErrRemoteDesktopUnavailable) || !strings.Contains(err.Error(), "session bus does not support descriptor passing") {
		t.Fatalf("session bus without descriptor passing error = %v", err)
	}
	select {
	case <-negotiationRejected:
	default:
		t.Fatal("D-Bus client did not negotiate descriptor passing with the test bus")
	}
	portal.mu.Lock()
	calls := portal.called
	portal.mu.Unlock()
	if calls != 0 {
		t.Fatalf("portal received %d authorization requests before descriptor capability rejection", calls)
	}
}

func startPortalUnixFDRejectingProxy(t *testing.T, upstreamAddress string) (string, <-chan struct{}) {
	t.Helper()
	upstreamPath, ok := strings.CutPrefix(upstreamAddress, "unix:path=")
	if !ok || upstreamPath == "" {
		t.Fatalf("synthetic portal bus address %q is not a Unix path", upstreamAddress)
	}
	path := testutil.ShortSocketPath(t, "portal-no-fd.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	negotiationRejected := make(chan struct{}, 1)
	proxyDone := make(chan error, 1)
	go func() {
		client, err := listener.AcceptUnix()
		if err != nil {
			proxyDone <- err
			return
		}
		upstream, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: upstreamPath, Net: "unix"})
		if err != nil {
			_ = client.Close()
			proxyDone <- err
			return
		}
		proxyDone <- rejectPortalUnixFDNegotiation(client, upstream, negotiationRejected)
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case err := <-proxyDone:
			if err != nil && !portalProxyClosed(err) {
				t.Errorf("D-Bus Unix-FD proxy: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("D-Bus Unix-FD proxy did not stop after the client closed")
		}
	})
	return "unix:path=" + path, negotiationRejected
}

func rejectPortalUnixFDNegotiation(client, upstream *net.UnixConn, rejected chan<- struct{}) (proxyErr error) {
	serverReplies := make(chan error, 1)
	go func() {
		_, err := io.Copy(client, upstream)
		serverReplies <- err
	}()
	defer func() {
		_ = client.Close()
		_ = upstream.Close()
		if err := <-serverReplies; proxyErr == nil && err != nil && !portalProxyClosed(err) {
			proxyErr = err
		}
	}()

	reader := bufio.NewReader(client)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if portalProxyClosed(err) {
				return nil
			}
			return err
		}
		command := strings.TrimSpace(line)
		if command == "NEGOTIATE_UNIX_FD" {
			if _, err := io.WriteString(client, "ERROR Unix descriptor passing unavailable\r\n"); err != nil {
				return err
			}
			rejected <- struct{}{}
			continue
		}
		if _, err := io.WriteString(upstream, line); err != nil {
			return err
		}
		if command == "BEGIN" {
			_, err := io.Copy(upstream, reader)
			if portalProxyClosed(err) {
				return nil
			}
			return err
		}
	}
}

func portalProxyClosed(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE)
}
