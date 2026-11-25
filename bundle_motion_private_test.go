package perfuncted

import (
	"context"
	"errors"
	"image"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/input"
)

type motionPrivateBackend struct {
	input.Inputter
	delay time.Duration
	moves int
}

func (b *motionPrivateBackend) MouseMove(context.Context, int, int) error {
	b.moves++
	if b.delay != 0 {
		time.Sleep(b.delay)
	}
	return nil
}
func (*motionPrivateBackend) PointerCoordinateSpace(context.Context) (input.CoordinateSpaceInfo, error) {
	return input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}, nil
}
func (*motionPrivateBackend) Close() error                                      { return nil }
func (*motionPrivateBackend) PointerLocation(context.Context) (int, int, error) { return 2, 3, nil }

type motionStepContext struct {
	context.Context
	calls     int
	trigger   int
	abort     bool
	onTrigger func()
}

func (c *motionStepContext) Err() error {
	c.calls++
	if c.calls == c.trigger {
		if c.onTrigger != nil {
			c.onTrigger()
		}
		if c.abort {
			return context.Canceled
		}
	}
	return nil
}
func (c *motionStepContext) Deadline() (time.Time, bool) { return time.Now().Add(time.Second), true }
func (c *motionStepContext) Done() <-chan struct{}       { return nil }

func TestMotionInternalAuthorityPerFrame(t *testing.T) {
	points := []motionPoint{{X: 0, Y: 0}, {X: 1, Y: 1}, {X: 2, Y: 2}}
	for _, tc := range []struct {
		name     string
		target   int
		close    bool
		duration time.Duration
	}{
		{"before-loop-cancel", 1, false, 0},
		{"before-loop-close", 1, true, 0},
		{"before-send-cancel", 2, false, 0},
		{"before-send-close", 2, true, 0},
		{"after-wait-cancel", 2, false, 80 * time.Millisecond},
		{"after-wait-close", 2, true, 80 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &motionPrivateBackend{}
			s := NewSessionForTesting(nil, backend, nil, nil, nil)
			defer s.Close()
			ctx := &motionStepContext{Context: context.Background(), trigger: tc.target, abort: !tc.close}
			if tc.close {
				ctx.onTrigger = func() { _ = s.Close() }
			}
			if err := s.Input.sendMotion(ctx, points, tc.duration, 0); err == nil {
				t.Fatalf("ignored authority loss: %+v", tc)
			}
		})
	}
	t.Run("skip-overdue", func(t *testing.T) {
		backend := &motionPrivateBackend{delay: 40 * time.Millisecond}
		s := NewSessionForTesting(nil, backend, nil, nil, nil)
		defer s.Close()
		if err := s.Input.sendMotion(context.Background(), points, 20*time.Millisecond, 0); err != nil {
			t.Fatal(err)
		}
		if backend.moves != 2 {
			t.Fatalf("overdue intermediate should be dropped, moves=%d", backend.moves)
		}
	})
	t.Run("cancel-during-wait", func(t *testing.T) {
		backend := &motionPrivateBackend{}
		s := NewSessionForTesting(nil, backend, nil, nil, nil)
		defer s.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { time.Sleep(5 * time.Millisecond); cancel() }()
		err := s.Input.sendMotion(ctx, points, 200*time.Millisecond, 1)
		if !errors.Is(err, context.Canceled) || backend.moves != 0 {
			t.Fatalf("wait cancellation err=%v moves=%d", err, backend.moves)
		}
	})
}

func TestMotionPrivateAdmissionAndPreflight(t *testing.T) {
	if err := ValidateMotionOptions(MotionOptions{Duration: -1}); err == nil {
		t.Fatal("invalid options allowed")
	}
	if err := ValidateMotionOptions(MotionOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		close bool
	}{
		{"cancel-before-drag", false}, {"close-before-drag", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &motionPrivateBackend{}
			s := NewSessionForTesting(nil, backend, nil, nil, nil)
			defer s.Close()
			ctx := &motionStepContext{Context: context.Background(), trigger: 1, abort: !tc.close}
			if tc.close {
				ctx.onTrigger = func() { _ = s.Close() }
			}
			err := s.Input.dragPoints(ctx, []motionPoint{{X: 1, Y: 1}, {X: 2, Y: 2}}, 0)
			if err == nil || backend.moves != 0 {
				t.Fatalf("admission err=%v moves=%d", err, backend.moves)
			}
		})
	}
	t.Run("move-closed", func(t *testing.T) {
		backend := &motionPrivateBackend{}
		s := NewSessionForTesting(nil, backend, nil, nil, nil)
		_ = s.Close()
		err := s.Input.MouseMoveSmooth(context.Background(), &image.Point{}, image.Pt(4, 4), MotionOptions{Duration: 20 * time.Millisecond})
		if !errors.Is(err, ErrSessionClosed) {
			t.Fatal(err)
		}
	})
	t.Run("move-canceled", func(t *testing.T) {
		backend := &motionPrivateBackend{}
		s := NewSessionForTesting(nil, backend, nil, nil, nil)
		defer s.Close()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := s.Input.MouseMoveSmooth(ctx, &image.Point{}, image.Pt(4, 4), MotionOptions{Duration: 20 * time.Millisecond})
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
	for _, tc := range []struct {
		name  string
		close bool
	}{
		{"cancel-after-path", false}, {"close-after-path", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &motionPrivateBackend{}
			s := NewSessionForTesting(nil, backend, nil, nil, nil)
			defer s.Close()
			ctx := &motionStepContext{Context: context.Background(), trigger: 2, abort: !tc.close}
			if tc.close {
				ctx.onTrigger = func() { _ = s.Close() }
			}
			err := s.Input.MouseMoveSmooth(ctx, &image.Point{}, image.Pt(4, 4), MotionOptions{Duration: 20 * time.Millisecond})
			if err == nil || backend.moves != 0 {
				t.Fatalf("authority err=%v moves=%d", err, backend.moves)
			}
		})
	}
}

func TestMotionLocationCapabilityRevokedBetweenChecks(t *testing.T) {
	backend := &motionPrivateBackend{}
	s := NewSessionForTesting(nil, backend, nil, nil, nil)
	defer s.Close()
	ctx := &motionStepContext{Context: context.Background(), trigger: 1}
	ctx.onTrigger = func() { _ = s.Close() }
	err := s.Input.MouseMoveSmooth(ctx, nil, image.Pt(4, 4), MotionOptions{Duration: 20 * time.Millisecond})
	if !errors.Is(err, ErrSessionClosed) || backend.moves != 0 {
		t.Fatalf("location admission err=%v moves=%d", err, backend.moves)
	}
}

// Verify the coordinate-space metadata does not silently grant location-read
// authority when only explicit-start movement is permitted.
func TestMotionNoPointerLocationPermission(t *testing.T) {
	backend := &motionPrivateBackend{}
	session := NewSessionForTesting(nil, backend, nil, nil, nil)
	defer session.Close()
	operations := session.Input.resolvedOperations[:0]
	for _, name := range session.Input.resolvedOperations {
		if name != "pointer-location" {
			operations = append(operations, name)
		}
	}
	session.Input.resolvedOperations = operations
	err := session.Input.MouseMoveSmooth(context.Background(), nil, image.Pt(4, 4), MotionOptions{Duration: 20 * time.Millisecond})
	if !errors.Is(err, ErrUnsupported) || backend.moves != 0 {
		t.Fatalf("location authority err=%v moves=%d", err, backend.moves)
	}
}

// Context and Session authority can be lost after non-mutating preflight and
// before the first event. Exercise all context-read boundaries rather than
// timing cancellation using wall clocks.
func TestMotionPreflightHandoffAuthority(t *testing.T) {
	for _, closeSession := range []bool{false, true} {
		for trigger := 1; trigger <= 35; trigger++ {
			backend := &motionPrivateBackend{}
			session := NewSessionForTesting(nil, backend, nil, nil, nil)
			ctx := &motionStepContext{Context: context.Background(), trigger: trigger, abort: !closeSession}
			if closeSession {
				ctx.onTrigger = func() { _ = session.Close() }
			}
			_ = session.Input.MouseMoveSmooth(ctx, &image.Point{}, image.Pt(5, 5), MotionOptions{Duration: 20 * time.Millisecond})
			_ = session.Close()
		}
	}
}
