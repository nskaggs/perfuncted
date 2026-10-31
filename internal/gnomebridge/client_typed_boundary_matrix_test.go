package gnomebridge

import (
	"context"
	"errors"
	"github.com/godbus/dbus/v5"
	"reflect"
	"strings"
	"testing"
)

// The bridge is a typed boundary. Every public mutator must invoke precisely
// its advertised bus method with typed, ordered arguments, and must propagate
// errors rather than reporting a successful desktop action.
func TestBridgeTypedMutatorProtocolMatrix(t *testing.T) {
	client, object := newRecordingClient(t)
	for _, tc := range []struct {
		method string
		args   []any
		invoke func() error
	}{
		{WindowsInterface + ".Activate", []any{"17"}, func() error { return client.Activate(context.Background(), "17") }},
		{WindowsInterface + ".Resize", []any{"17", int32(333), int32(200)}, func() error { return client.Resize(context.Background(), "17", 333, 200) }},
		{WindowsInterface + ".Minimize", []any{"17"}, func() error { return client.Minimize(context.Background(), "17") }},
		{WindowsInterface + ".Maximize", []any{"17"}, func() error { return client.Maximize(context.Background(), "17") }},
		{WindowsInterface + ".Restore", []any{"17"}, func() error { return client.Restore(context.Background(), "17") }},
		{WindowsInterface + ".Fullscreen", []any{"17"}, func() error { return client.Fullscreen(context.Background(), "17") }},
		{WindowsInterface + ".Unfullscreen", []any{"17"}, func() error { return client.Unfullscreen(context.Background(), "17") }},
		{WindowsInterface + ".Close", []any{"17"}, func() error { return client.CloseWindow(context.Background(), "17") }},
		{InputInterface + ".Key", []any{uint32(0xff0d), true}, func() error { return client.Key(context.Background(), 0xff0d, true) }},
		{InputInterface + ".Text", []any{"ñ"}, func() error { return client.Text(context.Background(), "ñ") }},
		{InputInterface + ".PointerMove", []any{int32(-10), int32(25)}, func() error { return client.PointerMove(context.Background(), -10, 25) }},
		{InputInterface + ".PointerButton", []any{uint32(1), false}, func() error { return client.PointerButton(context.Background(), 1, false) }},
		{InputInterface + ".Scroll", []any{"vertical", 2.5}, func() error { return client.Scroll(context.Background(), "vertical", 2.5) }},
	} {
		t.Run(tc.method, func(t *testing.T) {
			object.mu.Lock()
			before := len(object.calls)
			object.mu.Unlock()
			if err := tc.invoke(); err != nil {
				t.Fatalf("valid typed call failed: %v", err)
			}
			object.mu.Lock()
			calls := append([]recordedCall(nil), object.calls[before:]...)
			object.mu.Unlock()
			if len(calls) != 1 || calls[0].method != tc.method || !reflect.DeepEqual(calls[0].args, tc.args) {
				t.Fatalf("unexpected D-Bus calls: %+v want %s %#v", calls, tc.method, tc.args)
			}
		})
	}
	if win, err := client.GetActiveWindow(context.Background()); err != nil || win.ID != "17" || !win.Active {
		t.Fatalf("active window=%+v err=%v", win, err)
	}
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

type bridgeFailingObject struct {
	errorFor string
	failure  error
	calls    int
}

func (b *bridgeFailingObject) CallWithContext(ctx context.Context, method string, _ dbus.Flags, args ...any) *dbus.Call {
	b.calls++
	if err := ctx.Err(); err != nil {
		return &dbus.Call{Err: err}
	}
	if strings.HasSuffix(method, b.errorFor) {
		return &dbus.Call{Err: b.failure}
	}
	switch method {
	case CoreInterface + ".GetProtocolVersion":
		return &dbus.Call{Body: []any{ProtocolVersion}}
	case CoreInterface + ".GetExtensionVersion":
		return &dbus.Call{Body: []any{ExtensionVersion}}
	case CoreInterface + ".GetShellVersion":
		return &dbus.Call{Body: []any{"51.0"}}
	case CoreInterface + ".GetCapabilities":
		return &dbus.Call{Body: []any{[]string{"windows", "screen", "input", "clipboard"}}}
	}
	return &dbus.Call{}
}
func TestBridgeTypedMethodsFailClosedAfterCancellationOrBusLoss(t *testing.T) {
	failure := errors.New("synthetic D-Bus disconnected")
	for _, tc := range []struct {
		name, suffix string
		call         func(*Client, context.Context) error
	}{
		{"window activate", ".Activate", func(c *Client, ctx context.Context) error { return c.Activate(ctx, "17") }},
		{"window restore", ".Restore", func(c *Client, ctx context.Context) error { return c.Restore(ctx, "17") }},
		{"key injection", ".Key", func(c *Client, ctx context.Context) error { return c.Key(ctx, 65, true) }},
		{"pointer movement", ".PointerMove", func(c *Client, ctx context.Context) error { return c.PointerMove(ctx, 25, 30) }},
		{"scroll", ".Scroll", func(c *Client, ctx context.Context) error { return c.Scroll(ctx, "horizontal", 1) }},
		{"clipboard set", ".SetText", func(c *Client, ctx context.Context) error { return c.SetText(ctx, "secret") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := &bridgeFailingObject{errorFor: tc.suffix, failure: failure}
			client, err := newClientWithObject(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			before := o.calls
			if err := tc.call(client, context.Background()); !errors.Is(err, failure) {
				t.Fatalf("bus failure lost: %v", err)
			}
			if o.calls != before+1 {
				t.Fatalf("unexpected retries after transport failure %d", o.calls-before)
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			before = o.calls
			if err := tc.call(client, canceled); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled request succeeded: %v", err)
			}
			if o.calls != before {
				t.Fatal("canceled mutation reached D-Bus")
			}
		})
	}
}

func TestBridgeConstructorRequiresExplicitLiveBus(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if client, err := NewClientForBus(ctx, "unix:path=/unreachable"); client != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled constructor: %v, %v", client, err)
	}
	if client, err := NewClientForBus(context.Background(), ""); client != nil || !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unset bus did not fail closed: %v %v", client, err)
	}
}
