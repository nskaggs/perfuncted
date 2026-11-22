package perfuncted

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/window"
)

type scriptedFocusManager struct {
	*focusTestWindowManager
	infoReads   atomic.Int32
	failAt      int32
	switchAt    int32
	activateErr error
}

func (m *scriptedFocusManager) InfoByID(ctx context.Context, id string) (window.Info, error) {
	read := m.infoReads.Add(1)
	if read == m.failAt {
		return window.Info{}, ErrInvalidArgument
	}
	if read == m.switchAt {
		m.mu.Lock()
		for i := range m.windows {
			if m.windows[i].StableID() == id {
				m.windows[i].PID++
			}
		}
		m.mu.Unlock()
	}
	return m.focusTestWindowManager.InfoByID(ctx, id)
}

func (m *scriptedFocusManager) ActivateByID(ctx context.Context, id string) error {
	if m.activateErr != nil {
		return m.activateErr
	}
	return m.focusTestWindowManager.ActivateByID(ctx, id)
}

func scriptedWindowSession(t *testing.T) (*Session, *scriptedFocusManager, *windowInputSpy) {
	t.Helper()
	manager := &scriptedFocusManager{focusTestWindowManager: &focusTestWindowManager{
		handleWindowManager: newHandleWindowManager(window.Info{
			NativeID: "1", Title: "Editor", PID: 120, AppID: "org.example.Editor",
		}),
	}}
	backend := &windowInputSpy{}
	s := NewSessionForTesting(nil, backend, manager, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	return s, manager, backend
}

func TestWindowTargetCoverageEarlyReturnPaths(t *testing.T) {
	t.Run("nil view", func(t *testing.T) {
		target := &WindowTarget{}
		if err := target.Do(Type("x")); !errors.Is(err, ErrNilSession) {
			t.Fatal(err)
		}
	})
	t.Run("missing capability", func(t *testing.T) {
		s, _, backend := scriptedWindowSession(t)
		original := s.Windows.backend
		s.Windows.backend = nil
		t.Cleanup(func() { s.Windows.backend = original })
		if err := s.Window("Editor").Do(Type("text")); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("missing window backend: %v", err)
		}
		if len(backend.callsSnapshot()) != 0 {
			t.Fatal("input sent without window capability")
		}
	})
	t.Run("missing title", func(t *testing.T) {
		s, _, backend := scriptedWindowSession(t)
		if err := s.Window("Not this window").Do(Type("text")); !errors.Is(err, ErrWindowNotFound) {
			t.Fatalf("window not found: %v", err)
		}
		if len(backend.callsSnapshot()) != 0 {
			t.Fatal("input dispatched for missing title")
		}
	})
	t.Run("empty batch", func(t *testing.T) {
		s, manager, _ := scriptedWindowSession(t)
		if err := s.Window("Editor").Do(); err != nil {
			t.Fatal(err)
		}
		if len(manager.actions) != 0 {
			t.Fatal("empty batch activated window")
		}
	})
	t.Run("initial info failure", func(t *testing.T) {
		s, manager, backend := scriptedWindowSession(t)
		manager.failAt = 1
		if err := s.Window("Editor").Do(Type("text")); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("initial window info error: %v", err)
		}
		if len(backend.callsSnapshot()) != 0 {
			t.Fatal("dispatched after initial info error")
		}
	})
	t.Run("activation failure", func(t *testing.T) {
		s, manager, backend := scriptedWindowSession(t)
		manager.activateErr = ErrUnsupported
		if err := s.Window("Editor").Do(Type("text")); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("activation error: %v", err)
		}
		if len(backend.callsSnapshot()) != 0 {
			t.Fatal("dispatched after activation error")
		}
	})
	t.Run("wait observer error", func(t *testing.T) {
		s, manager, backend := scriptedWindowSession(t)
		manager.failAt = 2
		if err := s.Window("Editor").Do(Type("text")); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("wait observation error: %v", err)
		}
		if len(backend.callsSnapshot()) != 0 {
			t.Fatal("dispatched after unreadable focus observation")
		}
	})
	t.Run("focus identity changes during activation", func(t *testing.T) {
		s, manager, backend := scriptedWindowSession(t)
		manager.switchAt = 2
		if err := s.Window("Editor").Do(Type("text")); !errors.Is(err, ErrWindowIdentityChanged) {
			t.Fatalf("identity changed during wait: %v", err)
		}
		if len(backend.callsSnapshot()) != 0 {
			t.Fatal("dispatched to replaced window")
		}
	})
	t.Run("guard observation fails", func(t *testing.T) {
		s, manager, backend := scriptedWindowSession(t)
		manager.failAt = 3
		if err := s.Window("Editor").Do(Type("text")); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("guard info error: %v", err)
		}
		if len(backend.callsSnapshot()) != 0 {
			t.Fatal("dispatched after pre-input observation failure")
		}
	})
}

type cancelWaitWindowManager struct {
	*focusTestWindowManager
	reads  int
	cancel context.CancelFunc
}

func (m *cancelWaitWindowManager) InfoByID(ctx context.Context, id string) (window.Info, error) {
	info, err := m.focusTestWindowManager.InfoByID(ctx, id)
	m.reads++
	if m.reads == 2 {
		m.cancel()
	}
	return info, err
}

// Explicit caller cancellation during the focus observation must propagate
// unchanged; it is not an activation timeout and must never send a keystroke.
func TestWindowTargetCoverageCanceledDuringFocusWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	manager := &cancelWaitWindowManager{focusTestWindowManager: &focusTestWindowManager{
		handleWindowManager: newHandleWindowManager(window.Info{
			NativeID: "1", Title: "Editor", PID: 12, AppID: "org.example.Editor",
		}),
	}, cancel: cancel}
	backend := &windowInputSpy{}
	s := NewSessionForTesting(nil, backend, manager, nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	err := s.WithContext(ctx).Window("Editor").Do(Type("private"))
	if !errors.Is(err, context.Canceled) || len(backend.callsSnapshot()) != 0 {
		t.Fatalf("canceled focus wait = %v, input = %v", err, backend.callsSnapshot())
	}
}

// Activation can be accepted but never observed. A shorter internal focus
// deadline, rather than caller cancellation, must return the focus-specific
// uncertainty signal and must not inject any input.
func TestWindowTargetCoverageInternalFocusTimeout(t *testing.T) {
	manager := newHandleWindowManager(window.Info{
		NativeID: "1", Title: "Editor", PID: 42, AppID: "org.example.Editor",
	})
	backend := &windowInputSpy{}
	session := NewSessionForTesting(nil, backend, manager, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	session.config.Timeouts = TimeoutPolicy{Short: 15 * time.Millisecond, Poll: time.Millisecond}
	err := session.Window("Editor").Do(Type("private"))
	if !errors.Is(err, ErrWindowFocusUnverified) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("focus timeout: %v", err)
	}
	if len(backend.callsSnapshot()) != 0 {
		t.Fatal("input dispatched despite unobserved focus")
	}
}
