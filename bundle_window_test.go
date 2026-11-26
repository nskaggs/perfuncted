package perfuncted

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2/accessibility"
	"github.com/nskaggs/perfuncted/v2/window"
	"testing"
	"time"
)

func TestAdversarialResolveManagedWindowIsSingleAuthority(t *testing.T) {
	manager := &accessibilityWindowManager{windows: []window.Info{
		{NativeID: "one", Title: "Firefox", PID: 11},
		{NativeID: "two", Title: "Firefox", PID: 22},
	}}
	fake := &bundleAccessibilityFake{apps: []accessibility.Application{{Node: accessibility.Node{Name: "Firefox"}, PID: 11}}, gen: 1}
	session := newSessionForTesting(nil, nil, manager, nil, nil, fake)
	defer session.Close()
	if _, err := session.accessibility.WindowRoot(context.Background(), "one"); err != nil {
		t.Fatalf("WindowRoot one: %v", err)
	}
	if _, err := session.accessibility.FindApplication(context.Background(), accessibility.ApplicationFilter{WindowTitle: "Firefox"}); !errors.Is(err, accessibility.ErrAmbiguous) {
		t.Fatalf("shared title via FindApplication = %v, want ambiguous from the same resolver", err)
	}
}

func TestAdversarialSetTextSelectionRequiresExplicitSelection(t *testing.T) {
	spy := &accessibilityAutomationSpy{}
	fake := &accessibilityAutomationFake{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 3}, accessibilityAutomationSpy: spy}
	session := newSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	id := accessibility.NodeID{BusName: "org.test", ObjectPath: "/entry", Generation: 3}
	if err := session.accessibility.SetTextSelection(context.Background(), id, 2, 4, 9); err != nil {
		t.Fatalf("SetTextSelection: %v", err)
	}
	if len(spy.calls) != 1 || spy.calls[0] != "selection" {
		t.Fatalf("calls = %v, want one explicit selection call", spy.calls)
	}
}

// TestAdversarialBundleWrappersCoverAutomationSurface exercises every typed
// bundle wrapper once. A typo in a wrapper's gated operation name would
// otherwise surface only as ErrUnsupported on a live bus.
func TestAdversarialBundleWrappersCoverAutomationSurface(t *testing.T) {
	spy := &accessibilityAutomationSpy{}
	fake := &accessibilityAutomationFake{bundleAccessibilityFake: &bundleAccessibilityFake{gen: 5}, accessibilityAutomationSpy: spy}
	session := newSessionForTesting(nil, nil, nil, nil, nil, fake)
	defer session.Close()
	ctx := context.Background()
	id := accessibility.NodeID{BusName: "org.test", ObjectPath: "/node", Generation: 5}
	bundle := session.accessibility
	calls := []struct {
		name string
		call func() error
	}{
		{"action", func() error { return bundle.InvokeAction(ctx, id, 0) }},
		{"action-name", func() error { _, err := bundle.InvokeActionByName(ctx, id, "press"); return err }},
		{"default-action", func() error { _, err := bundle.InvokeDefaultAction(ctx, id); return err }},
		{"focus", func() error { return bundle.FocusNode(ctx, id) }},
		{"scroll", func() error { return bundle.ScrollTo(ctx, id, accessibility.ScrollAnyWhere) }},
		{"scroll-point", func() error { return bundle.ScrollToPoint(ctx, id, accessibility.CoordTypeScreen, 1, 2) }},
		{"set-position", func() error { return bundle.SetPosition(ctx, id, 1, 2, accessibility.CoordTypeScreen) }},
		{"set-size", func() error { return bundle.SetSize(ctx, id, 3, 4) }},
		{"set-extents", func() error { return bundle.SetExtents(ctx, id, 1, 2, 3, 4, accessibility.CoordTypeScreen) }},
		{"set-value", func() error { return bundle.SetValue(ctx, id, 0.5) }},
		{"text", func() error { return bundle.ReplaceEditableText(ctx, id, "x") }},
		{"insert", func() error { return bundle.InsertText(ctx, id, 0, "x") }},
		{"delete", func() error { return bundle.DeleteText(ctx, id, 0, 1) }},
		{"copy", func() error { return bundle.CopyText(ctx, id, 0, 1) }},
		{"cut", func() error { return bundle.CutText(ctx, id, 0, 1) }},
		{"paste", func() error { return bundle.PasteText(ctx, id, 0) }},
		{"caret", func() error { return bundle.SetCaretOffset(ctx, id, 1) }},
		{"selection", func() error { return bundle.SetTextSelection(ctx, id, 0, 0, 1) }},
		{"add-selection", func() error { return bundle.AddTextSelection(ctx, id, 0, 1) }},
		{"remove-selection", func() error { return bundle.RemoveTextSelection(ctx, id, 0) }},
		{"document-selections", func() error { return bundle.SetTextSelections(ctx, id, nil) }},
		{"select-child", func() error { return bundle.SelectChild(ctx, id, 0) }},
		{"deselect-child", func() error { return bundle.DeselectChild(ctx, id, 0) }},
		{"select-all", func() error { return bundle.SelectAll(ctx, id) }},
		{"clear-selection", func() error { return bundle.ClearSelection(ctx, id) }},
		{"deselect-selected-child", func() error { return bundle.DeselectSelectedChild(ctx, id) }},
		{"select-row", func() error { return bundle.SelectRow(ctx, id, 0) }},
		{"deselect-row", func() error { return bundle.DeselectRow(ctx, id, 0) }},
		{"select-column", func() error { return bundle.SelectColumn(ctx, id, 0) }},
		{"deselect-column", func() error { return bundle.DeselectColumn(ctx, id, 0) }},
		{"scroll-view", func() error { return bundle.ScrollNodeIntoView(ctx, id) }},
	}
	for _, tc := range calls {
		if err := tc.call(); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
	}
	if len(spy.calls) != len(calls) {
		t.Fatalf("automation calls = %d, want %d (%v)", len(spy.calls), len(calls), spy.calls)
	}
}

func TestManagedWindowResolverRejectsMissingScopeFailuresAndAmbiguity(t *testing.T) {
	if selected, err := (*accessibilityState)(nil).resolveManagedWindow(context.Background(), "", "", "test"); selected != nil || !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil bundle resolution = %v, %v; want nil and ErrNilSession", selected, err)
	}
	if selected, err := (&accessibilityState{}).resolveManagedWindow(context.Background(), "", "", "test"); selected != nil || !errors.Is(err, accessibility.ErrUnsupported) {
		t.Fatalf("bundle without managed windows = %v, %v; want unsupported", selected, err)
	}

	manager := newHandleWindowManager(
		window.Info{NativeID: "one", Title: "Shared Editor"},
		window.Info{NativeID: "two", Title: "Shared Editor"},
	)
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	defer session.Close()
	selected, err := session.accessibility.resolveManagedWindow(context.Background(), "", " Shared Editor ", "test")
	if selected != nil || !errors.Is(err, accessibility.ErrAmbiguous) {
		t.Fatalf("ambiguous title selected=%v err=%v; want no target and ErrAmbiguous", selected, err)
	}

	// A valid native ID must not override contradictory title evidence.
	selected, err = session.accessibility.resolveManagedWindow(context.Background(), "one", "Other Person Private Window", "test")
	if selected != nil || !errors.Is(err, accessibility.ErrNotFound) {
		t.Fatalf("conflicting managed window title selected=%v err=%v; want no target and ErrNotFound", selected, err)
	}

	providerFailure := errors.New("synthetic compositor window listing failure")
	manager.setError(providerFailure)
	selected, err = session.accessibility.resolveManagedWindow(context.Background(), "one", "", "test")
	if selected != nil || !errors.Is(err, providerFailure) {
		t.Fatalf("window listing failure selected=%v err=%v; want wrapped provider error", selected, err)
	}
}

func TestWindowTargetUsesPIDHintOnlyWhenManagedMetadataOmitsPID(t *testing.T) {
	session := newSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()
	info := window.Info{NativeID: "managed-42", Title: "Editor", AppID: "org.example.Editor", X: 10, Y: 20, W: 300, H: 200, Active: true}
	selected := session.windows.window(info)
	target := windowTargetFor(selected, 42)
	if target.ID != "managed-42" || target.PID != 0 || target.PIDHint != 42 || target.Title != "Editor" || target.AppID != "org.example.Editor" || target.Bounds != (accessibility.Rect{X: 10, Y: 20, Width: 300, Height: 200}) || !target.Active {
		t.Fatalf("window correlation target lost managed metadata or PID hint: %+v", target)
	}

	info.PID = 84
	selected = session.windows.window(info)
	target = windowTargetFor(selected, 42)
	if target.PID != 84 || target.PIDHint != 0 {
		t.Fatalf("caller PID hint overrode compositor PID: %+v", target)
	}
}

type countingManagedWindowTitleBackend struct {
	*handleWindowManager
	calls int
}

func (b *countingManagedWindowTitleBackend) ActiveTitle(context.Context) (string, error) {
	b.calls++
	return "private title", nil
}

// A pre-canceled request must not trigger a window manager read: even a
// discarded reply can incur a compositor operation after authority expired.
func TestWindowBundleActiveTitlePreCanceledRequestDoesNotReadBackend(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := &countingManagedWindowTitleBackend{handleWindowManager: newHandleWindowManager()}
	session := newSessionForTesting(nil, nil, backend, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	title, err := session.windows.ActiveTitle(ctx)
	if !errors.Is(err, context.Canceled) || title != "" || backend.calls != 0 {
		t.Fatalf("canceled title read: title=%q err=%v backend calls=%d", title, err, backend.calls)
	}
}

// An operation name advertised by a compositor is not sufficient to grant a
// dispatch. The concrete optional interface must also exist. This matters
// when an out-of-date or partial backend reports capabilities it cannot serve.
type advertisedDesktopWithoutMethods struct{ *handleWindowManager }

func (*advertisedDesktopWithoutMethods) SupportedOperations() []string {
	return []string{"list-desktops", "switch-desktop", "set-window-desktop", "keep-above", "keep-below", "cursor-position", "shade-when-supported", "tile-window", "float-window"}
}

func TestWindowDesktopAdvertisedButUnimplementedOperationsFailClosed(t *testing.T) {
	backend := &advertisedDesktopWithoutMethods{handleWindowManager: newHandleWindowManager()}
	session := newSessionForTesting(nil, nil, backend, nil, nil)
	defer session.Close()
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"list desktops", func() error {
			got, err := session.windows.ListDesktops(ctx)
			if len(got) != 0 {
				t.Errorf("unimplemented list leaked %+v", got)
			}
			return err
		}},
		{"switch desktop", func() error { return session.windows.SwitchDesktop(ctx, "d-1") }},
		{"assign desktop", func() error { return session.windows.SetWindowDesktopByID(ctx, "w-1", "d-1") }},
		{"keep above", func() error { return session.windows.SetKeepAboveByID(ctx, "w-1", true) }},
		{"keep below", func() error { return session.windows.SetKeepBelowByID(ctx, "w-1", true) }},
		{"cursor position", func() error {
			x, y, err := session.windows.CursorPosition(ctx)
			if x != 0 || y != 0 {
				t.Errorf("unimplemented pointer leaked coordinates %d,%d", x, y)
			}
			return err
		}},
		{"shade", func() error { return session.windows.SetShadeByID(ctx, "w-1", true) }},
		{"tile", func() error { return session.windows.TileWindowByID(ctx, "w-1", 12, 34) }},
		{"float", func() error { return session.windows.FloatWindowByID(ctx, "w-1") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, window.ErrNotSupported) {
				t.Fatalf("unimplemented desktop operation accepted: %v", err)
			}
		})
	}
}

// Once a session closes, desktop operations must reject the caller before
// executing any compositor request, even when the backend advertises support.
func TestWindowDesktopClosedSessionRevokesAllOperations(t *testing.T) {
	backend := &countingDesktopBackend{handleWindowManager: newHandleWindowManager()}
	session := newSessionForTesting(nil, nil, backend, nil, nil)
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"list desktops", func() error {
			list, err := session.windows.ListDesktops(ctx)
			if len(list) != 0 {
				t.Errorf("closed session leaked desktop list: %v", list)
			}
			return err
		}},
		{"switch desktop", func() error { return session.windows.SwitchDesktop(ctx, "desktop-1") }},
		{"set desktop", func() error { return session.windows.SetWindowDesktopByID(ctx, "window-1", "desktop-1") }},
		{"keep above", func() error { return session.windows.SetKeepAboveByID(ctx, "window-1", true) }},
		{"keep below", func() error { return session.windows.SetKeepBelowByID(ctx, "window-1", true) }},
		{"cursor position", func() error {
			x, y, err := session.windows.CursorPosition(ctx)
			if x != 0 || y != 0 {
				t.Errorf("closed session leaked cursor position %d,%d", x, y)
			}
			return err
		}},
		{"shade", func() error { return session.windows.SetShadeByID(ctx, "window-1", true) }},
		{"tile", func() error { return session.windows.TileWindowByID(ctx, "window-1", 10, 20) }},
		{"float", func() error { return session.windows.FloatWindowByID(ctx, "window-1") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := len(backend.calls)
			if err := tc.run(); !errors.Is(err, ErrSessionClosed) {
				t.Fatalf("closed session desktop operation returned %v", err)
			}
			if len(backend.calls) != before {
				t.Fatalf("closed session dispatched compositor request: %v", backend.calls[before:])
			}
		})
	}
}

type desktopMutationRaceManager struct {
	window.Manager
	after     func()
	seen      string
	resultErr error
}

func (m *desktopMutationRaceManager) Close() error { return nil }
func (m *desktopMutationRaceManager) SupportedOperations() []string {
	return []string{"list-desktops", "switch-desktop", "set-window-desktop", "keep-above", "keep-below", "cursor-position", "shade-when-supported", "tile-window", "float-window"}
}
func (m *desktopMutationRaceManager) finish(op string) error {
	m.seen = op
	if m.after != nil {
		m.after()
	}
	return m.resultErr
}
func (m *desktopMutationRaceManager) SwitchDesktop(_ context.Context, id string) error {
	if id != "desktop-id" {
		return errors.New("unexpected desktop ID")
	}
	return m.finish("switch")
}
func (m *desktopMutationRaceManager) SetWindowDesktopByID(_ context.Context, id, desktop string) error {
	if id != "window-id" || desktop != "desktop-id" {
		return errors.New("unexpected desktop assignment")
	}
	return m.finish("assign")
}
func (m *desktopMutationRaceManager) SetKeepAboveByID(_ context.Context, id string, on bool) error {
	if id != "window-id" || !on {
		return errors.New("unexpected above args")
	}
	return m.finish("above")
}
func (m *desktopMutationRaceManager) SetKeepBelowByID(_ context.Context, id string, on bool) error {
	if id != "window-id" || !on {
		return errors.New("unexpected below args")
	}
	return m.finish("below")
}
func (m *desktopMutationRaceManager) SetShadeByID(_ context.Context, id string, on bool) error {
	if id != "window-id" || !on {
		return errors.New("unexpected shade args")
	}
	return m.finish("shade")
}
func (m *desktopMutationRaceManager) TileWindowByID(_ context.Context, id string, x, y int) error {
	if id != "window-id" || x != 13 || y != 17 {
		return errors.New("unexpected tile args")
	}
	return m.finish("tile")
}
func (m *desktopMutationRaceManager) FloatWindowByID(_ context.Context, id string) error {
	if id != "window-id" {
		return errors.New("unexpected float args")
	}
	return m.finish("float")
}

func TestDesktopMutationsDoNotAcknowledgeCanceledOperation(t *testing.T) {
	calls := []struct {
		name   string
		invoke func(*windowBundleState, context.Context) error
	}{
		{"switch", func(b *windowBundleState, c context.Context) error { return b.SwitchDesktop(c, "desktop-id") }},
		{"assign", func(b *windowBundleState, c context.Context) error {
			return b.SetWindowDesktopByID(c, "window-id", "desktop-id")
		}},
		{"above", func(b *windowBundleState, c context.Context) error { return b.SetKeepAboveByID(c, "window-id", true) }},
		{"below", func(b *windowBundleState, c context.Context) error { return b.SetKeepBelowByID(c, "window-id", true) }},
		{"shade", func(b *windowBundleState, c context.Context) error { return b.SetShadeByID(c, "window-id", true) }},
		{"tile", func(b *windowBundleState, c context.Context) error { return b.TileWindowByID(c, "window-id", 13, 17) }},
		{"float", func(b *windowBundleState, c context.Context) error { return b.FloatWindowByID(c, "window-id") }},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			manager := &desktopMutationRaceManager{after: cancel}
			session := newSessionForTesting(nil, nil, manager, nil, nil)
			defer session.Close()
			err := tc.invoke(session.windows, ctx)
			if manager.seen != tc.name {
				t.Fatalf("backend not invoked: got %q", manager.seen)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("backend canceled during operation, returned %v, want context.Canceled", err)
			}
		})
	}
}

// Successful backend completion must remain successful, while concurrent
// transport failure and cancellation must retain both error identities.
func TestDesktopMutationCompletionAndBackendFailureContracts(t *testing.T) {
	calls := []struct {
		name   string
		invoke func(*windowBundleState, context.Context) error
	}{
		{"switch", func(b *windowBundleState, c context.Context) error { return b.SwitchDesktop(c, "desktop-id") }},
		{"assign", func(b *windowBundleState, c context.Context) error {
			return b.SetWindowDesktopByID(c, "window-id", "desktop-id")
		}},
		{"above", func(b *windowBundleState, c context.Context) error { return b.SetKeepAboveByID(c, "window-id", true) }},
		{"below", func(b *windowBundleState, c context.Context) error { return b.SetKeepBelowByID(c, "window-id", true) }},
		{"shade", func(b *windowBundleState, c context.Context) error { return b.SetShadeByID(c, "window-id", true) }},
		{"tile", func(b *windowBundleState, c context.Context) error { return b.TileWindowByID(c, "window-id", 13, 17) }},
		{"float", func(b *windowBundleState, c context.Context) error { return b.FloatWindowByID(c, "window-id") }},
	}
	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			for _, mode := range []string{"success", "backend-error", "cancel-and-error", "session-closed"} {
				t.Run(mode, func(t *testing.T) {
					manager := &desktopMutationRaceManager{}
					session := newSessionForTesting(nil, nil, manager, nil, nil)
					defer session.Close()
					backendErr := errors.New("synthetic backend failure")
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					switch mode {
					case "backend-error":
						manager.resultErr = backendErr
					case "cancel-and-error":
						manager.resultErr = backendErr
						manager.after = cancel
					case "session-closed":
						manager.after = func() { _ = session.Close() }
					}
					err := tc.invoke(session.windows, ctx)
					if manager.seen != tc.name {
						t.Fatalf("wrong backend call: %q", manager.seen)
					}
					switch mode {
					case "success":
						if err != nil {
							t.Fatalf("successful operation: %v", err)
						}
					case "backend-error":
						if !errors.Is(err, backendErr) {
							t.Fatalf("lost backend error: %v", err)
						}
					case "cancel-and-error":
						if !errors.Is(err, backendErr) || !errors.Is(err, context.Canceled) {
							t.Fatalf("lost failure or cancellation: %v", err)
						}
					case "session-closed":
						if !errors.Is(err, ErrSessionClosed) {
							t.Fatalf("closed session acknowledged success: %v", err)
						}
					}
				})
			}
		})
	}
}

type countingDesktopBackend struct {
	*handleWindowManager
	calls []string
}

func (b *countingDesktopBackend) SupportedOperations() []string {
	return []string{"list-desktops", "switch-desktop", "set-window-desktop", "keep-above", "keep-below", "cursor-position", "shade-when-supported", "tile-window", "float-window"}
}
func (b *countingDesktopBackend) ListDesktops(context.Context) ([]window.DesktopInfo, error) {
	b.calls = append(b.calls, "list")
	return []window.DesktopInfo{{}}, nil
}
func (b *countingDesktopBackend) SwitchDesktop(context.Context, string) error {
	b.calls = append(b.calls, "switch")
	return nil
}
func (b *countingDesktopBackend) SetWindowDesktopByID(context.Context, string, string) error {
	b.calls = append(b.calls, "set-desktop")
	return nil
}
func (b *countingDesktopBackend) SetKeepAboveByID(context.Context, string, bool) error {
	b.calls = append(b.calls, "above")
	return nil
}
func (b *countingDesktopBackend) SetKeepBelowByID(context.Context, string, bool) error {
	b.calls = append(b.calls, "below")
	return nil
}
func (b *countingDesktopBackend) CursorPosition(context.Context) (int, int, error) {
	b.calls = append(b.calls, "cursor")
	return 123, 456, nil
}
func (b *countingDesktopBackend) SetShadeByID(context.Context, string, bool) error {
	b.calls = append(b.calls, "shade")
	return nil
}
func (b *countingDesktopBackend) TileWindowByID(context.Context, string, int, int) error {
	b.calls = append(b.calls, "tile")
	return nil
}
func (b *countingDesktopBackend) FloatWindowByID(context.Context, string) error {
	b.calls = append(b.calls, "float")
	return nil
}

// A canceled request cannot authorize even a transient compositor call.
func TestWindowDesktopFacadePreCanceledRequestsDoNotDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := &countingDesktopBackend{handleWindowManager: newHandleWindowManager()}
	session := newSessionForTesting(nil, nil, backend, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	actions := []struct {
		name string
		run  func() error
	}{
		{"list", func() error {
			items, err := session.windows.ListDesktops(ctx)
			if len(items) != 0 {
				t.Errorf("canceled desktop list leaked %v", items)
			}
			return err
		}},
		{"switch", func() error { return session.windows.SwitchDesktop(ctx, "1") }},
		{"set-desktop", func() error { return session.windows.SetWindowDesktopByID(ctx, "5", "1") }},
		{"above", func() error { return session.windows.SetKeepAboveByID(ctx, "5", true) }},
		{"below", func() error { return session.windows.SetKeepBelowByID(ctx, "5", true) }},
		{"cursor", func() error {
			x, y, err := session.windows.CursorPosition(ctx)
			if x != 0 || y != 0 {
				t.Errorf("canceled cursor leaked coordinates %d %d", x, y)
			}
			return err
		}},
		{"shade", func() error { return session.windows.SetShadeByID(ctx, "5", true) }},
		{"tile", func() error { return session.windows.TileWindowByID(ctx, "5", 42, 23) }},
		{"float", func() error { return session.windows.FloatWindowByID(ctx, "5") }},
	}
	for _, action := range actions {
		t.Run(action.name, func(t *testing.T) {
			before := len(backend.calls)
			err := action.run()
			if !errors.Is(err, context.Canceled) || len(backend.calls) != before {
				t.Fatalf("canceled action returned %v; calls=%v", err, backend.calls)
			}
		})
	}
}

func (m *desktopMutationRaceManager) ListDesktops(context.Context) ([]window.DesktopInfo, error) {
	err := m.finish("list")
	return []window.DesktopInfo{{ID: "desktop-id", Position: 1, Current: true}}, err
}
func (m *desktopMutationRaceManager) CursorPosition(context.Context) (int, int, error) {
	err := m.finish("cursor")
	return 13, 17, err
}

// The desktop read result is an authorization-bound observation: cancellation
// must remain visible even when the compositor returned a separate error.
func TestDesktopReadRetainsConcurrentCancellationAndBackendFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		invoke func(*windowBundleState, context.Context) error
	}{
		{"list", func(b *windowBundleState, ctx context.Context) error { _, err := b.ListDesktops(ctx); return err }},
		{"cursor", func(b *windowBundleState, ctx context.Context) error { _, _, err := b.CursorPosition(ctx); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backendErr := errors.New("synthetic backend read failure")
			manager := &desktopMutationRaceManager{after: cancel, resultErr: backendErr}
			session := newSessionForTesting(nil, nil, manager, nil, nil)
			defer session.Close()
			err := tc.invoke(session.windows, ctx)
			if manager.seen != tc.name {
				t.Fatalf("wrong backend call: %q", manager.seen)
			}
			if !errors.Is(err, context.Canceled) || !errors.Is(err, backendErr) {
				t.Fatalf("concurrent cancellation and backend failure not preserved: %v", err)
			}
		})
	}
}

func TestDesktopReadSuccessCancellationAndSessionClosure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		invoke func(*windowBundleState, context.Context) error
	}{
		{"list", func(b *windowBundleState, ctx context.Context) error {
			ds, err := b.ListDesktops(ctx)
			if err == nil && (len(ds) != 1 || ds[0].ID != "desktop-id" || !ds[0].Current) {
				return errors.New("lost returned desktop identity")
			}
			return err
		}},
		{"cursor", func(b *windowBundleState, ctx context.Context) error {
			x, y, err := b.CursorPosition(ctx)
			if err == nil && (x != 13 || y != 17) {
				return errors.New("lost compositor cursor position")
			}
			return err
		}},
	} {
		for _, mode := range []string{"success", "late-cancellation", "session-closed"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				manager := &desktopMutationRaceManager{}
				session := newSessionForTesting(nil, nil, manager, nil, nil)
				defer session.Close()
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				switch mode {
				case "late-cancellation":
					manager.after = cancel
				case "session-closed":
					manager.after = func() { _ = session.Close() }
				}
				err := tc.invoke(session.windows, ctx)
				if manager.seen != tc.name {
					t.Fatalf("unexpected backend call: %q", manager.seen)
				}
				switch mode {
				case "success":
					if err != nil {
						t.Fatalf("read failed: %v", err)
					}
				case "late-cancellation":
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("canceled read acknowledged success: %v", err)
					}
				case "session-closed":
					if !errors.Is(err, ErrSessionClosed) {
						t.Fatalf("closed session acknowledged read: %v", err)
					}
				}
			})
		}
	}
}

type advertisedEventsWithoutSource struct{ *handleWindowManager }

func (*advertisedEventsWithoutSource) SupportedOperations() []string {
	return []string{"events", "discover"}
}

// A backend's static capability advertisement is not proof it implements an
// optional event subscription. Never expose a fabricated stream or succeed
// when the source interface is absent.
func TestWindowEventsAdvertisedWithoutSourceFailClosed(t *testing.T) {
	manager := &advertisedEventsWithoutSource{handleWindowManager: newHandleWindowManager()}
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	events, err := session.windows.Events()
	if events != nil || !errors.Is(err, ErrUnsupported) || !errors.Is(err, window.ErrNotSupported) {
		t.Fatalf("advertised but unimplemented events: stream=%v err=%v", events, err)
	}
}

type advertisedSyncWithoutImplementation struct{ *handleWindowManager }

func (*advertisedSyncWithoutImplementation) SupportedOperations() []string {
	return []string{"sync", "list"}
}

// Advertising an optional synchronization operation does not authorize
// success when the backend fails to implement the corresponding interface.
func TestWindowSyncAdvertisedWithoutImplementationFailsClosed(t *testing.T) {
	manager := &advertisedSyncWithoutImplementation{handleWindowManager: newHandleWindowManager()}
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	if err := session.windows.Sync(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unimplemented sync was accepted: %v", err)
	}
}

// Window enumeration can finish after the caller loses authority. The final
// post-filter check must discard all observed private window metadata.
func TestWindowListRejectsCancellationAfterFilteringResults(t *testing.T) {
	manager := newHandleWindowManager(window.Info{NativeID: "synthetic-private", Title: "private"})
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	parent, cancelTimeout := context.WithTimeout(context.Background(), time.Second)
	defer cancelTimeout()
	base, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	ctx := &cancelAtErrCallContext{Context: base, cancel: cancel, target: 2}
	windows, err := session.windows.List(ctx, WindowMatch{})
	if len(windows) != 0 || !errors.Is(err, context.Canceled) || ctx.calls != 2 {
		t.Fatalf("canceled post-filter enumeration escaped: windows=%v err=%v checks=%d", windows, err, ctx.calls)
	}
}

type cancelDuringManagedWindowSync struct {
	*handleWindowManager
	cancel context.CancelFunc
	calls  int
}

func (m *cancelDuringManagedWindowSync) Sync(context.Context) error {
	m.calls++
	if m.cancel != nil {
		m.cancel()
	}
	return nil
}

// A transport completing after caller cancellation cannot authorize the
// facade to claim that the requested synchronization completed successfully.
func TestWindowBundleSyncRejectsLateCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	backend := &cancelDuringManagedWindowSync{handleWindowManager: newHandleWindowManager(), cancel: cancel}
	session := newSessionForTesting(nil, nil, backend, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	err := session.windows.Sync(ctx)
	if !errors.Is(err, context.Canceled) || backend.calls != 1 {
		t.Fatalf("late cancel: err=%v backend calls=%d", err, backend.calls)
	}
}

func TestWindowBundleSyncRejectsPreCanceledOperation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := &cancelDuringManagedWindowSync{handleWindowManager: newHandleWindowManager()}
	session := newSessionForTesting(nil, nil, backend, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	err := session.windows.Sync(ctx)
	if !errors.Is(err, context.Canceled) || backend.calls != 0 {
		t.Fatalf("pre-canceled sync: err=%v backend calls=%d", err, backend.calls)
	}
}

type closeDuringManagedWindowSync struct {
	*handleWindowManager
	session *sessionCore
	calls   int
}

func (b *closeDuringManagedWindowSync) Sync(context.Context) error {
	b.calls++
	return b.session.Close() //nolint:contextcheck // Session.Close owns an independent teardown deadline after the backend finishes.
}

// A backend can finish syncing after Session.Close revokes ownership, even
// though the caller's context is still valid. In that case no stale success
// may be returned to the caller.
func TestManagedWindowSyncRejectsBackendCompletionAfterSessionClose(t *testing.T) {
	backend := &closeDuringManagedWindowSync{handleWindowManager: newHandleWindowManager()}
	session := newSessionForTesting(nil, nil, backend, nil, nil)
	backend.session = session
	defer func() { _ = session.Close() }()
	err := session.windows.Sync(context.Background())
	if !errors.Is(err, ErrSessionClosed) || backend.calls != 1 {
		t.Fatalf("sync completed after session closure: err=%v calls=%d", err, backend.calls)
	}
}

// An absent session must not fabricate invalidation notifications or panic
// during cancellation and shutdown of its optional components.
func TestMissingSessionWaitNotificationIsInert(t *testing.T) {
	var missing *sessionCore
	missing.notifyWaiters()
	session := newSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	session.notifyWaiters() // No wait hub exists until an observer subscribes.
}
