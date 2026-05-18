package perfuncted_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

type clipboardSpy struct {
	text       string
	getCalls   int
	setCalls   int
	closeCalls int
	ctxValue   any
	getErr     error
	setErr     error
	closeErr   error
}

func (c *clipboardSpy) Get(ctx context.Context) (string, error) {
	c.getCalls++
	c.ctxValue = ctx.Value(contextKey{})
	return c.text, c.getErr
}

func (c *clipboardSpy) Set(ctx context.Context, text string) error {
	c.setCalls++
	c.ctxValue = ctx.Value(contextKey{})
	c.text = text
	return c.setErr
}

func (c *clipboardSpy) Close() error {
	c.closeCalls++
	return c.closeErr
}

func TestClipboardBundle(t *testing.T) {
	sc := &pftest.Screenshotter{Width: 1024, Height: 768}
	inp := &pftest.Inputter{}
	cb := &pftest.Clipboard{}
	pf := pftest.New(sc, inp, nil, cb)

	t.Run("Clipboard", func(t *testing.T) {
		ctx := context.Background()
		if err := pf.Clipboard.Set(ctx, "hello"); err != nil {
			t.Fatal(err)
		}
		if got, err := pf.Clipboard.Get(ctx); err != nil || got != "hello" {
			t.Fatal(err, got)
		}
		if err := pf.Paste(ctx, "world"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPerfunctedPaste(t *testing.T) {
	inp := &pftest.Inputter{}
	cb := &pftest.Clipboard{}
	pf := pftest.New(nil, inp, nil, cb)
	if err := pf.Paste(context.Background(), "hello"); err != nil {
		t.Fatal(err)
	}
	if cb.Text != "hello" {
		t.Fatalf("Clipboard.Text = %q; want %q", cb.Text, "hello")
	}
	want := []string{"type:{ctrl+v}"}
	if len(inp.Calls) < len(want) {
		t.Fatalf("calls = %v", inp.Calls)
	}
	for i := range want {
		if inp.Calls[i] != want[i] {
			t.Errorf("call[%d] = %q; want %q", i, inp.Calls[i], want[i])
		}
	}
}

func TestClipboardBundleContextErrorsAndClose(t *testing.T) {
	ctx := context.WithValue(context.Background(), contextKey{}, "clipboard-token")
	closeErr := errors.New("clipboard close failed")
	cb := &clipboardSpy{closeErr: closeErr}
	pf := &perfuncted.Perfuncted{
		Clipboard: perfuncted.ClipboardBundle{Clipboard: cb},
	}

	if err := pf.Clipboard.Set(ctx, "hello"); err != nil {
		t.Fatalf("Clipboard.Set: %v", err)
	}
	if cb.setCalls != 1 {
		t.Fatalf("Set calls = %d, want 1", cb.setCalls)
	}
	if cb.ctxValue != "clipboard-token" {
		t.Fatalf("Set context value = %v, want clipboard-token", cb.ctxValue)
	}

	got, err := pf.Clipboard.Get(ctx)
	if err != nil {
		t.Fatalf("Clipboard.Get: %v", err)
	}
	if got != "hello" {
		t.Fatalf("Clipboard.Get = %q, want %q", got, "hello")
	}
	if cb.getCalls != 1 {
		t.Fatalf("Get calls = %d, want 1", cb.getCalls)
	}

	if err := pf.Close(); !errors.Is(err, closeErr) {
		t.Fatalf("Close error = %v, want %v", err, closeErr)
	}
	if cb.closeCalls != 1 {
		t.Fatalf("Close calls = %d, want 1", cb.closeCalls)
	}
}

func TestPerfunctedPasteFallsBackToInputWhenClipboardUnavailable(t *testing.T) {
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)

	if err := pf.Paste(context.Background(), "typed fallback"); err != nil {
		t.Fatalf("Paste fallback: %v", err)
	}
	if got := inp.Typed(); got != "typed fallback" {
		t.Fatalf("Paste fallback typed = %q, want %q", got, "typed fallback")
	}
}
