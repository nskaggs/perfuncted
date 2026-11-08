package wl

import (
	"context"
	"strings"
	"testing"
)

// Invalid wl_registry.bind metadata must be rejected locally, before
// the protocol request could cause a compositor error or disconnect.
func TestRegistryBindRejectsInvalidMetadataBeforeTransmission(t *testing.T) {
	for _, tc := range []struct {
		name, iface    string
		version, newID uint32
	}{
		{"empty interface", "", 1, 21},
		{"embedded NUL", "wl_output\x00fake", 1, 21},
		{"invalid UTF-8", "wl_output\xff", 1, 21},
		{"zero version", "wl_output", 0, 21},
		{"zero object ID", "wl_output", 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &Context{}
			reg := &Registry{}
			reg.SetCtx(ctx)
			reg.SetID(2)
			err := reg.BindContext(context.Background(), 42, tc.iface, tc.version, tc.newID)
			if err == nil || !strings.Contains(err.Error(), "invalid") {
				t.Fatalf("malformed registry bind returned %v", err)
			}
		})
	}
}
