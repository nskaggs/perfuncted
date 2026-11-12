package perfuncted

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
	"time"
)

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
