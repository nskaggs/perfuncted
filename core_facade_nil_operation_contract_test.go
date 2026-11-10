package perfuncted

import (
	"context"
	"errors"
	"image"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

// Public facade methods must reject an absent session without touching a
// display, spawning a process, or panicking on embedded bundleBase access.
func TestCoreFacadeMethodsFailClosedOnNilReceivers(t *testing.T) {
	var in *InputBundle
	var sc *ScreenBundle
	var win *WindowBundle
	var out *OutputBundle
	var cb *ClipboardBundle
	ctx := context.Background()
	calls := []struct {
		name   string
		invoke func() error
	}{
		{"key-down", func() error { return in.KeyDown(ctx, "A") }},
		{"key-up", func() error { return in.KeyUp(ctx, "A") }},
		{"type", func() error { return in.Type(ctx, "text") }},
		{"type-literal", func() error { return in.TypeLiteral(ctx, "text") }},
		{"move", func() error { return in.MouseMove(ctx, 1, 2) }},
		{"click", func() error { return in.MouseClick(ctx, 1, 2, 1) }},
		{"click-center", func() error { return in.ClickCenter(ctx, image.Rect(0, 0, 2, 2)) }},
		{"double-click", func() error { return in.DoubleClick(ctx, 1, 2) }},
		{"button-down", func() error { return in.MouseDown(ctx, 1) }},
		{"button-up", func() error { return in.MouseUp(ctx, 1) }},
		{"scroll-up", func() error { return in.ScrollUp(ctx, 1) }},
		{"scroll-down", func() error { return in.ScrollDown(ctx, 1) }},
		{"scroll-left", func() error { return in.ScrollLeft(ctx, 1) }},
		{"scroll-right", func() error { return in.ScrollRight(ctx, 1) }},
		{"pointer-location", func() error { _, _, err := in.PointerLocation(ctx); return err }},
		{"pointer-space", func() error { _, err := in.PointerCoordinateSpace(ctx); return err }},
		{"input-sync", func() error { return in.Sync(ctx) }},
		{"drag", func() error { return in.DragAndDrop(ctx, 1, 2, 3, 4) }},
		{"screen-grab", func() error { _, err := sc.Grab(ctx, image.Rectangle{}); return err }},
		{"screen-hash", func() error { _, err := sc.GrabFullHash(ctx); return err }},
		{"screen-pixels", func() error { _, err := sc.GetAllPixels(ctx); return err }},
		{"window-sync", func() error { return win.Sync(ctx) }},
		{"window-title", func() error { _, err := win.ActiveTitle(ctx); return err }},
		{"window-events", func() error { _, err := win.Events(); return err }},
		{"output-list", func() error { _, err := out.List(ctx); return err }},
		{"clipboard-get", func() error { _, err := cb.Get(ctx); return err }},
		{"clipboard-set", func() error { return cb.Set(ctx, "text") }},
		{"clipboard-paste-no-input", func() error { return cb.pasteWithInputContext(ctx, "text", nil) }},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.invoke(); !errors.Is(err, ErrNilSession) {
				t.Fatalf("absent session returned %v, want ErrNilSession", err)
			}
		})
	}
	if err := in.close(); err != nil {
		t.Fatalf("nil input close: %v", err)
	}
	if err := sc.close(); err != nil {
		t.Fatalf("nil screen close: %v", err)
	}
	if err := win.close(); err != nil {
		t.Fatalf("nil window close: %v", err)
	}
	if err := out.close(); err != nil {
		t.Fatalf("nil outputs close: %v", err)
	}
	if err := cb.close(); err != nil {
		t.Fatalf("nil clipboard close: %v", err)
	}
}

// Validate the action-and-wait API's error order without opening AT-SPI.
func TestCoreAccessibilityActionWaitRejectsInvalidCalls(t *testing.T) {
	var absent *Session
	root := accessibility.NodeID{}
	query := accessibility.Query{}
	_, _, err := absent.InvokeAccessibilityActionAndWait(context.Background(), root, query, "click", accessibility.SnapshotOptions{}, nil)
	if !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil session: %v", err)
	}
	live := &Session{}
	_, _, err = live.InvokeAccessibilityActionAndWait(nil, root, query, "click", accessibility.SnapshotOptions{}, nil)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil context: %v", err)
	}
	_, _, err = live.InvokeAccessibilityActionAndWait(context.Background(), root, query, "click", accessibility.SnapshotOptions{}, nil)
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("nil postcondition: %v", err)
	}
}
