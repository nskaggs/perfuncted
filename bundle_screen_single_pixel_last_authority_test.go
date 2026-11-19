package perfuncted

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
	"time"
)

// The FirstPixel helper checks caller authority while acquiring and sampling
// the frame. The facade must independently check it once more before it
// releases the sampled private pixel to an application.
func TestScreenGetPixelFinalCallerRevocationDoesNotPublishSample(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 1, 1))
	frame.SetRGBA(0, 0, color.RGBA{R: 101, G: 44, B: 9, A: 255})
	session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	parent, cancelDeadline := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancelDeadline()
	owner, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	ctx := &cancelAtErrCallContext{Context: owner, cancel: cancel, target: 4}
	pixel, err := session.Screen.GetPixel(ctx, 0, 0)
	if pixel != (color.RGBA{}) || !errors.Is(err, context.Canceled) || ctx.calls != 4 {
		t.Fatalf("final caller revocation leaked pixel=%+v err=%v checks=%d", pixel, err, ctx.calls)
	}
}
