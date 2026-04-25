package perfuncted_test

import (
	"testing"

	"github.com/nskaggs/perfuncted/pftest"
)

func TestClipboardBundle(t *testing.T) {
	sc := &pftest.Screenshotter{Width: 1024, Height: 768}
	inp := &pftest.Inputter{}
	cb := &pftest.Clipboard{}
	pf := pftest.New(sc, inp, nil, cb)

	t.Run("Clipboard", func(t *testing.T) {
		if err := pf.Clipboard.Set("hello"); err != nil {
			t.Fatal(err)
		}
		if got, err := pf.Clipboard.Get(); err != nil || got != "hello" {
			t.Fatal(err, got)
		}
		if err := pf.Clipboard.PasteWithInput("world", pf.Input); err != nil {
			t.Fatal(err)
		}
	})
}
