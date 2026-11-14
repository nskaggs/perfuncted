//go:build linux

package input

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// A correctly authenticated Response signal cannot authorize the session if
// the original D-Bus method is still blocked when the caller cancels. The
// portal's late method reply must not be treated as successful consent.
func TestPortalRequestCancellationWhileMethodReplyIsPending(t *testing.T) {
	client, portal := startSyntheticPortalBus(t)
	portal.mu.Lock()
	portal.createMethodDelay = 250 * time.Millisecond
	portal.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	results, err := portalRequest(ctx, client, client.Names()[0], "CreateSession", []any{map[string]dbus.Variant{}})
	if !errors.Is(err, context.DeadlineExceeded) || results != nil {
		t.Fatalf("late portal method reply granted session: results=%+v err=%v", results, err)
	}
}
