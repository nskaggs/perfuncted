package accessibility

import (
	"context"
	"strings"
	"testing"
)

// Internal mutation and metadata helpers must reject a missing operation
// context before consulting the provider or publishing private action data.
func TestATSPIInternalMutationAndActionMetadataRejectNilContextBeforeProvider(t *testing.T) {
	id := NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/editor/private", Generation: 15}
	calls := 0
	b := &dbusBackend{generation: 15, callOverride: func(context.Context, NodeID, string, []any) (any, error) {
		calls++
		return true, nil
	}}
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"mutation", func() error { return b.mutation(nil, id, componentIface, "GrabFocus") }},
		{"boolean mutation", func() error { return b.mutationBool(nil, id, componentIface, "GrabFocus") }},
		{"action metadata", func() error { _, err := b.actionMetadata(nil, id); return err }},
		{"action name", func() error { _, err := b.actionName(nil, id, 0); return err }},
		{"value mutation", func() error { return b.SetValue(nil, id, 2) }},
		{"document selection", func() error { return b.SetTextSelections(nil, id, nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil || !strings.Contains(err.Error(), "nil context") {
				t.Fatalf("missing context passed preflight: %v", err)
			}
		})
	}
	if calls != 0 {
		t.Fatalf("missing context dispatched %d provider operations", calls)
	}
}
