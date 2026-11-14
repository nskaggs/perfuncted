package window

import (
	"context"
	"errors"
	"testing"
)

// A revoked request must never reach KWin scripting, regardless of which
// window operation the caller selected. Each operation has a distinct
// wrapper around the script callback and must propagate cancellation.
func TestKWinMutationsDoNotStageScriptsAfterRequestCancellation(t *testing.T) {
	manager, provider := newSyntheticKWinManager(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name      string
		operation func(context.Context) error
	}{
		{"activate", func(ctx context.Context) error { return manager.ActivateByID(ctx, "42") }},
		{"move", func(ctx context.Context) error { return manager.MoveByID(ctx, "42", 13, 14) }},
		{"resize", func(ctx context.Context) error { return manager.ResizeByID(ctx, "42", 300, 200) }},
		{"close", func(ctx context.Context) error { return manager.CloseWindowByID(ctx, "42") }},
		{"minimize", func(ctx context.Context) error { return manager.MinimizeByID(ctx, "42") }},
		{"maximize", func(ctx context.Context) error { return manager.MaximizeByID(ctx, "42") }},
		{"fullscreen", func(ctx context.Context) error { return manager.FullscreenByID(ctx, "42") }},
		{"unfullscreen", func(ctx context.Context) error { return manager.UnfullscreenByID(ctx, "42") }},
		{"restore", func(ctx context.Context) error { return manager.RestoreByID(ctx, "42") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.operation(canceled); !errors.Is(err, context.Canceled) {
				t.Fatalf("revoked request was acknowledged: %v", err)
			}
			provider.mu.Lock()
			loads, starts, unloads := provider.loadCount, provider.startCount, provider.unloadCount
			provider.mu.Unlock()
			if loads != 0 || starts != 0 || unloads != 0 {
				t.Fatalf("revoked request reached compositor scripting: loads=%d starts=%d unloads=%d", loads, starts, unloads)
			}
		})
	}
}
