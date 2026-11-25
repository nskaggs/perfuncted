package input

import (
	"context"
	"errors"
	"github.com/godbus/dbus/v5"
	"math"
	"strings"
	"testing"
	"time"
)

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
