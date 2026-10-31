package accessibility

import (
	"context"
	"errors"
	"testing"
)

// A D-Bus transport can report a successful mutation after the operation's
// context expires. That result is no longer authoritative for callers.
func TestAccessibilitySetValueRejectsCancellationDuringProviderReply(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	backend := &dbusBackend{generation: 7, callOverride: func(ctx context.Context, id NodeID, method string, args []any) (any, error) {
		if method != propertiesIface+".Set" {
			t.Fatalf("unexpected wire operation %s", method)
		}
		cancel()
		return nil, nil
	}}
	id := NodeID{BusName: "org.example.Widget", ObjectPath: "/entry", Generation: 7}
	err := backend.SetValue(ctx, id, 3.14)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("SetValue accepted successful reply after operation canceled: %v", err)
	}
}

func TestAccessibilityActionMetadataCannotOutrunCancellation(t *testing.T) {
	id := NodeID{BusName: "org.example.Widget", ObjectPath: "/entry", Generation: 7}
	for _, tc := range []struct {
		name   string
		action string
		reply  any
		invoke func(*dbusBackend, context.Context) error
	}{
		{"metadata", actionIface + ".GetActions", []actionMetadataWire{{LocalizedName: "Activate"}}, func(b *dbusBackend, ctx context.Context) error { _, err := b.actionMetadata(ctx, id); return err }},
		{"machine name", actionIface + ".GetName", "activate", func(b *dbusBackend, ctx context.Context) error { _, err := b.actionName(ctx, id, 0); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			b := &dbusBackend{generation: 7, callOverride: func(_ context.Context, _ NodeID, method string, _ []any) (any, error) {
				if method != tc.action {
					t.Fatalf("unexpected action read %s", method)
				}
				cancel()
				return tc.reply, nil
			}}
			if err := tc.invoke(b, ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("action read accepted late canceled response: %v", err)
			}
		})
	}
}
