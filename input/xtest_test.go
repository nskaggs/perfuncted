package input

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2/internal/third_party/xgb/xproto"
	"github.com/nskaggs/perfuncted/v2/internal/x11"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Internal XTEST helpers must refuse canceled operations before touching an
// uninitialized X11 connection or querying the server's keyboard map.
func TestXTestInternalDispatchRejectsCanceledOperationsBeforeX11Access(t *testing.T) {
	var backend XTestBackend
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"pointer movement", func() error { return backend.mouseMove(ctx, 12, 34) }},
		{"button event", func() error { return backend.mouseButton(ctx, xproto.ButtonPress, 1) }},
		{"scroll", func() error { return backend.scroll(ctx, 4, 1) }},
		{"text preflight", func() error { return backend.validateTextKeysyms(ctx, "a") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled dispatch reached X11 transport: %v", err)
			}
		})
	}
}

func TestXTestNilBackendCloseHasNoConnectionSideEffects(t *testing.T) {
	var backend *XTestBackend
	if err := backend.Close(); err != nil {
		t.Fatalf("nil XTEST close: %v", err)
	}
}

// Even when the requested key itself exists, a missing modifier mapping must
// reject direct action dispatch before XTEST can synthesize a key event.
func TestXTestInternalActionRejectsUnmappedModifierBeforeTransport(t *testing.T) {
	b := &XTestBackend{keymap: map[xproto.Keysym]keycodeLevel{xproto.Keysym('a'): {keycode: 38}}}
	err := b.typeAction(context.Background(), keySend{key: "a", modifiers: modifiers{ctrl: true}})
	if err == nil || !strings.Contains(err.Error(), "not found in keymap") {
		t.Fatalf("unmapped accelerator modifier reached transport: %v", err)
	}
}

// Internal literal dispatch must reject both missing runes and missing level
// modifiers without sending the underlying XTEST keycode to the server.
func TestXTestInternalLiteralRejectsUnavailableRuneAndShiftLevel(t *testing.T) {
	b := &XTestBackend{keymap: map[xproto.Keysym]keycodeLevel{xproto.Keysym('A'): {keycode: 38, level: 1}}}
	for _, tc := range []struct {
		name string
		ch   rune
	}{
		{"missing rune", 'Ω'},
		{"missing shift modifier", 'A'},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := b.typeTextRune(context.Background(), tc.ch, nil)
			if err == nil || !strings.Contains(err.Error(), "not found in keymap") {
				t.Fatalf("unavailable literal %q reached transport: %v", tc.ch, err)
			}
		})
	}
}

// A direct helper call must reject revoked work before parsing a request or
// consulting an uninitialized X11 connection.
func TestXTestInternalTypePreflightRejectsCanceledAndMalformedRequests(t *testing.T) {
	var backend XTestBackend
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := backend.typeContext(canceled, "{unclosed"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled internal type parsed request: %v", err)
	}
	if err := backend.typeContext(context.Background(), "{unclosed"); err == nil {
		t.Fatal("malformed internal type request was accepted")
	}
}

// Empty actions must remain no-ops, and malformed direct actions must not
// attempt event injection on an unavailable X11 connection.
func TestXTestInternalTypeActionNoopAndUnknownKeyBeforeTransport(t *testing.T) {
	var backend XTestBackend
	if err := backend.typeAction(context.Background(), keySend{}); err != nil {
		t.Fatalf("empty action was rejected: %v", err)
	}
	if err := backend.typeAction(context.Background(), keySend{key: "unknown-key"}); err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("unknown key reached XTEST transport: %v", err)
	}
}

// A caller may cancel after the transport operation starts but before its
// reply is acknowledged. XTEST must not report success from that operation;
// similarly, a device failure and cancellation must both remain observable.
func TestXTestOperationDoesNotAcknowledgeLateCanceledRequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail error
	}{
		{name: "transport succeeded"},
		{name: "transport failed", fail: errors.New("synthetic X11 operation failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &XTestBackend{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			executed := false
			err := b.withOperation(ctx, func(context.Context) error {
				executed = true
				cancel()
				return tc.fail
			})
			if !executed {
				t.Fatal("operation never entered")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("late cancellation acknowledged as success: %v", err)
			}
			if tc.fail != nil && !errors.Is(err, tc.fail) {
				t.Fatalf("device failure lost after cancellation: %v", err)
			}
		})
	}
}

// Direct operation admission must reject nil and retired XTEST transports,
// preserve caller cancellation, and release each accepted operation exactly
// once. No such path may authorize device activity after shutdown.
func TestXTestOperationAdmissionHonorsCancellationAndShutdown(t *testing.T) {
	var absent *XTestBackend
	if ctx, finish, err := absent.beginOperation(context.Background()); err == nil || ctx != nil || finish != nil {
		t.Fatalf("nil backend admitted operation: ctx=%v finishNil=%t err=%v", ctx, finish == nil, err)
	}
	b := &XTestBackend{}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if ctx, finish, err := b.beginOperation(canceled); !errors.Is(err, context.Canceled) || ctx != nil || finish != nil {
		t.Fatalf("canceled operation admitted: ctx=%v finishNil=%t err=%v", ctx, finish == nil, err)
	}
	active, finish, err := b.beginOperation(context.Background())
	if err != nil || active == nil || finish == nil {
		t.Fatalf("live operation admission failed: ctx=%v err=%v", active, err)
	}
	finish()
	if err := b.Close(); err != nil {
		t.Fatalf("backend close: %v", err)
	}
	if ctx, finish, err := b.beginOperation(context.Background()); !errors.Is(err, errXTestBackendClosed) || ctx != nil || finish != nil {
		t.Fatalf("retired backend admitted operation: ctx=%v finishNil=%t err=%v", ctx, finish == nil, err)
	}
	if err := b.withOperation(context.Background(), func(context.Context) error { t.Error("post-close transport dispatched"); return nil }); !errors.Is(err, errXTestBackendClosed) {
		t.Fatalf("retired operation result: %v", err)
	}
}

// Closing XTEST must cancel both the active transport operation and a second
// operation queued at its serialization gate. The queued operation must never
// reach the device, even if the first operation releases the gate during Close.
func TestXTestCloseCancelsQueuedOperationWithoutDispatch(t *testing.T) {
	b := &XTestBackend{}
	entered := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- b.withOperation(context.Background(), func(ctx context.Context) error {
			close(entered)
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first XTEST operation did not acquire gate")
	}
	secondDone := make(chan error, 1)
	secondStarted := make(chan struct{})
	dispatched := make(chan struct{}, 1)
	go func() {
		close(secondStarted)
		secondDone <- b.withOperation(context.Background(), func(context.Context) error { dispatched <- struct{}{}; return nil })
	}()
	<-secondStarted
	closeDone := make(chan error, 1)
	go func() { closeDone <- b.Close() }()
	for name, ch := range map[string]<-chan error{"active": firstDone, "queued": secondDone} {
		select {
		case err := <-ch:
			if !errors.Is(err, context.Canceled) && !(name == "queued" && errors.Is(err, errXTestBackendClosed)) {
				t.Errorf("%s operation completed after close: %v", name, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s operation did not drain during close", name)
		}
	}
	select {
	case <-dispatched:
		t.Fatal("queued operation dispatched to closed XTEST device")
	default:
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("close failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("close failed to drain queued operations")
	}
	if err := b.withOperation(context.Background(), func(context.Context) error { t.Error("post-close request dispatched"); return nil }); !errors.Is(err, errXTestBackendClosed) {
		t.Fatalf("post-close admission: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("idempotent close failed: %v", err)
	}
}

// Type must keep its explicit modifier ownership consistent with KeyDown/Up.
// In particular a caller-held Ctrl cannot be released as a temporary key,
// and a later command must see the same state as the previous command.
func TestXTestExplicitModifierHoldAndReleaseAcrossCommands(t *testing.T) {
	b, events := newModifierXTestBackend(t)
	ctrl := keycodeForCtrl(t, b)
	a, err := b.keycodeFor("a")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		command string
		want    []xtestEvent
		held    bool
	}{
		{"{ctrl down}", []xtestEvent{{xproto.KeyPress, ctrl}}, true},
		{"{ctrl+a}", []xtestEvent{{xproto.KeyPress, byte(a)}, {xproto.KeyRelease, byte(a)}}, true},
		{"{ctrl up}", []xtestEvent{{xproto.KeyRelease, ctrl}}, false},
		{"{ctrl+a}", []xtestEvent{{xproto.KeyPress, ctrl}, {xproto.KeyPress, byte(a)}, {xproto.KeyRelease, byte(a)}, {xproto.KeyRelease, ctrl}}, false},
	} {
		*events = nil
		if err := b.Type(context.Background(), tc.command); err != nil {
			t.Fatalf("type %q: %v", tc.command, err)
		}
		if !reflect.DeepEqual(*events, tc.want) {
			t.Fatalf("type %q sent %+v, want %+v", tc.command, *events, tc.want)
		}
		if held := b.modifierHeld(xproto.Keycode(ctrl)); held != tc.held {
			t.Fatalf("type %q owned ctrl=%t want=%t", tc.command, held, tc.held)
		}
	}
}

func TestXTestModifierTransactionFailsClosedAtEveryProtocolBoundary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		failAt int
		want   []xtestEvent
	}{
		{"mod down fails", 1, []xtestEvent{{xproto.KeyPress, 8}, {xproto.KeyRelease, 8}}},
		{"key down fails", 2, []xtestEvent{{xproto.KeyPress, 8}, {xproto.KeyPress, 9}, {xproto.KeyRelease, 9}, {xproto.KeyRelease, 8}}},
		{"key up fails", 3, []xtestEvent{{xproto.KeyPress, 8}, {xproto.KeyPress, 9}, {xproto.KeyRelease, 9}, {xproto.KeyRelease, 9}, {xproto.KeyRelease, 8}}},
		{"mod up fails", 4, []xtestEvent{{xproto.KeyPress, 8}, {xproto.KeyPress, 9}, {xproto.KeyRelease, 9}, {xproto.KeyRelease, 8}, {xproto.KeyRelease, 8}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0xffe3, 0x61}, func(ev xtestEvent) error {
				calls++
				if calls == tc.failAt {
					return errors.New("synthetic XTEST wire write rejected")
				}
				return nil
			})
			// Ctrl is slot 8, A is slot 9.
			err := b.Type(context.Background(), "{ctrl+a}")
			if err == nil {
				t.Fatal("failed XTEST wire message was acknowledged")
			}
			if !reflect.DeepEqual(*events, tc.want) {
				t.Fatalf("failed wire request and cleanup events=%v, want=%v", *events, tc.want)
			}
			if b.modifierHeld(xproto.Keycode(8)) {
				t.Fatal("failed temporary modifier transaction retained caller-owned Ctrl")
			}
		})
	}
}

// Cancellation during a synchronous X11 pointer reply revokes permission to
// publish its coordinates, even if the transport successfully completed.
func TestXTestPointerLocationHidesCoordinatesCanceledDuringReply(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mock := &x11.MockConnection{}
	mock.QueryPointerFunc = func(xproto.Window) x11.QueryPointerCookie {
		cancel()
		return syntheticPointerReply{reply: &xproto.QueryPointerReply{RootX: 321, RootY: 654}}
	}
	backend := &XTestBackend{conn: mock, root: 1}
	x, y, err := backend.PointerLocation(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("query returned error %v", err)
	}
	if x != 0 || y != 0 {
		t.Fatalf("canceled query revealed pointer position (%d,%d)", x, y)
	}
}

// A caller that has already canceled may not get a different outcome from
// malformed button or scroll arguments. The XTEST connection is deliberately
// absent; none of these calls may reach its transport.
func TestXTestPreCanceledPointerAndScrollRejectBeforeValidation(t *testing.T) {
	b := &XTestBackend{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name string
		run  func(context.Context) error
	}{
		{"click", func(c context.Context) error { return b.MouseClick(c, 10, 20, 99) }},
		{"down", func(c context.Context) error { return b.MouseDown(c, 99) }},
		{"up", func(c context.Context) error { return b.MouseUp(c, 99) }},
		{"scroll up", func(c context.Context) error { return b.ScrollUp(c, -1) }},
		{"scroll down", func(c context.Context) error { return b.ScrollDown(c, -1) }},
		{"scroll left", func(c context.Context) error { return b.ScrollLeft(c, -1) }},
		{"scroll right", func(c context.Context) error { return b.ScrollRight(c, -1) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("pre-canceled operation returned %v", err)
			}
		})
	}
}

// A queued XTEST operation must not execute once its owner cancels, even if
// the backend itself remains alive and another operation owns the gate.
// Use a held callback for a deterministic admission boundary, not X11 timing.
func TestXTestQueuedOperationCanceledBeforeTransportDispatch(t *testing.T) {
	backend := &XTestBackend{}
	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- backend.withOperation(context.Background(), func(context.Context) error {
			close(entered)
			<-release
			return nil
		})
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("initial XTEST operation never acquired gate")
	}
	ctx, cancel := context.WithCancel(context.Background())
	invoked := make(chan struct{}, 1)
	queued := make(chan error, 1)
	go func() {
		queued <- backend.withOperation(ctx, func(context.Context) error {
			invoked <- struct{}{}
			return nil
		})
	}()
	// Cancellation must terminate a queued operation while gate is still held.
	cancel()
	select {
	case err := <-queued:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued operation cancellation: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled XTEST operation remained queued")
	}
	select {
	case <-invoked:
		t.Fatal("canceled request reached XTEST transport")
	default:
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("unrelated admitted operation failed: %v", err)
	}
	if err := backend.withOperation(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatalf("cancellation poisoned subsequent operations: %v", err)
	}
}

type syntheticPointerReply struct {
	reply *xproto.QueryPointerReply
	err   error
}

func (s syntheticPointerReply) Reply() (*xproto.QueryPointerReply, error) { return s.reply, s.err }

func TestXTestScrollButtonsAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		button byte
		act    func(*XTestBackend, context.Context, int) error
	}{
		{"up", 4, (*XTestBackend).ScrollUp},
		{"down", 5, (*XTestBackend).ScrollDown},
		{"left", 6, (*XTestBackend).ScrollLeft},
		{"right", 7, (*XTestBackend).ScrollRight},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, nil)
			if err := tc.act(b, context.Background(), 2); err != nil {
				t.Fatal(err)
			}
			expected := []xtestEvent{{xproto.ButtonPress, tc.button}, {xproto.ButtonRelease, tc.button}, {xproto.ButtonPress, tc.button}, {xproto.ButtonRelease, tc.button}}
			if !sameXTestEvents(*events, expected) {
				t.Fatalf("wrong scroll event sequence: %#v want %#v", *events, expected)
			}
			before := len(*events)
			if err := tc.act(b, context.Background(), -1); err == nil {
				t.Fatal("negative clicks were accepted")
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			if err := tc.act(b, canceled, 1); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled scroll=%v", err)
			}
			if len(*events) != before {
				t.Fatalf("invalid/canceled scroll injected events: %#v", (*events)[before:])
			}
		})
	}
}

func TestXTestScrollStopsImmediatelyOnTransportFailure(t *testing.T) {
	failure := errors.New("synthetic XTEST wheel transport failure")
	failedOnce := false
	b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, func(ev xtestEvent) error {
		if ev.eventType == xproto.ButtonRelease && !failedOnce {
			failedOnce = true
			return failure
		}
		return nil
	})
	if err := b.ScrollDown(context.Background(), 3); !errors.Is(err, failure) {
		t.Fatalf("scroll failure lost source error: %v", err)
	}
	expected := []xtestEvent{
		{eventType: xproto.ButtonPress, detail: 5},
		{eventType: xproto.ButtonRelease, detail: 5}, // failed transiently
		{eventType: xproto.ButtonRelease, detail: 5}, // best-effort cleanup
	}
	if !sameXTestEvents(*events, expected) {
		t.Fatalf("failed scroll did not clean up held wheel button: got %#v want %#v", *events, expected)
	}
}

func TestXTestPointerQueryAndSyncUseSameConnection(t *testing.T) {
	b, _ := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, nil)
	conn, ok := b.conn.(*x11.MockConnection)
	if !ok {
		t.Fatalf("unexpected X11 connection %T", b.conn)
	}
	queries := 0
	conn.QueryPointerFunc = func(window xproto.Window) x11.QueryPointerCookie {
		queries++
		if window != 1 {
			t.Fatalf("pointer queried on wrong root: %d", window)
		}
		return syntheticPointerReply{reply: &xproto.QueryPointerReply{RootX: 71, RootY: -52}}
	}
	x, y, err := b.PointerLocation(context.Background())
	if err != nil || x != 71 || y != -52 || queries != 1 {
		t.Fatalf("pointer=(%d,%d) err=%v queries=%d", x, y, err, queries)
	}
	if err := b.Sync(context.Background()); err != nil {
		t.Fatalf("synchronize mocked X connection: %v", err)
	}
	conn.QueryPointerFunc = func(xproto.Window) x11.QueryPointerCookie {
		return syntheticPointerReply{err: errors.New("disconnected")}
	}
	if _, _, err := b.PointerLocation(context.Background()); err == nil {
		t.Fatal("failed pointer query acknowledged")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := b.PointerLocation(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled query=%v", err)
	}
	if err := b.Sync(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled sync=%v", err)
	}
}

func TestXTestScrollPermanentReleaseFailureIsBoundedAndReported(t *testing.T) {
	failure := errors.New("permanent synthetic XTEST release failure")
	b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, func(ev xtestEvent) error {
		if ev.eventType == xproto.ButtonRelease {
			return failure
		}
		return nil
	})
	if err := b.ScrollRight(context.Background(), 5); !errors.Is(err, failure) {
		t.Fatalf("permanent release failure lost: %v", err)
	}
	if len(*events) != 3 {
		t.Fatalf("unbounded cleanup or additional clicks: %+v", *events)
	}
	for _, ev := range (*events)[1:] {
		if ev.eventType != xproto.ButtonRelease || ev.detail != 7 {
			t.Fatalf("cleanup targeted wrong XTEST button: %+v", *events)
		}
	}
	if err := b.ScrollRight(context.Background(), 0); err != nil {
		t.Fatalf("zero scroll should do no work: %v", err)
	}
	if len(*events) != 3 {
		t.Fatal("zero-click call injected input")
	}
}

// Four-level keyboard maps place Shift+AltGr characters in the fourth
// slot. The backend must apply both modifiers and restore only its own holds.
func TestXTestFourthLevelAppliesShiftAndAltGrInOrder(t *testing.T) {
	for _, tc := range []struct {
		name      string
		holdShift bool
	}{
		{"temporary modifiers", false},
		{"caller-owned shift", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, events := newTestXTestBackend(t, keycodesPerKeycode4, germanLikeKeysyms(), nil)
			shift, err := backend.keycodeFor("shift")
			if err != nil {
				t.Fatal(err)
			}
			altGr, err := backend.altGrKeycode()
			if err != nil {
				t.Fatal(err)
			}
			key, level, err := backend.keycodeAndLevel('@')
			if err != nil || level != 3 {
				t.Fatalf("at-sign mapped to key=%d level=%d err=%v, want level 3", key, level, err)
			}
			if tc.holdShift {
				if err := backend.KeyDown(context.Background(), "shift"); err != nil {
					t.Fatal(err)
				}
			}
			initial := len(*events)
			if err := backend.TypeLiteral(context.Background(), "@"); err != nil {
				t.Fatalf("fourth-level literal: %v", err)
			}
			var want []xtestEvent
			if !tc.holdShift {
				want = append(want, xtestEvent{eventType: xproto.KeyPress, detail: byte(shift)})
			}
			want = append(want,
				xtestEvent{eventType: xproto.KeyPress, detail: byte(altGr)},
				xtestEvent{eventType: xproto.KeyPress, detail: byte(key)},
				xtestEvent{eventType: xproto.KeyRelease, detail: byte(key)},
				xtestEvent{eventType: xproto.KeyRelease, detail: byte(altGr)},
			)
			if !tc.holdShift {
				want = append(want, xtestEvent{eventType: xproto.KeyRelease, detail: byte(shift)})
			}
			got := (*events)[initial:]
			if len(got) != len(want) {
				t.Fatalf("fourth-level events=%v want %v", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("event %d = %+v want %+v (all %v)", i, got[i], want[i], got)
				}
			}
			if !tc.holdShift && backend.modifierHeld(shift) {
				t.Fatal("temporary shift remains held")
			}
			if tc.holdShift && !backend.modifierHeld(shift) {
				t.Fatal("caller shift was released")
			}
			if backend.modifierHeld(altGr) {
				t.Fatal("temporary AltGr remains held")
			}
		})
	}
}
