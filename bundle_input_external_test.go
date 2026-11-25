package perfuncted_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
	"image"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

type doubleClickDualFailureBackend struct {
	pftest.Inputter
	pressError   error
	releaseError error
}

func (b *doubleClickDualFailureBackend) MouseDown(ctx context.Context, button int) error {
	_ = b.Inputter.MouseDown(ctx, button)
	return b.pressError
}
func (b *doubleClickDualFailureBackend) MouseUp(ctx context.Context, button int) error {
	_ = b.Inputter.MouseUp(ctx, button)
	return b.releaseError
}

// A failed press still requires a cleanup release, and neither failure may
// hide the other in the receipt returned to the caller.
func TestDoubleClickJoinsPressAndCleanupReleaseErrors(t *testing.T) {
	pressError := errors.New("synthetic click press failed")
	releaseError := errors.New("synthetic click release cleanup failed")
	backend := &doubleClickDualFailureBackend{pressError: pressError, releaseError: releaseError}
	session := perfuncted.NewSessionForTesting(nil, backend, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	err := session.Input.DoubleClick(context.Background(), 5, 7)
	expected := []string{"move:5,7", "mousedown", "mouseup"}
	if !errors.Is(err, pressError) || !errors.Is(err, releaseError) || !reflect.DeepEqual(backend.Calls, expected) {
		t.Fatalf("double-click error=%v calls=%v, expected both errors and %v", err, backend.Calls, expected)
	}
}

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

type revokeDuringDoubleClickInput struct {
	pftest.Inputter
	on       string
	after    func()
	releases int
}

func (b *revokeDuringDoubleClickInput) MouseMove(ctx context.Context, x, y int) error {
	err := b.Inputter.MouseMove(ctx, x, y)
	if b.on == "initial-move" {
		b.after()
	}
	return err
}
func (b *revokeDuringDoubleClickInput) MouseUp(ctx context.Context, button int) error {
	b.releases++
	err := b.Inputter.MouseUp(ctx, button)
	if b.on == "final-release" && b.releases == 2 {
		b.after()
	}
	return err
}

// A non-cooperative provider cannot turn a revoked gesture into success.
// Early revocation must not start a button press; late revocation must not
// acknowledge a successful double-click after its final release.
func TestDoubleClickRejectsAuthorityLossAtGestureBoundaries(t *testing.T) {
	for _, phase := range []string{"initial-move", "final-release"} {
		for _, mode := range []string{"cancel", "close"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				backend := &revokeDuringDoubleClickInput{on: phase}
				session := perfuncted.NewSessionForTesting(nil, backend, nil, nil, nil)
				t.Cleanup(func() { _ = session.Close() })
				wantErr := context.Canceled
				if mode == "cancel" {
					backend.after = cancel
				} else {
					wantErr = perfuncted.ErrSessionClosed
					backend.after = func() { _ = session.Close() }
				}
				err := session.Input.DoubleClick(ctx, 14, 23)
				want := []string{"move:14,23"}
				if phase == "final-release" {
					want = append(want, "mousedown", "mouseup", "mousedown", "mouseup")
				}
				if !errors.Is(err, wantErr) || !reflect.DeepEqual(backend.Calls, want) {
					t.Fatalf("double-click after %s/%s: err=%v calls=%v, want error=%v calls=%v", phase, mode, err, backend.Calls, wantErr, want)
				}
			})
		}
	}
}

type closeAfterFirstClickInput struct {
	pftest.Inputter
	after    func()
	releases int
}

func (b *closeAfterFirstClickInput) MouseUp(ctx context.Context, button int) error {
	b.releases++
	err := b.Inputter.MouseUp(ctx, button)
	if b.releases == 1 && b.after != nil {
		b.after()
	}
	return err
}

// A completed first click cannot authorize a second click once the session
// closes. The backend's Close may not cancel the caller context.
func TestDoubleClickDoesNotStartSecondClickAfterSessionCloses(t *testing.T) {
	backend := &closeAfterFirstClickInput{}
	session := perfuncted.NewSessionForTesting(nil, backend, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	backend.after = func() { _ = session.Close() }
	err := session.Input.DoubleClick(context.Background(), 14, 23)
	want := []string{"move:14,23", "mousedown", "mouseup"}
	if !errors.Is(err, perfuncted.ErrSessionClosed) || !reflect.DeepEqual(backend.Calls, want) {
		t.Fatalf("double click after session closure: err=%v calls=%v want=%v", err, backend.Calls, want)
	}
}

type revokeAfterDragMoveInput struct {
	pftest.Inputter
	after     func()
	moveCalls int
}

func (b *revokeAfterDragMoveInput) MouseMove(ctx context.Context, x, y int) error {
	b.moveCalls++
	err := b.Inputter.MouseMove(ctx, x, y)
	if b.moveCalls == 1 && b.after != nil {
		b.after()
	}
	return err
}

// A successful first pointer move can complete after its request was revoked.
// Do not start a button hold or issue a release for a button never pressed.
func TestDragDoesNotPressAfterFirstPointerMoveLosesAuthority(t *testing.T) {
	for _, mode := range []string{"cancel", "close"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend := &revokeAfterDragMoveInput{}
			session := perfuncted.NewSessionForTesting(nil, backend, nil, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			wantErr := context.Canceled
			if mode == "cancel" {
				backend.after = cancel
			} else {
				wantErr = perfuncted.ErrSessionClosed
				backend.after = func() { _ = session.Close() }
			}
			err := session.Input.DragAndDrop(ctx, 10, 20, 30, 40)
			wantCalls := []string{"move:10,20"}
			if !errors.Is(err, wantErr) || !reflect.DeepEqual(backend.Calls, wantCalls) {
				t.Fatalf("drag after authority loss: err=%v calls=%v, want error %v and calls %v", err, backend.Calls, wantErr, wantCalls)
			}
		})
	}
}

type revokeLateDragInput struct {
	pftest.Inputter
	after    func()
	on       string
	moves    int
	releases int
}

func (b *revokeLateDragInput) MouseMove(ctx context.Context, x, y int) error {
	b.moves++
	err := b.Inputter.MouseMove(ctx, x, y)
	if b.on == "destination" && b.moves == 2 {
		b.after()
	}
	return err
}
func (b *revokeLateDragInput) MouseUp(ctx context.Context, button int) error {
	b.releases++
	err := b.Inputter.MouseUp(ctx, button)
	if b.on == "release" && b.releases == 1 {
		b.after()
	}
	return err
}

// Once the mouse button is pressed, a late revoked destination move still
// needs a best-effort release, but cannot be reported as a successful drag.
// Revocation during the final release likewise cannot certify success.
func TestDragReportsAuthorityLossDuringHeldGesture(t *testing.T) {
	for _, phase := range []string{"destination", "release"} {
		for _, mode := range []string{"cancel", "close"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				backend := &revokeLateDragInput{on: phase}
				session := perfuncted.NewSessionForTesting(nil, backend, nil, nil, nil)
				t.Cleanup(func() { _ = session.Close() })
				expected := context.Canceled
				if mode == "cancel" {
					backend.after = cancel
				} else {
					expected = perfuncted.ErrSessionClosed
					backend.after = func() { _ = session.Close() }
				}
				err := session.Input.DragAndDrop(ctx, 11, 22, 33, 44)
				want := []string{"move:11,22", "mousedown", "move:33,44", "mouseup"}
				if !errors.Is(err, expected) || !reflect.DeepEqual(backend.Calls, want) {
					t.Fatalf("revoked drag: err=%v calls=%v expected error %v, calls %v", err, backend.Calls, expected, want)
				}
			})
		}
	}
}

type revokeDuringDragValidation struct {
	pftest.Inputter
	checked int
	revoke  func()
}

func (b *revokeDuringDragValidation) ValidatePointerTarget(context.Context, int, int) error {
	b.checked++
	if b.checked == 2 && b.revoke != nil {
		b.revoke()
	}
	return nil
}

// Destination validation is the last non-mutating stage of a drag. Losing
// authority there must prevent *all* synthetic input from being injected.
func TestDragRejectsAuthorizationLossAfterDestinationValidation(t *testing.T) {
	for _, mode := range []string{"cancel", "close"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend := &revokeDuringDragValidation{}
			session := perfuncted.NewSessionForTesting(nil, backend, nil, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			expected := context.Canceled
			if mode == "cancel" {
				backend.revoke = cancel
			} else {
				expected = perfuncted.ErrSessionClosed
				backend.revoke = func() { _ = session.Close() }
			}
			err := session.Input.DragAndDrop(ctx, 20, 20, 40, 40)
			if !errors.Is(err, expected) || backend.checked != 2 || len(backend.Calls) != 0 {
				t.Fatalf("drag after %s: err=%v validations=%d injected=%v", mode, err, backend.checked, backend.Calls)
			}
		})
	}
}

type singleActionAuthorityBackend struct {
	pftest.Inputter
	after func()
	calls int
}

func (b *singleActionAuthorityBackend) dispatch() error {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return nil
}
func (b *singleActionAuthorityBackend) KeyDown(context.Context, string) error { return b.dispatch() }
func (b *singleActionAuthorityBackend) KeyUp(context.Context, string) error   { return b.dispatch() }
func (b *singleActionAuthorityBackend) Type(context.Context, string) error    { return b.dispatch() }
func (b *singleActionAuthorityBackend) TypeLiteral(context.Context, string) error {
	return b.dispatch()
}
func (b *singleActionAuthorityBackend) MouseMove(context.Context, int, int) error {
	return b.dispatch()
}
func (b *singleActionAuthorityBackend) MouseClick(context.Context, int, int, int) error {
	return b.dispatch()
}
func (b *singleActionAuthorityBackend) MouseDown(context.Context, int) error   { return b.dispatch() }
func (b *singleActionAuthorityBackend) MouseUp(context.Context, int) error     { return b.dispatch() }
func (b *singleActionAuthorityBackend) ScrollUp(context.Context, int) error    { return b.dispatch() }
func (b *singleActionAuthorityBackend) ScrollDown(context.Context, int) error  { return b.dispatch() }
func (b *singleActionAuthorityBackend) ScrollLeft(context.Context, int) error  { return b.dispatch() }
func (b *singleActionAuthorityBackend) ScrollRight(context.Context, int) error { return b.dispatch() }

// A non-cooperative input provider must not receive pre-canceled events or
// cause the facade to acknowledge completion after the caller or session
// revokes authority. All individual injection methods share this contract.
func TestSingleInputActionsRejectAuthorizationLoss(t *testing.T) {
	actions := []struct {
		name string
		run  func(*perfuncted.Session, context.Context) error
	}{
		{"key-down", func(s *perfuncted.Session, c context.Context) error { return s.Input.KeyDown(c, "a") }},
		{"key-up", func(s *perfuncted.Session, c context.Context) error { return s.Input.KeyUp(c, "a") }},
		{"type", func(s *perfuncted.Session, c context.Context) error { return s.Input.Type(c, "hello") }},
		{"type-literal", func(s *perfuncted.Session, c context.Context) error { return s.Input.TypeLiteral(c, "hello") }},
		{"mouse-move", func(s *perfuncted.Session, c context.Context) error { return s.Input.MouseMove(c, 1, 2) }},
		{"mouse-click", func(s *perfuncted.Session, c context.Context) error { return s.Input.MouseClick(c, 1, 2, 1) }},
		{"mouse-down", func(s *perfuncted.Session, c context.Context) error { return s.Input.MouseDown(c, 1) }},
		{"mouse-up", func(s *perfuncted.Session, c context.Context) error { return s.Input.MouseUp(c, 1) }},
		{"scroll-up", func(s *perfuncted.Session, c context.Context) error { return s.Input.ScrollUp(c, 1) }},
		{"scroll-down", func(s *perfuncted.Session, c context.Context) error { return s.Input.ScrollDown(c, 1) }},
		{"scroll-left", func(s *perfuncted.Session, c context.Context) error { return s.Input.ScrollLeft(c, 1) }},
		{"scroll-right", func(s *perfuncted.Session, c context.Context) error { return s.Input.ScrollRight(c, 1) }},
	}
	for _, action := range actions {
		for _, mode := range []string{"pre-canceled", "late-canceled", "session-closed"} {
			t.Run(action.name+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				backend := &singleActionAuthorityBackend{}
				session := perfuncted.NewSessionForTesting(nil, backend, nil, nil, nil)
				t.Cleanup(func() { _ = session.Close() })
				expected := context.Canceled
				switch mode {
				case "pre-canceled":
					cancel()
				case "late-canceled":
					backend.after = cancel
				case "session-closed":
					expected = perfuncted.ErrSessionClosed
					backend.after = func() { _ = session.Close() }
				}
				err := action.run(session, ctx)
				calls := 1
				if mode == "pre-canceled" {
					calls = 0
				}
				if !errors.Is(err, expected) || backend.calls != calls {
					t.Fatalf("%s/%s: err=%v backend calls=%d, expected error=%v calls=%d", action.name, mode, err, backend.calls, expected, calls)
				}
			})
		}
	}
}

type authorityLossSyncBackend struct {
	pftest.Inputter
	after   func()
	calls   int
	failure error
}

func (b *authorityLossSyncBackend) Sync(context.Context) error {
	b.calls++
	if b.after != nil {
		b.after()
	}
	return b.failure
}

// An acknowledged sync cannot certify pending desktop input after its caller
// canceled or the owner closed. A pre-canceled request must not dispatch.
func TestInputSyncRejectsPreflightAndLateAuthorityLoss(t *testing.T) {
	for _, mode := range []string{"pre-cancel", "late-cancel", "session-close"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend := &authorityLossSyncBackend{}
			session := perfuncted.NewSessionForTesting(nil, backend, nil, nil, nil)
			t.Cleanup(func() { _ = session.Close() })
			wantErr := context.Canceled
			if mode == "pre-cancel" {
				cancel()
			} else if mode == "late-cancel" {
				backend.after = cancel
			} else {
				wantErr = perfuncted.ErrSessionClosed
				backend.after = func() { _ = session.Close() }
			}
			err := session.Input.Sync(ctx)
			wantCalls := 1
			if mode == "pre-cancel" {
				wantCalls = 0
			}
			if !errors.Is(err, wantErr) || backend.calls != wantCalls {
				t.Fatalf("Sync after %s: err=%v calls=%d want err=%v calls=%d", mode, err, backend.calls, wantErr, wantCalls)
			}
		})
	}
}

type inputPointerSyncSpy struct {
	pftest.Inputter
	pointerCalls int
	syncCalls    int
}

type dragCleanupInput struct {
	pftest.Inputter
	moveCalls       int
	mouseDownCalls  int
	mouseUpCalls    int
	moveErr         error
	mouseDownErr    error
	mouseDownFailAt int
	mouseUpErr      error
}

func (s *dragCleanupInput) MouseMove(ctx context.Context, x, y int) error {
	s.moveCalls++
	if s.moveCalls == 2 {
		return s.moveErr
	}
	return s.Inputter.MouseMove(ctx, x, y)
}

func (s *dragCleanupInput) MouseUp(ctx context.Context, button int) error {
	s.mouseUpCalls++
	_ = s.Inputter.MouseUp(ctx, button)
	return s.mouseUpErr
}

func (s *dragCleanupInput) MouseDown(ctx context.Context, button int) error {
	s.mouseDownCalls++
	_ = s.Inputter.MouseDown(ctx, button)
	if s.mouseDownErr != nil && (s.mouseDownFailAt == 0 || s.mouseDownCalls == s.mouseDownFailAt) {
		return s.mouseDownErr
	}
	return nil
}

type releaseRetryInput struct {
	pftest.Inputter
	mouseUpCalls int
	failAt       int
	releaseErr   error
}

func (s *releaseRetryInput) MouseUp(ctx context.Context, button int) error {
	s.mouseUpCalls++
	_ = s.Inputter.MouseUp(ctx, button)
	if s.mouseUpCalls == s.failAt {
		return s.releaseErr
	}
	return nil
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

func (m *windowSpyManager) MoveByID(
	ctx context.Context,
	id string,
	x int,
	y int,
) error {
	m.moveCalls = append(m.moveCalls, fmt.Sprintf("%s:%d,%d", id, x, y))
	return nil
}

func (m *windowSpyManager) CloseWindow(ctx context.Context, title string) error {
	m.closeCalls = append(m.closeCalls, title)
	return nil
}

func (m *windowSpyManager) CloseWindowByID(
	ctx context.Context,
	id string,
) error {
	m.closeCalls = append(m.closeCalls, id)
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
	if !slices.Contains(inp.Calls, "type:{ctrl+v}") {
		t.Errorf("expected type:{ctrl+v} in calls: %v", inp.Calls)
	}
}

func TestPerfunctedPaste_FallbackToType(t *testing.T) {
	// When clipboard is nil, Paste should fall back to literal typing.
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)
	ctx := context.Background()

	if err := pf.Paste(ctx, "abc"); err != nil {
		t.Fatalf("Paste: %v", err)
	}

	// Should have invoked the literal-typing path with the full string.
	if !slices.Contains(inp.Calls, "type-literal:abc") {
		t.Errorf("expected type-literal:abc in calls: %v", inp.Calls)
	}
}

func TestPerfunctedPaste_FallbackTypesArbitraryTextLiterally(t *testing.T) {
	// Arbitrary text that resembles key syntax (braces, modifiers) must
	// round-trip unchanged when the clipboard is unavailable. Previously the
	// fallback routed text through key-syntax parsing, which failed on braces.
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)
	ctx := context.Background()

	js := `(function(){const x = a && { b: 1, c: "}" } + "{" ; return x})()`
	if err := pf.Paste(ctx, js); err != nil {
		t.Fatalf("Paste with braces: %v", err)
	}

	if !slices.Contains(inp.Calls, "type-literal:"+js) {
		t.Errorf("expected literal paste in calls: %v", inp.Calls)
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

func TestInputBundleDoubleClickCleansUpAfterSecondMouseDownFailure(t *testing.T) {
	pressErr := errors.New("second press failed")
	inp := &dragCleanupInput{
		mouseDownErr:    pressErr,
		mouseDownFailAt: 2,
	}
	p := pftest.New(nil, inp, nil, nil)

	err := p.Input.DoubleClick(context.Background(), 1, 2)
	if !errors.Is(err, pressErr) {
		t.Fatalf("DoubleClick error = %v, want second press error", err)
	}
	if inp.mouseDownCalls != 2 {
		t.Fatalf("MouseDown calls = %d, want 2", inp.mouseDownCalls)
	}
	if inp.mouseUpCalls != 2 {
		t.Fatalf("MouseUp calls = %d, want first release and cleanup release", inp.mouseUpCalls)
	}
}

func TestInputBundleDoubleClickRetriesReleaseAfterReleaseFailure(t *testing.T) {
	releaseErr := errors.New("first release failed")
	inp := &releaseRetryInput{failAt: 1, releaseErr: releaseErr}
	p := pftest.New(nil, inp, nil, nil)

	err := p.Input.DoubleClick(context.Background(), 1, 2)
	if !errors.Is(err, releaseErr) {
		t.Fatalf("DoubleClick error = %v, want first release error", err)
	}
	if inp.mouseUpCalls != 2 {
		t.Fatalf("MouseUp calls = %d, want failed release plus cleanup retry", inp.mouseUpCalls)
	}
}

func TestInputBundleDoubleClickRetriesSecondReleaseAfterReleaseFailure(t *testing.T) {
	releaseErr := errors.New("second release failed")
	inp := &releaseRetryInput{failAt: 2, releaseErr: releaseErr}
	p := pftest.New(nil, inp, nil, nil)

	err := p.Input.DoubleClick(context.Background(), 1, 2)
	if !errors.Is(err, releaseErr) {
		t.Fatalf("DoubleClick error = %v, want second release error", err)
	}
	if inp.mouseUpCalls != 3 {
		t.Fatalf("MouseUp calls = %d, want two releases plus cleanup retry", inp.mouseUpCalls)
	}
}

func TestInputBundleDragAndDropReportsCleanupFailure(t *testing.T) {
	moveErr := errors.New("drag move failed")
	cleanupErr := errors.New("drag cleanup failed")
	inp := &dragCleanupInput{
		moveErr:    moveErr,
		mouseUpErr: cleanupErr,
	}
	pf := pftest.New(nil, inp, nil, nil)

	err := pf.Input.DragAndDrop(context.Background(), 1, 2, 3, 4)
	if !errors.Is(err, moveErr) {
		t.Fatalf("DragAndDrop error = %v, want move error", err)
	}
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("DragAndDrop error = %v, want cleanup error", err)
	}
	if inp.mouseUpCalls != 1 {
		t.Fatalf("cleanup MouseUp calls = %d, want 1", inp.mouseUpCalls)
	}
}

func TestInputBundleDragAndDropCleansUpAfterMouseDownFailure(t *testing.T) {
	downErr := errors.New("drag press failed")
	cleanupErr := errors.New("drag cleanup failed")
	inp := &dragCleanupInput{
		mouseDownErr: downErr,
		mouseUpErr:   cleanupErr,
	}
	pf := pftest.New(nil, inp, nil, nil)

	err := pf.Input.DragAndDrop(context.Background(), 1, 2, 3, 4)
	if !errors.Is(err, downErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("DragAndDrop error = %v, want press and cleanup errors", err)
	}
	if inp.mouseUpCalls != 1 {
		t.Fatalf("cleanup MouseUp calls = %d, want 1", inp.mouseUpCalls)
	}
}

func TestInputBundleDragAndDropRetriesReleaseAfterReleaseFailure(t *testing.T) {
	releaseErr := errors.New("first release failed")
	inp := &releaseRetryInput{failAt: 1, releaseErr: releaseErr}
	p := pftest.New(nil, inp, nil, nil)

	err := p.Input.DragAndDrop(context.Background(), 1, 2, 3, 4)
	if !errors.Is(err, releaseErr) {
		t.Fatalf("DragAndDrop error = %v, want first release error", err)
	}
	if inp.mouseUpCalls != 2 {
		t.Fatalf("MouseUp calls = %d, want failed release plus cleanup retry", inp.mouseUpCalls)
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

	if err := pf.Input.DoubleClick(context.TODO(), 50, 75); err != nil {
		t.Fatalf("DoubleClick(nil): %v", err)
	}
}

func TestInputBundleDragAndDropNilContext(t *testing.T) {
	boom := errors.New("drag failed")
	inp := &pftest.Inputter{Err: boom}
	pf := pftest.New(nil, inp, nil, nil)

	err := pf.Input.DragAndDrop(context.TODO(), 10, 20, 30, 40)
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

	title, err := pf.Windows.ActiveTitle(ctx)
	if err != nil {
		t.Fatalf("ActiveTitle: %v", err)
	}
	if title != "Firefox" {
		t.Fatalf("ActiveTitle = %q, want Firefox", title)
	}
	target, err := pf.Windows.Find(
		ctx,
		perfuncted.WindowMatch{TitleContains: "Firefox"},
	)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if err := target.WaitClosed(ctx); err != nil {
		t.Fatalf("WaitForClose: %v", err)
	}
	if err := target.Move(ctx, 10, 20); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if err := target.Close(ctx); err != nil {
		t.Fatalf("CloseWindow: %v", err)
	}
	if err := pf.Windows.Sync(ctx); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if len(mgr.moveCalls) != 1 || mgr.moveCalls[0] != "1:10,20" {
		t.Fatalf("moveCalls = %v, want [1:10,20]", mgr.moveCalls)
	}
	if len(mgr.closeCalls) != 1 || mgr.closeCalls[0] != "1" {
		t.Fatalf("closeCalls = %v, want [1]", mgr.closeCalls)
	}
	if mgr.syncCalls != 1 {
		t.Fatalf("syncCalls = %d, want 1", mgr.syncCalls)
	}
}

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
