package perfuncted_test

import (
	"context"
	"testing"

	"github.com/nskaggs/perfuncted/pftest"
)

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
