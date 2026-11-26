package perfuncted

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2/accessibility"
	"github.com/nskaggs/perfuncted/v2/window"
	"image"
	"iter"
	"reflect"
	"sync"
	"testing"
	"time"
)

type windowFullscreenFailureManager struct {
	*handleWindowManager
	actionErr error
}

func (m *windowFullscreenFailureManager) FullscreenByID(ctx context.Context, id string) error {
	if m.actionErr != nil {
		return m.actionErr
	}
	return m.handleWindowManager.FullscreenByID(ctx, id)
}
func (m *windowFullscreenFailureManager) UnfullscreenByID(ctx context.Context, id string) error {
	if m.actionErr != nil {
		return m.actionErr
	}
	return m.handleWindowManager.UnfullscreenByID(ctx, id)
}

func TestWindowFullscreenAndUnfullscreenRequireStableWindowIDAndLiveSession(t *testing.T) {
	manager := &windowFullscreenFailureManager{handleWindowManager: newHandleWindowManager(window.Info{NativeID: "private-101", Title: "Synthetic Editor"})}
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	defer session.Close()
	handle, err := session.windows.Find(context.Background(), window.Match{TitleExact: "Synthetic Editor"})
	if err != nil {
		t.Fatalf("find stable-ID window: %v", err)
	}
	if err := handle.Fullscreen(context.Background()); err != nil {
		t.Fatalf("fullscreen stable ID: %v", err)
	}
	if err := handle.Unfullscreen(context.Background()); err != nil {
		t.Fatalf("unfullscreen stable ID: %v", err)
	}
	manager.mu.Lock()
	calls := append([]string(nil), manager.actions...)
	manager.mu.Unlock()
	if !reflect.DeepEqual(calls, []string{"fullscreen:private-101", "unfullscreen:private-101"}) {
		t.Fatalf("window action used title or stale ID: %v", calls)
	}
	failure := errors.New("compositor rejected fullscreen")
	manager.actionErr = failure
	if err := handle.Fullscreen(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("fullscreen backend failure lost: %v", err)
	}
	if err := handle.Unfullscreen(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("unfullscreen backend failure lost: %v", err)
	}
	manager.actionErr = nil
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := handle.Fullscreen(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled fullscreen accepted: %v", err)
	}
	if err := handle.Unfullscreen(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled unfullscreen accepted: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := handle.Fullscreen(context.Background()); err == nil {
		t.Fatal("closed session fullscreen accepted")
	}
	if err := handle.Unfullscreen(context.Background()); err == nil {
		t.Fatal("closed session unfullscreen accepted")
	}
}

func TestAllWindowOperationsRejectCanceledContextBeforeBackendDispatch(t *testing.T) {
	manager := newHandleWindowManager(window.Info{NativeID: "private-101", Title: "Synthetic Editor"})
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	defer session.Close()
	handle, err := session.windows.Find(context.Background(), window.Match{TitleExact: "Synthetic Editor"})
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		call func(context.Context) error
	}{
		{"activate", handle.Activate},
		{"move", func(ctx context.Context) error { return handle.Move(ctx, 11, 23) }},
		{"resize", func(ctx context.Context) error { return handle.Resize(ctx, 100, 80) }},
		{"close", handle.Close},
		{"minimize", handle.Minimize},
		{"maximize", handle.Maximize},
		{"restore", handle.Restore},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(canceled); !errors.Is(err, context.Canceled) {
				t.Errorf("canceled action dispatched: %v", err)
			}
		})
	}
	if _, err := handle.Info(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled metadata refresh returned %v", err)
	}
	manager.mu.Lock()
	actions := append([]string(nil), manager.actions...)
	manager.mu.Unlock()
	if len(actions) != 0 {
		t.Fatalf("cancelled operations reached window backend: %v", actions)
	}
}

// Geometry the backends cannot represent must be refused at the facade, before
// any backend call, so no compositor is asked for a size it will silently
// truncate.
func TestWindowGeometryRejectsUnrepresentableValuesBeforeDispatch(t *testing.T) {
	for _, tc := range []struct {
		name    string
		invoke  func(*windowState) error
		wantErr error
	}{
		{
			name:    "resize negative width",
			invoke:  func(w *windowState) error { return w.Resize(context.Background(), -1, 100) },
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "resize negative height",
			invoke:  func(w *windowState) error { return w.Resize(context.Background(), 100, -1) },
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "resize zero size",
			invoke:  func(w *windowState) error { return w.Resize(context.Background(), 0, 0) },
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "resize beyond 16-bit geometry",
			invoke:  func(w *windowState) error { return w.Resize(context.Background(), window.MaxDimension+1, 100) },
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "move beyond representable range",
			invoke:  func(w *windowState) error { return w.Move(context.Background(), 1<<40, 0) },
			wantErr: ErrInvalidArgument,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := newHandleWindowManager(window.Info{NativeID: "opaque-id", Title: "Target"})
			session := newSessionForTesting(nil, nil, manager, nil, nil)
			t.Cleanup(func() {
				_ = session.Close()
			})
			target, err := session.windows.Find(context.Background(), WindowMatch{TitleExact: "Target"})
			if err != nil {
				t.Fatalf("Find: %v", err)
			}

			err = tc.invoke(target)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("geometry call = %v, want %v", err, tc.wantErr)
			}
			manager.mu.Lock()
			actions := append([]string(nil), manager.actions...)
			manager.mu.Unlock()
			if len(actions) != 0 {
				t.Fatalf("backend was called for rejected geometry: %v", actions)
			}
		})
	}
}

// Ordinary geometry, including negative positions for a monitor left of the
// primary, must still reach the backend unchanged.
func TestWindowGeometryAcceptsRepresentableValues(t *testing.T) {
	for _, tc := range []struct {
		name       string
		invoke     func(*windowState) error
		wantAction string
	}{
		{
			name:       "resize",
			invoke:     func(w *windowState) error { return w.Resize(context.Background(), 1280, 720) },
			wantAction: "resize:opaque-id",
		},
		{
			name: "resize to the maximum",
			invoke: func(w *windowState) error {
				return w.Resize(context.Background(), window.MaxDimension, window.MaxDimension)
			},
			wantAction: "resize:opaque-id",
		},
		{
			name:       "move to a negative origin",
			invoke:     func(w *windowState) error { return w.Move(context.Background(), -1920, -1080) },
			wantAction: "move:opaque-id",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := newHandleWindowManager(window.Info{NativeID: "opaque-id", Title: "Target"})
			session := newSessionForTesting(nil, nil, manager, nil, nil)
			t.Cleanup(func() {
				_ = session.Close()
			})
			target, err := session.windows.Find(context.Background(), WindowMatch{TitleExact: "Target"})
			if err != nil {
				t.Fatalf("Find: %v", err)
			}

			if err := tc.invoke(target); err != nil {
				t.Fatalf("geometry call: %v", err)
			}
			manager.mu.Lock()
			actions := append([]string(nil), manager.actions...)
			manager.mu.Unlock()
			if len(actions) != 1 || actions[0] != tc.wantAction {
				t.Fatalf("backend actions = %v, want [%s]", actions, tc.wantAction)
			}
		})
	}
}

type cancelingWindowReadBackend struct {
	*handleWindowManager
	cancel    context.CancelFunc
	listCalls int
	infoCalls int
}

func (b *cancelingWindowReadBackend) List(context.Context) ([]window.Info, error) {
	b.listCalls++
	b.cancel()
	return []window.Info{{NativeID: "private-1", Title: "private window"}}, nil
}
func (b *cancelingWindowReadBackend) InfoByID(_ context.Context, id string) (window.Info, error) {
	b.infoCalls++
	b.cancel()
	return window.Info{NativeID: id, Title: "new private title"}, nil
}

// A compositor can ignore caller cancellation and return metadata after its
// authorization has ended. That late result must not escape the Session.
func TestManagedWindowListRejectsMetadataFromCanceledRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	backend := &cancelingWindowReadBackend{handleWindowManager: newHandleWindowManager(), cancel: cancel}
	session := newSessionForTesting(nil, nil, backend, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	got, err := session.windows.List(ctx, WindowMatch{})
	if !errors.Is(err, context.Canceled) || len(got) != 0 {
		t.Fatalf("canceled window listing leaked %v, err=%v", got, err)
	}
	if backend.listCalls != 1 {
		t.Fatalf("unexpected calls: %d", backend.listCalls)
	}
}

// Window.Info must not commit newly observed metadata after cancellation: the
// cached snapshot is authoritative only for an accepted, live observation.
func TestManagedWindowInfoDoesNotPublishCanceledMetadata(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	backend := &cancelingWindowReadBackend{handleWindowManager: newHandleWindowManager(), cancel: cancel}
	session := newSessionForTesting(nil, nil, backend, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	handle := session.windows.window(window.Info{NativeID: "private-1", Title: "old title"})
	info, err := handle.Info(ctx)
	if !errors.Is(err, context.Canceled) || info != (window.Info{}) {
		t.Fatalf("canceled window metadata leaked %+v, err=%v", info, err)
	}
	if title := handle.Title(); title != "old title" {
		t.Fatalf("canceled window metadata overwritten cached title: %q", title)
	}
	if backend.infoCalls != 1 {
		t.Fatalf("unexpected calls: %d", backend.infoCalls)
	}
}

func TestManagedWindowHandlesDiscardResultsAfterSessionClose(t *testing.T) {
	t.Run("list", func(t *testing.T) {
		backend := &cancelingWindowReadBackend{handleWindowManager: newHandleWindowManager()}
		session := newSessionForTesting(nil, nil, backend, nil, nil)
		backend.cancel = func() {
			if err := session.Close(); err != nil {
				t.Error(err)
			}
		}
		windows, err := session.windows.List(context.Background(), WindowMatch{})
		if !errors.Is(err, ErrSessionClosed) || len(windows) != 0 {
			t.Fatalf("closed session leaked windows %+v, err=%v", windows, err)
		}
	})
	t.Run("info", func(t *testing.T) {
		backend := &cancelingWindowReadBackend{handleWindowManager: newHandleWindowManager()}
		session := newSessionForTesting(nil, nil, backend, nil, nil)
		backend.cancel = func() {
			if err := session.Close(); err != nil {
				t.Error(err)
			}
		}
		handle := session.windows.window(window.Info{NativeID: "private-1", Title: "previous"})
		info, err := handle.Info(context.Background())
		if !errors.Is(err, ErrSessionClosed) || info != (window.Info{}) || handle.Title() != "previous" {
			t.Fatalf("closed session leaked window data %+v, title=%q err=%v", info, handle.Title(), err)
		}
	})
}

// This context models a session being revoked at the last caller-authority
// check after window metadata has already been read and filtered. The caller
// itself remains live, so only the final session ownership check can reject
// the stale result.
type closeSessionAtWindowPostcheckContext struct {
	context.Context
	session  *sessionCore
	checks   int
	closeErr error
}

func (c *closeSessionAtWindowPostcheckContext) Err() error {
	c.checks++
	if c.checks == 2 {
		c.closeErr = c.session.Close() //nolint:contextcheck // Teardown deliberately revokes session ownership during the caller's still-live read.
	}
	return c.Context.Err()
}

func TestWindowListRejectsSessionCloseAfterMetadataFiltering(t *testing.T) {
	manager := newHandleWindowManager(window.Info{NativeID: "private-window", Title: "sensitive"})
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	base, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	ctx := &closeSessionAtWindowPostcheckContext{Context: base, session: session}
	windows, err := session.windows.List(ctx, WindowMatch{})
	if ctx.closeErr != nil || !errors.Is(err, ErrSessionClosed) || len(windows) != 0 || ctx.checks != 2 {
		t.Fatalf("session closed after filtering: got windows=%v err=%v cleanup=%v checks=%d", windows, err, ctx.closeErr, ctx.checks)
	}
	if base.Err() != nil {
		t.Fatalf("caller expired; expected session ownership alone to reject metadata: %v", base.Err())
	}
}

type handleWindowManager struct {
	mu sync.Mutex

	windows []window.Info
	err     error
	changes chan struct{}
	actions []string
	closed  bool
}

func newHandleWindowManager(windows ...window.Info) *handleWindowManager {
	return &handleWindowManager{
		windows: windows,
		changes: make(chan struct{}, 8),
	}
}

func (m *handleWindowManager) List(context.Context) ([]window.Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	return append([]window.Info(nil), m.windows...), nil
}

func (m *handleWindowManager) IterateWindows(
	ctx context.Context,
) iter.Seq2[window.Info, error] {
	return func(yield func(window.Info, error) bool) {
		windows, err := m.List(ctx)
		if err != nil {
			yield(window.Info{}, err)
			return
		}
		for _, info := range windows {
			if !yield(info, nil) {
				return
			}
		}
	}
}

func (m *handleWindowManager) setWindows(
	notify bool,
	windows ...window.Info,
) {
	m.mu.Lock()
	m.windows = windows
	m.mu.Unlock()
	if notify {
		m.changes <- struct{}{}
	}
}

func (m *handleWindowManager) setError(err error) {
	m.mu.Lock()
	m.err = err
	m.mu.Unlock()
}

func (m *handleWindowManager) WindowChanges() <-chan struct{} {
	return m.changes
}

func (m *handleWindowManager) Activate(
	context.Context,
	string,
) error {
	return errors.New("title operation used")
}

func (m *handleWindowManager) Move(
	context.Context,
	string,
	int,
	int,
) error {
	return errors.New("title operation used")
}

func (m *handleWindowManager) Resize(
	context.Context,
	string,
	int,
	int,
) error {
	return errors.New("title operation used")
}

func (m *handleWindowManager) ActiveTitle(context.Context) (string, error) {
	return "", nil
}

func (m *handleWindowManager) CloseWindow(
	context.Context,
	string,
) error {
	return errors.New("title operation used")
}

func (m *handleWindowManager) Minimize(
	context.Context,
	string,
) error {
	return errors.New("title operation used")
}

func (m *handleWindowManager) Maximize(
	context.Context,
	string,
) error {
	return errors.New("title operation used")
}

func (m *handleWindowManager) Fullscreen(
	context.Context,
	string,
) error {
	return errors.New("title operation used")
}

func (m *handleWindowManager) Unfullscreen(
	context.Context,
	string,
) error {
	return errors.New("title operation used")
}

func (m *handleWindowManager) Restore(
	context.Context,
	string,
) error {
	return errors.New("title operation used")
}

func (m *handleWindowManager) Close() error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	return nil
}

func (m *handleWindowManager) record(action string) error {
	m.mu.Lock()
	m.actions = append(m.actions, action)
	m.mu.Unlock()
	return nil
}

func (m *handleWindowManager) ActivateByID(
	_ context.Context,
	id string,
) error {
	return m.record("activate:" + id)
}

func (m *handleWindowManager) MoveByID(
	_ context.Context,
	id string,
	_ int,
	_ int,
) error {
	return m.record("move:" + id)
}

func (m *handleWindowManager) ResizeByID(
	_ context.Context,
	id string,
	_ int,
	_ int,
) error {
	return m.record("resize:" + id)
}

func (m *handleWindowManager) CloseWindowByID(
	_ context.Context,
	id string,
) error {
	return m.record("close:" + id)
}

func (m *handleWindowManager) MinimizeByID(
	_ context.Context,
	id string,
) error {
	return m.record("minimize:" + id)
}

func (m *handleWindowManager) MaximizeByID(
	_ context.Context,
	id string,
) error {
	return m.record("maximize:" + id)
}

func (m *handleWindowManager) FullscreenByID(
	_ context.Context,
	id string,
) error {
	return m.record("fullscreen:" + id)
}

func (m *handleWindowManager) UnfullscreenByID(
	_ context.Context,
	id string,
) error {
	return m.record("unfullscreen:" + id)
}

func (m *handleWindowManager) RestoreByID(
	_ context.Context,
	id string,
) error {
	return m.record("restore:" + id)
}

func (m *handleWindowManager) InfoByID(
	_ context.Context,
	id string,
) (window.Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return window.Info{}, m.err
	}
	for _, info := range m.windows {
		if info.StableID() == id {
			return info, nil
		}
	}
	return window.Info{}, window.ErrWindowNotFound
}

func TestWindowHandleRemainsStableAcrossTitleChange(t *testing.T) {
	manager := newHandleWindowManager(window.Info{
		NativeID: "kwin-{opaque-id}",
		Title:    "Old title",
	})
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() {
		_ = session.Close()
	})

	target, err := session.windows.Find(
		context.Background(),
		WindowMatch{TitleExact: "Old title"},
	)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	manager.setWindows(false, window.Info{
		NativeID: "kwin-{opaque-id}",
		Title:    "New title",
	})
	if activateErr := target.Activate(
		context.Background(),
	); activateErr != nil {
		t.Fatalf("Activate: %v", activateErr)
	}
	info, err := target.Info(context.Background())
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Title != "New title" || target.Title() != "New title" {
		t.Fatalf("refreshed title = %q / %q", info.Title, target.Title())
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.actions) != 1 ||
		manager.actions[0] != "activate:kwin-{opaque-id}" {
		t.Fatalf("actions = %v", manager.actions)
	}
}

func TestWindowCachedInfoDoesNotRefreshWindowManager(t *testing.T) {
	manager := newHandleWindowManager(window.Info{
		NativeID: "browser",
		Title:    "Firefox",
		AppID:    "org.mozilla.firefox",
		PID:      42,
	})
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() {
		_ = session.Close()
	})
	target, err := session.windows.Find(context.Background(), WindowMatch{TitleContains: "Firefox"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	got := target.CachedInfo()
	if got.NativeID != "browser" || got.Title != "Firefox" || got.AppID != "org.mozilla.firefox" || got.PID != 42 {
		t.Fatalf("cached info = %+v", got)
	}
}

func TestWindowFindRejectsAmbiguous(t *testing.T) {
	manager := newHandleWindowManager(
		window.Info{NativeID: "1", Title: "Editor"},
		window.Info{NativeID: "2", Title: "Editor"},
	)
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() {
		_ = session.Close()
	})
	_, err := session.windows.Find(
		context.Background(),
		WindowMatch{TitleExact: "Editor"},
	)
	if !errors.Is(err, ErrWindowAmbiguous) {
		t.Fatalf("Find error = %v", err)
	}

}

func TestWaitUsesEventsAndPollingRecovery(t *testing.T) {
	t.Run("event wake", func(t *testing.T) {
		manager := newHandleWindowManager()
		session := newSessionForTesting(nil, nil, manager, nil, nil)
		t.Cleanup(func() {
			_ = session.Close()
		})
		result := make(chan error, 1)
		go func() {
			_, err := session.windows.Wait(
				context.Background(),
				WindowMatch{TitleExact: "Ready"},
			)
			result <- err
		}()
		time.Sleep(20 * time.Millisecond)
		manager.setWindows(true, window.Info{
			NativeID: "ready",
			Title:    "Ready",
		})
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("Wait: %v", err)
			}
		case <-time.After(300 * time.Millisecond):
			t.Fatal("event did not wake Wait")
		}
	})

	t.Run("missed event fallback", func(t *testing.T) {
		manager := newHandleWindowManager()
		session := newSessionForTesting(nil, nil, manager, nil, nil)
		t.Cleanup(func() {
			_ = session.Close()
		})
		ctx, cancel := context.WithTimeout(
			context.Background(),
			time.Second,
		)
		defer cancel()
		go func() {
			time.Sleep(20 * time.Millisecond)
			manager.setWindows(false, window.Info{
				NativeID: "ready",
				Title:    "Ready",
			})
		}()
		if err := session.Wait(
			ctx,
			WindowExists(WindowMatch{TitleExact: "Ready"}),
			WaitEvery(10*time.Millisecond),
		); err != nil {
			t.Fatalf("Wait: %v", err)
		}
	})
}

func TestWaitCompositionErrorsCancellationAndShutdown(t *testing.T) {
	manager := newHandleWindowManager(window.Info{
		NativeID: "1",
		Title:    "Ready",
	})
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	if err := session.Wait(
		context.Background(),
		All(
			WindowExists(WindowMatch{TitleExact: "Ready"}),
			Not(WindowExists(WindowMatch{TitleExact: "Missing"})),
			Any(
				WindowExists(WindowMatch{TitleExact: "Ready"}),
				WindowExists(WindowMatch{TitleExact: "Other"}),
			),
		),
	); err != nil {
		t.Fatalf("composed Wait: %v", err)
	}

	backendErr := errors.New("terminal backend error")
	manager.setError(backendErr)
	err := session.Wait(
		context.Background(),
		WindowExists(WindowMatch{}),
	)
	if !errors.Is(err, backendErr) {
		t.Fatalf("terminal error = %v", err)
	}
	manager.setError(nil)

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := session.Wait(
		cancelled,
		WindowExists(WindowMatch{TitleExact: "Missing"}),
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}

	waitResult := make(chan error, 1)
	go func() {
		waitResult <- session.Wait(
			context.Background(),
			WindowExists(WindowMatch{TitleExact: "Missing"}),
		)
	}()
	time.Sleep(20 * time.Millisecond)
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-waitResult:
		if !errors.Is(err, ErrSessionClosed) {
			t.Fatalf("shutdown wait error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("session shutdown did not release waiter")
	}
}

func TestClosedSessionRejectsWindowWork(t *testing.T) {
	session := newSessionForTesting(
		nil,
		nil,
		newHandleWindowManager(),
		nil,
		nil,
	)
	retained := session.windows.window(window.Info{NativeID: "retained"})
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	checks := []struct {
		name string
		call func() error
	}{
		{name: "screen", call: func() error {
			_, err := session.screen.Grab(context.Background(), image.Rectangle{})
			return err
		}},
		{name: "input", call: func() error {
			return session.input.KeyDown(context.Background(), "a")
		}},
		{name: "windows list", call: func() error {
			_, err := session.windows.List(context.Background(), WindowMatch{})
			return err
		}},
		{name: "windows wait", call: func() error {
			return session.Wait(context.Background(), WindowExists(WindowMatch{}))
		}},
		{name: "retained window", call: func() error {
			return retained.Close(context.Background())
		}},
		{name: "outputs", call: func() error {
			_, err := session.outputs.List(context.Background())
			return err
		}},
		{name: "clipboard", call: func() error {
			_, err := session.clipboard.Get(context.Background())
			return err
		}},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if err := check.call(); !errors.Is(err, ErrSessionClosed) {
				t.Fatalf("operation after Close error = %v, want ErrSessionClosed", err)
			}
		})
	}
}

func TestWaitRejectsCanceledContextBeforeSatisfiedCondition(t *testing.T) {
	session := newSessionForTesting(nil, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := session.Wait(ctx, Predicate("already satisfied", func(context.Context) (bool, error) {
		return true, nil
	})); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v, want context.Canceled", err)
	}
}

func TestWaitRejectsZeroValueSession(t *testing.T) {
	if err := (&sessionCore{}).Wait(
		context.Background(),
		Predicate("never", func(context.Context) (bool, error) {
			return false, nil
		}),
	); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("Wait on zero-value session error = %v, want %v", err, ErrSessionClosed)
	}
}

func TestApplicationWaitForWindowAndExitBeforeWindow(t *testing.T) {
	manager := newHandleWindowManager()
	session := newSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() {
		_ = session.Close()
	})
	app, err := session.Launch(
		context.Background(),
		Command{Name: "sh", Args: []string{"-c", "sleep 1"}},
	)
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		manager.setWindows(true, window.Info{
			NativeID: "browser",
			Title:    "Browser",
			PID:      int32(app.PID()),
		})
	}()
	target, err := app.WaitForWindow(
		context.Background(),
		WindowMatch{TitleExact: "Browser"},
	)
	if err != nil {
		t.Fatalf("WaitForWindow: %v", err)
	}
	if target.ID().String() != "browser" {
		t.Fatalf("window id = %q", target.ID())
	}

	exited, err := session.Launch(
		context.Background(),
		Command{Name: "true"},
	)
	if err != nil {
		t.Fatalf("Launch true: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = exited.WaitForWindow(
		ctx,
		WindowMatch{TitleExact: "Never"},
	)
	if !errors.Is(err, ErrApplicationExited) {
		t.Fatalf("exit-before-window error = %v", err)
	}
}

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
