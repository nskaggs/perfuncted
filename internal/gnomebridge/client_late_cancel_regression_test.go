//go:build linux

package gnomebridge

import (
	"context"
	"errors"
	"testing"

	"github.com/godbus/dbus/v5"
)

// A D-Bus service can acknowledge an operation at the exact moment its caller
// cancels. A late success must not be exposed as a completed caller operation.
type lateSuccessfulBridgeReply struct{ cancel context.CancelFunc }

func (o lateSuccessfulBridgeReply) CallWithContext(_ context.Context, method string, _ dbus.Flags, _ ...any) *dbus.Call {
	o.cancel()
	switch method {
	case WindowsInterface + ".GetWindow":
		return &dbus.Call{Body: []any{WindowInfo{ID: "7", Title: "synthetic editor"}}}
	case ClipboardInterface + ".GetText":
		return &dbus.Call{Body: []any{"private synthetic clipboard"}}
	case ScreenInterface + ".CaptureFull":
		return &dbus.Call{Body: []any{int32(0), int32(0), int32(640), int32(480), int32(640), int32(480), float64(1)}}
	default:
		return &dbus.Call{}
	}
}
func TestBridgeLateProviderSuccessDoesNotMaskCallerCancellation(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(context.Context, *Client) error
	}{
		{"core ping", func(ctx context.Context, c *Client) error { return c.Ping(ctx) }},
		{"window mutation", func(ctx context.Context, c *Client) error { return c.Move(ctx, "7", 20, 30) }},
		{"typed window read", func(ctx context.Context, c *Client) error { _, err := c.GetWindow(ctx, "7"); return err }},
		{"clipboard read", func(ctx context.Context, c *Client) error { _, err := c.GetText(ctx); return err }},
		{"image UnixFD transfer", func(ctx context.Context, c *Client) error { _, err := c.CaptureFull(ctx, 7); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c := &Client{obj: lateSuccessfulBridgeReply{cancel: cancel}, supportsFDs: true, closeDone: make(chan struct{})}
			if err := tc.call(ctx, c); !errors.Is(err, context.Canceled) {
				t.Fatalf("late provider success acknowledged on canceled %s: %v", tc.name, err)
			}
		})
	}
}
