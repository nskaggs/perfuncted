package perfuncted

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// Malformed readiness patterns are configuration errors, not missing sockets.
// Retrying them hides the cause and can needlessly consume startup time.
func TestWaitForGlobReturnsInvalidPatternInsteadOfTimeout(t *testing.T) {
	err := waitForGlob(context.Background(), "[unterminated", 2, time.Millisecond)
	if !errors.Is(err, filepath.ErrBadPattern) {
		t.Fatalf("malformed readiness glob returned %v; want ErrBadPattern", err)
	}
}
