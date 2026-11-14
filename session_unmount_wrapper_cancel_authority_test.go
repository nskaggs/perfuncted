package perfuncted

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The production unmount entry point must honor cancellation before mount
// inspection. The managed runtime and its contents must remain untouched.
func TestUnmountSubdirsContextCanceledPreservesRuntime(t *testing.T) {
	root := t.TempDir()
	sentinel := filepath.Join(root, "owned-by-other-operation")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("synthetic caller revocation")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	if err := unmountSubdirsContext(ctx, root); !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("canceled mount cleanup returned %v, want context cancellation and cause", err)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "preserve" {
		t.Fatalf("canceled mount cleanup altered runtime: content=%q error=%v", data, err)
	}
}
