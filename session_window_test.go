package perfuncted

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/window"
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
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	defer session.Close()
	handle, err := session.Windows.Find(context.Background(), window.Match{TitleExact: "Synthetic Editor"})
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
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	defer session.Close()
	handle, err := session.Windows.Find(context.Background(), window.Match{TitleExact: "Synthetic Editor"})
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
		invoke  func(*Window) error
		wantErr error
	}{
		{
			name:    "resize negative width",
			invoke:  func(w *Window) error { return w.Resize(context.Background(), -1, 100) },
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "resize negative height",
			invoke:  func(w *Window) error { return w.Resize(context.Background(), 100, -1) },
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "resize zero size",
			invoke:  func(w *Window) error { return w.Resize(context.Background(), 0, 0) },
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "resize beyond 16-bit geometry",
			invoke:  func(w *Window) error { return w.Resize(context.Background(), window.MaxDimension+1, 100) },
			wantErr: ErrInvalidArgument,
		},
		{
			name:    "move beyond representable range",
			invoke:  func(w *Window) error { return w.Move(context.Background(), 1<<40, 0) },
			wantErr: ErrInvalidArgument,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := newHandleWindowManager(window.Info{NativeID: "opaque-id", Title: "Target"})
			session := NewSessionForTesting(nil, nil, manager, nil, nil)
			t.Cleanup(func() {
				_ = session.Close()
			})
			target, err := session.Windows.Find(context.Background(), WindowMatch{TitleExact: "Target"})
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
		invoke     func(*Window) error
		wantAction string
	}{
		{
			name:       "resize",
			invoke:     func(w *Window) error { return w.Resize(context.Background(), 1280, 720) },
			wantAction: "resize:opaque-id",
		},
		{
			name:       "resize to the maximum",
			invoke:     func(w *Window) error { return w.Resize(context.Background(), window.MaxDimension, window.MaxDimension) },
			wantAction: "resize:opaque-id",
		},
		{
			name:       "move to a negative origin",
			invoke:     func(w *Window) error { return w.Move(context.Background(), -1920, -1080) },
			wantAction: "move:opaque-id",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := newHandleWindowManager(window.Info{NativeID: "opaque-id", Title: "Target"})
			session := NewSessionForTesting(nil, nil, manager, nil, nil)
			t.Cleanup(func() {
				_ = session.Close()
			})
			target, err := session.Windows.Find(context.Background(), WindowMatch{TitleExact: "Target"})
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
	session := NewSessionForTesting(nil, nil, backend, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	got, err := session.Windows.List(ctx, WindowMatch{})
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
	session := NewSessionForTesting(nil, nil, backend, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	handle := session.Windows.window(window.Info{NativeID: "private-1", Title: "old title"})
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
		session := NewSessionForTesting(nil, nil, backend, nil, nil)
		backend.cancel = func() {
			if err := session.Close(); err != nil {
				t.Error(err)
			}
		}
		windows, err := session.Windows.List(context.Background(), WindowMatch{})
		if !errors.Is(err, ErrSessionClosed) || len(windows) != 0 {
			t.Fatalf("closed session leaked windows %+v, err=%v", windows, err)
		}
	})
	t.Run("info", func(t *testing.T) {
		backend := &cancelingWindowReadBackend{handleWindowManager: newHandleWindowManager()}
		session := NewSessionForTesting(nil, nil, backend, nil, nil)
		backend.cancel = func() {
			if err := session.Close(); err != nil {
				t.Error(err)
			}
		}
		handle := session.Windows.window(window.Info{NativeID: "private-1", Title: "previous"})
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
	session  *Session
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
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	base, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	ctx := &closeSessionAtWindowPostcheckContext{Context: base, session: session}
	windows, err := session.Windows.List(ctx, WindowMatch{})
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
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() {
		_ = session.Close()
	})

	target, err := session.Windows.Find(
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
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() {
		_ = session.Close()
	})
	target, err := session.Windows.Find(context.Background(), WindowMatch{TitleContains: "Firefox"})
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
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
	t.Cleanup(func() {
		_ = session.Close()
	})
	_, err := session.Windows.Find(
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
		session := NewSessionForTesting(nil, nil, manager, nil, nil)
		t.Cleanup(func() {
			_ = session.Close()
		})
		result := make(chan error, 1)
		go func() {
			_, err := session.Windows.Wait(
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
		session := NewSessionForTesting(nil, nil, manager, nil, nil)
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
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
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
	session := NewSessionForTesting(
		nil,
		nil,
		newHandleWindowManager(),
		nil,
		nil,
	)
	retained := session.Windows.window(window.Info{NativeID: "retained"})
	if err := session.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	checks := []struct {
		name string
		call func() error
	}{
		{name: "screen", call: func() error {
			_, err := session.Screen.Grab(context.Background(), image.Rectangle{})
			return err
		}},
		{name: "input", call: func() error {
			return session.Input.KeyDown(context.Background(), "a")
		}},
		{name: "windows list", call: func() error {
			_, err := session.Windows.List(context.Background(), WindowMatch{})
			return err
		}},
		{name: "windows wait", call: func() error {
			return session.Wait(context.Background(), WindowExists(WindowMatch{}))
		}},
		{name: "retained window", call: func() error {
			return retained.Close(context.Background())
		}},
		{name: "outputs", call: func() error {
			_, err := session.Outputs.List(context.Background())
			return err
		}},
		{name: "clipboard", call: func() error {
			_, err := session.Clipboard.Get(context.Background())
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
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
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
	if err := (&Session{}).Wait(
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
	session := NewSessionForTesting(nil, nil, manager, nil, nil)
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
