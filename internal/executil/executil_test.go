package executil

import (
	"context"
	"testing"
)

func TestExecUtilDefaults(t *testing.T) {
	if LookPath == nil {
		t.Fatal("LookPath is nil")
	}
	if CommandContext == nil {
		t.Fatal("CommandContext is nil")
	}

	cmd := CommandContext(context.Background(), "echo", "test")
	if cmd == nil {
		t.Fatal("CommandContext returned nil cmd")
	}
}
