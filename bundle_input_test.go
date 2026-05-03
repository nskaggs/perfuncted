package perfuncted_test

import (
	"context"
	"image"
	"testing"

	"github.com/nskaggs/perfuncted/pftest"
)

func TestInputBundleTypeWithDelayUsesKeyTaps(t *testing.T) {
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)

	if err := pf.Input.TypeWithDelay("A b\n\t", 0); err != nil {
		t.Fatalf("TypeWithDelay: %v", err)
	}

	want := []string{"tap:A", "tap:space", "tap:b", "tap:enter", "tap:tab"}
	if len(inp.Calls) != len(want) {
		t.Fatalf("unexpected call count: got %v want %v", inp.Calls, want)
	}
	for i, call := range want {
		if inp.Calls[i] != call {
			t.Fatalf("call %d = %q, want %q (all calls: %v)", i, inp.Calls[i], call, inp.Calls)
		}
	}
	if typed := inp.Typed(); typed != "" {
		t.Fatalf("TypeWithDelay should not use bulk Type, got typed text %q", typed)
	}
}

func TestPerfunctedTypeFast_ClipboardPath(t *testing.T) {
	inp := &pftest.Inputter{}
	cb := &pftest.Clipboard{}
	pf := pftest.New(nil, inp, nil, cb)

	// Perfuncted.TypeFast uses Clipboard.PasteWithInput internally.
	if err := pf.TypeFast("hello world"); err != nil {
		t.Fatalf("TypeFast: %v", err)
	}

	// Verify clipboard was set.
	if cb.Text != "hello world" {
		t.Errorf("clipboard text = %q, want %q", cb.Text, "hello world")
	}

	// Verify PressCombo was called with ctrl+v.
	found := false
	for _, call := range inp.Calls {
		if call == "combo:ctrl+v" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected combo:ctrl+v in calls: %v", inp.Calls)
	}
}

func TestPerfunctedTypeFast_FallbackToType(t *testing.T) {
	// When clipboard is nil, TypeFast should fall back to per-character Type.
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)

	if err := pf.TypeFast("abc"); err != nil {
		t.Fatalf("TypeFast: %v", err)
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

func TestPerfunctedTypeFast_ClipboardSetFails(t *testing.T) {
	// When clipboard.Set fails, Perfuncted.TypeFast returns the error
	// (no fallback — that's InputBundle.TypeFast's job).
	inp := &pftest.Inputter{}
	cb := &pftest.Clipboard{SetErr: context.DeadlineExceeded}
	pf := pftest.New(nil, inp, nil, cb)

	err := pf.TypeFast("fallback")
	if err == nil {
		t.Fatal("expected error when clipboard.Set fails")
	}
	t.Logf("got expected error: %v", err)
}

func TestInputBundleTypeFast_NoClipboardAvailable(t *testing.T) {
	// InputBundle.TypeFast calls clipboard.Open() which may fail.
	// When it fails, it falls back to Inputter.Type().
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)

	// This tests the InputBundle.TypeFast path via Perfuncted.
	// clipboard.Open() will likely fail in test env, triggering fallback.
	err := pf.Input.TypeFast("test")
	if err != nil {
		t.Logf("TypeFast error: %v", err)
	} else {
		t.Log("TypeFast succeeded (clipboard available)")
	}
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

	if err := pf.Input.Scroll(3, 0); err != nil {
		t.Fatalf("Scroll: %v", err)
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

	if err := pf.Input.Scroll(-2, 0); err != nil {
		t.Fatalf("Scroll: %v", err)
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

	if err := pf.Input.Scroll(0, 5); err != nil {
		t.Fatalf("Scroll: %v", err)
	}

	if len(inp.Calls) != 1 {
		t.Fatalf("expected 1 call, got %v", inp.Calls)
	}
	// dy > 0 maps to ScrollDown (positive = scroll down)
	if inp.Calls[0] != "scroll-down:5" {
		t.Errorf("call = %q, want scroll-down:5", inp.Calls[0])
	}
}

func TestInputBundleScroll_DyNegative(t *testing.T) {
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)

	if err := pf.Input.Scroll(0, -3); err != nil {
		t.Fatalf("Scroll: %v", err)
	}

	if len(inp.Calls) != 1 {
		t.Fatalf("expected 1 call, got %v", inp.Calls)
	}
	// Note: dy < 0 maps to ScrollUp (negative = scroll up)
	if inp.Calls[0] != "scroll-up:3" {
		t.Errorf("call = %q, want scroll-up:3", inp.Calls[0])
	}
}

func TestInputBundleScroll_Zero(t *testing.T) {
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)

	if err := pf.Input.Scroll(0, 0); err != nil {
		t.Fatalf("Scroll: %v", err)
	}

	if len(inp.Calls) != 0 {
		t.Fatalf("expected 0 calls for zero scroll, got %v", inp.Calls)
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

func TestInputBundleClickCenter_OddSize(t *testing.T) {
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)

	// Rect from (0,0) to (5,5) → center (2,2) (integer division)
	if err := pf.Input.ClickCenter(image.Rect(0, 0, 5, 5)); err != nil {
		t.Fatalf("ClickCenter: %v", err)
	}

	if len(inp.Calls) != 1 {
		t.Fatalf("expected 1 call, got %v", inp.Calls)
	}
	if inp.Calls[0] != "click:2,2" {
		t.Errorf("call = %q, want click:2,2", inp.Calls[0])
	}
}

func TestInputBundleModifierDown(t *testing.T) {
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)

	if err := pf.Input.ModifierDown("ctrl"); err != nil {
		t.Fatalf("ModifierDown: %v", err)
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

	if err := pf.Input.ModifierUp("shift"); err != nil {
		t.Fatalf("ModifierUp: %v", err)
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
