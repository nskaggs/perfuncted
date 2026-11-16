package pftest

import (
	"context"
	"errors"
	"testing"
)

// A canceled read must neither disclose a title nor consume the next mock
// observation. Otherwise later fixture operations become order-dependent.
func TestActiveTitleCancellationDoesNotAdvancePlayback(t *testing.T) {
	manager := &Manager{Titles: []string{"first", "second"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := manager.ActiveTitle(ctx)
	if got != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled title = %q, %v; want empty and context.Canceled", got, err)
	}
	title, err := manager.ActiveTitle(context.Background())
	if err != nil || title != "first" {
		t.Fatalf("next title = %q, %v; want first observation", title, err)
	}
}
