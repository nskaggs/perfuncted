package perfuncted

import (
	"context"
	"errors"
	"image"
	"testing"
	"time"
)

// Window motion may be observed successfully just as the caller cancels.
// Before entering the stable-image phase the facade must check authority
// again, discarding the change hash without dispatching another screenshot.
func TestScreenSettleRejectsCallerRevocationAtPhaseBoundary(t *testing.T) {
	base, deadlineCancel := context.WithTimeout(context.Background(), time.Second)
	defer deadlineCancel()
	owner, cancel := context.WithCancelCause(base)
	defer cancel(nil)
	ctx := &cancelAtErrCallContext{Context: owner, cancel: cancel, target: 10}
	source := &settleFinalCaptureRevocation{}
	session := NewSessionForTesting(source, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	hash, err := session.Screen.WaitForSettle(ctx, image.Rect(0, 0, 3, 3), nil, 1, time.Millisecond)
	if hash != 0 || !errors.Is(err, context.Canceled) || source.calls != 2 || ctx.calls != 10 {
		t.Fatalf("canceled phase transition: hash=%08x err=%v checks=%d captures=%d", hash, err, ctx.calls, source.calls)
	}
}
