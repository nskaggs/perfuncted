package perfuncted

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestStaleSessionDiscoveryHonorsCancellationBetweenRoots(t *testing.T) {
	preferred := t.TempDir()
	if err := os.WriteFile(filepath.Join(preferred, "ordinary-entry"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", preferred)

	base, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	ctx := &cancelAtErrCallContext{Context: base, cancel: cancel, target: 3}
	dirs, err := staleSessionDirectories(ctx)
	if len(dirs) != 0 || !errors.Is(err, context.Canceled) || !errors.Is(err, context.Cause(base)) {
		t.Fatalf("discovery after cancellation between roots = dirs %v, err %v", dirs, err)
	}
}

func TestStaleSessionDiscoveryHonorsCancellationDuringEntryScan(t *testing.T) {
	entry, err := os.MkdirTemp("/tmp", "perfuncted-xdg-cancel-scan-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(entry) })
	t.Setenv("TMPDIR", "/tmp")

	base, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	ctx := &cancelAtErrCallContext{Context: base, cancel: cancel, target: 2}
	dirs, err := staleSessionDirectories(ctx)
	if len(dirs) != 0 || !errors.Is(err, context.Canceled) || !errors.Is(err, context.Cause(base)) {
		t.Fatalf("discovery after cancellation during entry scan = dirs %v, err %v", dirs, err)
	}
}

func TestStaleSessionDiscoveryScansFallbackWhenPreferredRootIsMissing(t *testing.T) {
	fallback, err := os.MkdirTemp("/tmp", "perfuncted-xdg-fallback-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(fallback) })
	preferred := filepath.Join(t.TempDir(), "removed-tmpdir")
	t.Setenv("TMPDIR", preferred)

	dirs, err := staleSessionDirectories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(dirs, fallback) {
		t.Fatalf("discovery skipped short-root runtime %q when preferred root %q was missing: %v", fallback, preferred, dirs)
	}
}
