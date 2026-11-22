package perfuncted_test

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/pftest"
)

type motionSpy struct {
	pftest.Inputter
	locationErr error
	spaceErr    error
	space       input.CoordinateSpaceInfo
	onSpace     func()
	reject      bool
	failedAt    int
	cancel      context.CancelFunc
	down        int
	up          int
	moves       int
	validation  int
	onValidate  func()
	onLocate    func()
	onMove      func()
	onDown      func()
}

func (s *motionSpy) MouseMove(ctx context.Context, x, y int) error {
	s.moves++
	if s.onMove != nil {
		s.onMove()
	}
	if s.cancel != nil && s.moves == 2 {
		s.cancel()
	}
	if s.failedAt != 0 && s.moves == s.failedAt {
		return errors.New("move failed")
	}
	return s.Inputter.MouseMove(ctx, x, y)
}
func (s *motionSpy) MouseDown(ctx context.Context, b int) error {
	s.down++
	if s.onDown != nil {
		s.onDown()
	}
	return s.Inputter.MouseDown(ctx, b)
}
func (s *motionSpy) MouseUp(ctx context.Context, b int) error {
	s.up++
	return s.Inputter.MouseUp(ctx, b)
}
func (s *motionSpy) PointerLocation(ctx context.Context) (int, int, error) {
	if s.onLocate != nil {
		s.onLocate()
	}
	if s.locationErr != nil {
		return 0, 0, s.locationErr
	}
	return 5, 7, nil
}
func (s *motionSpy) PointerCoordinateSpace(_ context.Context) (input.CoordinateSpaceInfo, error) {
	if s.onSpace != nil {
		s.onSpace()
	}
	if s.spaceErr != nil {
		return input.CoordinateSpaceInfo{}, s.spaceErr
	}
	if s.space.Kind != "" {
		return s.space, nil
	}
	return input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}, nil
}
func (s *motionSpy) ValidatePointerTarget(_ context.Context, x, y int) error {
	s.validation++
	if s.onValidate != nil {
		s.onValidate()
	}
	if s.reject && x > 5 {
		return errors.New("outside bounds")
	}
	return nil
}
func TestMotionOptionsAndEnvelope(t *testing.T) {
	a, b := image.Pt(0, 0), image.Pt(100, 100)
	opts := perfuncted.MotionOptions{Duration: 80 * time.Millisecond, Easing: "ease-in-out", Curve: .4, Variation: 16, Seed: 99}
	s := &motionSpy{}
	pf := pftest.New(nil, s, nil, nil)
	defer pf.Close()
	if err := pf.Input.MouseMoveSmooth(context.Background(), &a, b, opts); err != nil {
		t.Fatal(err)
	}
	if s.moves < 3 || s.moves > 121 {
		t.Fatalf("moves=%d", s.moves)
	}
	if s.validation != s.moves {
		t.Fatalf("validation count=%d moves=%d", s.validation, s.moves)
	}
	if s.Inputter.Calls[0] != "move:0,0" || s.Inputter.Calls[len(s.Inputter.Calls)-1] != "move:100,100" {
		t.Fatal(s.Inputter.Calls)
	}
	for _, c := range s.Inputter.Calls {
		var x, y int
		if _, err := fmt.Sscanf(c, "move:%d,%d", &x, &y); err != nil {
			t.Fatal(err)
		}
		if x < -66 || x > 166 || y < -66 || y > 166 {
			t.Fatal(c)
		}
	}
	r := &motionSpy{}
	other := pftest.New(nil, r, nil, nil)
	defer other.Close()
	if err := other.Input.MouseMoveSmooth(context.Background(), &a, b, opts); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s.Inputter.Calls, r.Inputter.Calls) {
		t.Fatal("seeded motion not deterministic")
	}
}
func TestMotionValidationBeforeInput(t *testing.T) {
	invalid := []perfuncted.MotionOptions{
		{Duration: -time.Second}, {Duration: 11 * time.Second}, {Duration: 20 * time.Millisecond, Easing: "unknown"},
		{Duration: time.Second, Curve: math.NaN()}, {Duration: time.Second, Curve: math.Inf(1)},
		{Duration: time.Second, Curve: .6}, {Duration: time.Second, Variation: -1}, {Duration: time.Second, Variation: 17},
	}
	for _, o := range invalid {
		s := &motionSpy{}
		pf := pftest.New(nil, s, nil, nil)
		if err := pf.Input.MouseMoveSmooth(context.Background(), nil, image.Pt(3, 4), o); err == nil {
			t.Fatalf("accepted %+v", o)
		}
		if err := pf.Input.DragAndDropSmooth(context.Background(), image.Pt(1, 2), image.Pt(3, 4), o); err == nil {
			t.Fatalf("drag accepted %+v", o)
		}
		if s.moves+s.down+s.up+s.validation != 0 {
			t.Fatalf("invalid motion injected input: %+v", s)
		}
		pf.Close()
	}
	s := &motionSpy{reject: true}
	pf := pftest.New(nil, s, nil, nil)
	defer pf.Close()
	err := pf.Input.DragAndDropSmooth(context.Background(), image.Pt(0, 0), image.Pt(10, 10), perfuncted.MotionOptions{Duration: 40 * time.Millisecond})
	if err == nil || s.moves != 0 || s.down != 0 || s.up != 0 {
		t.Fatalf("bounds rejection: %v %+v", err, s)
	}
}
func TestMotionUnknownOriginAndImmediate(t *testing.T) {
	s := &motionSpy{locationErr: input.ErrNotSupported}
	pf := pftest.New(nil, s, nil, nil)
	defer pf.Close()
	err := pf.Input.MouseMoveSmooth(context.Background(), nil, image.Pt(20, 20), perfuncted.MotionOptions{Duration: 40 * time.Millisecond})
	if !errors.Is(err, input.ErrNotSupported) || s.moves != 0 {
		t.Fatalf("unknown origin: %v %+v", err, s)
	}
	if err := pf.Input.MouseMoveSmooth(context.Background(), nil, image.Pt(20, 20), perfuncted.MotionOptions{}); err != nil {
		t.Fatal(err)
	}
	if s.moves != 1 || s.up != 0 {
		t.Fatalf("direct move: %+v", s)
	}
}
func TestMotionDragCleanup(t *testing.T) {
	o := perfuncted.MotionOptions{Duration: 40 * time.Millisecond}
	for _, tc := range []struct {
		name             string
		failAt           int
		cancel           bool
		wantDown, wantUp int
	}{
		{"success", 0, false, 1, 1}, {"mid-failure", 2, false, 1, 1}, {"cancel-after-press", 2, true, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &motionSpy{failedAt: tc.failAt}
			ctx := context.Background()
			if tc.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				s.cancel = cancel
			}
			pf := pftest.New(nil, s, nil, nil)
			defer pf.Close()
			err := pf.Input.DragAndDropSmooth(ctx, image.Pt(1, 1), image.Pt(60, 60), o)
			if tc.name == "success" && err != nil {
				t.Fatal(err)
			}
			if tc.name != "success" && err == nil {
				t.Fatal("expected failure")
			}
			if s.down != tc.wantDown || s.up != tc.wantUp {
				t.Fatalf("button state: %+v err %v", s, err)
			}
			if s.moves > 4 {
				t.Fatalf("unexpected subsequent moves: %d", s.moves)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &motionSpy{}
	pf := pftest.New(nil, s, nil, nil)
	defer pf.Close()
	if err := pf.Input.DragAndDropSmooth(ctx, image.Pt(1, 1), image.Pt(60, 60), o); !errors.Is(err, context.Canceled) {
		t.Fatalf("precancel: %v", err)
	}
	if s.moves != 0 || s.down != 0 || s.up != 0 {
		t.Fatalf("precancel side effects: %+v", s)
	}
}
func TestMotionObservedOriginAndNoop(t *testing.T) {
	s := &motionSpy{}
	pf := pftest.New(nil, s, nil, nil)
	defer pf.Close()
	if err := pf.Input.MouseMoveSmooth(context.Background(), nil, image.Pt(5, 7), perfuncted.MotionOptions{Duration: 30 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if s.moves != 0 {
		t.Fatalf("noop moved: %+v", s)
	}
	if err := pf.Input.MouseMoveSmooth(context.Background(), nil, image.Pt(9, 12), perfuncted.MotionOptions{Duration: 30 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s.Inputter.Calls[0], "move:5,7") {
		t.Fatal(s.Inputter.Calls)
	}
}

// TestMotionAuthorityTransitions guards the boundaries where a backend call
// completes only after caller cancellation or Session.Close.
func TestMotionAuthorityTransitions(t *testing.T) {
	opts := perfuncted.MotionOptions{Duration: 40 * time.Millisecond}
	a, b := image.Pt(1, 1), image.Pt(20, 20)
	for _, stage := range []string{"validation-cancel", "validation-close", "location-cancel", "location-close", "move-cancel", "move-close", "down-cancel", "down-close"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s := &motionSpy{}
			pf := pftest.New(nil, s, nil, nil)
			defer pf.Close()
			stop := func() {
				if strings.HasSuffix(stage, "-cancel") {
					cancel()
				} else {
					_ = pf.Close()
				}
			}
			switch {
			case strings.HasPrefix(stage, "validation"):
				s.onValidate = stop
			case strings.HasPrefix(stage, "location"):
				s.onLocate = stop
			case strings.HasPrefix(stage, "move"):
				s.onMove = stop
			case strings.HasPrefix(stage, "down"):
				s.onDown = stop
			}
			var err error
			if strings.HasPrefix(stage, "down") || strings.HasPrefix(stage, "move") {
				err = pf.Input.DragAndDropSmooth(ctx, a, b, opts)
			} else {
				origin := &a
				if strings.HasPrefix(stage, "location") {
					origin = nil
				}
				err = pf.Input.MouseMoveSmooth(ctx, origin, b, opts)
			}
			if err == nil {
				t.Fatalf("authority loss ignored: %+v", s)
			}
			if strings.HasPrefix(stage, "down") && s.up != 1 {
				t.Fatalf("held button not released: %+v", s)
			}
			if strings.HasPrefix(stage, "validation") && s.moves != 0 {
				t.Fatalf("validation mutated: %+v", s)
			}
		})
	}
}

func TestMotionCancellationInTimedWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &motionSpy{}
	pf := pftest.New(nil, s, nil, nil)
	defer pf.Close()
	s.onMove = func() {
		if s.moves == 1 {
			cancel()
		}
	}
	err := pf.Input.MouseMoveSmooth(ctx, ptrPoint(image.Pt(0, 0)), image.Pt(50, 50), perfuncted.MotionOptions{Duration: 500 * time.Millisecond})
	if !errors.Is(err, context.Canceled) || s.moves != 1 {
		t.Fatalf("cancellation: %v %+v", err, s)
	}
}
func ptrPoint(p image.Point) *image.Point { return &p }
