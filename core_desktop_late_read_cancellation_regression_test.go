package perfuncted

import (
	"context"
	"errors"
	"image"
	"testing"

	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/window"
)

type cancelingCoreScreenRead struct {
	capabilityScreen
	cancel context.CancelFunc
}

func (b *cancelingCoreScreenRead) Grab(context.Context, image.Rectangle) (image.Image, error) {
	b.cancel()
	return image.NewRGBA(image.Rect(0, 0, 2, 2)), nil
}

type cancelingCoreOutputRead struct {
	cancel context.CancelFunc
}

func (b *cancelingCoreOutputRead) List(context.Context) ([]output.Info, error) {
	b.cancel()
	return []output.Info{{Name: "PRIVATE-OUTPUT"}}, nil
}
func (b *cancelingCoreOutputRead) Close() error { return nil }

type cancelingCoreClipboardRead struct {
	capabilityClipboard
	cancel context.CancelFunc
}

func (b *cancelingCoreClipboardRead) Get(context.Context) (string, error) {
	b.cancel()
	return "private clipboard", nil
}

type cancelingCoreActiveTitleRead struct {
	*handleWindowManager
	cancel context.CancelFunc
}

func (b *cancelingCoreActiveTitleRead) ActiveTitle(context.Context) (string, error) {
	b.cancel()
	return "private window title", nil
}

// Desktop read facades must not return a private result that arrives after
// caller cancellation, even when the underlying provider ignores the context.
func TestCoreDesktopReadsDiscardLateCanceledResults(t *testing.T) {
	t.Run("screen image", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		session := NewSessionForTesting(&cancelingCoreScreenRead{cancel: cancel}, nil, nil, nil, nil)
		t.Cleanup(func() { _ = session.Close() })
		img, err := session.Screen.Grab(ctx, image.Rectangle{})
		if !errors.Is(err, context.Canceled) || img != nil {
			t.Fatalf("canceled screen returned %T err=%v", img, err)
		}
	})
	t.Run("outputs", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		session := NewSessionForTesting(nil, nil, nil, &cancelingCoreOutputRead{cancel: cancel}, nil)
		t.Cleanup(func() { _ = session.Close() })
		infos, err := session.Outputs.List(ctx)
		if !errors.Is(err, context.Canceled) || len(infos) != 0 {
			t.Fatalf("canceled outputs returned %+v err=%v", infos, err)
		}
	})
	t.Run("clipboard text", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		session := NewSessionForTesting(nil, nil, nil, nil, &cancelingCoreClipboardRead{cancel: cancel})
		t.Cleanup(func() { _ = session.Close() })
		text, err := session.Clipboard.Get(ctx)
		if !errors.Is(err, context.Canceled) || text != "" {
			t.Fatalf("canceled clipboard returned %q err=%v", text, err)
		}
	})
	t.Run("active window title", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		manager := &cancelingCoreActiveTitleRead{handleWindowManager: newHandleWindowManager(window.Info{Title: "window"}), cancel: cancel}
		session := NewSessionForTesting(nil, nil, manager, nil, nil)
		t.Cleanup(func() { _ = session.Close() })
		title, err := session.Windows.ActiveTitle(ctx)
		if !errors.Is(err, context.Canceled) || title != "" {
			t.Fatalf("canceled active title returned %q err=%v", title, err)
		}
	})
}

// Cancellation can arrive inside a compositor's hash primitive too. An
// acknowledged digest is still a private observation from a canceled read.
func TestCoreScreenHashRejectsLateCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	screenshotter := &cancelingCoreScreenRead{cancel: cancel}
	session := NewSessionForTesting(screenshotter, nil, nil, nil, nil)
	t.Cleanup(func() { _ = session.Close() })
	got, err := session.Screen.GrabFullHash(ctx)
	if !errors.Is(err, context.Canceled) || got != 0 {
		t.Fatalf("canceled screen hash returned %d, err=%v", got, err)
	}
}

// Caller cancellation is not the only authority boundary. Session.Close may
// complete while a provider is reading; old data must not escape after close.
func TestCoreDesktopReadsDiscardResultsAfterSessionClose(t *testing.T) {
	t.Run("screen", func(t *testing.T) {
		backend := &cancelingCoreScreenRead{}
		session := NewSessionForTesting(backend, nil, nil, nil, nil)
		backend.cancel = func() {
			if err := session.Close(); err != nil {
				t.Error(err)
			}
		}
		img, err := session.Screen.Grab(context.Background(), image.Rectangle{})
		if !errors.Is(err, ErrSessionClosed) || img != nil {
			t.Fatalf("closed session exposed screen %T err=%v", img, err)
		}
	})
	t.Run("output", func(t *testing.T) {
		backend := &cancelingCoreOutputRead{}
		session := NewSessionForTesting(nil, nil, nil, backend, nil)
		backend.cancel = func() {
			if err := session.Close(); err != nil {
				t.Error(err)
			}
		}
		items, err := session.Outputs.List(context.Background())
		if !errors.Is(err, ErrSessionClosed) || len(items) != 0 {
			t.Fatalf("closed session exposed outputs %+v err=%v", items, err)
		}
	})
	t.Run("clipboard", func(t *testing.T) {
		backend := &cancelingCoreClipboardRead{}
		session := NewSessionForTesting(nil, nil, nil, nil, backend)
		backend.cancel = func() {
			if err := session.Close(); err != nil {
				t.Error(err)
			}
		}
		text, err := session.Clipboard.Get(context.Background())
		if !errors.Is(err, ErrSessionClosed) || text != "" {
			t.Fatalf("closed session exposed clipboard %q err=%v", text, err)
		}
	})
	t.Run("window title", func(t *testing.T) {
		backend := &cancelingCoreActiveTitleRead{handleWindowManager: newHandleWindowManager()}
		session := NewSessionForTesting(nil, nil, backend, nil, nil)
		backend.cancel = func() {
			if err := session.Close(); err != nil {
				t.Error(err)
			}
		}
		title, err := session.Windows.ActiveTitle(context.Background())
		if !errors.Is(err, ErrSessionClosed) || title != "" {
			t.Fatalf("closed session exposed title %q err=%v", title, err)
		}
	})
}
