package perfuncted

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Only live caller authority permits proceeding from ownership checks to
// child cleanup, or from an empty mount scan to deleting the runtime tree.
// Revocation at either boundary must preserve all private contents.
func TestReapSessionPreservesPrivateRuntimeAtAuthorityBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		check int
	}{
		{name: "before first child PID inspection", check: 2},
		{name: "after mount safety inspection before directory deletion", check: 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			t.Setenv("TMPDIR", base)
			runtime := filepath.Join(base, "perfuncted-xdg-owned")
			if err := os.Mkdir(runtime, 0700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(runtime, "do-not-remove")
			if err := os.WriteFile(marker, []byte("private runtime data"), 0600); err != nil {
				t.Fatal(err)
			}
			owner, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			ctx := &cancelAtErrCallContext{Context: owner, cancel: cancel, target: tc.check}
			err := reapSessionDir(ctx, runtime)
			if !errors.Is(err, context.Canceled) || !errors.Is(err, context.Cause(owner)) || ctx.calls < tc.check {
				t.Fatalf("cleanup revocation: err=%v checks=%d trigger=%d", err, ctx.calls, tc.check)
			}
			content, readErr := os.ReadFile(marker)
			if readErr != nil || string(content) != "private runtime data" {
				t.Fatalf("cleanup removed private runtime after cancellation: data=%q err=%v", content, readErr)
			}
		})
	}
}
