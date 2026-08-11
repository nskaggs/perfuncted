package window

import (
	"context"
	"errors"
	"testing"
)

func TestNoOpSyncHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		sync func(context.Context) error
	}{
		{name: "sway", sync: (&SwayManager{}).Sync},
		{name: "gnome", sync: (&GnomeManager{}).Sync},
		{name: "kwin", sync: (&KWinScriptManager{}).Sync},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.sync(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("Sync error = %v, want context.Canceled", err)
			}
		})
	}
}
