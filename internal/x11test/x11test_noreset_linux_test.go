//go:build linux

package x11test

import (
	"testing"

	"github.com/jezek/xgb"
)

func TestXvfbNoResetAllowsReconnectAfterLastClientDisconnects(t *testing.T) {
	display, stop, err := StartXvfbNoReset()
	if err != nil {
		t.Skipf("private Xvfb unavailable: %v", err)
	}
	t.Cleanup(stop)

	for attempt := 0; attempt < 2; attempt++ {
		conn, err := xgb.NewConnDisplay(display)
		if err != nil {
			t.Fatalf("connect to private Xvfb on attempt %d: %v", attempt+1, err)
		}
		conn.Close()
	}
}
