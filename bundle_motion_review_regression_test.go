package perfuncted_test

import (
	"context"
	"errors"
	"image"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/pftest"
)

type motionNoSpace struct{ input.Inputter }

func TestSmoothRequiresAuthoritativeCoordinateSpace(t *testing.T) {
	opts := perfuncted.MotionOptions{Duration: 35 * time.Millisecond}
	a, b := image.Pt(0, 0), image.Pt(40, 40)
	for _, tc := range []struct {
		name        string
		backend     input.Inputter
		unsupported bool
	}{
		{"missing", motionNoSpace{Inputter: &pftest.Inputter{}}, true},
		{"unsupported", &motionSpy{spaceErr: input.ErrNotSupported}, true},
		{"unknown-kind", &motionSpy{space: input.CoordinateSpaceInfo{Kind: "browser-dpr", ScaleX: 1, ScaleY: 1}}, false},
		{"wrong-logical-scale", &motionSpy{space: input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 2, ScaleY: 2}}, false},
		{"invalid-physical-scale", &motionSpy{space: input.CoordinateSpaceInfo{Kind: input.CoordinateSpacePhysical, ScaleX: math.NaN(), ScaleY: 1}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := pftest.New(nil, tc.backend, nil, nil)
			defer session.Close()
			for _, drag := range []bool{false, true} {
				var err error
				if drag {
					err = session.Input.DragAndDropSmooth(context.Background(), a, b, opts)
				} else {
					err = session.Input.MouseMoveSmooth(context.Background(), &a, b, opts)
				}
				if err == nil {
					t.Fatal("unproven coordinate space accepted")
				}
				if tc.unsupported && !errors.Is(err, perfuncted.ErrUnsupported) {
					t.Fatalf("expected unsupported: %v", err)
				}
			}
			if tc.name == "missing" {
				if err := session.Input.MouseMove(context.Background(), 10, 10); err != nil {
					t.Fatal(err)
				}
				if err := session.Input.DragAndDrop(context.Background(), 0, 0, 1, 1); err != nil {
					t.Fatal(err)
				}
			} else {
				spy, ok := tc.backend.(*motionSpy)
				if !ok {
					t.Fatalf("expected motion spy, got %T", tc.backend)
				}
				if spy.moves != 0 || spy.down != 0 || spy.up != 0 || spy.validation != 0 {
					t.Fatalf("rejected request injected events: %+v", spy)
				}
			}
		})
	}
	for _, space := range []input.CoordinateSpaceInfo{
		{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1},
		{Kind: input.CoordinateSpacePhysical, ScaleX: 2, ScaleY: 1.5},
	} {
		spy := &motionSpy{space: space}
		session := pftest.New(nil, spy, nil, nil)
		if err := session.Input.MouseMoveSmooth(context.Background(), &a, b, opts); err != nil {
			t.Fatal(err)
		}
		if spy.Calls[len(spy.Calls)-1] != "move:40,40" {
			t.Fatal("coordinates scaled without request", spy.Calls)
		}
		if err := session.Input.DragAndDropSmooth(context.Background(), a, b, opts); err != nil {
			t.Fatal(err)
		}
		session.Close()
	}
}

func TestSmoothZeroDistanceDragIsImmediateWithoutRedundantMotion(t *testing.T) {
	spy := &motionSpy{}
	session := pftest.New(nil, spy, nil, nil)
	defer session.Close()
	at := image.Pt(200, 200)
	start := time.Now()
	err := session.Input.DragAndDropSmooth(context.Background(), at, at, perfuncted.MotionOptions{Duration: 800 * time.Millisecond, Easing: "ease-in-out", Curve: .4, Variation: 16, Seed: 9})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("zero-distance drag waited %v", elapsed)
	}
	want := []string{"move:200,200", "mousedown", "mouseup"}
	if !reflect.DeepEqual(spy.Calls, want) {
		t.Fatalf("got %v want %v", spy.Calls, want)
	}
	if spy.validation != 1 {
		t.Fatalf("validated %d points, want 1", spy.validation)
	}
}

type rejectNegativeCurveInput struct{ *motionSpy }

func (r *rejectNegativeCurveInput) ValidatePointerTarget(_ context.Context, x, y int) error {
	r.validation++
	if y < 0 {
		return errors.New("curve outside output")
	}
	return nil
}
func TestSmoothCurvedIntermediatePreflightRejectsBeforeButton(t *testing.T) {
	spy := &motionSpy{}
	backend := &rejectNegativeCurveInput{motionSpy: spy}
	session := pftest.New(nil, backend, nil, nil)
	defer session.Close()
	err := session.Input.DragAndDropSmooth(context.Background(), image.Pt(0, 0), image.Pt(100, 0), perfuncted.MotionOptions{Duration: 80 * time.Millisecond, Curve: -.5})
	if err == nil {
		t.Fatal("offscreen curve accepted")
	}
	if spy.moves != 0 || spy.down != 0 || spy.up != 0 {
		t.Fatalf("preflight injected input: %+v", spy)
	}
	if spy.validation <= 1 {
		t.Fatalf("intermediate was not validated: %d", spy.validation)
	}
}

// A zero-duration option delegates to the original immediate drag and does
// not start a provenance-dependent smooth transaction.
func TestSmoothZeroDurationPreservesLegacyDrag(t *testing.T) {
	spy := &motionSpy{spaceErr: input.ErrNotSupported}
	session := pftest.New(nil, spy, nil, nil)
	defer session.Close()
	err := session.Input.DragAndDropSmooth(context.Background(), image.Pt(1, 2), image.Pt(3, 4), perfuncted.MotionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"move:1,2", "mousedown", "move:3,4", "mouseup"}
	if !reflect.DeepEqual(spy.Calls, want) {
		t.Fatalf("legacy calls %v want %v", spy.Calls, want)
	}
}
