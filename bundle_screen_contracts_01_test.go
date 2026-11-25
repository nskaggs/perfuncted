package perfuncted

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A failed multi-pixel capture must never expose a partially initialized
// result, irrespective of how many private coordinates were requested.
func TestScreenMultiplePixelsRejectsCaptureTransportFailureWithoutPartialResults(t *testing.T) {
	failure := errors.New("synthetic screenshot transport unavailable")
	backend := &failingSettleBaselineCapture{failure: failure}
	session := NewSessionForTesting(backend, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	points := []image.Point{{X: 1, Y: 2}, {X: 3, Y: 4}, {X: 5, Y: 6}}
	colors, err := session.Screen.GetMultiplePixels(context.Background(), points)
	if colors != nil || !errors.Is(err, failure) || backend.calls != 1 {
		t.Fatalf("failed multi-pixel capture disclosed data: colors=%v err=%v backend calls=%d", colors, err, backend.calls)
	}
}

// The shared grab helper rejects nil (including typed-nil) images before
// returning. Multiple-pixel reads must preserve that rejection and never
// expose a partially initialized pixel slice.
func TestScreenMultiplePixelsRejectsNilCaptureThroughSharedValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame image.Image
	}{
		{name: "nil image", frame: nil},
		{name: "typed nil image", frame: (*image.RGBA)(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: tc.frame}, nil, nil, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			pixels, err := session.Screen.GetMultiplePixels(context.Background(), []image.Point{{X: 3, Y: 4}, {X: 4, Y: 4}})
			if pixels != nil || err == nil || !strings.Contains(err.Error(), "nil image") {
				t.Fatalf("invalid capture exposed multiple pixels: pixels=%v err=%v", pixels, err)
			}
		})
	}
}

// ScreenBundle delegates nil caller contexts to backendContext, which treats
// them as Background. Post-capture checks must use that effective context too.
func TestScreenCaptureRegionNilContextDoesNotPanicAfterSuccessfulGrab(t *testing.T) {
	session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: image.NewRGBA(image.Rect(0, 0, 2, 2))}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	path := filepath.Join(t.TempDir(), "capture.png")
	defer func() {
		if failure := recover(); failure != nil {
			t.Errorf("CaptureRegion nil context panic: %v", failure)
		}
	}()
	if err := session.Screen.CaptureRegion(nil, image.Rect(0, 0, 2, 2), path); err != nil {
		t.Fatalf("nil context capture: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	actual, err := png.Decode(f)
	if err != nil || actual.Bounds() != image.Rect(0, 0, 2, 2) {
		t.Fatalf("capture file bounds=%v err=%v", actual, err)
	}
}

func TestScreenMultiplePixelsNilContextDoesNotPanicAfterSuccessfulGrab(t *testing.T) {
	frame := image.NewRGBA(image.Rect(0, 0, 1, 1))
	frame.SetRGBA(0, 0, color.RGBA{R: 0x33, G: 0x44, B: 0x55, A: 0xff})
	session := NewSessionForTesting(syntheticMalformedScreenshotter{frame: frame}, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	defer func() {
		if failure := recover(); failure != nil {
			t.Errorf("GetMultiplePixels nil context panic: %v", failure)
		}
	}()
	pixels, err := session.Screen.GetMultiplePixels(nil, []image.Point{{X: 1, Y: 1}})
	if err != nil || len(pixels) != 1 || pixels[0] != (color.RGBA{R: 0x33, G: 0x44, B: 0x55, A: 0xff}) {
		t.Fatalf("nil context pixels=%v err=%v", pixels, err)
	}
}

func TestScreenBundleOperationsFailClosedWithoutBackend(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()

	ctx := context.Background()
	region := image.Rect(2, 3, 8, 9)
	cases := []struct {
		name      string
		operation string
		run       func() error
	}{
		{
			name:      "wait for no change",
			operation: "wait-stable",
			run: func() error {
				_, err := session.Screen.WaitForNoChange(ctx, region, 1, time.Millisecond)
				return err
			},
		},
		{
			name:      "wait for no change from initial hash",
			operation: "wait-stable",
			run: func() error {
				_, err := session.Screen.WaitForNoChangeFrom(ctx, region, 7, 1, time.Millisecond)
				return err
			},
		},
		{
			name:      "find color",
			operation: "pixel",
			run: func() error {
				_, err := session.Screen.FindColor(ctx, region, color.RGBA{R: 1, A: 255}, 0)
				return err
			},
		},
		{
			name:      "wait for change",
			operation: "wait-change",
			run: func() error {
				_, err := session.Screen.WaitForChange(ctx, region, 7, time.Millisecond)
				return err
			},
		},
		{
			name:      "wait for hash",
			operation: "wait",
			run: func() error {
				_, err := session.Screen.WaitFor(ctx, region, 9, time.Millisecond)
				return err
			},
		},
		{
			name:      "scan for hash",
			operation: "wait",
			run: func() error {
				_, err := session.Screen.ScanFor(ctx, []image.Rectangle{region}, []uint32{9}, time.Millisecond)
				return err
			},
		},
		{
			name:      "resolve dimensions",
			operation: "resolution",
			run: func() error {
				_, _, err := session.Screen.Resolution(ctx)
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			var unavailable *CapabilityError
			if !errors.Is(err, ErrUnavailable) || !errors.As(err, &unavailable) || unavailable.Operation != tc.operation {
				t.Fatalf("operation error = %v, want unavailable screen capability for %q", err, tc.operation)
			}
		})
	}
}

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

// Settle must stop before running a caller action when the baseline capture
// fails, propagate an action failure without waiting for a change, and never
// return a stable hash when an action has not produced any visible change.
func TestScreenWaitForSettleRejectsFailedActionAndAbsentChange(t *testing.T) {
	rect := image.Rect(0, 0, 4, 4)
	actionErr := errors.New("synthetic action refused")
	t.Run("unavailable source never dispatches", func(t *testing.T) {
		session := NewSessionForTesting(nil, nil, nil, nil, nil)
		defer session.Close()
		invoked := false
		hash, err := session.Screen.WaitForSettle(context.Background(), rect, func() error { invoked = true; return nil }, 1, time.Millisecond)
		if hash != 0 || !errors.Is(err, ErrUnavailable) || invoked {
			t.Fatalf("unavailable screenshot source returned hash=%x err=%v invoked=%t", hash, err, invoked)
		}
	})
	t.Run("failed action does not wait or capture extra frames", func(t *testing.T) {
		frame := image.NewRGBA(rect)
		frame.SetRGBA(1, 1, color.RGBA{R: 255, A: 255})
		mock := &captureTestScreenshotter{img: frame}
		session := NewSessionForTesting(mock, nil, nil, nil, nil)
		defer session.Close()
		calls := 0
		hash, err := session.Screen.WaitForSettle(context.Background(), rect, func() error { calls++; return actionErr }, 2, time.Millisecond)
		if hash != 0 || !errors.Is(err, actionErr) || calls != 1 {
			t.Fatalf("failed action hash=%x err=%v calls=%d", hash, err, calls)
		}
	})
	t.Run("no visible change cannot be reported settled", func(t *testing.T) {
		frame := image.NewRGBA(rect)
		frame.SetRGBA(2, 2, color.RGBA{B: 255, A: 255})
		mock := &captureTestScreenshotter{img: frame}
		session := NewSessionForTesting(mock, nil, nil, nil, nil)
		defer session.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
		defer cancel()
		calls := 0
		hash, err := session.Screen.WaitForSettle(ctx, rect, func() error { calls++; return nil }, 2, time.Millisecond)
		if hash != 0 || err == nil || calls != 1 {
			t.Fatalf("unchanged screen declared stable: hash=%x err=%v actionCalls=%d", hash, err, calls)
		}
	})
}

// The action may withdraw caller authority after the initial hash was read.
// No change-detection or stability polling is allowed after that cancellation.
func TestScreenWaitForSettleCancellingActionSkipsChangePoll(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := &countingSettleCapture{syntheticMalformedScreenshotter: syntheticMalformedScreenshotter{frame: image.NewRGBA(image.Rect(0, 0, 2, 2))}}
	session := NewSessionForTesting(backend, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	actions := 0
	hash, err := session.Screen.WaitForSettle(ctx, image.Rect(0, 0, 2, 2), func() error {
		actions++
		cancel()
		return nil
	}, 1, time.Millisecond)
	if !errors.Is(err, context.Canceled) || hash != 0 || actions != 1 {
		t.Fatalf("canceled action proceeded: hash=%08x err=%v actions=%d", hash, err, actions)
	}
	if backend.grabs != 1 {
		t.Fatalf("canceled action started new screen polling phase: calls=%d", backend.grabs)
	}
}

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
