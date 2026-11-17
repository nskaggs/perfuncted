package window

import (
	"context"
	"strings"
	"testing"
)

func TestWaitForKWinResultUsesBoundedFallbackTimeout(t *testing.T) {
	_, err := waitForKWinResult(context.Background(), &pfReceiver{ch: make(chan string)}, 31)
	if err == nil || !strings.Contains(err.Error(), "timeout") || !strings.Contains(err.Error(), "script 31 did not call back") {
		t.Fatalf("waitForKWinResult error = %v, want bounded callback timeout for script 31", err)
	}
}
