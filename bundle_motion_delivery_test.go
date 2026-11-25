package perfuncted

import (
	"context"
	"errors"
	"image"
	"slices"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/input"
)

type motionDeliveryBackend struct {
	input.Inputter
	events    []string
	failureAt int
	flushes   int
}

func (b *motionDeliveryBackend) MouseMove(context.Context, int, int) error {
	b.events = append(b.events, "move")
	return nil
}
func (b *motionDeliveryBackend) MouseDown(context.Context, int) error {
	b.events = append(b.events, "down")
	return nil
}
func (b *motionDeliveryBackend) MouseUp(context.Context, int) error {
	b.events = append(b.events, "up")
	return nil
}
func (b *motionDeliveryBackend) Close() error { return nil }
func (b *motionDeliveryBackend) PointerCoordinateSpace(context.Context) (input.CoordinateSpaceInfo, error) {
	return input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}, nil
}
func (b *motionDeliveryBackend) FlushMotionRequests(context.Context) error {
	b.events = append(b.events, "flush")
	b.flushes++
	if b.failureAt == b.flushes {
		return errors.New("delivery acknowledgement failed")
	}
	return nil
}

func TestMotionDeliveryAcknowledgement(t *testing.T) {
	for _, tc := range []struct {
		name     string
		drag     bool
		failure  int
		wantTail []string
	}{
		{"move", false, 0, []string{"move", "flush"}},
		{"move-failed", false, 1, []string{"move", "flush"}},
		{"drag", true, 0, []string{"move", "flush", "up", "flush"}},
		{"drag-endpoint-failed", true, 1, []string{"move", "flush", "up"}},
		{"drag-release-failed", true, 2, []string{"move", "flush", "up", "flush"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &motionDeliveryBackend{failureAt: tc.failure}
			s := NewSessionForTesting(nil, backend, nil, nil, nil)
			defer s.Close()
			start, end := image.Pt(10, 20), image.Pt(40, 50)
			opts := MotionOptions{Duration: 20 * time.Millisecond}
			var err error
			if tc.drag {
				err = s.Input.DragAndDropSmooth(context.Background(), start, end, opts)
			} else {
				err = s.Input.MouseMoveSmooth(context.Background(), &start, end, opts)
			}
			if (err != nil) != (tc.failure != 0) {
				t.Fatalf("delivery error: %v", err)
			}
			if len(backend.events) < len(tc.wantTail) || !slices.Equal(backend.events[len(backend.events)-len(tc.wantTail):], tc.wantTail) {
				t.Fatalf("request order %v, want tail %v", backend.events, tc.wantTail)
			}
			if tc.drag && slices.Index(backend.events, "down") != 1 {
				t.Fatalf("drag press order: %v", backend.events)
			}
		})
	}
}
