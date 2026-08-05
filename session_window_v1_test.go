package perfuncted

import (
	"context"
	"errors"
	"image"
	"iter"
	"sync"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/window"
)

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
