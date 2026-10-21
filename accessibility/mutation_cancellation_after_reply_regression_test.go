package accessibility

import (
	"context"
	"errors"
	"testing"
)

func TestMutationsCannotReportSuccessAfterCallerCancelsWhileProviderReplies(t *testing.T) {
	id := NodeID{BusName: "org.test", ObjectPath: "/node", Generation: 1}
	for _, tc := range []struct {
		name   string
		run    func(*dbusBackend, context.Context) error
		method string
	}{
		{"focus", func(b *dbusBackend, ctx context.Context) error { return b.GrabFocus(ctx, id) }, componentIface + ".GrabFocus"},
		{"set text", func(b *dbusBackend, ctx context.Context) error { return b.SetTextContents(ctx, id, "hello") }, editableTextIface + ".SetTextContents"},
		{"action", func(b *dbusBackend, ctx context.Context) error { return b.InvokeAction(ctx, id, 0) }, actionIface + ".DoAction"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			mutations := 0
			b := &dbusBackend{generation: 1, callOverride: func(_ context.Context, _ NodeID, method string, _ []any) (any, error) {
				if method == actionIface+".GetActions" {
					return []actionMetadataWire{{LocalizedName: "activate"}}, nil
				}
				if method != tc.method {
					t.Fatalf("unexpected method %q", method)
				}
				mutations++
				cancel()
				return true, nil
			}}
			err := tc.run(b, ctx)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("operation reported success or wrong error after cancellation: %v", err)
			}
			if mutations != 1 {
				t.Fatalf("mutation calls=%d, want one", mutations)
			}
			if tc.name == "action" {
				var dispatch *ActionInvocationError
				if !errors.As(err, &dispatch) || dispatch.Dispatch != DispatchUnknown {
					t.Fatalf("canceled action should preserve uncertain dispatch: %v", err)
				}
			}
		})
	}
}
