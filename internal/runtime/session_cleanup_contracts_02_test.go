package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A matching runtime name is not an ownership grant. Reaping a directory
// writable by untrusted accounts can delete files they contributed or modified.
func TestStaleCleanupRejectsNonprivateRuntimeDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	for _, tc := range []struct {
		name   string
		mode   os.FileMode
		remove bool
	}{
		{"private-runtime", 0700, true},
		{"group-writable-runtime", 0770, false},
		{"world-writable-runtime", 0777, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(root, "perfuncted-xdg-"+tc.name)
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			marker := filepath.Join(dir, "private-payload")
			if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, tc.mode); err != nil {
				t.Fatal(err)
			}
			if err := reapSessionDir(context.Background(), dir); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(marker)
			if tc.remove && !os.IsNotExist(err) {
				t.Fatalf("private stale runtime not reaped: %v", err)
			}
			if !tc.remove && err != nil {
				t.Fatalf("untrusted writable runtime deleted: %v", err)
			}
		})
	}
}

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

// A previously enumerated managed runtime may become inaccessible before its
// inode is inspected. Fail closed: never consider a permission error evidence
// that the directory is stale or that its contents can safely be removed.
func TestReapManagedRuntimePreservesDirectoryWhenInspectionIsDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses parent directory search permissions")
	}
	base := t.TempDir()
	preferred := filepath.Join(base, "inaccessible-parent")
	runtime := filepath.Join(preferred, "perfuncted-xdg-private-data")
	if err := os.MkdirAll(runtime, 0700); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(runtime, "keep")
	if err := os.WriteFile(payload, []byte("foreign runtime content"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", preferred)
	if err := os.Chmod(preferred, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(preferred, 0700); err != nil {
			t.Errorf("restore directory permissions: %v", err)
		}
	})
	err := reapSessionDir(context.Background(), runtime)
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("inaccessible runtime not rejected: %v", err)
	}
	if err := os.Chmod(preferred, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(payload)
	if err != nil || string(data) != "foreign runtime content" {
		t.Fatalf("reaper damaged inaccessible runtime: data=%q err=%v", data, err)
	}
}

// A failed first mount scan leaves ownership of mounted subtrees unknown.
// Cleanup must fail before invoking any unmount helper, even if a partial
// listing accompanied the inspection failure.
func TestManagedUnmountRefusesIncompleteInitialMountInspection(t *testing.T) {
	const root = "/synthetic/perfuncted-xdg-unverified"
	inspectErr := errors.New("incomplete mount namespace snapshot")
	for _, tc := range []struct {
		name   string
		mounts []string
	}{
		{"no mount listing", nil},
		{"partial mount listing", []string{root + "/private"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads, unmounts := 0, 0
			err := unmountSubdirsWith(context.Background(), root, func(dir string) ([]string, error) {
				if dir != root {
					t.Fatalf("inspected wrong directory: %q", dir)
				}
				reads++
				return tc.mounts, inspectErr
			}, func(context.Context, string) error { unmounts++; return nil })
			if !errors.Is(err, inspectErr) || !strings.Contains(err.Error(), "inspect mounts before cleanup") || reads != 1 || unmounts != 0 {
				t.Fatalf("unsafe cleanup after incomplete mount scan: err=%v reads=%d unmounts=%d", err, reads, unmounts)
			}
		})
	}
}

// Cancellation and a simultaneous mount-table error are independent reasons
// cleanup must stop. Preserve both so callers can distinguish cancellation
// from the inspection failure, and do not mutate any mount.
func TestManagedUnmountPreservesCancellationAndInitialInspectionFailure(t *testing.T) {
	const root = "/synthetic/perfuncted-xdg-canceled-mount-inspection"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inspectErr := errors.New("mountinfo became unavailable")
	unmountCalls := 0
	err := unmountSubdirsWith(ctx, root, func(string) ([]string, error) {
		cancel()
		return []string{root + "/subtree"}, inspectErr
	}, func(context.Context, string) error { unmountCalls++; return nil })
	if !errors.Is(err, context.Canceled) || !errors.Is(err, inspectErr) || unmountCalls != 0 {
		t.Fatalf("canceled failed inspection lost causes or mutated mount: err=%v unmounts=%d", err, unmountCalls)
	}
}
