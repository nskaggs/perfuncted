package perfuncted

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A capture provider can complete with a valid frame just before the caller
// withdraws authority. The post-validation check must still reject pixels.
func TestScreenGrabRejectsCancellationAfterLayoutValidation(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
	session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	base, deadlineCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer deadlineCancel()
	owner, cancel := context.WithCancelCause(base)
	defer cancel(nil)
	ctx := &cancelAtErrCallContext{Context: owner, cancel: cancel, target: 2}
	got, err := session.Screen.Grab(ctx, frame.Bounds())
	if got != nil || !errors.Is(err, context.Canceled) || ctx.calls != 2 {
		t.Fatalf("completed frame published after cancellation: image=%v err=%v checks=%d", got, err, ctx.calls)
	}
}

// A caller can also cancel between a completed and validated Grab and the
// screenshot's filesystem staging. No transient file may be created then.
func TestScreenCaptureRegionRejectsCancellationBeforeFileStaging(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 2, 2))
	session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	base, deadlineCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer deadlineCancel()
	owner, cancel := context.WithCancelCause(base)
	defer cancel(nil)
	ctx := &cancelAtErrCallContext{Context: owner, cancel: cancel, target: 3}
	root := t.TempDir()
	destination := filepath.Join(root, "private.png")
	err := session.Screen.CaptureRegion(ctx, frame.Bounds(), destination)
	if !errors.Is(err, context.Canceled) || ctx.calls != 3 {
		t.Fatalf("canceled file staging: err=%v checks=%d", err, ctx.calls)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("canceled screenshot left files: entries=%v err=%v", entries, err)
	}
}
