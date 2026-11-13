package accessibility

import (
	"context"
	"errors"
	"testing"
)

// CopyText is a mutation despite its read-like name. A successful provider
// response may not acknowledge a copy after the caller cancels the operation.
func TestATSPICopyTextRejectsLateCanceledProviderAcknowledgment(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	id := NodeID{BusName: "org.example.Editor", ObjectPath: "/text", Generation: 8}
	calls := 0
	b := &dbusBackend{generation: 8, callOverride: func(_ context.Context, got NodeID, method string, args []any) (any, error) {
		calls++
		if got != id || method != editableTextIface+".CopyText" || len(args) != 2 || args[0] != int32(0) || args[1] != int32(9) {
			t.Fatalf("untrusted wire dispatch: id=%+v method=%q args=%v", got, method, args)
		}
		cancel()
		return nil, nil
	}}
	if err := b.CopyText(ctx, id, 0, 9); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled copy acknowledged: %v", err)
	}
	if calls != 1 {
		t.Fatalf("copy dispatch count=%d want exactly one", calls)
	}
}

// A stale generation cannot authorize any private action metadata query or
// actual mutation, even when the provider mock would accept the operation.
func TestATSPIStaleActionHandlesRejectBeforeProviderDispatch(t *testing.T) {
	stale := NodeID{BusName: "org.example.Editor", ObjectPath: "/private", Generation: 6}
	count := 0
	b := &dbusBackend{generation: 7, callOverride: func(context.Context, NodeID, string, []any) (any, error) { count++; return true, nil }}
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		invoke func() error
	}{
		{"copy text", func() error { return b.CopyText(ctx, stale, 0, 1) }},
		{"focus", func() error { return b.GrabFocus(ctx, stale) }},
		{"action metadata", func() error { _, err := b.actionMetadata(ctx, stale); return err }},
		{"action name", func() error { _, err := b.actionName(ctx, stale, 0); return err }},
		{"action dispatch", func() error { return b.dispatchAction(ctx, stale, Action{Index: 0, LocalizedName: "Private"}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.invoke(); !errors.Is(err, ErrStaleNode) {
				t.Fatalf("stale handle returned %v", err)
			}
			if count != 0 {
				t.Fatalf("stale handle dispatched %d requests", count)
			}
		})
	}
}
