package input

import (
	"context"
	"errors"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/env"
	"net"
	"strings"
	"testing"
	"time"
)

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
