//go:build linux
// +build linux

package input

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

type xtestEvent struct {
	eventType byte
	detail    byte
}

type orderingXTestConnection struct {
	*x11.MockConnection

	mu           sync.Mutex
	events       []xtestEvent
	firstStarted chan struct{}
	firstRelease chan struct{}
	firstOnce    sync.Once
	closeCalls   int
}

var _ x11.Connection = (*orderingXTestConnection)(nil)

func newOrderingXTestConnection(keysyms []xproto.Keysym) *orderingXTestConnection {
	return &orderingXTestConnection{
		MockConnection: &x11.MockConnection{
			SetupFunc: func() *xproto.SetupInfo {
				return &xproto.SetupInfo{
					MinKeycode: 8,
					MaxKeycode: 8 + xproto.Keycode(len(keysyms)) - 1,
				}
			},
			GetKeyboardMappingFunc: func(xproto.Keycode, byte) x11.GetKeyboardMappingCookie {
				return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{
					KeysymsPerKeycode: 1,
					Keysyms:           keysyms,
				})
			},
		},
		firstStarted: make(chan struct{}),
		firstRelease: make(chan struct{}),
	}
}

func (c *orderingXTestConnection) FakeInputChecked(eventType byte, detail byte, _ uint32, _ xproto.Window, _, _ int16, _ byte) x11.XTestFakeInputCookie {
	c.mu.Lock()
	c.events = append(c.events, xtestEvent{eventType: eventType, detail: detail})
	first := len(c.events) == 1
	c.mu.Unlock()
	if first {
		c.firstOnce.Do(func() { close(c.firstStarted) })
		<-c.firstRelease
	}
	return x11.NewMockXTestFakeInputCookie(nil)
}

func (c *orderingXTestConnection) Close() {
	c.mu.Lock()
	c.closeCalls++
	c.mu.Unlock()
}

func (c *orderingXTestConnection) eventsSnapshot() []xtestEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]xtestEvent(nil), c.events...)
}

func (c *orderingXTestConnection) closeCallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closeCalls
}

func newTestXTestBackend(
	t *testing.T,
	keysymsPerKeycode byte,
	keysyms []xproto.Keysym,
	fail func(xtestEvent) error,
) (*XTestBackend, *[]xtestEvent) {
	t.Helper()
	if keysymsPerKeycode == 0 || len(keysyms)%int(keysymsPerKeycode) != 0 {
		t.Fatalf("invalid test keymap: %d keysyms per keycode, %d keysyms", keysymsPerKeycode, len(keysyms))
	}

	events := make([]xtestEvent, 0, 8)
	conn := &x11.MockConnection{
		SetupFunc: func() *xproto.SetupInfo {
			return &xproto.SetupInfo{
				MinKeycode: 8,
				MaxKeycode: 8 + xproto.Keycode(len(keysyms)/int(keysymsPerKeycode)) - 1,
			}
		},
		GetKeyboardMappingFunc: func(xproto.Keycode, byte) x11.GetKeyboardMappingCookie {
			return x11.NewMockGetKeyboardMappingCookie(&xproto.GetKeyboardMappingReply{
				KeysymsPerKeycode: keysymsPerKeycode,
				Keysyms:           keysyms,
			})
		},
		FakeInputCheckedFunc: func(eventType byte, detail byte, _ uint32, _ xproto.Window, _, _ int16, _ byte) x11.XTestFakeInputCookie {
			event := xtestEvent{eventType: eventType, detail: detail}
			events = append(events, event)
			if fail != nil {
				if err := fail(event); err != nil {
					return x11.NewMockXTestFakeInputCookie(err)
				}
			}
			return x11.NewMockXTestFakeInputCookie(nil)
		},
	}
	return &XTestBackend{conn: conn, root: 1}, &events
}

func TestXTestTypeReleasesTemporaryModifiersAfterKeyFailure(t *testing.T) {
	const (
		shift = byte(8)
		ctrl  = byte(9)
		key   = byte(12)
	)
	wantErr := errors.New("synthetic key failure")
	b, events := newTestXTestBackend(t, 1, []xproto.Keysym{
		0xffe1, // shift
		0xffe3, // ctrl
		0xffe9, // alt
		0xffeb, // super
		0x61,   // a
	}, func(event xtestEvent) error {
		if event.eventType == xproto.KeyPress && event.detail == key {
			return wantErr
		}
		return nil
	})

	err := b.typeContext(context.Background(), "{ctrl+shift+a}")
	if !errors.Is(err, wantErr) {
		t.Fatalf("typeContext error = %v, want %v", err, wantErr)
	}

	want := []xtestEvent{
		{eventType: xproto.KeyPress, detail: shift},
		{eventType: xproto.KeyPress, detail: ctrl},
		{eventType: xproto.KeyPress, detail: key},
		{eventType: xproto.KeyRelease, detail: ctrl},
		{eventType: xproto.KeyRelease, detail: shift},
	}
	if !sameXTestEvents(*events, want) {
		t.Fatalf("events = %#v, want %#v", *events, want)
	}
}

func TestXTestTypeReleasesEarlierModifiersWhenModifierSetupFails(t *testing.T) {
	const (
		ctrl  = byte(9)
		shift = byte(8)
	)
	wantErr := errors.New("synthetic modifier failure")
	b, events := newTestXTestBackend(t, 1, []xproto.Keysym{
		0xffe1, // shift
		0xffe3, // ctrl
		0xffe9, // alt
		0xffeb, // super
		0x61,   // a
	}, func(event xtestEvent) error {
		if event.eventType == xproto.KeyPress && event.detail == ctrl {
			return wantErr
		}
		return nil
	})

	err := b.typeContext(context.Background(), "{ctrl+shift+a}")
	if !errors.Is(err, wantErr) {
		t.Fatalf("typeContext error = %v, want %v", err, wantErr)
	}

	want := []xtestEvent{
		{eventType: xproto.KeyPress, detail: shift},
		{eventType: xproto.KeyPress, detail: ctrl},
		{eventType: xproto.KeyRelease, detail: shift},
	}
	if !sameXTestEvents(*events, want) {
		t.Fatalf("events = %#v, want %#v", *events, want)
	}
}

func TestXTestTypeTextReleasesShiftAfterKeyFailure(t *testing.T) {
	const (
		shift = byte(8)
		key   = byte(9)
	)
	wantErr := errors.New("synthetic text key failure")
	b, events := newTestXTestBackend(t, 2, []xproto.Keysym{
		0xffe1, 0,
		0x61, 0x41,
	}, func(event xtestEvent) error {
		if event.eventType == xproto.KeyPress && event.detail == key {
			return wantErr
		}
		return nil
	})

	err := b.typeContext(context.Background(), "A")
	if !errors.Is(err, wantErr) {
		t.Fatalf("typeContext error = %v, want %v", err, wantErr)
	}

	want := []xtestEvent{
		{eventType: xproto.KeyPress, detail: shift},
		{eventType: xproto.KeyPress, detail: key},
		{eventType: xproto.KeyRelease, detail: shift},
	}
	if !sameXTestEvents(*events, want) {
		t.Fatalf("events = %#v, want %#v", *events, want)
	}
}

func TestXTestTypeReleasesTemporaryModifiersAfterCancellation(t *testing.T) {
	const (
		shift = byte(8)
		ctrl  = byte(9)
		key   = byte(12)
	)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b, events := newTestXTestBackend(t, 1, []xproto.Keysym{
		0xffe1, // shift
		0xffe3, // ctrl
		0xffe9, // alt
		0xffeb, // super
		0x61,   // a
	}, func(event xtestEvent) error {
		if event.eventType == xproto.KeyPress && event.detail == key {
			cancel()
		}
		return nil
	})
	b.delay = 10 * time.Millisecond

	err := b.typeContext(ctx, "{ctrl+shift+a}")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("typeContext error = %v, want context.Canceled", err)
	}

	want := []xtestEvent{
		{eventType: xproto.KeyPress, detail: shift},
		{eventType: xproto.KeyPress, detail: ctrl},
		{eventType: xproto.KeyPress, detail: key},
		{eventType: xproto.KeyRelease, detail: key},
		{eventType: xproto.KeyRelease, detail: ctrl},
		{eventType: xproto.KeyRelease, detail: shift},
	}
	if !sameXTestEvents(*events, want) {
		t.Fatalf("events = %#v, want %#v", *events, want)
	}
}

func sameXTestEvents(got, want []xtestEvent) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

type xtestOperationOrderingCase struct {
	name       string
	newBackend func() (*XTestBackend, *orderingXTestConnection)
	first      func(*XTestBackend) error
	second     func(*XTestBackend) error
	want       []xtestEvent
}

func TestXTestLogicalOperationsDoNotInterleave(t *testing.T) {
	tests := []xtestOperationOrderingCase{
		{
			name: "Type",
			newBackend: func() (*XTestBackend, *orderingXTestConnection) {
				conn := newOrderingXTestConnection([]xproto.Keysym{0x61, 0x62, 0x63})
				return &XTestBackend{conn: conn, root: 1, delay: 0}, conn
			},
			first:  func(b *XTestBackend) error { return b.Type(context.Background(), "ab") },
			second: func(b *XTestBackend) error { return b.Type(context.Background(), "c") },
			want: []xtestEvent{
				{eventType: xproto.KeyPress, detail: 8},
				{eventType: xproto.KeyRelease, detail: 8},
				{eventType: xproto.KeyPress, detail: 9},
				{eventType: xproto.KeyRelease, detail: 9},
				{eventType: xproto.KeyPress, detail: 10},
				{eventType: xproto.KeyRelease, detail: 10},
			},
		},
		{
			name: "MouseClick",
			newBackend: func() (*XTestBackend, *orderingXTestConnection) {
				conn := newOrderingXTestConnection(nil)
				return &XTestBackend{conn: conn, root: 1, delay: 0}, conn
			},
			first: func(b *XTestBackend) error {
				return b.MouseClick(context.Background(), 1, 2, 1)
			},
			second: func(b *XTestBackend) error {
				return b.MouseClick(context.Background(), 3, 4, 2)
			},
			want: []xtestEvent{
				{eventType: xproto.MotionNotify, detail: 0},
				{eventType: xproto.ButtonPress, detail: 1},
				{eventType: xproto.ButtonRelease, detail: 1},
				{eventType: xproto.MotionNotify, detail: 0},
				{eventType: xproto.ButtonPress, detail: 2},
				{eventType: xproto.ButtonRelease, detail: 2},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, conn := tt.newBackend()
			assertXTestOperationsDoNotInterleave(t, b, conn, tt.first, tt.second, tt.want)
		})
	}
}

func assertXTestOperationsDoNotInterleave(
	t *testing.T,
	b *XTestBackend,
	conn *orderingXTestConnection,
	first func(*XTestBackend) error,
	second func(*XTestBackend) error,
	want []xtestEvent,
) {
	t.Helper()
	firstDone := make(chan error, 1)
	go func() { firstDone <- first(b) }()
	select {
	case <-conn.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first operation did not reach the connection")
	}

	secondDone := make(chan error, 1)
	go func() { secondDone <- second(b) }()
	select {
	case err := <-secondDone:
		t.Fatalf("second operation completed while first was blocked: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if got := len(conn.eventsSnapshot()); got != 1 {
		t.Fatalf("events while first operation was blocked = %d, want 1", got)
	}

	close(conn.firstRelease)
	for name, done := range map[string]<-chan error{"first": firstDone, "second": secondDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s operation: %v", name, err)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s operation did not finish", name)
		}
	}
	if got := conn.eventsSnapshot(); !sameXTestEvents(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}
}

func TestXTestQueuedOperationHonorsCancellation(t *testing.T) {
	conn := newOrderingXTestConnection([]xproto.Keysym{0x61, 0x62})
	b := &XTestBackend{conn: conn, root: 1, delay: 0}

	firstDone := make(chan error, 1)
	go func() { firstDone <- b.Type(context.Background(), "a") }()
	select {
	case <-conn.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("first operation did not reach the connection")
	}

	queuedCtx, cancel := context.WithCancel(context.Background())
	queuedDone := make(chan error, 1)
	go func() { queuedDone <- b.Type(queuedCtx, "b") }()
	cancel()
	select {
	case err := <-queuedDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued operation error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("queued operation did not honor cancellation")
	}

	close(conn.firstRelease)
	select {
	case err := <-firstDone:
		if err != nil {
			t.Fatalf("first operation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first operation did not finish")
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestXTestCloseWaitsForActiveOperation(t *testing.T) {
	conn := newOrderingXTestConnection([]xproto.Keysym{0x61})
	b := &XTestBackend{conn: conn, root: 1, delay: time.Second}

	opDone := make(chan error, 1)
	go func() { opDone <- b.Type(context.Background(), "a") }()
	select {
	case <-conn.firstStarted:
	case <-time.After(time.Second):
		t.Fatal("operation did not reach the connection")
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- b.Close() }()
	select {
	case err := <-closeDone:
		t.Fatalf("Close returned while operation was active: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if got := conn.closeCallCount(); got != 0 {
		t.Fatalf("connection close calls while operation was active = %d, want 0", got)
	}

	close(conn.firstRelease)
	select {
	case err := <-opDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("operation error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("active operation did not finish after release")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not finish after active operation ended")
	}
	if got := conn.closeCallCount(); got != 1 {
		t.Fatalf("connection close calls = %d, want 1", got)
	}
}

func TestXTestRejectsNewWorkAfterClose(t *testing.T) {
	conn := newOrderingXTestConnection([]xproto.Keysym{0x61})
	b := &XTestBackend{conn: conn, root: 1, delay: 0}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := b.Type(context.Background(), "a"); !errors.Is(err, errXTestBackendClosed) {
		t.Fatalf("Type after Close = %v, want backend-closed error", err)
	}
	if err := b.MouseClick(context.Background(), 1, 2, 1); !errors.Is(err, errXTestBackendClosed) {
		t.Fatalf("MouseClick after Close = %v, want backend-closed error", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("repeated Close: %v", err)
	}
	if got := conn.closeCallCount(); got != 1 {
		t.Fatalf("connection close calls after repeated Close = %d, want 1", got)
	}
}
