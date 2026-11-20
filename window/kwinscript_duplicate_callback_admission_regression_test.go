//go:build linux

package window

import (
	"testing"
	"time"
)

// A KWin script can invoke the one-shot callback more than once. The callback
// handler must not block the D-Bus dispatch thread or replace the first result
// with a later, potentially unrelated window observation.
func TestKWinScriptReceiverIgnoresDuplicateCallbacksWithoutBlocking(t *testing.T) {
	receiver := &pfReceiver{ch: make(chan string, 1)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := receiver.ReportWindows(`{"id":"first"}`); err != nil {
			t.Errorf("first report returned D-Bus error: %v", err)
		}
		if err := receiver.ReportWindows(`{"id":"second"}`); err != nil {
			t.Errorf("duplicate report returned D-Bus error: %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("duplicate callback blocked the D-Bus dispatch path")
	}
	select {
	case got := <-receiver.ch:
		if got != `{"id":"first"}` {
			t.Fatalf("duplicate overwrote first response: %q", got)
		}
	default:
		t.Fatal("first callback was dropped")
	}
	select {
	case got := <-receiver.ch:
		t.Fatalf("duplicate enqueued extra callback: %q", got)
	default:
	}
}
