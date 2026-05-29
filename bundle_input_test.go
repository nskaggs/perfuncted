package perfuncted_test

import (
	"context"
	"errors"
	"fmt"
	"image"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

type inputPointerSyncSpy struct {
	pftest.Inputter
	pointerCalls int
	syncCalls    int
}

func (s *inputPointerSyncSpy) PointerLocation(context.Context) (int, int, error) {
	s.pointerCalls++
	return 42, 24, nil
}

func (s *inputPointerSyncSpy) Sync(context.Context) error {
	s.syncCalls++
	return nil
}

type windowSpyManager struct {
	pftest.Manager
	moveCalls  []string
	closeCalls []string
	syncCalls  int
}

func (m *windowSpyManager) Move(ctx context.Context, title string, x, y int) error {
	m.moveCalls = append(m.moveCalls, fmt.Sprintf("%s:%d,%d", title, x, y))
	return nil
}

func (m *windowSpyManager) CloseWindow(ctx context.Context, title string) error {
	m.closeCalls = append(m.closeCalls, title)
	return nil
}

func (m *windowSpyManager) Sync(ctx context.Context) error {
	m.syncCalls++
	return nil
}

func TestPerfunctedPaste_ClipboardPath(t *testing.T) {
	inp := &pftest.Inputter{}
	cb := &pftest.Clipboard{}
	pf := pftest.New(nil, inp, nil, cb)
	ctx := context.Background()

	// Perfuncted.Paste uses clipboard+PasteCombo when a clipboard is available.
	if err := pf.Paste(ctx, "hello world"); err != nil {
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
	ctx := context.Background()

	if err := pf.Paste(ctx, "abc"); err != nil {
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
	ctx := context.Background()

	err := pf.Paste(ctx, "fallback")
	if err == nil {
		t.Fatal("expected error when clipboard.Set fails")
	}
	t.Logf("got expected error: %v", err)
}

func TestInputBundleDoubleClick(t *testing.T) {
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)
	ctx := context.Background()

	if err := pf.Input.DoubleClick(ctx, 50, 75); err != nil {
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
	ctx := context.Background()

	if err := pf.Input.ScrollRight(ctx, 3); err != nil {
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
	ctx := context.Background()

	if err := pf.Input.ScrollLeft(ctx, 2); err != nil {
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
	ctx := context.Background()

	if err := pf.Input.ScrollDown(ctx, 5); err != nil {
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
	ctx := context.Background()

	if err := pf.Input.ScrollUp(ctx, 3); err != nil {
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
	ctx := context.Background()

	// Rect from (10,20) to (30,40) → center (20,30)
	if err := pf.Input.ClickCenter(ctx, image.Rect(10, 20, 30, 40)); err != nil {
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
	ctx := context.Background()

	if err := pf.Input.ModifierDown(ctx, "ctrl"); err != nil {
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
	ctx := context.Background()

	if err := pf.Input.ModifierUp(ctx, "shift"); err != nil {
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
	err := pf.Input.Type(context.Background(), "hello")
	if err == nil {
		t.Fatal("expected error for nil Inputter")
	}
	t.Logf("got expected error: %v", err)
}

func TestInputBundleDragAndDrop(t *testing.T) {
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)
	ctx := context.Background()

	if err := pf.Input.DragAndDrop(ctx, 10, 20, 30, 40); err != nil {
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

func TestInputBundleDoubleClickNilContext(t *testing.T) {
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)

	//lint:ignore SA1012 regression test for nil-context handling
	if err := pf.Input.DoubleClick(nil, 50, 75); err != nil {
		t.Fatalf("DoubleClick(nil): %v", err)
	}
}

func TestInputBundleDragAndDropNilContext(t *testing.T) {
	boom := errors.New("drag failed")
	inp := &pftest.Inputter{Err: boom}
	pf := pftest.New(nil, inp, nil, nil)

	//lint:ignore SA1012 regression test for nil-context handling
	err := pf.Input.DragAndDrop(nil, 10, 20, 30, 40)
	if !errors.Is(err, boom) {
		t.Fatalf("DragAndDrop(nil) error = %v, want %v", err, boom)
	}
}

func TestInputBundleMouseMoveDownUpPointerLocationAndSync(t *testing.T) {
	spy := &inputPointerSyncSpy{}
	pf := pftest.New(nil, spy, nil, nil)
	ctx := context.Background()

	if err := pf.Input.MouseMove(ctx, 10, 20); err != nil {
		t.Fatalf("MouseMove: %v", err)
	}
	if err := pf.Input.MouseDown(ctx, 1); err != nil {
		t.Fatalf("MouseDown: %v", err)
	}
	if err := pf.Input.MouseUp(ctx, 1); err != nil {
		t.Fatalf("MouseUp: %v", err)
	}
	x, y, err := pf.Input.PointerLocation(ctx)
	if err != nil {
		t.Fatalf("PointerLocation: %v", err)
	}
	if err := pf.Input.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	wantCalls := []string{"move:10,20", "mousedown", "mouseup"}
	if len(spy.Calls) != len(wantCalls) {
		t.Fatalf("calls = %v, want %v", spy.Calls, wantCalls)
	}
	for i, want := range wantCalls {
		if spy.Calls[i] != want {
			t.Fatalf("call %d = %q, want %q", i, spy.Calls[i], want)
		}
	}
	if x != 42 || y != 24 {
		t.Fatalf("PointerLocation = (%d,%d), want (42,24)", x, y)
	}
	if spy.pointerCalls != 1 || spy.syncCalls != 1 {
		t.Fatalf("pointerCalls=%d syncCalls=%d, want 1/1", spy.pointerCalls, spy.syncCalls)
	}
}

func TestWindowBundleActiveTitleMoveCloseSyncAndWaiters(t *testing.T) {
	mgr := &windowSpyManager{
		Manager: pftest.Manager{
			Titles: []string{"Firefox"},
			Lists: [][]window.Info{
				{{ID: 1, Title: "Firefox"}},
				{},
			},
		},
	}
	pf := pftest.New(nil, nil, mgr, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	title, err := pf.Window.ActiveTitle(ctx)
	if err != nil {
		t.Fatalf("ActiveTitle: %v", err)
	}
	if title != "Firefox" {
		t.Fatalf("ActiveTitle = %q, want Firefox", title)
	}
	win, err := pf.Window.WaitForWindow(ctx, "Firefox", time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForWindow: %v", err)
	}
	if win.Title != "Firefox" {
		t.Fatalf("WaitForWindow title = %q, want Firefox", win.Title)
	}
	if err := pf.Window.WaitForClose(ctx, "Firefox", time.Millisecond); err != nil {
		t.Fatalf("WaitForClose: %v", err)
	}
	if err := pf.Window.Move(ctx, "Firefox", 10, 20); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if err := pf.Window.CloseWindow(ctx, "Firefox"); err != nil {
		t.Fatalf("CloseWindow: %v", err)
	}
	if err := pf.Window.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(mgr.moveCalls) != 1 || mgr.moveCalls[0] != "Firefox:10,20" {
		t.Fatalf("moveCalls = %v, want [Firefox:10,20]", mgr.moveCalls)
	}
	if len(mgr.closeCalls) != 1 || mgr.closeCalls[0] != "Firefox" {
		t.Fatalf("closeCalls = %v, want [Firefox]", mgr.closeCalls)
	}
	if mgr.syncCalls != 1 {
		t.Fatalf("syncCalls = %d, want 1", mgr.syncCalls)
	}
}
