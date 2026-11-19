package perfuncted

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/window"
)

// This context models a session being revoked at the last caller-authority
// check after window metadata has already been read and filtered. The caller
// itself remains live, so only the final session ownership check can reject
// the stale result.
type closeSessionAtWindowPostcheckContext struct {
	context.Context
	session  *Session
	checks   int
	closeErr error
}

func (c *closeSessionAtWindowPostcheckContext) Err() error {
	c.checks++
	if c.checks == 2 {
		c.closeErr = c.session.Close() //nolint:contextcheck // Teardown deliberately revokes session ownership during the caller's still-live read.
	}
	return c.Context.Err()
}

func TestWindowListRejectsSessionCloseAfterMetadataFiltering(t *testing.T) {
	manager := newHandleWindowManager(window.Info{NativeID: "private-window", Title: "sensitive"})
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	base, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	ctx := &closeSessionAtWindowPostcheckContext{Context: base, session: session}
	windows, err := session.Windows.List(ctx, WindowMatch{})
	if ctx.closeErr != nil || !errors.Is(err, ErrSessionClosed) || len(windows) != 0 || ctx.checks != 2 {
		t.Fatalf("session closed after filtering: got windows=%v err=%v cleanup=%v checks=%d", windows, err, ctx.closeErr, ctx.checks)
	}
	if base.Err() != nil {
		t.Fatalf("caller expired; expected session ownership alone to reject metadata: %v", base.Err())
	}
}
