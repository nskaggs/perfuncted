package perfuncted_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/nskaggs/perfuncted/pftest"
)

// A drag with an impossible destination must not move or press the mouse at
// its valid start point; otherwise a failed drag can still affect a document.
type boundedDragInputter struct{ pftest.Inputter }

func (*boundedDragInputter) ValidatePointerTarget(ctx context.Context, x, y int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if x < 0 || x >= 2867 || y < 0 || y >= 1200 {
		return fmt.Errorf("outside pointer bounds: %d,%d", x, y)
	}
	return nil
}

func TestDragPreflightsBothTargetsBeforeAnyMouseEvent(t *testing.T) {
	for _, tc := range []struct {
		name           string
		x1, y1, x2, y2 int
	}{
		{"invalid destination X", 1200, 600, 2867, 600},
		{"invalid destination Y", 1200, 600, 1200, 1200},
		{"invalid origin", -1, 600, 1200, 600},
		{"negative destination", 1200, 600, -1, 600},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &boundedDragInputter{}
			session := pftest.New(nil, backend, nil, nil)
			err := session.Input.DragAndDrop(context.Background(), tc.x1, tc.y1, tc.x2, tc.y2)
			if err == nil {
				t.Errorf("drag with invalid endpoint unexpectedly succeeded")
			}
			if len(backend.Calls) != 0 {
				t.Errorf("invalid drag injected events before validation: %v", backend.Calls)
			}
		})
	}
}

// The preflight must not prevent a valid drag or reorder its input events.
func TestDragWithPreflightedValidTargetsStillExecutes(t *testing.T) {
	backend := &boundedDragInputter{}
	session := pftest.New(nil, backend, nil, nil)
	if err := session.Input.DragAndDrop(context.Background(), 1200, 600, 1800, 900); err != nil {
		t.Fatal(err)
	}
	want := []string{"move:1200,600", "mousedown", "move:1800,900", "mouseup"}
	if !reflect.DeepEqual(backend.Calls, want) {
		t.Fatalf("valid drag events = %v, want %v", backend.Calls, want)
	}
}
