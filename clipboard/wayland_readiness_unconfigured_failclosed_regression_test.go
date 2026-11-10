package clipboard

import (
	"context"
	"strings"
	"testing"
)

// An absent reader cannot establish that the compositor serves the selection.
// Readiness is a positive protocol observation, not merely absence of failure.
func TestWaylandReadinessRefusesMissingReader(t *testing.T) {
	for _, command := range [][]string{nil, {}, {""}} {
		backend := &extCmdClipboard{getCmd: command}
		err := backend.waitWaylandContent(context.Background(), "private clipboard selection")
		if err == nil || !strings.Contains(err.Error(), "readiness") {
			t.Fatalf("missing reader %q accepted as ready: %v", command, err)
		}
	}
}
