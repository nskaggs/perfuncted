package input

import (
	"context"
	"errors"
	"fmt"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"strings"
	"sync"
	"testing"
	"time"
)

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
