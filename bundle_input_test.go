package perfuncted_test

import (
	"context"
	"image"
	"testing"

	"github.com/nskaggs/perfuncted/pftest"
)

func TestPerfunctedPaste_ClipboardPath(t *testing.T) {
	inp := &pftest.Inputter{}
	cb := &pftest.Clipboard{}
	pf := pftest.New(nil, inp, nil, cb)

	// Perfuncted.Paste uses clipboard+PasteCombo when a clipboard is available.
	if err := pf.Paste("hello world"); err != nil {
		t.Fatalf("Paste: %v", err)
	}

	// Verify clipboard was set.
	if cb.Text != "hello world" {
		t.Errorf("clipboard text = %q, want %q", cb.Text, "hello world")
	}

	// Verify Type was called with "{ctrl+v}" (brace-combo Ctrl+V).
	found := false
	for _, call := range inp.Calls {
		if call == "type:{ctrl+v}" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected type:{ctrl+v} in calls: %v", inp.Calls)
	}
}

func TestPerfunctedPaste_FallbackToType(t *testing.T) {
	// When clipboard is nil, Paste should fall back to Type.
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)

	if err := pf.Paste("abc"); err != nil {
		t.Fatalf("Paste: %v", err)
	}

	// Should have called Type with the full string.
	found := false
	for _, call := range inp.Calls {
		if call == "type:abc" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected type:abc in calls: %v", inp.Calls)
	}
}

func TestPerfunctedPaste_ClipboardSetFails(t *testing.T) {
	// When clipboard.Set fails, Perfuncted.Paste returns the error.
	inp := &pftest.Inputter{}
	cb := &pftest.Clipboard{SetErr: context.DeadlineExceeded}
	pf := pftest.New(nil, inp, nil, cb)

	err := pf.Paste("fallback")
	if err == nil {
		t.Fatal("expected error when clipboard.Set fails")
	}
	t.Logf("got expected error: %v", err)
}

func TestInputBundleDoubleClick(t *testing.T) {
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)

	if err := pf.Input.DoubleClick(50, 75); err != nil {
		t.Fatalf("DoubleClick: %v", err)
	}

	// DoubleClick: MouseMove, MouseDown, MouseUp, MouseDown, MouseUp
	want := []string{
		"move:50,75",
		"mousedown",
		"mouseup",
		"mousedown",
		"mouseup",
	}
	if len(inp.Calls) != len(want) {
		t.Fatalf("unexpected call count: got %v want %v", inp.Calls, want)
	}
	for i, call := range want {
		if inp.Calls[i] != call {
			t.Fatalf("call %d = %q, want %q (all calls: %v)", i, inp.Calls[i], call, inp.Calls)
		}
	}
}

func TestInputBundleScroll_DxPositive(t *testing.T) {
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)

	if err := pf.Input.ScrollRight(3); err != nil {
		t.Fatalf("ScrollRight: %v", err)
	}

	if len(inp.Calls) != 1 {
		t.Fatalf("expected 1 call, got %v", inp.Calls)
	}
	if inp.Calls[0] != "scroll-right:3" {
		t.Errorf("call = %q, want scroll-right:3", inp.Calls[0])
	}
}

func TestInputBundleScroll_DxNegative(t *testing.T) {
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)

	if err := pf.Input.ScrollLeft(2); err != nil {
		t.Fatalf("ScrollLeft: %v", err)
	}

	if len(inp.Calls) != 1 {
		t.Fatalf("expected 1 call, got %v", inp.Calls)
	}
	if inp.Calls[0] != "scroll-left:2" {
		t.Errorf("call = %q, want scroll-left:2", inp.Calls[0])
	}
}

func TestInputBundleScroll_DyPositive(t *testing.T) {
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)

	if err := pf.Input.ScrollDown(5); err != nil {
		t.Fatalf("ScrollDown: %v", err)
	}

	if len(inp.Calls) != 1 {
		t.Fatalf("expected 1 call, got %v", inp.Calls)
	}
	if inp.Calls[0] != "scroll-down:5" {
		t.Errorf("call = %q, want scroll-down:5", inp.Calls[0])
	}
}

func TestInputBundleScroll_DyNegative(t *testing.T) {
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)

	if err := pf.Input.ScrollUp(3); err != nil {
		t.Fatalf("ScrollUp: %v", err)
	}

	if len(inp.Calls) != 1 {
		t.Fatalf("expected 1 call, got %v", inp.Calls)
	}
	if inp.Calls[0] != "scroll-up:3" {
		t.Errorf("call = %q, want scroll-up:3", inp.Calls[0])
	}
}

func TestInputBundleClickCenter(t *testing.T) {
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)

	// Rect from (10,20) to (30,40) → center (20,30)
	if err := pf.Input.ClickCenter(image.Rect(10, 20, 30, 40)); err != nil {
		t.Fatalf("ClickCenter: %v", err)
	}

	if len(inp.Calls) != 1 {
		t.Fatalf("expected 1 call, got %v", inp.Calls)
	}
	if inp.Calls[0] != "click:20,30" {
		t.Errorf("call = %q, want click:20,30", inp.Calls[0])
	}
}

func TestInputBundleModifierDown(t *testing.T) {
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)

	// modifierDown is an alias for KeyDown.
	if err := pf.Input.KeyDown("ctrl"); err != nil {
		t.Fatalf("KeyDown: %v", err)
	}

	if len(inp.Calls) != 1 {
		t.Fatalf("expected 1 call, got %v", inp.Calls)
	}
	if inp.Calls[0] != "down:ctrl" {
		t.Errorf("call = %q, want down:ctrl", inp.Calls[0])
	}
}

func TestInputBundleModifierUp(t *testing.T) {
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)

	// modifierUp is an alias for KeyUp.
	if err := pf.Input.KeyUp("shift"); err != nil {
		t.Fatalf("KeyUp: %v", err)
	}

	if len(inp.Calls) != 1 {
		t.Fatalf("expected 1 call, got %v", inp.Calls)
	}
	if inp.Calls[0] != "up:shift" {
		t.Errorf("call = %q, want up:shift", inp.Calls[0])
	}
}

func TestInputBundleType_NilCheck(t *testing.T) {
	// Type with nil Inputter should return error.
	pf := pftest.New(nil, nil, nil, nil)
	err := pf.Input.Type("hello")
	if err == nil {
		t.Fatal("expected error for nil Inputter")
	}
	t.Logf("got expected error: %v", err)
}

func TestInputBundleDragAndDrop(t *testing.T) {
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)

	if err := pf.Input.DragAndDrop(10, 20, 30, 40); err != nil {
		t.Fatalf("DragAndDrop: %v", err)
	}

	// DragAndDrop: MouseMove(start), MouseDown, MouseMove(end), MouseUp
	want := []string{
		"move:10,20",
		"mousedown",
		"move:30,40",
		"mouseup",
	}
	if len(inp.Calls) != len(want) {
		t.Fatalf("unexpected call count: got %v want %v", inp.Calls, want)
	}
	for i, call := range want {
		if inp.Calls[i] != call {
			t.Fatalf("call %d = %q, want %q (all calls: %v)", i, inp.Calls[i], call, inp.Calls)
		}
	}
}
