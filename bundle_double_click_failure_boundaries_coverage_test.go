package perfuncted_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

type doubleClickBoundaryBackend struct {
	pftest.Inputter
	failAt    string
	failure   error
	after     func()
	downCount int
	upCount   int
}

func (b *doubleClickBoundaryBackend) MouseMove(ctx context.Context, x, y int) error {
	if err := b.Inputter.MouseMove(ctx, x, y); err != nil {
		return err
	}
	if b.failAt == "initial-move" {
		return b.failure
	}
	return nil
}
func (b *doubleClickBoundaryBackend) MouseDown(ctx context.Context, button int) error {
	b.downCount++
	if err := b.Inputter.MouseDown(ctx, button); err != nil {
		return err
	}
	if b.failAt == "first-press" && b.downCount == 1 {
		return b.failure
	}
	return nil
}
func (b *doubleClickBoundaryBackend) MouseUp(ctx context.Context, button int) error {
	b.upCount++
	err := b.Inputter.MouseUp(ctx, button)
	if b.after != nil && b.upCount == 1 {
		b.after()
	}
	return err
}

// Before the first press there is nothing to release. A failing positioning
// move must not trigger input, and a failed press must release the held button.
func TestDoubleClickFailsClosedOnInitialMoveAndFirstPressErrors(t *testing.T) {
	failure := errors.New("synthetic first gesture transport error")
	for _, tc := range []struct {
		name string
		want []string
	}{
		{"initial-move", []string{"move:12,18"}},
		{"first-press", []string{"move:12,18", "mousedown", "mouseup"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &doubleClickBoundaryBackend{failAt: tc.name, failure: failure}
			session := perfuncted.NewSessionForTesting(nil, backend, nil, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			err := session.Input.DoubleClick(context.Background(), 12, 18)
			if !errors.Is(err, failure) || !reflect.DeepEqual(backend.Calls, tc.want) {
				t.Fatalf("gesture failure=%v calls=%v; expected %v calls=%v", err, backend.Calls, failure, tc.want)
			}
		})
	}
}

// Cancel after the first release, or during the inter-click delay, must not
// allow a second click. Both paths are deterministic synthetic events.
func TestDoubleClickStopsAfterCancellationAtFirstRelease(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := &doubleClickBoundaryBackend{after: cancel}
	session := perfuncted.NewSessionForTesting(nil, backend, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	err := session.Input.DoubleClick(ctx, 12, 18)
	want := []string{"move:12,18", "mousedown", "mouseup"}
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(backend.Calls, want) {
		t.Fatalf("canceled gesture err=%v calls=%v expected=%v", err, backend.Calls, want)
	}
}
