package perfuncted_test

import (
	"testing"

	"github.com/nskaggs/perfuncted/pftest"
)

func TestPerfunctedPaste(t *testing.T) {
	inp := &pftest.Inputter{}
	cb := &pftest.Clipboard{}
	pf := pftest.New(nil, inp, nil, cb)
	if err := pf.Paste("hello"); err != nil {
		t.Fatal(err)
	}
	if cb.Text != "hello" {
		t.Fatalf("Clipboard.Text = %q; want %q", cb.Text, "hello")
	}
	want := []string{"combo:ctrl+v"}
	if len(inp.Calls) < len(want) {
		t.Fatalf("calls = %v", inp.Calls)
	}
	for i := range want {
		if inp.Calls[i] != want[i] {
			t.Errorf("call[%d] = %q; want %q", i, inp.Calls[i], want[i])
		}
	}
}
