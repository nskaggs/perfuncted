package perfuncted_test

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A connected but inert Wayland socket must not trap public context-aware
// backend openers during compositor detection. No actual desktop or input
// device is used: these calls must abort before backend initialization.
func TestBackendOpenersAbortWedgedCompositorDetectionAtCallerDeadline(t *testing.T) {
	t.Setenv("PF_FORCE_INPUT", "")
	dir, err := os.MkdirTemp("/var/tmp", "pf-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	listener, err := net.Listen("unix", filepath.Join(dir, "wayland-hung"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=wayland-hung", "XDG_RUNTIME_DIR=" + dir, "XDG_SESSION_TYPE=wayland", "XDG_CURRENT_DESKTOP=SWAY"})
	for _, tc := range []struct {
		name string
		open func(context.Context) error
	}{
		{"input", func(ctx context.Context) error {
			b, err := input.OpenRuntimeContext(ctx, rt, 1280, 720)
			if b != nil {
				_ = b.Close()
			}
			return err
		}},
		{"screen", func(ctx context.Context) error {
			b, err := screen.OpenRuntimeContext(ctx, rt)
			if b != nil {
				_ = b.Close()
			}
			return err
		}},
		{"window", func(ctx context.Context) error {
			b, err := window.OpenRuntimeContext(ctx, rt)
			if b != nil {
				_ = b.Close()
			}
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
			defer cancel()
			started := time.Now()
			if err := tc.open(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("wedged compositor returned %v, want caller deadline", err)
			}
			if elapsed := time.Since(started); elapsed > time.Second {
				t.Fatalf("backend opener took %s beyond caller deadline", elapsed)
			}
		})
	}
}

type cancelDuringClipboardSet struct {
	cancel context.CancelFunc
	calls  int
}

func (c *cancelDuringClipboardSet) Get(context.Context) (string, error) { return "", nil }
func (c *cancelDuringClipboardSet) Set(context.Context, string) error {
	c.calls++
	c.cancel()
	return nil
}
func (c *cancelDuringClipboardSet) Close() error { return nil }

// Permission to send desktop input is withdrawn at cancellation. A backend
// ignoring its context must not be able to dispatch a canceled paste.
func TestPasteRejectsCanceledRequestBeforeDesktopSideEffects(t *testing.T) {
	for _, withClipboard := range []bool{false, true} {
		name := "literal-fallback"
		if withClipboard {
			name = "clipboard-path"
		}
		t.Run(name, func(t *testing.T) {
			input := &pftest.Inputter{}
			var clipboard *pftest.Clipboard
			if withClipboard {
				clipboard = &pftest.Clipboard{Text: "existing"}
			}
			session := perfuncted.NewSessionForTesting(nil, input, nil, nil, clipboard)
			t.Cleanup(func() { _ = session.Close() })
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := session.Paste(ctx, "sensitive")
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled paste: %v", err)
			}
			if len(input.Calls) != 0 {
				t.Fatalf("canceled paste sent keystrokes: %v", input.Calls)
			}
			if withClipboard && clipboard.Text != "existing" {
				t.Fatalf("canceled paste mutated clipboard: %q", clipboard.Text)
			}
		})
	}
}

// Cancellation while a clipboard backend is updating content must not
// authorize a follow-up keyboard shortcut, even if Set returns nil.
func TestPasteDoesNotTypeAfterClipboardSetCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	clipboard := &cancelDuringClipboardSet{cancel: cancel}
	input := &pftest.Inputter{}
	session := perfuncted.NewSessionForTesting(nil, input, nil, nil, clipboard)
	t.Cleanup(func() { _ = session.Close() })
	err := session.Paste(ctx, "new selection")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled clipboard operation reported %v", err)
	}
	if clipboard.calls != 1 {
		t.Fatalf("clipboard Set called %d times", clipboard.calls)
	}
	if len(input.Calls) != 0 {
		t.Fatalf("keyboard shortcut sent after cancellation: %v", input.Calls)
	}
}

type cancelAfterInputAcknowledgement struct {
	pftest.Inputter
	cancel     context.CancelFunc
	dispatched int
}

func (i *cancelAfterInputAcknowledgement) Type(ctx context.Context, text string) error {
	i.dispatched++
	i.cancel()
	return nil
}
func (i *cancelAfterInputAcknowledgement) TypeLiteral(ctx context.Context, text string) error {
	i.dispatched++
	i.cancel()
	return nil
}

// A provider may acknowledge dispatch after the request is revoked; do not
// represent that observation as a verified successful paste.
func TestPasteRejectsSuccessAcknowledgedAfterCancellation(t *testing.T) {
	for _, withClipboard := range []bool{false, true} {
		name := "literal-fallback"
		if withClipboard {
			name = "clipboard-shortcut"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			input := &cancelAfterInputAcknowledgement{cancel: cancel}
			var clipboard *pftest.Clipboard
			if withClipboard {
				clipboard = &pftest.Clipboard{}
			}
			session := perfuncted.NewSessionForTesting(nil, input, nil, nil, clipboard)
			t.Cleanup(func() { _ = session.Close() })
			err := session.Paste(ctx, "payload")
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("late-canceled paste returned %v, want cancellation", err)
			}
			if input.dispatched != 1 {
				t.Fatalf("expected one attempted input operation, got %d", input.dispatched)
			}
		})
	}
}

type cancelAfterSetClipboard struct {
	pftest.Clipboard
	cancel   context.CancelFunc
	setCalls int
}

func (c *cancelAfterSetClipboard) Set(_ context.Context, text string) error {
	c.setCalls++
	c.Text = text
	c.cancel()
	return nil
}

type cancelAfterLiteralInput struct {
	pftest.Inputter
	cancel context.CancelFunc
}

func (i *cancelAfterLiteralInput) TypeLiteral(ctx context.Context, text string) error {
	if err := i.Inputter.TypeLiteral(ctx, text); err != nil {
		return err
	}
	i.cancel()
	return nil
}

func TestPasteReturnsCancellationAfterLateBackendSuccess(t *testing.T) {
	t.Run("clipboard", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		clipboard := &cancelAfterSetClipboard{cancel: cancel}
		input := &pftest.Inputter{}
		session := pftest.New(nil, input, nil, clipboard)
		defer session.Close()

		err := session.Paste(ctx, "private clipboard text")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Paste error = %v, want context.Canceled", err)
		}
		if clipboard.Text != "private clipboard text" || clipboard.setCalls != 1 {
			t.Fatalf("clipboard text=%q sets=%d, want one completed clipboard write", clipboard.Text, clipboard.setCalls)
		}
		if len(input.Calls) != 0 {
			t.Fatalf("paste shortcut was dispatched after cancellation: %v", input.Calls)
		}
	})

	t.Run("literal input fallback", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		input := &cancelAfterLiteralInput{cancel: cancel}
		session := pftest.New(nil, input, nil, nil)
		defer session.Close()

		err := session.Paste(ctx, "late typed text")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Paste fallback error = %v, want context.Canceled", err)
		}
		if len(input.Calls) != 1 || input.Calls[0] != "type-literal:late typed text" {
			t.Fatalf("input dispatches = %v, want one literal-text dispatch", input.Calls)
		}
	})
}
