package accessibility

import (
	"context"
	"errors"
	"testing"
)

// The selected action's metadata is not a valid result once its invocation
// loses authorization at the dispatch boundary. Preserve the cancellation
// error but never publish the selected private action to an abandoned caller.
func TestCanceledActionDispatchWithholdsSelectedMetadata(t *testing.T) {
	id := NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/dialog/private", Generation: 9}
	for _, tc := range []struct {
		name   string
		invoke func(*dbusBackend, context.Context) (Action, error)
	}{
		{"by name", func(b *dbusBackend, ctx context.Context) (Action, error) {
			return b.InvokeActionByName(ctx, id, "save")
		}},
		{"default", func(b *dbusBackend, ctx context.Context) (Action, error) { return b.InvokeDefaultAction(ctx, id) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			b := &dbusBackend{generation: 9, callOverride: func(_ context.Context, _ NodeID, method string, _ []any) (any, error) {
				switch method {
				case actionIface + ".GetActions":
					return []actionMetadataWire{{LocalizedName: "Private save", Description: "private document", KeyBinding: "Ctrl+S"}}, nil
				case actionIface + ".GetName":
					return "save", nil
				case actionIface + ".DoAction":
					calls++
					cancel()
					return true, nil
				default:
					t.Fatalf("unexpected method %q", method)
					return nil, nil
				}
			}}
			action, err := tc.invoke(b, ctx)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled invocation accepted: action=%+v err=%v", action, err)
			}
			if action != (Action{}) {
				t.Fatalf("canceled invocation exposed selected metadata: %+v", action)
			}
			if calls != 1 {
				t.Fatalf("expected exactly one attempted dispatch, got %d", calls)
			}
		})
	}
}
